//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
)

const genericExternalChannelWakeAdapterName = "external_channel"
const genericExternalChannelWakeChannel = "external_channel"
const genericExternalChannelPollCommandName = "external_channel.poll_due"
const genericExternalChannelWakeOutcomeMarker = "EXTERNAL_CHANNEL_OUTCOME"
const genericExternalChannelWakeOutcomeSchema = "aphelion.external_channel_wake.v1"

type genericExternalChannelWakeAdapter struct{}

func newGenericExternalChannelWakeAdapter() durableWakeIngressAdapter {
	return genericExternalChannelWakeAdapter{}
}

func (genericExternalChannelWakeAdapter) Name() string { return genericExternalChannelWakeAdapterName }

func (genericExternalChannelWakeAdapter) Supports(agent core.DurableAgent) bool {
	if strings.ToLower(strings.TrimSpace(agent.Status)) != "active" {
		return false
	}
	external := agent.ChannelConfig.ExternalConfig()
	if external == nil || strings.TrimSpace(external.Adapter) == "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(external.Adapter), codexAppServerAdapterName) {
		return false
	}
	mode := strings.TrimSpace(agent.WakeupMode)
	return mode == "" || strings.EqualFold(mode, "poll")
}

func (genericExternalChannelWakeAdapter) Prepare(_ context.Context, rt *Runtime, agent core.DurableAgent, now time.Time) (*durableWakeTurnPlan, error) {
	if rt == nil || rt.store == nil {
		return nil, fmt.Errorf("external channel adapter runtime is unavailable")
	}
	external := agent.ChannelConfig.ExternalConfig()
	if external == nil {
		return nil, fmt.Errorf("external channel adapter requires external channel_config")
	}
	adapterName := externalChannelAdapter(agent)
	if adapterName == "" {
		return nil, fmt.Errorf("external channel adapter requires channel_config.external.adapter")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	state, continuity, err := loadDurableAgentContinuityFromStore(rt.store, agent.AgentID)
	if err != nil {
		return nil, err
	}
	runtimeState := externalChannelStateForAdapter(continuity, adapterName)
	if !externalChannelPollDue(runtimeState, strings.TrimSpace(external.PollInterval), now) {
		return nil, nil
	}
	runtimeState = externalChannelRecordAttempt(runtimeState, adapterName, genericExternalChannelPollCommandName, now)
	runtimeState.LastStatus = "wake_started"
	runtimeState.LastError = ""
	continuity.ExternalChannel = encodeGenericExternalChannelState(runtimeState, adapterName)
	raw, err := continuity.Marshal()
	if err != nil {
		return nil, err
	}
	state.StateJSON = raw
	if err := rt.store.SaveDurableAgentState(*state); err != nil {
		return nil, err
	}

	key := session.SessionKey{ChatID: durableWakeSyntheticChatID(agent.AgentID), Scope: durableAgentScopeRef(agent)}
	return &durableWakeTurnPlan{
		Channel:      genericExternalChannelWakeChannel,
		AuditChannel: genericExternalChannelWakeChannel,
		Key:          key,
		Inbound: core.InboundMessage{
			ChatID:         key.ChatID,
			ChatType:       genericExternalChannelWakeChannel,
			ChatTitle:      "external-channel",
			SenderName:     "external_channel",
			Text:           genericExternalChannelWakePrompt(agent, *external, now),
			MessageID:      durableWakeMessageID(now),
			DurableAgentID: strings.TrimSpace(agent.AgentID),
			Timestamp:      now,
		},
		SessionChatType:      genericExternalChannelWakeChannel,
		SessionUserName:      "external_channel",
		PromptContextErrHint: "load external channel durable wake prompt context",
		PolicyReason:         "mapped from generic external_channel durable-agent adapter command dispatch",
		PersistenceErrCtx: turnCommitErrorContext{
			ConvertMessages: "convert external channel durable wake messages",
			LoadPlanState:   "load external channel durable wake plan state before save",
			LoadOperation:   "load external channel durable wake operation state before save",
			SaveSession:     "save external channel durable wake session",
			RecordOutbound:  "record external channel durable wake outbound reply",
		},
		SendErrCtx:   "send external channel durable wake reply",
		RecordErrCtx: "record external channel durable wake outbound reply",
		GovernorContext: func(agent core.DurableAgent, policy core.DurableAgentLivePolicy, _ core.InboundMessage, pending []core.DurableAgentConversationMessage) string {
			lines := []string{
				"You are handling a durable-agent wake from the generic external_channel adapter dispatcher.",
				"The parent runtime only determined that a configured external-channel poll is due; it did not execute channel-specific work.",
				"Use only this child's charter, policy, and explicitly available tools/grants to perform or decline the adapter-local work.",
				"If the needed adapter/tool/runtime material is unavailable, say what is blocked and what exact grant/materialization is missing.",
				"Do not claim external-channel reads, writes, sends, deletes, archive actions, or attachment/body access unless actually performed by an authorized child-local tool this turn.",
				"Finish with exactly one typed outcome line shaped like: EXTERNAL_CHANNEL_OUTCOME: {\"schema_version\":\"aphelion.external_channel_wake.v1\",\"status\":\"blocked\",\"reason_code\":\"missing_grant\",\"adapter\":\"" + adapterName + "\",\"agent_id\":\"" + strings.TrimSpace(agent.AgentID) + "\",\"error\":\"...\",\"evidence_refs\":[]}.",
				"Set status to exactly completed only if authorized adapter-local work actually completed; otherwise set status to exactly blocked.",
			}
			if charter := strings.TrimSpace(policy.Charter); charter != "" {
				lines = append(lines, "Charter: "+charter)
			}
			lines = append(lines,
				"Durable agent id: "+strings.TrimSpace(agent.AgentID),
				"External adapter: "+adapterName,
				"External query label: "+strings.TrimSpace(external.Query),
				"Poll interval: "+strings.TrimSpace(external.PollInterval),
			)
			lines = append(lines, durableParentConversationGovernorLines(pending)...)
			return strings.Join(lines, "\n")
		},
		Finalize: func(turnSummary string) error {
			return finalizeGenericExternalChannelWake(rt, agent, adapterName, turnSummary, now)
		},
		FinalizeFailure: func(turnSummary string, cause error) error {
			return finalizeGenericExternalChannelWakeFailure(rt, agent, adapterName, turnSummary, cause, now)
		},
	}, nil
}

func genericExternalChannelWakePrompt(agent core.DurableAgent, external core.DurableAgentExternalChannelConfig, now time.Time) string {
	lines := []string{
		"External-channel poll is due.",
		"Agent: " + strings.TrimSpace(agent.AgentID),
		"Adapter: " + strings.TrimSpace(external.Adapter),
		"Poll interval: " + strings.TrimSpace(external.PollInterval),
		"Scheduled at: " + now.UTC().Format(time.RFC3339),
		"Handle this as a child-local adapter command within the current charter and grants.",
		"If the adapter/tool/runtime grant is missing, report the blocker instead of improvising parent authority.",
		"End with one typed outcome line shaped like: EXTERNAL_CHANNEL_OUTCOME: {\"schema_version\":\"aphelion.external_channel_wake.v1\",\"status\":\"blocked\",\"reason_code\":\"missing_grant\",\"adapter\":\"" + strings.TrimSpace(external.Adapter) + "\",\"agent_id\":\"" + strings.TrimSpace(agent.AgentID) + "\",\"error\":\"...\",\"evidence_refs\":[]}. Set status to exactly completed only after authorized adapter-local work actually completed; otherwise set status to exactly blocked.",
	}
	if query := strings.TrimSpace(external.Query); query != "" {
		lines = append(lines, "Configured query/selector: "+query)
	}
	if len(external.SurfaceRules) > 0 {
		lines = append(lines, "Surface rules: "+strings.Join(external.SurfaceRules, ", "))
	}
	return strings.Join(lines, "\n")
}

func finalizeGenericExternalChannelWake(rt *Runtime, agent core.DurableAgent, adapterName string, turnSummary string, now time.Time) error {
	if rt == nil || rt.store == nil {
		return nil
	}
	outcome := genericExternalChannelWakeOutcomeFromSummary(turnSummary)
	if !outcome.Completed {
		return finalizeGenericExternalChannelWakeFailureWithOutcome(rt, agent, adapterName, turnSummary, errors.New(outcome.Error), outcome, now)
	}
	state, continuity, err := loadDurableAgentContinuityFromStore(rt.store, agent.AgentID)
	if err != nil {
		return err
	}
	runtimeState := externalChannelStateForAdapter(continuity, adapterName)
	runtimeState = externalChannelRecordSuccess(runtimeState, externalChannelCommandLifecycle{
		Adapter:      adapterName,
		Command:      genericExternalChannelPollCommandName,
		LastStatus:   "wake_completed",
		ResetBackoff: true,
	}, now)
	continuity.ExternalChannel = encodeGenericExternalChannelState(runtimeState, adapterName)
	raw, err := continuity.Marshal()
	if err != nil {
		return err
	}
	state.StateJSON = raw
	if err := rt.store.SaveDurableAgentState(*state); err != nil {
		return err
	}
	artifact := genericExternalChannelReviewArtifactWithOutcome(agent, adapterName, turnSummary, now, "wake_completed", "", outcome)
	artifact.LocalActions = []string{"External-channel wake completed after child reported authorized adapter-local work completed."}
	if _, err := durableagent.NewRuntime(rt.store).QueueReviewArtifact(agent, artifact); err != nil {
		return fmt.Errorf("queue external channel wake review artifact: %w", err)
	}
	return nil
}

func finalizeGenericExternalChannelWakeFailure(rt *Runtime, agent core.DurableAgent, adapterName string, turnSummary string, cause error, now time.Time) error {
	outcome := genericExternalChannelWakeOutcome{
		Completed:  false,
		Status:     "wake_blocked",
		Error:      errorText(cause),
		Source:     "runtime_error",
		Schema:     genericExternalChannelWakeOutcomeSchema,
		Adapter:    strings.TrimSpace(adapterName),
		AgentID:    strings.TrimSpace(agent.AgentID),
		ReasonCode: "runtime_error",
	}
	return finalizeGenericExternalChannelWakeFailureWithOutcome(rt, agent, adapterName, turnSummary, cause, outcome, now)
}

func finalizeGenericExternalChannelWakeFailureWithOutcome(rt *Runtime, agent core.DurableAgent, adapterName string, turnSummary string, cause error, outcome genericExternalChannelWakeOutcome, now time.Time) error {
	if rt == nil || rt.store == nil {
		return nil
	}
	if cause == nil {
		cause = fmt.Errorf("external channel wake did not complete")
	}
	state, continuity, err := loadDurableAgentContinuityFromStore(rt.store, agent.AgentID)
	if err != nil {
		return err
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
		return err
	}
	state.StateJSON = raw
	if err := rt.store.SaveDurableAgentState(*state); err != nil {
		return err
	}
	if strings.TrimSpace(outcome.Error) == "" {
		outcome.Error = cause.Error()
	}
	if strings.TrimSpace(outcome.Status) == "" {
		outcome.Status = "wake_blocked"
	}
	artifact := genericExternalChannelReviewArtifactWithOutcome(agent, adapterName, turnSummary, now, "wake_blocked", cause.Error(), outcome)
	artifact.LocalActions = []string{"External-channel wake blocked; recorded explicit failure/backoff instead of success."}
	if _, err := durableagent.NewRuntime(rt.store).QueueReviewArtifact(agent, artifact); err != nil {
		return fmt.Errorf("queue external channel wake failure review artifact: %w", err)
	}
	return nil
}

func genericExternalChannelReviewArtifact(agent core.DurableAgent, adapterName string, turnSummary string, now time.Time, status string, errorText string) core.DurableReviewArtifact {
	return genericExternalChannelReviewArtifactWithOutcome(agent, adapterName, turnSummary, now, status, errorText, genericExternalChannelWakeOutcome{
		Status:     status,
		Error:      errorText,
		Source:     "runtime",
		Schema:     genericExternalChannelWakeOutcomeSchema,
		Adapter:    strings.TrimSpace(adapterName),
		AgentID:    strings.TrimSpace(agent.AgentID),
		ReasonCode: "",
	})
}

func genericExternalChannelReviewArtifactWithOutcome(agent core.DurableAgent, adapterName string, turnSummary string, now time.Time, status string, errorText string, outcome genericExternalChannelWakeOutcome) core.DurableReviewArtifact {
	metadata := map[string]string{
		"channel_kind":            strings.TrimSpace(agent.ChannelKind),
		"channel_adapter":         adapterName,
		"trigger_kinds":           "external_channel,poll_due",
		"child_local_subject":     "false",
		"external_channel_status": status,
		"status":                  status,
		"status_source":           "external_channel_status",
		"wake_outcome_schema":     firstNonEmpty(outcome.Schema, genericExternalChannelWakeOutcomeSchema),
		"wake_outcome_source":     firstNonEmpty(outcome.Source, "runtime"),
		"wake_outcome_status":     firstNonEmpty(outcome.Status, status),
	}
	if strings.TrimSpace(outcome.ReasonCode) != "" {
		metadata["wake_outcome_reason_code"] = strings.TrimSpace(outcome.ReasonCode)
	}
	if strings.TrimSpace(outcome.Adapter) != "" {
		metadata["wake_outcome_adapter"] = strings.TrimSpace(outcome.Adapter)
	}
	if strings.TrimSpace(outcome.AgentID) != "" {
		metadata["wake_outcome_agent_id"] = strings.TrimSpace(outcome.AgentID)
	}
	if strings.TrimSpace(outcome.GrantID) != "" {
		metadata["grant_id"] = strings.TrimSpace(outcome.GrantID)
	}
	if len(outcome.EvidenceRefs) > 0 {
		metadata["wake_outcome_evidence_refs"] = strings.Join(outcome.EvidenceRefs, ",")
	}
	if strings.TrimSpace(errorText) != "" {
		metadata["external_channel_error"] = truncateRunes(errorText, 900)
	}
	applyChildRuntimeBlockOperatorMetadata(metadata, agent, adapterName, errorText)
	return core.DurableReviewArtifact{
		AgentID:       strings.TrimSpace(agent.AgentID),
		Summary:       genericExternalChannelReviewSummary(agent, adapterName, turnSummary, status, errorText),
		IntervalLabel: now.UTC().Format(time.RFC3339),
		RiskFlags:     []string{"external_channel", "adapter_dispatch"},
		Metadata:      metadata,
	}
}

type genericExternalChannelWakeOutcome struct {
	Completed    bool
	Status       string
	ReasonCode   string
	Error        string
	Adapter      string
	AgentID      string
	GrantID      string
	EvidenceRefs []string
	Schema       string
	Source       string
}

type genericExternalChannelWakeOutcomeContract struct {
	SchemaVersion string   `json:"schema_version"`
	Status        string   `json:"status"`
	ReasonCode    string   `json:"reason_code,omitempty"`
	Adapter       string   `json:"adapter,omitempty"`
	AgentID       string   `json:"agent_id,omitempty"`
	GrantID       string   `json:"grant_id,omitempty"`
	Error         string   `json:"error,omitempty"`
	EvidenceRefs  []string `json:"evidence_refs,omitempty"`
}

func genericExternalChannelWakeOutcomeFromSummary(turnSummary string) genericExternalChannelWakeOutcome {
	if contract, ok := extractGenericExternalChannelOutcomeContract(turnSummary); ok {
		outcome := genericExternalChannelWakeOutcome{
			Status:       normalizeExternalChannelWakeOutcomeStatus(contract.Status),
			ReasonCode:   normalizeExternalChannelWakeOutcomeReason(contract.ReasonCode),
			Error:        strings.TrimSpace(contract.Error),
			Adapter:      strings.TrimSpace(contract.Adapter),
			AgentID:      strings.TrimSpace(contract.AgentID),
			GrantID:      strings.TrimSpace(contract.GrantID),
			EvidenceRefs: normalizeExternalChannelWakeEvidenceRefs(contract.EvidenceRefs),
			Schema:       firstNonEmpty(strings.TrimSpace(contract.SchemaVersion), genericExternalChannelWakeOutcomeSchema),
			Source:       "typed_outcome",
		}
		switch outcome.Status {
		case "wake_completed":
			outcome.Completed = true
			return outcome
		case "wake_blocked":
			if outcome.Error == "" {
				outcome.Error = firstNonEmpty(outcome.ReasonCode, "external channel child reported blocked status")
			}
			return outcome
		default:
			outcome.Status = "wake_blocked"
			outcome.ReasonCode = firstNonEmpty(outcome.ReasonCode, "invalid_typed_outcome_status")
			outcome.Error = "external channel typed outcome status missing or invalid; not marking poll as successful"
			return outcome
		}
	}
	return genericExternalChannelWakeOutcome{Completed: false, Status: "wake_blocked", ReasonCode: "missing_outcome", Error: "external channel typed outcome missing; not marking poll as successful", Schema: genericExternalChannelWakeOutcomeSchema, Source: "missing_outcome"}
}

func extractGenericExternalChannelOutcomeContract(text string) (genericExternalChannelWakeOutcomeContract, bool) {
	raw := extractGenericExternalChannelStatusLine(text, genericExternalChannelWakeOutcomeMarker)
	if raw == "" {
		return genericExternalChannelWakeOutcomeContract{}, false
	}
	var contract genericExternalChannelWakeOutcomeContract
	if err := json.Unmarshal([]byte(raw), &contract); err != nil {
		return genericExternalChannelWakeOutcomeContract{
			SchemaVersion: genericExternalChannelWakeOutcomeSchema,
			Status:        "blocked",
			ReasonCode:    "invalid_typed_outcome_json",
			Error:         "external channel typed outcome JSON was invalid",
		}, true
	}
	return contract, true
}

func normalizeExternalChannelWakeOutcomeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "complete", "ok", "success", "wake_completed":
		return "wake_completed"
	case "blocked", "blocker", "failed", "failure", "error", "unavailable", "wake_blocked":
		return "wake_blocked"
	default:
		return ""
	}
}

func normalizeExternalChannelWakeOutcomeReason(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	replacer := strings.NewReplacer("-", "_", " ", "_", "/", "_", ".", "_")
	reason = replacer.Replace(reason)
	for strings.Contains(reason, "__") {
		reason = strings.ReplaceAll(reason, "__", "_")
	}
	return strings.Trim(reason, "_")
}

func normalizeExternalChannelWakeEvidenceRefs(refs []string) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref != "" {
			out = append(out, ref)
		}
	}
	return out
}

func extractGenericExternalChannelStatusLine(text string, key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, line := range strings.Split(text, "\n") {
		left, right, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.ToLower(strings.TrimSpace(left)) == key {
			return strings.TrimSpace(right)
		}
	}
	return ""
}

func genericExternalChannelReviewSummary(agent core.DurableAgent, adapterName string, turnSummary string, status string, errorText string) string {
	if block, ok := classifyDurableWakeChildRuntimeBlockErrorText(errorText); ok && block.Reason == "grant_expired" {
		agentName := durableAgentDisplayName(agent.AgentID)
		grantLabel := childRuntimeGrantLabel(block, adapterName)
		return fmt.Sprintf("%s wake paused: %s expired.", agentName, grantLabel)
	}
	parts := []string{
		fmt.Sprintf("External-channel wake %s from child %s via adapter %s.", externalChannelReviewStatusPhrase(status), strings.TrimSpace(agent.AgentID), strings.TrimSpace(adapterName)),
	}
	if trimmed := strings.TrimSpace(turnSummary); trimmed != "" {
		parts = append(parts, truncateRunes(trimmed, 900))
	}
	if strings.TrimSpace(errorText) != "" {
		parts = append(parts, "Error: "+truncateRunes(strings.TrimSpace(errorText), 300))
	}
	return strings.Join(parts, " ")
}

func externalChannelReviewStatusPhrase(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	status = strings.ReplaceAll(status, "-", "_")
	switch status {
	case "wake_completed", "completed", "complete", "success", "succeeded", "ok":
		return "completed"
	case "wake_blocked", "blocked", "blocker", "refused", "unavailable":
		return "blocked"
	default:
		if status == "" {
			return "updated"
		}
		return strings.ReplaceAll(status, "_", " ")
	}
}

func applyChildRuntimeBlockOperatorMetadata(metadata map[string]string, agent core.DurableAgent, adapterName string, errorText string) {
	if metadata == nil {
		return
	}
	block, ok := classifyDurableWakeChildRuntimeBlockErrorText(errorText)
	if !ok {
		return
	}
	metadata["child_runtime_block_reason"] = block.Reason
	if block.GrantID != "" {
		metadata["grant_id"] = block.GrantID
	}
	grantLabel := childRuntimeGrantLabel(block, adapterName)
	if grantLabel != "" {
		metadata["grant_label"] = grantLabel
	}
	agentName := durableAgentDisplayName(agent.AgentID)
	switch block.Reason {
	case "grant_expired":
		metadata["operator_status"] = "paused"
		metadata["operator_title"] = agentName + " wake paused"
		metadata["operator_summary"] = fmt.Sprintf("The %s expired, so %s did not wake.", grantLabel, agentName)
		metadata["operator_point"] = "Backoff is recorded; no retry loop is running."
		metadata["operator_action"] = "no_action_unless_work_item"
		metadata["operator_next_action"] = fmt.Sprintf("Renew the grant only if %s has a concrete parent/user work item.", agentName)
	default:
		metadata["operator_title"] = agentName + " wake blocked"
		metadata["operator_summary"] = fmt.Sprintf("%s did not wake because the child runtime blocked access.", agentName)
	}
}

type durableWakeChildRuntimeBlock struct {
	Reason  string
	GrantID string
}

func classifyDurableWakeChildRuntimeBlockError(err error) (durableWakeChildRuntimeBlock, bool) {
	if err == nil {
		return durableWakeChildRuntimeBlock{}, false
	}
	return classifyDurableWakeChildRuntimeBlockErrorText(err.Error())
}

func classifyDurableWakeChildRuntimeBlockErrorText(raw string) (durableWakeChildRuntimeBlock, bool) {
	text := strings.ToLower(strings.TrimSpace(raw))
	if text == "" {
		return durableWakeChildRuntimeBlock{}, false
	}
	if !strings.Contains(text, "child_runtime_blocked:") && !strings.Contains(text, "grant_expired") {
		return durableWakeChildRuntimeBlock{}, false
	}
	block := durableWakeChildRuntimeBlock{GrantID: extractChildRuntimeBlockGrantID(raw)}
	switch {
	case strings.Contains(text, "grant_expired"):
		block.Reason = "grant_expired"
	case strings.Contains(text, "grant_revoked"):
		block.Reason = "grant_revoked"
	case strings.Contains(text, "grant_policy_hash_mismatch"):
		block.Reason = "grant_policy_hash_mismatch"
	case strings.Contains(text, "grant_stale"):
		block.Reason = "grant_stale"
	case strings.Contains(text, "grant_missing"), strings.Contains(text, "grant_not_found"):
		block.Reason = "grant_missing"
	default:
		block.Reason = "child_runtime_blocked"
	}
	return block, true
}

func extractChildRuntimeBlockGrantID(raw string) string {
	for _, field := range strings.Fields(strings.TrimSpace(raw)) {
		field = strings.Trim(field, " ,.;")
		if value, ok := strings.CutPrefix(field, "grant_id="); ok {
			return strings.Trim(value, " ,.;")
		}
	}
	return ""
}

func durableAgentDisplayName(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "Child"
	}
	parts := strings.FieldsFunc(agentID, func(r rune) bool { return r == '-' || r == '_' || r == '.' })
	if len(parts) == 0 {
		return agentID
	}
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		switch strings.ToLower(part) {
		case "id":
			parts[i] = "ID"
		case "api":
			parts[i] = "API"
		default:
			runes := []rune(strings.ToLower(part))
			if len(runes) > 0 {
				runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
			}
			parts[i] = string(runes)
		}
	}
	return strings.Join(parts, " ")
}

func childRuntimeGrantLabel(block durableWakeChildRuntimeBlock, adapterName string) string {
	adapter := externalChannelAdapterDisplayName(adapterName)
	if adapter == "" {
		adapter = "child runtime"
	}
	if strings.Contains(strings.ToLower(block.GrantID), "heartbeat") {
		return adapter + " heartbeat grant"
	}
	return adapter + " grant"
}

func externalChannelAdapterDisplayName(adapterName string) string {
	adapterName = strings.ToLower(strings.TrimSpace(adapterName))
	switch adapterName {
	case "codex_app_server":
		return "Codex app-server"
	case "codex_image_generation":
		return "Codex image-generation"
	default:
		return strings.ReplaceAll(adapterName, "_", " ")
	}
}

func encodeGenericExternalChannelState(runtimeState core.DurableAgentExternalChannelRuntimeState, adapterName string) *core.DurableAgentExternalChannelRuntimeState {
	runtimeState.Adapter = strings.ToLower(strings.TrimSpace(adapterName))
	return core.NormalizeDurableAgentContinuityState(core.DurableAgentContinuityState{ExternalChannel: &runtimeState}).ExternalChannel
}
