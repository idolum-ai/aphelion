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
	if strings.TrimSpace(agent.ChannelKind) == scheduledReviewChannelKind && agent.ChannelConfig.ScheduledReviewConfig() != nil {
		return r.shouldSuppressScheduledReviewChildPoll(agent, now)
	}
	adapterName, pollInterval, ok := durableWakeExternalBackoffIdentity(agent)
	if !ok {
		return false, nil
	}
	_, continuity, err := loadDurableAgentContinuityFromStore(r.store, agent.AgentID)
	if err != nil {
		return false, err
	}
	runtimeState := externalChannelStateForAdapter(continuity, adapterName)
	if !runtimeState.BackoffUntil.IsZero() && now.UTC().Before(runtimeState.BackoffUntil.UTC()) {
		return true, nil
	}
	pending, err := r.pendingDurableAgentParentConversation(agent.AgentID, 1)
	if err != nil {
		return false, err
	}
	if len(pending) > 0 {
		return false, nil
	}
	if agent.ChannelConfig.ExternalConfig() == nil {
		return true, nil
	}
	return !externalChannelPollDue(runtimeState, pollInterval, now), nil
}

func (r *Runtime) recordDurableWakeChildRuntimeBlock(agent core.DurableAgent, cause error, now time.Time) (bool, error) {
	if r == nil || r.store == nil || cause == nil {
		return false, nil
	}
	block, ok := classifyDurableWakeChildRuntimeBlockError(cause)
	if !ok {
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
	applyExternalChannelReadinessRepairArtifact(&artifact, agent, externalChannelReadinessForArtifact(cause, adapterName))
	if artifact.Metadata == nil || strings.TrimSpace(artifact.Metadata["external_channel_repair_kind"]) == "" {
		if block.Reason == "grant_expired" {
			artifact.LocalActions = []string{"Wake held because a required child-runtime grant expired; recorded backoff and queued admin review instead of retrying or auto-renewing."}
			artifact.Questions = []string{"Approve renewal or replacement of the required child-runtime grant only if this child should wake; otherwise leave it expired."}
		} else {
			artifact.LocalActions = []string{"Child runtime wake blocked by capability/grant state; recorded backoff/suppression instead of retrying noisily."}
			artifact.Questions = []string{"Only renew or create the required grant when there is a concrete parent/user work item."}
		}
	}
	if _, err := durableagent.NewRuntime(r.store).QueueReviewArtifact(agent, artifact); err != nil {
		return true, fmt.Errorf("queue child-runtime blocked wake review artifact: %w", err)
	}
	return true, nil
}

func externalChannelReadinessForArtifact(cause error, adapterName string) externalChannelAdapterReadiness {
	readiness, ok := externalChannelReadinessFromError(cause)
	if ok {
		return readiness
	}
	return externalChannelAdapterReadiness{Adapter: strings.TrimSpace(adapterName), RepairKind: externalChannelReadinessRepairNone}
}

func applyExternalChannelReadinessRepairArtifact(artifact *core.DurableReviewArtifact, agent core.DurableAgent, readiness externalChannelAdapterReadiness) {
	if artifact == nil {
		return
	}
	repairKind := strings.TrimSpace(readiness.RepairKind)
	if repairKind == "" || repairKind == externalChannelReadinessRepairNone {
		return
	}
	switch repairKind {
	case externalChannelReadinessRepairToolLifecycleVerify,
		externalChannelReadinessRepairToolLifecycleRefresh,
		externalChannelReadinessRepairChildRuntimeMaterial:
	default:
		return
	}
	adapterName := firstNonEmpty(strings.TrimSpace(readiness.Adapter), externalChannelAdapter(agent))
	if artifact.Metadata == nil {
		artifact.Metadata = map[string]string{}
	}
	artifact.Metadata["external_channel_repair_kind"] = repairKind
	artifact.Metadata["external_channel_failure_code"] = strings.TrimSpace(readiness.FailureCode)
	artifact.Metadata["external_channel_next_repair"] = strings.TrimSpace(readiness.NextRepair)
	artifact.Metadata["tool_name"] = adapterName
	artifact.Metadata["target_agent_id"] = strings.TrimSpace(agent.AgentID)
	switch repairKind {
	case externalChannelReadinessRepairToolLifecycleVerify:
		artifact.LocalActions = []string{
			fmt.Sprintf("Scheduled wake held because %s has installed status with passed audit/probe evidence but is not verified.", adapterName),
			fmt.Sprintf("Run tool_authority install_set status=verified for %s only if the existing runtime-authored audit_run and probe_run evidence is fresh.", adapterName),
		}
		artifact.Questions = []string{
			fmt.Sprintf("Approve exact lifecycle verification for %s: tool_authority install_set tool_name=%s status=verified.", adapterName, adapterName),
		}
	case externalChannelReadinessRepairToolLifecycleRefresh:
		artifact.LocalActions = []string{
			fmt.Sprintf("Scheduled wake held because %s lifecycle evidence is missing, stale, failed, or incomplete.", adapterName),
			fmt.Sprintf("Run bounded tool_authority audit_run and probe_run for %s, then verify with install_set status=verified before waking the child.", adapterName),
		}
		artifact.Questions = []string{
			fmt.Sprintf("Approve exact lifecycle refresh for %s: audit_run, probe_run, then install_set verified; no mailbox content or mutation.", adapterName),
		}
	case externalChannelReadinessRepairChildRuntimeMaterial:
		artifact.LocalActions = []string{
			fmt.Sprintf("Scheduled wake held because durable_agent:%s lacks active child_runtime material for tool grant %s.", strings.TrimSpace(agent.AgentID), adapterName),
			fmt.Sprintf("Create or repair the active %s tool grant with child_runtime material; do not print secret values.", adapterName),
		}
		artifact.Questions = []string{
			fmt.Sprintf("Approve exact runtime materialization for durable_agent:%s: active tool grant target=%s with child_runtime paths/env needed by the child.", strings.TrimSpace(agent.AgentID), adapterName),
		}
	}
}
