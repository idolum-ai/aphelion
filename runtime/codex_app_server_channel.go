//go:build linux

package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
)

const (
	codexAppServerAdapterName     = "codex_app_server"
	codexAppServerWakeChannel     = "codex_app_server"
	codexAppServerMaxMessageBytes = int64(1 << 20)
)

var errCodexAppServerNoStatusEnvelope = errors.New("codex app-server turn did not return a durable child status envelope")

type codexAppServerDoer interface {
	Do(ctx context.Context, req codexAppServerRequest) (codexAppServerResult, error)
}

type codexAppServerRequest struct {
	Agent        core.DurableAgent
	Address      string
	MemoryRoot   string
	ThreadID     string
	Prompt       string
	Now          time.Time
	StatusSchema string
}

type codexAppServerResult struct {
	ThreadID       string
	TurnID         string
	Text           string
	EnvelopeRaw    []byte
	Envelope       core.DurableChildStatusEnvelope
	PayloadHash    string
	ApprovalLog    []codexAppServerApprovalDecision
	CodexEvents    []session.WorkCodexEvent
	PatchPreview   string
	Notifications  int
	Completed      bool
	ArtifactRel    string
	ArtifactSHA256 string
}

type codexAppServerApprovalDecision struct {
	Method   string `json:"method"`
	Decision string `json:"decision"`
	Command  string `json:"command,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type codexAppServerApprovalHandler func(method string, params map[string]any) codexAppServerApprovalDecision

type codexAppServerWakeAdapter struct {
	doer codexAppServerDoer
}

func newCodexAppServerWakeAdapter() durableWakeIngressAdapter {
	return &codexAppServerWakeAdapter{doer: realCodexAppServerDoer{}}
}

func (a *codexAppServerWakeAdapter) Name() string { return codexAppServerAdapterName }

func (a *codexAppServerWakeAdapter) Supports(agent core.DurableAgent) bool {
	if strings.ToLower(strings.TrimSpace(agent.Status)) != "active" {
		return false
	}
	if externalChannelAdapter(agent) != codexAppServerAdapterName {
		return false
	}
	mode := strings.TrimSpace(agent.WakeupMode)
	return mode == "" || strings.EqualFold(mode, "poll")
}

func (a *codexAppServerWakeAdapter) Prepare(ctx context.Context, rt *Runtime, agent core.DurableAgent, now time.Time) (*durableWakeTurnPlan, error) {
	if rt == nil || rt.store == nil {
		return nil, fmt.Errorf("codex app-server adapter runtime is unavailable")
	}
	external := agent.ChannelConfig.ExternalConfig()
	if external == nil {
		return nil, fmt.Errorf("codex app-server adapter requires external channel_config")
	}
	address := strings.TrimSpace(external.Address)
	if address == "" {
		return nil, fmt.Errorf("codex app-server adapter requires channel address")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	state, continuity, err := loadDurableAgentContinuityFromStore(rt.store, agent.AgentID)
	if err != nil {
		return nil, err
	}
	runtimeState := externalChannelStateForAdapter(continuity, codexAppServerAdapterName)
	codexState := decodeCodexAdapterState(runtimeState.AdapterState)
	if !externalChannelPollDue(runtimeState, strings.TrimSpace(external.PollInterval), now) {
		return nil, nil
	}

	_, memoryRoot := durableagent.LocalRoots(agent.AgentID, agent.LocalStorageRoots)
	if strings.TrimSpace(memoryRoot) == "" {
		if dbPath := strings.TrimSpace(rt.store.DBPath()); dbPath != "" {
			_, memoryRoot = durableagent.DefaultLocalRoots(dbPath, strings.TrimSpace(agent.AgentID))
		}
	}
	if strings.TrimSpace(memoryRoot) == "" {
		return nil, fmt.Errorf("codex app-server adapter requires durable agent memory root")
	}

	doer := a.doer
	if doer == nil {
		doer = realCodexAppServerDoer{}
	}
	prompt := codexAppServerStatusPrompt(agent, now)
	result, err := doer.Do(ctx, codexAppServerRequest{
		Agent:        agent,
		Address:      address,
		MemoryRoot:   memoryRoot,
		ThreadID:     firstNonEmpty(strings.TrimSpace(codexState.ThreadID), strings.TrimSpace(runtimeState.SessionRef)),
		Prompt:       prompt,
		Now:          now,
		StatusSchema: strings.TrimSpace(external.Query),
	})
	if err != nil {
		return nil, recordCodexAppServerFailure(rt.store, state, continuity, runtimeState, memoryRoot, agent, result, err, now)
	}
	if err := core.ValidateDurableChildStatusEnvelopeForAgent(result.Envelope, agent); err != nil {
		return nil, recordCodexAppServerFailure(rt.store, state, continuity, runtimeState, memoryRoot, agent, result, fmt.Errorf("validate codex app-server status envelope: %w", err), now)
	}
	if !strings.EqualFold(strings.TrimSpace(result.Envelope.CapabilityPosture), "read_only") {
		return nil, recordCodexAppServerFailure(rt.store, state, continuity, runtimeState, memoryRoot, agent, result, fmt.Errorf("codex app-server status capability_posture %q is not read_only", strings.TrimSpace(result.Envelope.CapabilityPosture)), now)
	}

	artifactRel, artifactSHA, err := writeCodexAppServerHeartbeatArtifact(memoryRoot, agent, result, now)
	if err != nil {
		return nil, err
	}
	result.ArtifactRel = artifactRel
	result.ArtifactSHA256 = artifactSHA

	codexState.ThreadID = strings.TrimSpace(result.ThreadID)
	codexState.LastTurnID = strings.TrimSpace(result.TurnID)
	codexState.LastPayloadHash = firstNonEmpty(strings.TrimSpace(result.Envelope.PayloadHash), strings.TrimSpace(result.PayloadHash))
	runtimeState = externalChannelRecordSuccess(runtimeState, externalChannelCommandLifecycle{
		Adapter:      codexAppServerAdapterName,
		Command:      codexAppServerStatusCommandName,
		SessionRef:   strings.TrimSpace(result.ThreadID),
		LastArtifact: artifactRel,
		LastStatus:   "ok",
		ResetBackoff: true,
	}, now)
	continuity.ExternalChannel = encodeCodexExternalChannelState(runtimeState, codexState)
	raw, err := continuity.Marshal()
	if err != nil {
		return nil, err
	}
	state.StateJSON = raw
	if err := rt.store.SaveDurableAgentState(*state); err != nil {
		return nil, err
	}

	key := session.SessionKey{ChatID: durableWakeSyntheticChatID(agent.AgentID), Scope: durableAgentScopeRef(agent)}
	summary := codexAppServerWakeSummary(agent, result, artifactRel)
	return &durableWakeTurnPlan{
		Channel:      codexAppServerWakeChannel,
		AuditChannel: codexAppServerWakeChannel,
		Key:          key,
		Inbound: core.InboundMessage{
			ChatID:         key.ChatID,
			ChatType:       codexAppServerWakeChannel,
			ChatTitle:      "codex-app-server",
			SenderName:     "codex_app_server",
			Text:           summary,
			MessageID:      durableWakeMessageID(now),
			DurableAgentID: strings.TrimSpace(agent.AgentID),
			Timestamp:      now,
		},
		SessionChatType:      codexAppServerWakeChannel,
		SessionUserName:      "codex_app_server",
		PromptContextErrHint: "load codex app-server durable wake prompt context",
		PolicyReason:         "mapped from generic codex_app_server durable-agent channel adapter",
		PersistenceErrCtx: turnCommitErrorContext{
			ConvertMessages: "convert codex app-server durable wake messages",
			LoadPlanState:   "load codex app-server durable wake plan state before save",
			LoadOperation:   "load codex app-server durable wake operation state before save",
			SaveSession:     "save codex app-server durable wake session",
			RecordOutbound:  "record codex app-server durable wake outbound reply",
		},
		SendErrCtx:   "send codex app-server durable wake reply",
		RecordErrCtx: "record codex app-server durable wake outbound reply",
		GovernorContext: func(agent core.DurableAgent, policy core.DurableAgentLivePolicy, _ core.InboundMessage, pending []core.DurableAgentConversationMessage) string {
			lines := []string{
				"You are handling a durable-agent wake from a generic codex_app_server channel adapter.",
				"The adapter already performed the remote read-only status task and stored the heartbeat artifact.",
				"Report the concrete status and next bounded step. Do not claim additional remote actions.",
				"No UI/app manipulation, screenshots, file-content inspection, process killing, command-line args, or writes are authorized.",
			}
			if charter := strings.TrimSpace(policy.Charter); charter != "" {
				lines = append(lines, "Charter: "+charter)
			}
			lines = append(lines, "Durable agent id: "+strings.TrimSpace(agent.AgentID))
			lines = append(lines, "Channel address: "+address)
			lines = append(lines, durableParentConversationGovernorLines(pending)...)
			return strings.Join(lines, "\n")
		},
		Finalize: func(turnSummary string) error {
			_, err := durableagent.NewRuntime(rt.store).QueueReviewArtifact(agent, core.DurableReviewArtifact{
				AgentID:       strings.TrimSpace(agent.AgentID),
				Summary:       firstNonEmpty(strings.TrimSpace(turnSummary), summary),
				IntervalLabel: now.UTC().Format(time.RFC3339),
				LocalActions: []string{
					"Ran a bounded read-only heartbeat through the generic codex_app_server channel adapter.",
					"Stored the resulting durable_child_status envelope as a child artifact.",
				},
				RiskFlags: []string{"remote_child_runtime", "read_only_status", "codex_app_server"},
				ArtifactRefs: []string{
					fmt.Sprintf("artifact://durable-agent/%s/%s", strings.TrimSpace(agent.AgentID), artifactRel),
				},
				Metadata: map[string]string{
					"channel_kind":          strings.TrimSpace(agent.ChannelKind),
					"channel_adapter":       codexAppServerAdapterName,
					"channel_address":       address,
					"thread_id":             strings.TrimSpace(result.ThreadID),
					"turn_id":               strings.TrimSpace(result.TurnID),
					"payload_hash":          firstNonEmpty(strings.TrimSpace(result.Envelope.PayloadHash), strings.TrimSpace(result.PayloadHash)),
					"artifact_ref":          artifactRel,
					"artifact_sha256":       artifactSHA,
					"trigger_kinds":         "codex_app_server,heartbeat,status",
					"child_local_subject":   "false",
					"approvals_decisions":   summarizeCodexApprovalDecisions(result.ApprovalLog),
					"notifications_count":   fmt.Sprintf("%d", result.Notifications),
					"single_session_thread": codexAppServerBoolString(strings.TrimSpace(result.ThreadID) != ""),
				},
			})
			return err
		},
	}, nil
}
