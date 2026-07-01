//go:build linux

package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	runtimecodex "github.com/idolum-ai/aphelion/runtime/codex"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) recordDurableWakeExternalFailure(agent core.DurableAgent, cause error, now time.Time) (bool, error) {
	if r == nil || r.store == nil || cause == nil {
		return false, nil
	}
	adapterName, _, ok := durableWakeExternalBackoffIdentity(agent)
	if !ok {
		return false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	state, continuity, err := loadDurableAgentContinuityFromStore(r.store, agent.AgentID)
	if err != nil {
		return true, err
	}
	runtimeState := externalChannelStateForAdapter(continuity, adapterName)
	failureCode := durableWakeFailureCode(cause)
	runtimeState = externalChannelRecordFailure(runtimeState, externalChannelCommandLifecycle{
		Adapter:    adapterName,
		Command:    genericExternalChannelPollCommandName,
		LastStatus: "wake_failed",
		LastError:  truncateRunes(failureCode+": "+cause.Error(), 900),
	}, now)
	if strings.EqualFold(adapterName, runtimecodex.AdapterName) {
		continuity.ExternalChannel = encodeCodexExternalChannelState(runtimeState, decodeCodexAdapterState(runtimeState.AdapterState))
	} else {
		continuity.ExternalChannel = encodeGenericExternalChannelState(runtimeState, adapterName)
	}
	raw, err := continuity.Marshal()
	if err != nil {
		return true, err
	}
	state.StateJSON = raw
	if err := r.store.SaveDurableAgentState(*state); err != nil {
		return true, err
	}

	artifact := genericExternalChannelReviewArtifact(agent, adapterName, "", now, "wake_failed", cause.Error())
	if artifact.Metadata == nil {
		artifact.Metadata = map[string]string{}
	}
	artifact.Metadata["wake_failure_code"] = failureCode
	artifact.LocalActions = []string{"External-channel wake failed before completion; recorded backoff/suppression instead of retrying noisily."}
	artifact.Questions = []string{"Repair the recorded blocker only if there is a concrete parent/user work item for this child."}
	if _, err := durableagent.NewRuntime(r.store).QueueReviewArtifact(agent, artifact); err != nil {
		return true, fmt.Errorf("queue external wake failure review artifact: %w", err)
	}
	return true, nil
}

type durableWakeExternalChannelStateIntentPayload struct {
	AgentID      string                        `json:"agent_id"`
	Adapter      string                        `json:"adapter"`
	ResultStatus session.ChildTaskResultStatus `json:"result_status"`
	BlockerKind  string                        `json:"blocker_kind,omitempty"`
	Error        string                        `json:"error,omitempty"`
	Summary      string                        `json:"summary,omitempty"`
}

func durableWakeExternalChannelStateIntent(agent core.DurableAgent, plan durableWakeTurnPlan, status session.ChildTaskResultStatus, summary string, cause error, now time.Time) (session.ChildTaskOutcomeIntentInput, bool) {
	adapterName, _, ok := durableWakeExternalBackoffIdentity(agent)
	if !ok {
		return session.ChildTaskOutcomeIntentInput{}, false
	}
	if plan.Finalize != nil || plan.FinalizeFailure != nil || plan.OutcomeIntents != nil {
		return session.ChildTaskOutcomeIntentInput{}, false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if status == "" {
		status = session.ChildTaskResultUpdate
	}
	blockerKind := ""
	if parsedStatus, parsedBlocker := durableWakeChildTaskStatusFromStructuredSummary(summary); parsedStatus != "" {
		blockerKind = parsedBlocker
	} else if parsedStatus, parsedBlocker := durableWakeChildTaskStatusFromSummary(summary); parsedStatus != "" {
		blockerKind = parsedBlocker
	}
	if strings.TrimSpace(blockerKind) == "" {
		blockerKind = durableWakeChildBlockerFromSummary(summary)
	}
	payloadRaw, _ := json.Marshal(durableWakeExternalChannelStateIntentPayload{
		AgentID:      strings.TrimSpace(agent.AgentID),
		Adapter:      adapterName,
		ResultStatus: status,
		BlockerKind:  strings.TrimSpace(blockerKind),
		Error:        durableWakeExternalChannelStateError(status, blockerKind, summary, cause),
		Summary:      truncatePreview(strings.TrimSpace(summary), 500),
	})
	return session.ChildTaskOutcomeIntentInput{
		Kind:        session.ChildTaskOutcomeIntentExternalChannelState,
		Sequence:    15,
		PayloadJSON: string(payloadRaw),
		ResultRef:   "external_channel_state:" + strings.TrimSpace(agent.AgentID),
		CreatedAt:   now.UTC(),
	}, true
}

func durableWakeExternalChannelStateError(status session.ChildTaskResultStatus, blockerKind string, summary string, cause error) string {
	if cause != nil {
		return truncateRunes(cause.Error(), 900)
	}
	summary = truncatePreview(strings.TrimSpace(summary), 500)
	blockerKind = strings.TrimSpace(blockerKind)
	switch status {
	case session.ChildTaskResultBlocked:
		if blockerKind != "" && summary != "" {
			return truncateRunes(blockerKind+": "+summary, 900)
		}
		if blockerKind != "" {
			return blockerKind
		}
		if summary != "" {
			return summary
		}
		return "child task blocked"
	case session.ChildTaskResultFailed:
		if summary != "" {
			return summary
		}
		return "child task failed"
	default:
		return ""
	}
}

func (r *Runtime) applyDurableWakeExternalChannelStateIntent(agent core.DurableAgent, intent session.ChildTaskOutcomeIntent) error {
	if r == nil || r.store == nil {
		return nil
	}
	var payload durableWakeExternalChannelStateIntentPayload
	if err := json.Unmarshal([]byte(intent.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("decode external-channel state intent: %w", err)
	}
	agentID := firstNonEmpty(strings.TrimSpace(agent.AgentID), strings.TrimSpace(payload.AgentID))
	if agentID == "" {
		return fmt.Errorf("external-channel state intent missing agent_id")
	}
	if strings.TrimSpace(agent.AgentID) == "" {
		loaded, err := r.store.DurableAgent(agentID)
		if err != nil {
			return err
		}
		if loaded == nil {
			return fmt.Errorf("durable agent %q not found", agentID)
		}
		agent = *loaded
	}
	adapterName := firstNonEmpty(strings.TrimSpace(payload.Adapter), externalChannelAdapter(agent))
	if adapterName == "" {
		return fmt.Errorf("external-channel state intent missing adapter")
	}
	now := intent.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	state, continuity, err := loadDurableAgentContinuityFromStore(r.store, agentID)
	if err != nil {
		return err
	}
	runtimeState := externalChannelStateForAdapter(continuity, adapterName)
	switch payload.ResultStatus {
	case session.ChildTaskResultCompleted:
		runtimeState = externalChannelRecordSuccess(runtimeState, externalChannelCommandLifecycle{
			Adapter:      adapterName,
			Command:      genericExternalChannelPollCommandName,
			LastStatus:   "wake_completed",
			ResetBackoff: true,
		}, now)
	case session.ChildTaskResultBlocked:
		runtimeState = externalChannelRecordFailure(runtimeState, externalChannelCommandLifecycle{
			Adapter:    adapterName,
			Command:    genericExternalChannelPollCommandName,
			LastStatus: "wake_blocked",
			LastError:  truncateRunes(firstNonEmpty(strings.TrimSpace(payload.Error), "child task blocked"), 900),
		}, now)
	case session.ChildTaskResultFailed:
		runtimeState = externalChannelRecordFailure(runtimeState, externalChannelCommandLifecycle{
			Adapter:    adapterName,
			Command:    genericExternalChannelPollCommandName,
			LastStatus: "wake_failed",
			LastError:  truncateRunes(firstNonEmpty(strings.TrimSpace(payload.Error), "child task failed"), 900),
		}, now)
	default:
		runtimeState = externalChannelRecordAttempt(runtimeState, adapterName, genericExternalChannelPollCommandName, now)
		runtimeState.LastStatus = firstNonEmpty(strings.TrimSpace(string(payload.ResultStatus)), "wake_update")
		runtimeState.LastError = ""
		runtimeState.LastErrorAt = time.Time{}
		runtimeState.BackoffUntil = time.Time{}
	}
	if strings.EqualFold(adapterName, runtimecodex.AdapterName) {
		continuity.ExternalChannel = encodeCodexExternalChannelState(runtimeState, decodeCodexAdapterState(runtimeState.AdapterState))
	} else {
		continuity.ExternalChannel = encodeGenericExternalChannelState(runtimeState, adapterName)
	}
	raw, err := continuity.Marshal()
	if err != nil {
		return err
	}
	state.StateJSON = raw
	return r.store.SaveDurableAgentState(*state)
}

func durableWakeFailureCode(cause error) string {
	if cause == nil {
		return "unknown"
	}
	msg := strings.ToLower(strings.TrimSpace(cause.Error()))
	switch {
	case strings.Contains(msg, "child_runtime_blocked"):
		return "child_blocked"
	case strings.Contains(msg, "network is unreachable"),
		strings.Contains(msg, "no such host"),
		strings.Contains(msg, "temporary failure in name resolution"),
		strings.Contains(msg, "i/o timeout"),
		strings.Contains(msg, "connection refused"):
		return "network_unreachable"
	case strings.Contains(msg, "inference backend"),
		strings.Contains(msg, "provider"),
		strings.Contains(msg, "context window"),
		strings.Contains(msg, "stored-response"):
		return "provider_unavailable"
	case strings.Contains(msg, "sandbox"),
		strings.Contains(msg, "runner"),
		strings.Contains(msg, "executable"),
		strings.Contains(msg, "permission denied"):
		return "runtime_unavailable"
	default:
		return "transient"
	}
}
