//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
)

func (r *Runtime) shouldSuppressDurableWakeChildPoll(agent core.DurableAgent, now time.Time) (bool, error) {
	if r == nil || r.store == nil {
		return false, nil
	}
	external := agent.ChannelConfig.ExternalConfig()
	if external == nil {
		return false, nil
	}
	adapterName := externalChannelAdapter(agent)
	if adapterName == "" {
		return false, nil
	}
	pending, err := r.pendingDurableAgentParentConversation(agent.AgentID, 1)
	if err != nil {
		return false, err
	}
	if len(pending) > 0 {
		return false, nil
	}
	_, continuity, err := loadDurableAgentContinuityFromStore(r.store, agent.AgentID)
	if err != nil {
		return false, err
	}
	runtimeState := externalChannelStateForAdapter(continuity, adapterName)
	return !externalChannelPollDue(runtimeState, strings.TrimSpace(external.PollInterval), now), nil
}

func (r *Runtime) recordDurableWakeChildRuntimeBlock(agent core.DurableAgent, cause error, now time.Time) (bool, error) {
	if r == nil || r.store == nil || cause == nil {
		return false, nil
	}
	block, ok := classifyDurableWakeChildRuntimeBlockError(cause)
	if !ok {
		return false, nil
	}
	external := agent.ChannelConfig.ExternalConfig()
	if external == nil {
		return false, nil
	}
	adapterName := externalChannelAdapter(agent)
	if adapterName == "" {
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
	runtimeState = externalChannelRecordFailure(runtimeState, externalChannelCommandLifecycle{
		Adapter:    adapterName,
		Command:    genericExternalChannelPollCommandName,
		LastStatus: "wake_blocked",
		LastError:  truncateRunes(cause.Error(), 900),
	}, now)
	continuity.ExternalChannel = encodeGenericExternalChannelState(runtimeState, adapterName)
	raw, err := continuity.Marshal()
	if err != nil {
		return true, err
	}
	state.StateJSON = raw
	if err := r.store.SaveDurableAgentState(*state); err != nil {
		return true, err
	}
	artifact := genericExternalChannelReviewArtifact(agent, adapterName, "", now, "wake_blocked", cause.Error())
	if block.Reason == "grant_expired" {
		artifact.LocalActions = []string{"Backoff is recorded; no retry loop is running."}
		artifact.Questions = []string{"Renew the grant only if there is a concrete parent/user work item."}
	} else {
		artifact.LocalActions = []string{"Child runtime wake blocked by capability/grant state; recorded backoff/suppression instead of retrying noisily."}
		artifact.Questions = []string{"Only renew or create the required grant when there is a concrete parent/user work item."}
	}
	if _, err := durableagent.NewRuntime(r.store).QueueReviewArtifact(agent, artifact); err != nil {
		return true, fmt.Errorf("queue child-runtime blocked wake review artifact: %w", err)
	}
	return true, nil
}

func durableWakeChildRuntimeBlockedError(err error) bool {
	_, ok := classifyDurableWakeChildRuntimeBlockError(err)
	return ok
}
