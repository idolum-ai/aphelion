//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/turn"
)

type durableWakeGovernorContextBuilder func(
	agent core.DurableAgent,
	policy core.DurableAgentLivePolicy,
	msg core.InboundMessage,
	pendingParentConversation []core.DurableAgentConversationMessage,
) string

type durableWakeTurnPlan struct {
	Channel              string
	AuditChannel         string
	Key                  session.SessionKey
	Inbound              core.InboundMessage
	SessionChatType      string
	SessionUserName      string
	PromptContextErrHint string
	PolicyReason         string
	PersistenceErrCtx    turnCommitErrorContext
	SendErrCtx           string
	RecordErrCtx         string
	GovernorContext      durableWakeGovernorContextBuilder
	Finalize             func(turnSummary string) error
}

type durableWakeIngressAdapter interface {
	Name() string
	Supports(agent core.DurableAgent) bool
	Prepare(ctx context.Context, runtime *Runtime, agent core.DurableAgent, now time.Time) (*durableWakeTurnPlan, error)
}

func defaultDurableWakeIngressAdapters() []durableWakeIngressAdapter {
	return nil
}

func (r *Runtime) durableWakeAdapterForAgent(agent core.DurableAgent) durableWakeIngressAdapter {
	if r == nil {
		return nil
	}
	for _, adapter := range r.durableWakeAdapters {
		if adapter == nil {
			continue
		}
		if adapter.Supports(agent) {
			return adapter
		}
	}
	return nil
}

func (r *Runtime) pollDurableWakeAgents(ctx context.Context, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	agents, err := r.store.ListDurableAgents()
	if err != nil {
		return err
	}
	var errs []string
	for _, agent := range agents {
		adapter := r.durableWakeAdapterForAgent(agent)
		if adapter == nil {
			continue
		}
		if r.shouldRunDurableWakeInChild(agent) {
			if err := r.pollDurableAgentWakeViaChild(ctx, agent, now); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", agent.AgentID, err))
			}
			continue
		}
		if err := r.runDurableAgentChildWakeLoaded(ctx, agent, now); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", agent.AgentID, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf(strings.Join(errs, "; "))
	}
	return nil
}

func (r *Runtime) runDurableAgentChildWakeLoaded(ctx context.Context, agent core.DurableAgent, now time.Time) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("durable child wake runtime is unavailable")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	adapter := r.durableWakeAdapterForAgent(agent)
	if adapter == nil {
		return fmt.Errorf(
			"durable agent %q channel %q has no wake ingress adapter",
			strings.TrimSpace(agent.AgentID),
			strings.TrimSpace(agent.ChannelKind),
		)
	}
	plan, err := adapter.Prepare(ctx, r, agent, now.UTC())
	if err != nil {
		return fmt.Errorf("prepare durable wake via %s: %w", strings.TrimSpace(adapter.Name()), err)
	}
	if plan == nil {
		return nil
	}
	return r.runDurableWakeTurn(ctx, agent, *plan, now.UTC())
}

func (r *Runtime) runDurableWakeTurn(ctx context.Context, agent core.DurableAgent, plan durableWakeTurnPlan, now time.Time) error {
	scope, err := r.scopeForDurableAgent(agent)
	if err != nil {
		return err
	}
	if len(agent.LocalStorageRoots) == 0 {
		agent.LocalStorageRoots = []string{scope.WorkingRoot, scope.SharedMemoryRoot}
	}

	key := plan.Key
	if key.ChatID == 0 {
		key.ChatID = plan.Inbound.ChatID
	}
	if key.Scope.Kind == "" {
		key.Scope = durableAgentScopeRef(agent)
	}

	unlock := r.lockSession(key)
	defer unlock()
	defer r.clearChatTurnPhase(key.ChatID)

	if err := r.markDurableAgentAwake(agent.AgentID, plan.Inbound.MessageID); err != nil {
		return fmt.Errorf("mark durable wake agent awake: %w", err)
	}
	if err := r.ensureDurableAgentPolicyOffered(agent); err != nil {
		return fmt.Errorf("record durable wake offered policy: %w", err)
	}
	defer func() {
		if dormantErr := r.markDurableAgentDormant(agent.AgentID); dormantErr != nil {
			log.Printf("WARN durable wake agent dormant state update failed agent_id=%s err=%v", agent.AgentID, dormantErr)
		}
	}()

	pendingParentConversation, err := r.pendingDurableAgentParentConversation(agent.AgentID, 3)
	if err != nil {
		return fmt.Errorf("load durable wake parent conversation: %w", err)
	}

	turnSummary, err := r.runDurableWakeConversation(ctx, agent, scope, key, plan, pendingParentConversation)
	if err != nil {
		if markErr := r.markDurableAgentPolicyApplyFailure(agent, err); markErr != nil {
			return fmt.Errorf("run durable wake turn: %w (and failed to record apply failure: %v)", err, markErr)
		}
		return fmt.Errorf("run durable wake turn: %w", err)
	}
	if err := r.markDurableAgentPolicyApplied(agent); err != nil {
		return fmt.Errorf("record durable wake applied policy: %w", err)
	}
	if plan.Finalize != nil {
		if err := plan.Finalize(turnSummary); err != nil {
			return err
		}
	}
	if len(pendingParentConversation) > 0 {
		if ackErr := r.acknowledgeDurableAgentParentConversation(agent.AgentID, now); ackErr == nil {
			_ = r.queueDurableAgentParentConversationAck(agent, pendingParentConversation, turnSummary, now)
		}
	}
	return nil
}

func (r *Runtime) runDurableWakeConversation(
	ctx context.Context,
	agent core.DurableAgent,
	scope sandbox.Scope,
	key session.SessionKey,
	plan durableWakeTurnPlan,
	pendingParentConversation []core.DurableAgentConversationMessage,
) (string, error) {
	livePolicy := core.NormalizeDurableAgentLivePolicy(agent.LivePolicy)
	channel := firstNonEmpty(strings.TrimSpace(plan.Channel), "durable_wake")
	assembled, err := r.assembleInteractiveLikeTurn(ctx, interactiveLikeAssemblyInput{
		Scope:                scope,
		Key:                  key,
		Msg:                  plan.Inbound,
		RunKind:              session.TurnRunKindInteractive,
		Channel:              channel,
		PrincipalRole:        "durable_agent",
		AuditChannel:         firstNonEmpty(strings.TrimSpace(plan.AuditChannel), channel),
		PromptContextErrHint: firstNonEmpty(strings.TrimSpace(plan.PromptContextErrHint), "load durable wake prompt context"),
		PolicyReason:         firstNonEmpty(strings.TrimSpace(plan.PolicyReason), "mapped from interactive face policy for durable wake channels"),
	})
	if err != nil {
		return "", err
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

	sess.ChatType = firstNonEmpty(strings.TrimSpace(plan.SessionChatType), strings.TrimSpace(plan.Inbound.ChatType), channel)
	sess.ChatTitle = strings.TrimSpace(plan.Inbound.ChatTitle)
	sess.UserName = firstNonEmpty(strings.TrimSpace(plan.SessionUserName), strings.TrimSpace(plan.Inbound.SenderName), "durable_agent")
	tools := r.toolsForPrincipal(principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: strings.TrimSpace(agent.AgentID)}, key)
	coordinator := &durableGroupTurnCoordinator{
		runtime:                   r,
		registered:                agent,
		livePolicy:                livePolicy,
		scope:                     scope,
		msg:                       plan.Inbound,
		key:                       key,
		sess:                      sess,
		prepared:                  prepared,
		exec:                      exec,
		facePolicy:                facePolicy,
		useMaterialFloor:          useMaterialFloor,
		governorName:              machine.Options.GovernorName,
		faceName:                  machine.Options.FaceName,
		channelName:               channel,
		principalRole:             "durable_agent",
		hiddenInputs:              hiddenInputs,
		promptContext:             promptContext,
		tools:                     tools,
		currentFaceModel:          r.currentFaceRenderer(),
		baseGovernorAwareness:     baseGovernorAwareness,
		audit:                     audit,
		allowStream:               false,
		pendingParentConversation: pendingParentConversation,
		governorContextBuilder:    plan.GovernorContext,
	}
	machine.Governor = coordinator
	machine.Face = coordinator

	errCtx := plan.PersistenceErrCtx
	if errCtx == (turnCommitErrorContext{}) {
		errCtx = turnCommitErrorContext{
			ConvertMessages: "convert durable wake messages",
			LoadPlanState:   "load durable wake plan state before save",
			LoadOperation:   "load durable wake operation state before save",
			SaveSession:     "save durable wake session",
			RecordOutbound:  "record durable wake outbound reply",
		}
	}
	machine.Persistence = &turnPersistencePort{
		runtime: r,
		key:     key,
		sess:    sess,
		errCtx:  errCtx,
		audit:   audit,
	}
	machine.Delivery = &turnDeliveryPort{
		runtime:         r,
		key:             key,
		sess:            sess,
		msg:             plan.Inbound,
		inboundWasVoice: prepared.InboundWasVoice,
		deliver:         false,
		recordOutbound:  false,
		audit:           audit,
		sendErrCtx:      firstNonEmpty(strings.TrimSpace(plan.SendErrCtx), "send durable wake reply"),
		recordErrCtx:    firstNonEmpty(strings.TrimSpace(plan.RecordErrCtx), "record durable wake outbound reply"),
	}
	turnResult, err := machine.Handle(ctx, turn.Request{
		RunKind:          session.TurnRunKindInteractive,
		SessionKey:       key,
		Inbound:          plan.Inbound,
		InboundWasVoice:  prepared.InboundWasVoice,
		Session:          sess,
		Now:              now,
		PreparedUserText: prepared.LedgerText,
	})
	if err != nil {
		if turnResult == nil || !turnResult.Commit.Persisted {
			return "", err
		}
	}
	if turnResult == nil || turnResult.Turn == nil {
		return "", fmt.Errorf("durable wake turn did not return a result")
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(turnResult.VisibleReply), nil
}
