//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/turn"
)

const (
	durableTelegramChannelGroup = "telegram_group"
	durableTelegramChannelDM    = "telegram_dm"
)

func (r *Runtime) handleDurableTelegramGroupInbound(ctx context.Context, msg core.InboundMessage) (result *core.TurnResult, err error) {
	agentID := strings.TrimSpace(msg.DurableAgentID)
	if agentID == "" {
		return nil, fmt.Errorf("durable telegram inbound missing agent id")
	}
	registered, err := r.loadDurableTelegramAgent(agentID)
	if err != nil {
		return nil, err
	}
	if err := validateDurableTelegramInboundChat(*registered, msg); err != nil {
		return nil, err
	}
	if !r.durableGroupSenderAuthorized(*registered, msg.SenderID) {
		log.Printf(
			"INFO durable telegram inbound denied agent_id=%s channel=%s sender_id=%d chat_id=%d",
			strings.TrimSpace(registered.AgentID),
			strings.TrimSpace(registered.ChannelKind),
			msg.SenderID,
			msg.ChatID,
		)
		return nil, nil
	}
	livePolicy := core.NormalizeDurableAgentLivePolicy(registered.LivePolicy)
	allowLocalReply := durableGroupAllowsLocalReply(livePolicy)

	stopTyping := func() {}
	if allowLocalReply {
		stopTyping = r.startChatActionLoop(ctx, msg.ChatID, "typing")
	}
	defer stopTyping()

	key := session.SessionKey{
		ChatID: msg.ChatID,
		Scope:  durableAgentScopeRef(*registered),
	}
	unlock := r.lockSession(key)
	defer unlock()

	if err := r.markDurableAgentAwake(registered.AgentID, msg.MessageID); err != nil {
		return nil, fmt.Errorf("mark durable agent awake: %w", err)
	}
	if err := r.ensureDurableAgentPolicyOffered(*registered); err != nil {
		return nil, fmt.Errorf("record durable agent offered policy: %w", err)
	}
	defer func() {
		if dormantErr := r.markDurableAgentDormant(registered.AgentID); dormantErr != nil {
			log.Printf("WARN durable agent dormant state update failed agent_id=%s err=%v", registered.AgentID, dormantErr)
		}
	}()

	scope, err := r.scopeForDurableAgent(*registered)
	if err != nil {
		return nil, fmt.Errorf("resolve durable agent scope: %w", err)
	}
	bootstrapLLM := core.NormalizeNodeLLMBootstrap(registered.BootstrapLLM)
	if !bootstrapLLM.Configured() {
		return nil, fmt.Errorf("durable agent %q requires child-local llm bootstrap", registered.AgentID)
	}
	child := r.durableGroupChild
	if child == nil || !child.Supports(scope, *registered) {
		return nil, fmt.Errorf("durable agent %q isolated child execution is unavailable", registered.AgentID)
	}
	childResult, childErr := child.Run(ctx, scope, *registered, msg)
	if childErr != nil {
		if markErr := r.markDurableAgentPolicyApplyFailure(*registered, childErr); markErr != nil {
			log.Printf("WARN durable agent policy failure state update failed agent_id=%s err=%v", registered.AgentID, markErr)
		}
		return nil, fmt.Errorf("run durable child: %w", childErr)
	}
	if err := r.markDurableAgentPolicyApplied(*registered); err != nil {
		return nil, fmt.Errorf("record durable agent applied policy: %w", err)
	}
	if childResult.AllowLocalReply {
		outboundID, outboundType, sendErr := r.sendReply(ctx, msg, childResult.ReplyText, childResult.TurnResult.Media, r.shouldReplyWithVoice(childResult.InboundWasVoice))
		if sendErr != nil {
			return &childResult.TurnResult, fmt.Errorf("send durable telegram reply: %w", sendErr)
		}
		if outboundID != 0 {
			if err := r.store.RecordOutbound(key, childResult.TurnIndex, outboundID, outboundType); err != nil {
				return &childResult.TurnResult, fmt.Errorf("record durable telegram outbound reply: %w", err)
			}
		}
	}
	return &childResult.TurnResult, nil
}

type durableGroupRunOptions struct {
	DeliverReply bool
	AllowStream  bool
}

func (r *Runtime) RunDurableTelegramGroupChild(ctx context.Context, msg core.InboundMessage) (*DurableGroupChildResult, error) {
	registered, err := r.loadDurableTelegramAgent(strings.TrimSpace(msg.DurableAgentID))
	if err != nil {
		return nil, err
	}
	if err := validateDurableTelegramInboundChat(*registered, msg); err != nil {
		return nil, err
	}
	scope, err := r.scopeForDurableAgent(*registered)
	if err != nil {
		return nil, fmt.Errorf("resolve durable agent scope: %w", err)
	}
	return r.runDurableTelegramGroupTurn(ctx, msg, *registered, scope, durableGroupRunOptions{})
}

func (r *Runtime) loadDurableTelegramAgent(agentID string) (*core.DurableAgent, error) {
	registered, err := r.store.DurableAgent(strings.TrimSpace(agentID))
	if err != nil {
		return nil, fmt.Errorf("load durable agent: %w", err)
	}
	if registered == nil {
		return nil, fmt.Errorf("durable agent %q not found", strings.TrimSpace(agentID))
	}
	switch durableTelegramChannel(registered.ChannelKind) {
	case durableTelegramChannelGroup, durableTelegramChannelDM:
	default:
		return nil, fmt.Errorf("durable agent %q is not a telegram channel agent", strings.TrimSpace(agentID))
	}
	if status := strings.ToLower(strings.TrimSpace(registered.Status)); status != "" && status != "active" {
		return nil, fmt.Errorf("durable agent %q is not active", strings.TrimSpace(agentID))
	}
	return registered, nil
}

func (r *Runtime) runDurableTelegramGroupTurn(ctx context.Context, msg core.InboundMessage, registered core.DurableAgent, scope sandbox.Scope, opts durableGroupRunOptions) (*DurableGroupChildResult, error) {
	if len(registered.LocalStorageRoots) == 0 {
		registered.LocalStorageRoots = []string{scope.WorkingRoot, scope.SharedMemoryRoot}
	}
	key := session.SessionKey{
		ChatID: msg.ChatID,
		Scope:  durableAgentScopeRef(registered),
	}
	defer r.clearChatTurnPhase(msg.ChatID)
	preparedMsg := msg
	preparedMsg.Text = durableTelegramInboundText(registered, msg)
	livePolicy := core.NormalizeDurableAgentLivePolicy(registered.LivePolicy)
	allowLocalReply := durableGroupAllowsLocalReply(livePolicy)
	pendingParentConversation, err := r.pendingDurableAgentParentConversation(registered.AgentID, 3)
	if err != nil {
		return nil, fmt.Errorf("load durable agent parent conversation: %w", err)
	}
	channel := durableTelegramChannel(registered.ChannelKind)
	assembled, err := r.assembleInteractiveLikeTurn(ctx, interactiveLikeAssemblyInput{
		Scope:                scope,
		Key:                  key,
		Msg:                  msg,
		PrepareInbound:       &preparedMsg,
		RunKind:              session.TurnRunKindInteractive,
		Channel:              channel,
		PrincipalRole:        "durable_agent",
		AuditChannel:         channel,
		PromptContextErrHint: "load durable agent prompt context",
		PolicyReason:         "mapped from interactive face policy for durable telegram channels",
	})
	if err != nil {
		return nil, err
	}

	now := assembled.Now
	sess := assembled.Sess
	prepared := assembled.Prepared
	facePolicy := assembled.FacePolicy
	useMaterialFloor := assembled.UseMaterialFloor
	exec := assembled.Exec
	promptContext := assembled.PromptContext
	hiddenInputs := assembled.HiddenInputs
	baseGovernorAwareness := assembled.BaseGovernorAwareness
	audit := assembled.Audit
	machine := assembled.Machine
	defer r.emitTurnAudit(audit)

	sess.ChatType = durableTelegramChatType(msg.ChatType, channel)
	sess.ChatTitle = strings.TrimSpace(msg.ChatTitle)
	sess.UserName = strings.TrimSpace(msg.SenderName)
	coordinator := &durableGroupTurnCoordinator{
		runtime:                   r,
		registered:                registered,
		livePolicy:                livePolicy,
		scope:                     scope,
		msg:                       msg,
		key:                       key,
		sess:                      sess,
		prepared:                  prepared,
		exec:                      exec,
		facePolicy:                facePolicy,
		useMaterialFloor:          useMaterialFloor,
		governorName:              machine.Options.GovernorName,
		faceName:                  machine.Options.FaceName,
		channelName:               machine.Options.Channel,
		principalRole:             "durable_agent",
		hiddenInputs:              hiddenInputs,
		promptContext:             promptContext,
		tools:                     agent.ToolRegistry(nil),
		currentFaceModel:          r.currentFaceRenderer(),
		baseGovernorAwareness:     baseGovernorAwareness,
		audit:                     audit,
		allowStream:               opts.AllowStream,
		pendingParentConversation: pendingParentConversation,
	}
	machine.Governor = coordinator
	machine.Face = coordinator
	machine.Persistence = &turnPersistencePort{
		runtime:     r,
		key:         key,
		scope:       scope,
		sess:        sess,
		runIDSource: coordinator,
		errCtx: turnCommitErrorContext{
			ConvertMessages: "convert durable telegram messages",
			LoadPlanState:   "load durable telegram plan state before save",
			LoadOperation:   "load durable telegram operation state before save",
			SaveSession:     "save durable telegram session",
			RecordOutbound:  "record durable telegram outbound reply",
		},
		audit: audit,
	}
	machine.Delivery = &turnDeliveryPort{
		runtime:         r,
		key:             key,
		sess:            sess,
		msg:             msg,
		inboundWasVoice: prepared.InboundWasVoice,
		deliver:         opts.DeliverReply && allowLocalReply,
		recordOutbound:  opts.DeliverReply && allowLocalReply,
		audit:           audit,
		sendErrCtx:      "send durable telegram reply",
		recordErrCtx:    "record durable telegram outbound reply",
		hooks: turnCommitHooks{
			QueueDurableArtifact: func(result *turn.Result) error {
				replyText := ""
				if result != nil {
					replyText = strings.TrimSpace(result.VisibleReply)
				}
				artifact := durableTelegramReviewArtifact(registered, livePolicy, msg, replyText)
				if artifact == nil {
					return nil
				}
				if _, hookErr := r.queueDurableReviewArtifactPending(registered, *artifact); hookErr != nil {
					return fmt.Errorf("queue durable telegram review artifact: %w", hookErr)
				}
				return nil
			},
		},
	}
	turnResult, err := machine.Handle(ctx, turn.Request{
		RunKind:          session.TurnRunKindInteractive,
		SessionKey:       key,
		Inbound:          msg,
		InboundWasVoice:  prepared.InboundWasVoice,
		ReplyWithVoice:   r.preparedReplyWithVoice(prepared),
		Session:          sess,
		Now:              now,
		PreparedUserText: prepared.LedgerText,
	})
	if err != nil {
		if turnResult == nil || !turnResult.Commit.Persisted {
			return nil, err
		}
	}
	if turnResult == nil || turnResult.Turn == nil {
		return nil, fmt.Errorf("durable telegram turn did not return a result")
	}
	if err != nil {
		return nil, err
	}
	turnReply := strings.TrimSpace(turnResult.VisibleReply)
	if len(pendingParentConversation) > 0 {
		if durableTurnInferenceUnavailable(turnResult, turnReply) {
			log.Printf("WARN durable parent conversation not acknowledged due to transient inference failure agent_id=%s", registered.AgentID)
			return &DurableGroupChildResult{
				TurnResult:      *turnResult.Turn,
				ReplyText:       turnReply,
				AllowLocalReply: allowLocalReply,
				InboundWasVoice: prepared.InboundWasVoice,
				TurnIndex:       sess.TurnCount,
			}, nil
		}

		if registered.ReviewTargetChatID != 0 {
			if queueErr := r.queueDurableAgentParentConversationAck(registered, pendingParentConversation, turnReply, now); queueErr != nil {
				log.Printf("WARN durable parent conversation ack artifact failed agent_id=%s err=%v", registered.AgentID, queueErr)
				log.Printf("WARN durable parent conversation remains pending because review artifact queue failed agent_id=%s", registered.AgentID)
				return &DurableGroupChildResult{
					TurnResult:      *turnResult.Turn,
					ReplyText:       turnReply,
					AllowLocalReply: allowLocalReply,
					InboundWasVoice: prepared.InboundWasVoice,
					TurnIndex:       sess.TurnCount,
				}, nil
			}
		}
		if ackErr := r.acknowledgeDurableAgentParentConversation(registered.AgentID, pendingParentConversation, now); ackErr != nil {
			log.Printf("WARN durable parent conversation acknowledge failed agent_id=%s err=%v", registered.AgentID, ackErr)
		}
	}
	return &DurableGroupChildResult{
		TurnResult:      *turnResult.Turn,
		ReplyText:       turnReply,
		AllowLocalReply: allowLocalReply,
		InboundWasVoice: prepared.InboundWasVoice,
		TurnIndex:       sess.TurnCount,
	}, nil
}

func (r *Runtime) scopeForDurableAgent(agent core.DurableAgent) (sandbox.Scope, error) {
	workspaceRoot, memoryRoot := durableagent.LocalRoots(agent.AgentID, agent.LocalStorageRoots)
	if workspaceRoot == "" || memoryRoot == "" {
		workspaceRoot, memoryRoot = durableagent.DefaultLocalRoots(r.cfg.Sessions.DBPath, agent.AgentID)
	}
	for _, root := range []string{workspaceRoot, memoryRoot} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return sandbox.Scope{}, fmt.Errorf("create durable agent root %s: %w", root, err)
		}
	}
	profiles, err := SandboxProfilesFromConfig(r.cfg.Sandbox)
	if err != nil {
		return sandbox.Scope{}, err
	}
	return sandbox.DurableAgentScopeWithProfile(agent.AgentID, r.cfg.Agent.PromptRoot, workspaceRoot, memoryRoot, profiles.DurableAgent, agent.NetworkPolicy)
}

func durableAgentScopeRef(agent core.DurableAgent) session.ScopeRef {
	return session.NormalizeScopeRef(session.ScopeRef{
		Kind:            session.ScopeKindDurableAgent,
		ID:              strings.TrimSpace(agent.AgentID),
		DurableAgentID:  strings.TrimSpace(agent.AgentID),
		ParentScopeKind: session.ScopeKind(strings.TrimSpace(agent.ParentScopeKind)),
		ParentScopeID:   strings.TrimSpace(agent.ParentScopeID),
	})
}
