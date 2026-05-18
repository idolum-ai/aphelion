//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
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
	if strings.EqualFold(adapterName, codexAppServerAdapterName) {
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
