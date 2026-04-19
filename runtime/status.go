//go:build linux

package runtime

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) IsTelegramAdmin(userID int64) bool {
	if r == nil || r.resolver == nil || userID <= 0 {
		return false
	}
	actor, ok := r.resolver.ResolveTelegramUser(userID)
	return ok && actor.Role == principal.RoleAdmin
}

func (r *Runtime) StatusDiagnostics(chatID int64) ([]string, error) {
	if r == nil || r.store == nil || chatID == 0 {
		return nil, nil
	}

	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	lines := make([]string, 0, 8)

	run, err := r.store.LatestTurnRun(key)
	switch {
	case err == nil:
		lines = append(lines, fmt.Sprintf("Latest persisted turn: %s (%s).", strings.TrimSpace(string(run.Status)), strings.TrimSpace(string(run.Kind))))
		if !run.LastActivityAt.IsZero() {
			lines = append(lines, "Last activity: "+run.LastActivityAt.UTC().Format(time.RFC3339)+".")
		}
		if tool := strings.TrimSpace(run.LastToolName); tool != "" {
			lines = append(lines, "Last tool: "+tool+".")
		}
		if run.ProgressMessageID != 0 {
			lines = append(lines, fmt.Sprintf("Progress message id: %d.", run.ProgressMessageID))
		}
		if errorText := strings.TrimSpace(run.ErrorText); errorText != "" {
			lines = append(lines, "Last error: "+truncateStatusDiagnostic(errorText, 180)+".")
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return nil, err
	}

	continuation, exists, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return nil, err
	}
	if !exists {
		return lines, nil
	}
	continuation = session.NormalizeContinuationState(continuation)
	if continuation.Status == session.ContinuationStatusPending || continuation.Status == session.ContinuationStatusApproved || continuation.Status == session.ContinuationStatusRevoked {
		line := "Continuation: " + string(continuation.Status)
		if continuation.RemainingTurns > 0 {
			if continuation.RemainingTurns == 1 {
				line += " (1 turn remaining)"
			} else {
				line += fmt.Sprintf(" (%d turns remaining)", continuation.RemainingTurns)
			}
		}
		lines = append(lines, line+".")
	}
	return lines, nil
}

func (r *Runtime) ChatStatusSnapshot(chatID int64, router core.RouterStatusSnapshot) (core.ChatStatusSnapshot, error) {
	system, err := r.SystemStatusSnapshot(router)
	if err != nil {
		return core.ChatStatusSnapshot{}, err
	}

	snapshot := core.ChatStatusSnapshot{
		GeneratedAt:   system.GeneratedAt,
		ChatID:        chatID,
		QueueDepth:    system.QueueDepthByChat[chatID],
		RestartHealth: system.RestartHealth,
	}
	if ids := system.ActiveTurnsByChat[chatID]; len(ids) > 0 {
		snapshot.ActiveTurnIDs = append(snapshot.ActiveTurnIDs, ids...)
	}
	for _, item := range system.PendingItems {
		if item.ChatID == chatID {
			snapshot.PendingItems = append(snapshot.PendingItems, item)
		}
	}
	for _, continuation := range system.Continuations {
		if continuation.ChatID != chatID {
			continue
		}
		copied := continuation
		snapshot.Continuation = &copied
		break
	}
	if run, ok := system.LatestTurnRunsByChat[chatID]; ok {
		copied := run
		snapshot.LatestTurnRun = &copied
	}
	if r != nil && r.store != nil {
		key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
		statusState, exists, stateErr := r.store.StatusStateIfExists(key)
		if stateErr != nil {
			return core.ChatStatusSnapshot{}, stateErr
		}
		if exists {
			snapshot.OperationStatus, snapshot.OperationStage, snapshot.OperationSummary = operationStatusFields(statusState.OperationState)
			snapshot.PlanStepStatus, snapshot.PlanStep = planStatusFields(statusState.PlanState)
			snapshot.PlanCompletedSteps, snapshot.PlanTotalSteps, snapshot.PlanFullyExecuted = planProgressFields(statusState.PlanState)
			snapshot.HiddenInputCategories, snapshot.HiddenInputSummary = hiddenInputStatusFields(statusState.LastFloorMetadata)
			snapshot.DeliveryStatus, snapshot.DeliverySummary = deliveryStatusFields(snapshot.LatestTurnRun, statusState.OutboundCountAtTurn)
		}
	}
	if phase, ok := r.chatTurnPhase(chatID); ok {
		snapshot.TurnPhase = strings.TrimSpace(phase.Phase)
		snapshot.TurnPhaseSummary = strings.TrimSpace(phase.Summary)
		snapshot.TurnPhaseUpdatedAt = phase.UpdatedAt
	}
	for _, stale := range system.StaleRunningTurns {
		if stale.ChatID == chatID {
			snapshot.StaleRunningTurns = append(snapshot.StaleRunningTurns, stale)
		}
	}
	return snapshot, nil
}

func (r *Runtime) SystemStatusSnapshot(router core.RouterStatusSnapshot) (core.SystemStatusSnapshot, error) {
	now := time.Now().UTC()
	snapshot := core.SystemStatusSnapshot{
		GeneratedAt:          now,
		ActiveTurnsByChat:    cloneActiveTurnMap(router.ActiveTurnsByChat),
		QueueDepthByChat:     cloneQueueDepthMap(router.QueueDepthByChat),
		PendingItems:         make([]core.PendingItem, 0, 16),
		Continuations:        make([]core.ContinuationStatusSnapshot, 0, 8),
		LatestTurnRunsByChat: make(map[int64]core.TurnRunStatusSnapshot),
		StaleRunningTurns:    make([]core.TurnRunStatusSnapshot, 0, 8),
		HotChats:             make([]core.ChatStatusRollup, 0, 8),
		RestartHealth:        r.restartHealthSnapshot(),
	}

	for _, ids := range snapshot.ActiveTurnsByChat {
		snapshot.ActiveTurnCount += len(ids)
	}
	snapshot.ActiveChatIDs = sortedInt64Keys(snapshot.ActiveTurnsByChat)

	for _, chatID := range sortedInt64KeysFromInt(snapshot.QueueDepthByChat) {
		depth := snapshot.QueueDepthByChat[chatID]
		if depth <= 0 {
			continue
		}
		snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
			Kind:    core.PendingItemKindQueue,
			ChatID:  chatID,
			ID:      "queue:" + strconv.FormatInt(chatID, 10),
			Summary: fmt.Sprintf("queue_depth=%d", depth),
		})
	}

	if r == nil || r.store == nil {
		sortPendingItems(snapshot.PendingItems)
		snapshot.HotChats = buildHotChatRollups(snapshot)
		return snapshot, nil
	}

	pendingDecisions, err := r.store.PendingDecisions()
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	for _, pending := range pendingDecisions {
		age := statusAge(now, pending.UpdatedAt, pending.CreatedAt)
		timeout := time.Duration(pending.TimeoutNanos)
		stale := timeout > 0 && age > timeout
		snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
			Kind:      core.PendingItemKindDecision,
			ChatID:    pending.ChatID,
			ID:        strings.TrimSpace(pending.ID),
			Summary:   renderDecisionSummary(pending),
			Age:       age,
			CreatedAt: pending.CreatedAt,
			UpdatedAt: pending.UpdatedAt,
			Stale:     stale,
		})
	}

	continuations, err := r.store.ContinuationStates()
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	for _, row := range continuations {
		state := session.NormalizeContinuationState(row.State)
		status := strings.TrimSpace(string(state.Status))
		if status == "" {
			continue
		}
		snapshot.Continuations = append(snapshot.Continuations, core.ContinuationStatusSnapshot{
			ChatID:           row.Key.ChatID,
			Status:           status,
			RemainingTurns:   state.RemainingTurns,
			DecisionID:       strings.TrimSpace(state.DecisionID),
			ApprovedBy:       state.ApprovedBy,
			PersonaIntent:    strings.TrimSpace(string(state.PersonaIntent.Decision)),
			GovernorIntent:   strings.TrimSpace(string(state.GovernorIntent.Decision)),
			GovernorRatified: state.GovernorIntent.Ratified,
			BlockedReason:    strings.TrimSpace(state.HandshakeBlockedReason),
			UpdatedAt:        coalesceTime(row.UpdatedAt, state.UpdatedAt),
		})
		if state.Status == session.ContinuationStatusPending || state.Status == session.ContinuationStatusApproved {
			updatedAt := coalesceTime(row.UpdatedAt, state.UpdatedAt)
			snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
				Kind:      core.PendingItemKindContinuation,
				ChatID:    row.Key.ChatID,
				ID:        continuationItemID(state, row.Key.ChatID),
				Summary:   renderContinuationSummary(state),
				Age:       statusAge(now, updatedAt, time.Time{}),
				UpdatedAt: updatedAt,
			})
		}
	}

	latestRuns, err := r.store.LatestTurnRunsByChat(500)
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	for _, run := range latestRuns {
		snapshot.LatestTurnRunsByChat[run.ChatID] = turnRunSnapshot(run)
	}

	pendingRecovery, err := r.store.PendingRecoveryTurnRuns(500)
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	for _, run := range pendingRecovery {
		snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
			Kind:      core.PendingItemKindRecovery,
			ChatID:    run.ChatID,
			ID:        fmt.Sprintf("recovery:%d", run.ID),
			Summary:   fmt.Sprintf("turn_run_id=%d status=%s", run.ID, run.Status),
			Age:       statusAge(now, run.LastActivityAt, run.StartedAt),
			CreatedAt: run.StartedAt,
			UpdatedAt: run.LastActivityAt,
		})
	}

	staleRuns, err := r.staleRunningTurnRuns(now)
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	for _, run := range staleRuns {
		snapshot.StaleRunningTurns = append(snapshot.StaleRunningTurns, turnRunSnapshot(run))
		snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
			Kind:      core.PendingItemKindStaleTurn,
			ChatID:    run.ChatID,
			ID:        fmt.Sprintf("stale:%d", run.ID),
			Summary:   fmt.Sprintf("turn_run_id=%d last_activity=%s", run.ID, run.LastActivityAt.UTC().Format(time.RFC3339)),
			Age:       statusAge(now, run.LastActivityAt, run.StartedAt),
			CreatedAt: run.StartedAt,
			UpdatedAt: run.LastActivityAt,
			Stale:     true,
		})
	}

	sort.Slice(snapshot.Continuations, func(i, j int) bool {
		if snapshot.Continuations[i].ChatID == snapshot.Continuations[j].ChatID {
			return snapshot.Continuations[i].Status < snapshot.Continuations[j].Status
		}
		return snapshot.Continuations[i].ChatID < snapshot.Continuations[j].ChatID
	})
	sort.Slice(snapshot.StaleRunningTurns, func(i, j int) bool {
		if snapshot.StaleRunningTurns[i].ChatID == snapshot.StaleRunningTurns[j].ChatID {
			return snapshot.StaleRunningTurns[i].ID < snapshot.StaleRunningTurns[j].ID
		}
		return snapshot.StaleRunningTurns[i].ChatID < snapshot.StaleRunningTurns[j].ChatID
	})
	sortPendingItems(snapshot.PendingItems)
	snapshot.HotChats = buildHotChatRollups(snapshot)
	return snapshot, nil
}

func (r *Runtime) staleRunningTurnRuns(now time.Time) ([]session.TurnRun, error) {
	if r == nil || r.staleTurnSweep == nil || r.staleTurnThreshold <= 0 {
		return nil, nil
	}
	cutoff := now.Add(-r.staleTurnThreshold)
	limit := r.staleTurnLimit
	if limit <= 0 {
		limit = 50
	}
	return r.staleTurnSweep(cutoff, limit)
}

func (r *Runtime) restartHealthSnapshot() core.RestartHealthSnapshot {
	if r == nil {
		return core.RestartHealthSnapshot{}
	}
	return core.RestartHealthSnapshot{
		WatchdogTriggered:  r.staleWatchdogTriggered.Load(),
		StaleTurnThreshold: r.staleTurnThreshold,
		StaleTurnLimit:     r.staleTurnLimit,
	}
}

func cloneActiveTurnMap(in map[int64][]uint64) map[int64][]uint64 {
	out := make(map[int64][]uint64, len(in))
	for chatID, ids := range in {
		if len(ids) == 0 {
			continue
		}
		copied := append([]uint64(nil), ids...)
		sort.Slice(copied, func(i, j int) bool { return copied[i] < copied[j] })
		out[chatID] = copied
	}
	return out
}

func cloneQueueDepthMap(in map[int64]int) map[int64]int {
	out := make(map[int64]int, len(in))
	for chatID, depth := range in {
		if depth <= 0 {
			continue
		}
		out[chatID] = depth
	}
	return out
}

func sortedInt64Keys(values map[int64][]uint64) []int64 {
	keys := make([]int64, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func sortedInt64KeysFromInt(values map[int64]int) []int64 {
	keys := make([]int64, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func sortPendingItems(items []core.PendingItem) {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.ChatID != b.ChatID {
			return a.ChatID < b.ChatID
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Summary < b.Summary
	})
}

func buildHotChatRollups(snapshot core.SystemStatusSnapshot) []core.ChatStatusRollup {
	rollups := map[int64]*core.ChatStatusRollup{}
	ensure := func(chatID int64) *core.ChatStatusRollup {
		rollup := rollups[chatID]
		if rollup == nil {
			rollup = &core.ChatStatusRollup{ChatID: chatID}
			rollups[chatID] = rollup
		}
		return rollup
	}

	for chatID, ids := range snapshot.ActiveTurnsByChat {
		rollup := ensure(chatID)
		rollup.ActiveTurnCount = len(ids)
	}
	for chatID, depth := range snapshot.QueueDepthByChat {
		rollup := ensure(chatID)
		rollup.QueueDepth = depth
	}
	for _, pending := range snapshot.PendingItems {
		rollup := ensure(pending.ChatID)
		rollup.PendingCount++
	}
	for chatID, run := range snapshot.LatestTurnRunsByChat {
		rollup := ensure(chatID)
		rollup.LatestStatus = strings.TrimSpace(run.Status)
		rollup.LastActivityAt = run.LastActivityAt
	}

	out := make([]core.ChatStatusRollup, 0, len(rollups))
	for _, rollup := range rollups {
		out = append(out, *rollup)
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i], out[j]
		if left.PendingCount != right.PendingCount {
			return left.PendingCount > right.PendingCount
		}
		if left.ActiveTurnCount != right.ActiveTurnCount {
			return left.ActiveTurnCount > right.ActiveTurnCount
		}
		if left.QueueDepth != right.QueueDepth {
			return left.QueueDepth > right.QueueDepth
		}
		if !left.LastActivityAt.Equal(right.LastActivityAt) {
			return left.LastActivityAt.After(right.LastActivityAt)
		}
		return left.ChatID < right.ChatID
	})
	return out
}

func turnRunSnapshot(run session.TurnRun) core.TurnRunStatusSnapshot {
	return core.TurnRunStatusSnapshot{
		ID:                    run.ID,
		ChatID:                run.ChatID,
		Kind:                  strings.TrimSpace(string(run.Kind)),
		Status:                strings.TrimSpace(string(run.Status)),
		RequestText:           truncateStatusDiagnostic(strings.TrimSpace(run.RequestText), 220),
		LastActivityAt:        run.LastActivityAt,
		ProgressMessageID:     run.ProgressMessageID,
		LastToolName:          strings.TrimSpace(run.LastToolName),
		LastToolPreview:       truncateStatusDiagnostic(strings.TrimSpace(run.LastToolPreview), 220),
		LastToolResultPreview: truncateStatusDiagnostic(strings.TrimSpace(run.LastToolResultPreview), 220),
		LastToolError:         truncateStatusDiagnostic(strings.TrimSpace(run.LastToolError), 220),
		ErrorText:             strings.TrimSpace(firstNonEmptyStatus(run.ErrorText, run.LastToolError)),
		StartedAt:             run.StartedAt,
	}
}

func renderDecisionSummary(record session.PendingDecisionRecord) string {
	kind := strings.TrimSpace(record.Kind)
	prompt := truncateStatusDiagnostic(record.Prompt, 80)
	if kind == "" {
		return "prompt=" + prompt
	}
	if prompt == "" {
		return "kind=" + kind
	}
	return fmt.Sprintf("kind=%s prompt=%s", kind, prompt)
}

func continuationItemID(state session.ContinuationState, chatID int64) string {
	if decisionID := strings.TrimSpace(state.DecisionID); decisionID != "" {
		return decisionID
	}
	return "continuation:" + strconv.FormatInt(chatID, 10)
}

func renderContinuationSummary(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	parts := []string{
		fmt.Sprintf("status=%s", strings.TrimSpace(string(state.Status))),
		fmt.Sprintf("remaining_turns=%d", state.RemainingTurns),
	}
	if decision := strings.TrimSpace(string(state.PersonaIntent.Decision)); decision != "" {
		parts = append(parts, "persona_intent="+decision)
	}
	if decision := strings.TrimSpace(string(state.GovernorIntent.Decision)); decision != "" {
		parts = append(parts, "governor_intent="+decision)
	}
	if state.GovernorIntent.Ratified {
		parts = append(parts, "governor_ratified=true")
	}
	if reason := strings.TrimSpace(state.HandshakeBlockedReason); reason != "" {
		parts = append(parts, "blocked_reason="+reason)
	}
	return strings.Join(parts, " ")
}

func statusAge(now time.Time, preferred time.Time, fallback time.Time) time.Duration {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	ts := preferred
	if ts.IsZero() {
		ts = fallback
	}
	if ts.IsZero() {
		return 0
	}
	age := now.Sub(ts)
	if age < 0 {
		return 0
	}
	return age
}

func coalesceTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func firstNonEmptyStatus(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func operationStatusFields(state session.OperationState) (status string, stage string, summary string) {
	normalized := session.NormalizeOperationState(state)
	status = strings.TrimSpace(string(normalized.Status))
	stage = strings.TrimSpace(normalized.Stage)
	summary = strings.TrimSpace(firstNonEmptyStatus(normalized.Summary, normalized.Objective))
	summary = truncateStatusDiagnostic(summary, 160)
	return status, stage, summary
}

func planStatusFields(state session.PlanState) (status string, step string) {
	normalized := session.NormalizePlanState(state)
	if len(normalized.Steps) == 0 {
		explanation := strings.TrimSpace(normalized.Explanation)
		if explanation != "" {
			return "", truncateStatusDiagnostic(explanation, 160)
		}
		return "", ""
	}

	picked := normalized.Steps[0]
	for _, candidate := range normalized.Steps {
		if candidate.Status == session.PlanStatusInProgress {
			picked = candidate
			break
		}
		if candidate.Status == session.PlanStatusPending && picked.Status == session.PlanStatusCompleted {
			picked = candidate
		}
	}
	return strings.TrimSpace(string(picked.Status)), truncateStatusDiagnostic(strings.TrimSpace(picked.Step), 160)
}

func planProgressFields(state session.PlanState) (completed int, total int, fullyExecuted bool) {
	normalized := session.NormalizePlanState(state)
	total = len(normalized.Steps)
	if total == 0 {
		return 0, 0, false
	}
	for _, step := range normalized.Steps {
		if session.NormalizePlanStatus(step.Status) == session.PlanStatusCompleted {
			completed++
		}
	}
	return completed, total, completed == total
}

func hiddenInputStatusFields(raw string) ([]string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ""
	}
	var metadata core.FloorMetadata
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return nil, ""
	}

	seen := map[string]struct{}{}
	categories := make([]string, 0, len(metadata.HiddenInputs))
	for _, input := range metadata.HiddenInputs {
		category := strings.TrimSpace(input.Category)
		if category == "" {
			continue
		}
		if _, ok := seen[category]; ok {
			continue
		}
		seen[category] = struct{}{}
		categories = append(categories, category)
	}
	sort.Strings(categories)

	summary := strings.TrimSpace(metadata.ProvenanceSummary)
	if summary == "" {
		parts := make([]string, 0, 2)
		for _, input := range metadata.HiddenInputs {
			if detail := strings.TrimSpace(input.Summary); detail != "" {
				parts = append(parts, detail)
			}
			if len(parts) == 2 {
				break
			}
		}
		summary = strings.Join(parts, "; ")
	}
	return categories, truncateStatusDiagnostic(summary, 160)
}

func deliveryStatusFields(latest *core.TurnRunStatusSnapshot, outboundCountAtTurn int) (status string, summary string) {
	if latest == nil {
		return "", ""
	}
	runStatus := strings.ToLower(strings.TrimSpace(latest.Status))
	switch runStatus {
	case "running":
		return "in_flight", "turn is still executing"
	case "completed":
		if outboundCountAtTurn > 0 {
			return "delivered", "latest persisted turn has a recorded outbound delivery"
		}
		return "persisted_not_delivered", "latest turn persisted but no outbound delivery is recorded"
	case "failed":
		errText := strings.ToLower(strings.TrimSpace(latest.ErrorText))
		if strings.Contains(errText, "send outbound reply") || strings.Contains(errText, "send durable group reply") {
			if outboundCountAtTurn > 0 {
				return "delivery_error_recovered", "delivery reported an error, but outbound delivery is recorded"
			}
			return "delivery_failed", "persisted turn failed during delivery; no retry queue is active"
		}
		if outboundCountAtTurn > 0 {
			return "failed_after_delivery", "turn failed after outbound delivery was recorded"
		}
		return "failed_before_delivery", "turn failed before outbound delivery was recorded"
	case "interrupted":
		if outboundCountAtTurn > 0 {
			return "interrupted_after_delivery", "turn was interrupted after outbound delivery was recorded"
		}
		return "interrupted_before_delivery", "turn was interrupted before outbound delivery was recorded"
	default:
		if outboundCountAtTurn > 0 {
			return "delivered", "outbound delivery is recorded for the latest turn"
		}
		return "", ""
	}
}

func truncateStatusDiagnostic(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	if text == "" || maxRunes <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	if maxRunes == 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}
