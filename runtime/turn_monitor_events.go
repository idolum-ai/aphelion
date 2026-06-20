//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

func (m *turnMonitor) ToolStarted(ctx context.Context, name string, input json.RawMessage) {
	startedAt := time.Now().UTC()
	preview := safeToolInputPreview(input)
	if m.audit != nil {
		m.audit.ToolStarted(name, preview)
	}
	if m.runID != 0 {
		if err := m.runtime.store.NoteTurnRunToolStart(m.runID, name, preview); err != nil {
			if m.runtime.expectedShutdownNoise(ctx, err) {
				log.Printf("INFO suppressing expected shutdown tool-start note failure id=%d tool=%s err=%v", m.runID, name, err)
			} else {
				log.Printf("WARN note turn run tool start id=%d tool=%s err=%v", m.runID, name, err)
			}
		}
	}
	if m.toolStarts != nil {
		m.toolStartsMu.Lock()
		m.toolStarts[toolDurationKey(name, input)] = append(m.toolStarts[toolDurationKey(name, input)], startedAt)
		m.toolStartsMu.Unlock()
	}
	payload := map[string]any{
		"run_id":  m.runID,
		"tool":    strings.TrimSpace(name),
		"preview": preview,
	}
	if effect := execEffectPayload(name, input); len(effect) > 0 {
		payload["exec_effect"] = effect
	}
	m.recordExecEffectAttempt(name, input, session.EffectAttemptStatusAttempted, "", startedAt)
	m.runtime.recordExecutionEvent(m.key, core.ExecutionEventToolStarted, "tool", "started", payload, startedAt)
	if m.progress != nil {
		m.progress.ToolStarted(ctx, name, input)
	}
}

func (m *turnMonitor) ToolFinished(ctx context.Context, name string, input json.RawMessage, output string, err error) {
	preview := safeToolInputPreview(input)
	resultPreview := redactRuntimeEvidenceText(truncatePreview(strings.TrimSpace(output), 220))
	errorText := ""
	if err != nil {
		errorText = redactRuntimeEvidenceText(trimError(err.Error()))
	}
	if m.audit != nil {
		m.audit.ToolFinished(name, preview, resultPreview, errorText)
	}
	if m.runID != 0 {
		if storeErr := m.runtime.store.NoteTurnRunToolFinish(m.runID, resultPreview, errorText); storeErr != nil {
			if m.runtime.expectedShutdownNoise(ctx, storeErr) {
				log.Printf("INFO suppressing expected shutdown tool-finish note failure id=%d tool=%s err=%v", m.runID, name, storeErr)
			} else {
				log.Printf("WARN note turn run tool finish id=%d tool=%s err=%v", m.runID, name, storeErr)
			}
		}
	}
	eventType := core.ExecutionEventToolSucceeded
	status := "succeeded"
	if err != nil {
		eventType = core.ExecutionEventToolFailed
		status = "failed"
	}
	toolDurationMS := int64(0)
	if m.toolStarts != nil {
		key := toolDurationKey(name, input)
		m.toolStartsMu.Lock()
		starts := m.toolStarts[key]
		if len(starts) > 0 {
			startedAt := starts[0]
			toolDurationMS = elapsedMillisSince(startedAt)
			if len(starts) == 1 {
				delete(m.toolStarts, key)
			} else {
				m.toolStarts[key] = starts[1:]
			}
		}
		m.toolStartsMu.Unlock()
	}
	payload := map[string]any{
		"run_id":           m.runID,
		"tool":             strings.TrimSpace(name),
		"preview":          preview,
		"result_preview":   resultPreview,
		"error":            errorText,
		"tool_duration_ms": toolDurationMS,
	}
	if digest, ok := agent.BuildToolOutputDigest(output, agent.DefaultToolOutputDigestInlineLimit); ok {
		safe := redactLargeToolOutputEvidence(preview, output, digest)
		if ref := m.recordLargeToolOutputEvidence(name, safe, time.Now().UTC()); ref != "" {
			digest.EvidenceRef = ref
		}
		digest.Head = safe.Digest.Head
		digest.Tail = safe.Digest.Tail
		payload["result_digest"] = digest.Payload()
	}
	if effect := execEffectPayload(name, input); len(effect) > 0 {
		payload["exec_effect"] = effect
	}
	statusForAttempt := session.EffectAttemptStatusExecuted
	if err != nil && errors.Is(err, toolpkg.ErrExecRejectedBeforeDispatch) {
		statusForAttempt = session.EffectAttemptStatusRejected
	} else if err != nil && execEffectHasSideEffects(name, input) {
		statusForAttempt = session.EffectAttemptStatusUncertain
	} else if err != nil {
		statusForAttempt = session.EffectAttemptStatusFailed
	}
	m.recordExecEffectAttempt(name, input, statusForAttempt, errorText, time.Now().UTC())
	m.runtime.recordExecutionEvent(m.key, eventType, "tool", status, payload, time.Now().UTC())
	if m.progress != nil {
		m.progress.ToolFinished(ctx, name, err)
	}
}

func (m *turnMonitor) recordExecEffectAttempt(name string, input json.RawMessage, status session.EffectAttemptStatus, errorText string, observedAt time.Time) {
	if m == nil || m.runtime == nil || m.runtime.store == nil || m.runID == 0 {
		return
	}
	effect := execEffectPayload(name, input)
	if len(effect) == 0 {
		return
	}
	rawCommand := execRawCommand(name, input)
	if rawCommand == "" {
		return
	}
	command := redactRuntimeEvidenceText(commandeffect.NormalizeCommand(rawCommand))
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	boundaryKind := ""
	if boundary, ok := commandeffect.BoundaryForCommand(rawCommand); ok {
		boundaryKind = string(boundary.Kind)
	}
	subject := effectAttemptSubjectJSON(rawCommand)
	completedAt := time.Time{}
	if session.NormalizeEffectAttemptStatus(status) != session.EffectAttemptStatusAttempted {
		completedAt = observedAt
	}
	if _, err := m.runtime.store.UpsertEffectAttempt(session.EffectAttemptInput{
		AttemptID:    session.EffectAttemptID(session.SessionIDForKey(m.key), 0, "exec_pre_dispatch:"+strings.TrimSpace(name), command),
		Key:          m.key,
		TurnRunID:    m.runID,
		Executor:     "turn",
		Tool:         strings.TrimSpace(name),
		Command:      command,
		EffectKind:   workPayloadString(effect, "kind"),
		EffectReason: workPayloadString(effect, "reason"),
		BoundaryKind: boundaryKind,
		SubjectJSON:  subject,
		Status:       status,
		ErrorText:    errorText,
		EvidenceRefs: []string{fmt.Sprintf("turn_run:%d", m.runID)},
		StartedAt:    observedAt,
		CompletedAt:  completedAt,
		UpdatedAt:    observedAt,
	}); err != nil {
		log.Printf("WARN record exec effect attempt failed run_id=%d tool=%s err=%v", m.runID, name, err)
	}
}

type largeToolOutputEvidenceRedaction struct {
	InputPreview   session.EvidenceTextRedaction
	Output         session.EvidenceTextRedaction
	Digest         agent.ToolOutputDigest
	RedactionClass string
	RedactedKinds  []string
}

func redactLargeToolOutputEvidence(inputPreview string, output string, digest agent.ToolOutputDigest) largeToolOutputEvidenceRedaction {
	redactedOutput := session.RedactEvidenceText(output)
	redactedInput := session.RedactEvidenceText(inputPreview)
	redactedHead := session.RedactEvidenceText(digest.Head)
	redactedTail := session.RedactEvidenceText(digest.Tail)
	redactedKinds := append([]string(nil), redactedOutput.Kinds...)
	for _, kind := range redactedInput.Kinds {
		redactedKinds = appendUniqueRuntimeString(redactedKinds, kind)
	}
	for _, kind := range redactedHead.Kinds {
		redactedKinds = appendUniqueRuntimeString(redactedKinds, kind)
	}
	for _, kind := range redactedTail.Kinds {
		redactedKinds = appendUniqueRuntimeString(redactedKinds, kind)
	}
	safeDigest := digest
	safeDigest.Head = redactedHead.Text
	safeDigest.Tail = redactedTail.Text
	return largeToolOutputEvidenceRedaction{
		InputPreview:   redactedInput,
		Output:         redactedOutput,
		Digest:         safeDigest,
		RedactionClass: session.EvidenceRedactionClassForRedactions(redactedOutput, redactedInput, redactedHead, redactedTail),
		RedactedKinds:  redactedKinds,
	}
}

func (m *turnMonitor) recordLargeToolOutputEvidence(name string, safe largeToolOutputEvidenceRedaction, observedAt time.Time) string {
	if m == nil || m.runtime == nil || m.runtime.store == nil || strings.TrimSpace(safe.Output.Text) == "" {
		return ""
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	toolName := strings.TrimSpace(name)
	digest := safe.Digest
	retentionNote := "large tool output retained in evidence payload; prompt-facing material remains digest-bounded"
	if safe.RedactionClass == session.EvidenceRedactionSecret {
		retentionNote = "credential-bearing large tool output retained only as redacted ordinary evidence; payload hydration is withheld"
	} else if safe.RedactionClass != session.EvidenceRedactionNone {
		retentionNote = "redacted large tool output retained in evidence payload; raw secret values are not retained in ordinary evidence"
	}
	sourceRef := fmt.Sprintf("tool_output:%d:%s:%s", m.runID, toolName, strings.TrimPrefix(strings.TrimSpace(digest.SHA256), "sha256:"))
	payloadRaw, err := json.Marshal(map[string]any{
		"run_id":          m.runID,
		"tool":            toolName,
		"input_preview":   safe.InputPreview.Text,
		"output":          safe.Output.Text,
		"bytes":           digest.Bytes,
		"lines":           digest.Lines,
		"sha256":          digest.SHA256,
		"head":            safe.Digest.Head,
		"tail":            safe.Digest.Tail,
		"head_bytes":      digest.HeadBytes,
		"tail_bytes":      digest.TailBytes,
		"omitted_bytes":   digest.OmittedBytes,
		"omitted_lines":   digest.OmittedLines,
		"redaction_class": safe.RedactionClass,
		"redacted_kinds":  safe.RedactedKinds,
		"retention_note":  retentionNote,
	})
	if err != nil {
		log.Printf("WARN marshal large tool output evidence failed run_id=%d tool=%s err=%v", m.runID, toolName, err)
		return ""
	}
	obj, err := m.runtime.store.UpsertEvidenceObject(session.EvidenceObjectInput{
		EvidenceType:    session.EvidenceSourceToolOutput,
		SourceKind:      session.EvidenceSourceToolOutput,
		SourceRef:       sourceRef,
		SourceID:        fmt.Sprintf("%d", m.runID),
		SessionID:       session.SessionIDForKey(m.key),
		ChatID:          m.key.ChatID,
		UserID:          m.key.UserID,
		Scope:           m.key.Scope,
		EpistemicStatus: session.EvidenceStatusAttested,
		RedactionClass:  safe.RedactionClass,
		SubjectKey:      toolName,
		Summary:         fmt.Sprintf("large tool output tool=%s bytes=%d lines=%d sha256=%s", toolName, digest.Bytes, digest.Lines, digest.SHA256),
		Digest:          safe.Digest.Render(),
		PayloadJSON:     string(payloadRaw),
		ObservedAt:      observedAt,
	})
	if err != nil {
		log.Printf("WARN record large tool output evidence failed run_id=%d tool=%s err=%v", m.runID, toolName, err)
		return ""
	}
	return obj.ID
}

func execEffectPayload(name string, input json.RawMessage) map[string]any {
	command := execRawCommand(name, input)
	if command == "" {
		return nil
	}
	effect := commandeffect.Classify(command)
	out := map[string]any{
		"command":      redactRuntimeEvidenceText(command),
		"kind":         string(effect.Kind),
		"reason":       strings.TrimSpace(effect.Reason),
		"side_effects": effect.SideEffects,
	}
	if workdir := execPayloadString(name, input, "workdir"); workdir != "" {
		out["workdir"] = redactRuntimeEvidenceText(workdir)
	}
	if effect.Command != "" {
		out["command_root"] = effect.Command
	}
	if effect.GitSubcommand != "" {
		out["git_subcommand"] = effect.GitSubcommand
	}
	return out
}

func execRawCommand(name string, input json.RawMessage) string {
	if !strings.EqualFold(strings.TrimSpace(name), "exec") || len(input) == 0 {
		return ""
	}
	payload := map[string]any{}
	if err := json.Unmarshal(input, &payload); err != nil {
		return ""
	}
	return firstNonEmpty(payloadString(payload, "command"), payloadString(payload, "cmd"))
}

func execPayloadString(name string, input json.RawMessage, field string) string {
	if !strings.EqualFold(strings.TrimSpace(name), "exec") || len(input) == 0 {
		return ""
	}
	payload := map[string]any{}
	if err := json.Unmarshal(input, &payload); err != nil {
		return ""
	}
	return payloadString(payload, field)
}

func execEffectHasSideEffects(name string, input json.RawMessage) bool {
	command := execRawCommand(name, input)
	if command == "" {
		return false
	}
	return commandeffect.Classify(command).SideEffects
}

func safeToolInputPreview(input json.RawMessage) string {
	return redactRuntimeEvidenceText(toolInputPreview(input))
}

func redactRuntimeEvidenceText(value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	return session.RedactEvidenceText(value).Text
}

func (m *turnMonitor) ModelRequestStarted(ctx context.Context, event agent.ModelRequestEvent) {
	if m == nil || m.runtime == nil {
		return
	}
	payload := map[string]any{
		"run_id":                 m.runID,
		"attempt":                event.Attempt,
		"history_count":          event.HistoryCount,
		"tool_count":             event.ToolCount,
		"estimated_input_tokens": event.EstimatedInputTokens,
		"context_window":         event.ContextWindow,
		"context_max_tokens":     event.ContextMaxTokens,
		"context_hard_tokens":    event.ContextHardTokens,
		"context_compacted":      event.ContextPreflightCompacted,
	}
	if admission := modelContextAdmissionPayload(event); len(admission) > 0 {
		payload["context_admission"] = admission
	}
	m.runtime.recordExecutionEvent(m.key, core.ExecutionEventModelRequestStarted, "model", "started", payload, time.Now().UTC())
}

func (m *turnMonitor) ModelRequestFinished(ctx context.Context, event agent.ModelRequestEvent) {
	if m == nil || m.runtime == nil {
		return
	}
	eventType := core.ExecutionEventModelRequestSucceeded
	status := "succeeded"
	if strings.TrimSpace(event.Error) != "" {
		eventType = core.ExecutionEventModelRequestFailed
		status = "failed"
	}
	payload := map[string]any{
		"run_id":            m.runID,
		"attempt":           event.Attempt,
		"history_count":     event.HistoryCount,
		"tool_count":        event.ToolCount,
		"tool_call_count":   event.ToolCallCount,
		"output_chars":      event.OutputChars,
		"model_duration_ms": durationMillis(event.Duration),
	}
	appendModelContextPreflightPayload(payload, event)
	if strings.TrimSpace(event.Error) != "" {
		payload["error"] = trimError(event.Error)
		payload["failure_kind"] = event.FailureKind
		payload["retryable"] = event.Retryable
	}
	appendTokenUsagePayload(payload, event.TokenUsage)
	m.runtime.recordExecutionEvent(m.key, eventType, "model", status, payload, time.Now().UTC())
}

func appendModelContextPreflightPayload(payload map[string]any, event agent.ModelRequestEvent) {
	if event.EstimatedInputTokens > 0 {
		payload["estimated_input_tokens"] = event.EstimatedInputTokens
	}
	if event.ContextWindow > 0 {
		payload["context_window"] = event.ContextWindow
	}
	if event.ContextMaxTokens > 0 {
		payload["context_max_tokens"] = event.ContextMaxTokens
	}
	if event.ContextHardTokens > 0 {
		payload["context_hard_tokens"] = event.ContextHardTokens
	}
	if event.ContextPreflightCompacted {
		payload["context_compacted"] = true
		payload["context_original_tokens"] = event.ContextPreflightOriginalTokens
		payload["context_compacted_tokens"] = event.ContextPreflightCompactedTokens
		payload["context_original_tool_chars"] = event.ContextPreflightOriginalToolChars
		payload["context_compacted_tool_chars"] = event.ContextPreflightCompactedToolChars
	}
	if admission := modelContextAdmissionPayload(event); len(admission) > 0 {
		payload["context_admission"] = admission
	}
}

func modelContextAdmissionPayload(event agent.ModelRequestEvent) map[string]any {
	payload := map[string]any{}
	if event.ContextAdmissionToolEvidenceLayers > 0 {
		payload["tool_evidence_layers"] = event.ContextAdmissionToolEvidenceLayers
	}
	if event.ContextAdmissionToolEvidencePacked > 0 {
		payload["tool_evidence_packed"] = event.ContextAdmissionToolEvidencePacked
	}
	if event.ContextAdmissionToolEvidenceDigests > 0 {
		payload["tool_evidence_digests"] = event.ContextAdmissionToolEvidenceDigests
	}
	if event.ContextAdmissionSuppressedLayers > 0 {
		payload["suppressed_layers"] = event.ContextAdmissionSuppressedLayers
	}
	return payload
}

func (m *turnMonitor) ToolBatchStarted(ctx context.Context, event agent.ToolBatchEvent) {
	if m == nil || m.runtime == nil {
		return
	}
	payload := map[string]any{
		"run_id":     m.runID,
		"mode":       strings.TrimSpace(event.Mode),
		"batch_size": event.BatchSize,
		"tools":      append([]string(nil), event.ToolNames...),
	}
	appendToolBatchParallelEvidence(payload, event)
	m.runtime.recordExecutionEvent(m.key, core.ExecutionEventToolBatchStarted, "tool_batch", "started", payload, time.Now().UTC())
}

func (m *turnMonitor) ToolBatchFinished(ctx context.Context, event agent.ToolBatchEvent) {
	if m == nil || m.runtime == nil {
		return
	}
	status := "succeeded"
	if event.FailedCount > 0 {
		status = "completed_with_errors"
	}
	payload := map[string]any{
		"run_id":            m.runID,
		"mode":              strings.TrimSpace(event.Mode),
		"batch_size":        event.BatchSize,
		"tools":             append([]string(nil), event.ToolNames...),
		"failed_count":      event.FailedCount,
		"batch_duration_ms": durationMillis(event.Duration),
	}
	appendToolBatchParallelEvidence(payload, event)
	m.runtime.recordExecutionEvent(m.key, core.ExecutionEventToolBatchCompleted, "tool_batch", status, payload, time.Now().UTC())
}

func appendToolBatchParallelEvidence(payload map[string]any, event agent.ToolBatchEvent) {
	if payload == nil {
		return
	}
	payload["parallel_eligible"] = event.ParallelEligible
	payload["parallel_safe_count"] = event.ParallelSafeCount
	if reason := strings.TrimSpace(event.ParallelBlockedReason); reason != "" {
		payload["parallel_blocked_reason"] = reason
	}
	if event.ParallelMissedOpportunity {
		payload["parallel_missed_opportunity"] = true
		if reason := strings.TrimSpace(event.ParallelMissedReason); reason != "" {
			payload["parallel_missed_reason"] = reason
		}
	}
}

func appendTokenUsagePayload(payload map[string]any, usage core.TokenUsage) {
	if payload == nil {
		return
	}
	if usage.InputTokens != 0 {
		payload["input_tokens"] = usage.InputTokens
	}
	if usage.OutputTokens != 0 {
		payload["output_tokens"] = usage.OutputTokens
	}
	if usage.TotalTokens != 0 {
		payload["total_tokens"] = usage.TotalTokens
	}
	if usage.CacheReadTokens != 0 {
		payload["cache_read_tokens"] = usage.CacheReadTokens
	}
	if usage.CacheWriteTokens != 0 {
		payload["cache_write_tokens"] = usage.CacheWriteTokens
	}
	if usage.CacheCreationTokens != 0 {
		payload["cache_creation_input_tokens"] = usage.CacheCreationTokens
	}
}

func toolDurationKey(name string, input json.RawMessage) string {
	return strings.TrimSpace(name) + "\n" + string(input)
}

func (m *turnMonitor) Finish(ctx context.Context, turnErr error) {
	if m.progress != nil {
		m.progress.Finish(ctx)
	}
	if m.stopRunActivityHeartbeat != nil {
		m.stopRunActivityHeartbeat()
		m.stopRunActivityHeartbeat = nil
	}
	if m.runID == 0 {
		return
	}
	m.runtime.unregisterActiveTurn(m.runID)
	if m.cancelTurn != nil {
		m.cancelTurn()
		m.cancelTurn = nil
	}

	status := session.TurnRunStatusCompleted
	errorText := ""
	if turnErr != nil {
		status = session.TurnRunStatusFailed
		if errors.Is(turnErr, context.Canceled) {
			status = session.TurnRunStatusInterrupted
		}
		errorText = trimError(turnErr.Error())
	}
	if err := m.runtime.store.CompleteTurnRun(m.runID, status, errorText); err != nil {
		if m.runtime.expectedShutdownNoise(ctx, err) {
			log.Printf("INFO suppressing expected shutdown turn completion failure id=%d status=%s err=%v", m.runID, status, err)
		} else {
			log.Printf("WARN complete turn run id=%d status=%s err=%v", m.runID, status, err)
		}
	}
	if m.ingressSurface != "" && m.ingressUpdateID > 0 {
		ingressStatus := session.TelegramIngressUpdateCompleted
		if turnErr != nil {
			if errors.Is(turnErr, context.Canceled) {
				ingressStatus = session.TelegramIngressUpdateDropped
			} else {
				ingressStatus = session.TelegramIngressUpdateFailed
			}
		}
		if err := m.runtime.store.MarkTelegramIngressCompleted(m.ingressSurface, m.ingressUpdateID, m.runID, ingressStatus, errorText, time.Now().UTC()); err != nil {
			if m.runtime.expectedShutdownNoise(ctx, err) {
				log.Printf("INFO suppressing expected shutdown telegram ingress completion failure update_id=%d status=%s err=%v", m.ingressUpdateID, ingressStatus, err)
			} else {
				log.Printf("WARN complete telegram ingress update_id=%d status=%s err=%v", m.ingressUpdateID, ingressStatus, err)
			}
		}
	}
	eventType := core.ExecutionEventTurnCompleted
	eventStatus := "completed"
	if turnErr != nil {
		if errors.Is(turnErr, context.Canceled) {
			eventType = core.ExecutionEventTurnInterrupted
			eventStatus = "interrupted"
		} else {
			eventType = core.ExecutionEventTurnFailed
			eventStatus = "failed"
		}
	}
	m.runtime.recordExecutionEvent(m.key, eventType, "turn", eventStatus, map[string]any{
		"run_id":           m.runID,
		"error":            errorText,
		"turn_duration_ms": elapsedMillisSince(m.startedAt),
	}, time.Now().UTC())
}
