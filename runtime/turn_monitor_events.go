//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func (m *turnMonitor) ToolStarted(ctx context.Context, name string, input json.RawMessage) {
	startedAt := time.Now().UTC()
	preview := toolInputPreview(input)
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
	m.runtime.recordExecutionEvent(m.key, core.ExecutionEventToolStarted, "tool", "started", map[string]any{
		"run_id":  m.runID,
		"tool":    strings.TrimSpace(name),
		"preview": preview,
	}, startedAt)
	if m.progress != nil {
		m.progress.ToolStarted(ctx, name, input)
	}
}

func (m *turnMonitor) ToolFinished(ctx context.Context, name string, input json.RawMessage, output string, err error) {
	preview := toolInputPreview(input)
	resultPreview := truncatePreview(strings.TrimSpace(output), 220)
	errorText := ""
	if err != nil {
		errorText = trimError(err.Error())
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
	m.runtime.recordExecutionEvent(m.key, eventType, "tool", status, map[string]any{
		"run_id":           m.runID,
		"tool":             strings.TrimSpace(name),
		"preview":          preview,
		"result_preview":   resultPreview,
		"error":            errorText,
		"tool_duration_ms": toolDurationMS,
	}, time.Now().UTC())
	if m.progress != nil {
		m.progress.ToolFinished(ctx, name, err)
	}
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
