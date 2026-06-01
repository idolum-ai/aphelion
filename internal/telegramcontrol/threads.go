//go:build linux

package telegramcontrol

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramcommands"
	"github.com/idolum-ai/aphelion/session"
)

type ThreadController struct {
	Store              *session.SQLiteStore
	Rebind             func(core.InboundMessage) error
	RouteAccepted      func(context.Context, core.InboundMessage) error
	StopForMessage     func(core.InboundMessage) core.StopResult
	Promote            func(context.Context, int64, int64, int64) (session.TelegramThreadPromotionResult, error)
	PreparePromotion   func(context.Context, int64, int64, string) (session.TelegramThreadPromotionResult, error)
	CancelPromotion    func(context.Context, int64, int64, string) (session.TelegramThreadPromotionResult, error)
	SupersedePromotion func(context.Context, int64, int64, string) (session.TelegramThreadPromotionResult, error)
	Absorb             func(context.Context, int64, int64, int64) (string, error)
	IsAbsorbUserError  func(error) bool
}

func (c ThreadController) rebindTelegramIngressForMessage(msg core.InboundMessage) error {
	if c.Rebind == nil {
		return nil
	}
	return c.Rebind(msg)
}

func (c ThreadController) routeAccepted(ctx context.Context, msg core.InboundMessage) error {
	if c.RouteAccepted == nil {
		return nil
	}
	return c.RouteAccepted(ctx, msg)
}

func (c ThreadController) stopForMessage(msg core.InboundMessage) core.StopResult {
	if c.StopForMessage == nil {
		return core.StopResult{}
	}
	return c.StopForMessage(msg)
}

func (c ThreadController) CreateTelegramThread(_ context.Context, msg core.InboundMessage) (session.TelegramThread, error) {
	if c.Store == nil {
		return session.TelegramThread{}, fmt.Errorf("thread store is unavailable")
	}
	thread, _, err := c.Store.CreateTelegramThreadForUpdate(msg.ChatID, msg.SenderID, msg.IngressUpdateID, msg.MessageID, "", time.Now().UTC())
	if err != nil {
		return session.TelegramThread{}, err
	}
	return thread, nil
}

func (c ThreadController) RecordTelegramThreadGuideMessage(chatID int64, threadID int64, messageID int64) error {
	if c.Store == nil || chatID == 0 || threadID <= 0 || messageID <= 0 {
		return nil
	}
	return c.Store.RecordTelegramThreadMessage(chatID, threadID, messageID, "thread_guide", "thread_guide", time.Now().UTC())
}

func (c ThreadController) RecordTelegramThreadCallbackMessage(chatID int64, threadID int64, messageID int64, surface string) error {
	if c.Store == nil {
		return nil
	}
	return c.Store.RecordTelegramCallbackMessageThread(chatID, messageID, threadID, surface, time.Now().UTC())
}

func (c ThreadController) ClearTelegramThreadCallbackMessage(chatID int64, messageID int64, surface string) error {
	if c.Store == nil {
		return nil
	}
	return c.Store.ClearTelegramCallbackMessageThread(chatID, messageID, surface, time.Now().UTC())
}

func (c ThreadController) RecordTelegramThreadReminderMessage(chatID int64, threadID int64, messageID int64, summary string, summaryKind string, sourceLastActivityAt time.Time, createdBySenderID int64) error {
	if c.Store == nil || chatID == 0 || threadID <= 0 || messageID <= 0 {
		return nil
	}
	_, err := c.Store.RecordTelegramThreadReminder(chatID, threadID, messageID, summary, summaryKind, sourceLastActivityAt, createdBySenderID, time.Now().UTC())
	return err
}

func (c ThreadController) IgnoreTelegramThreadReminder(_ context.Context, chatID int64, _ int64, threadID int64, messageID int64) (string, error) {
	if c.Store == nil {
		return "", fmt.Errorf("thread store is unavailable")
	}
	reminder, changed, err := c.Store.MarkTelegramThreadReminderStatus(chatID, messageID, session.TelegramThreadReminderStatusIgnored, time.Now().UTC())
	if err != nil {
		return "", err
	}
	if !changed {
		if reminder.ID == 0 {
			return "", telegramcommands.ThreadUserError("This reminder is no longer active.")
		}
		return "Reminder is already " + string(reminder.Status) + ".", nil
	}
	label := fmt.Sprintf("%d", threadID)
	if thread, ok, err := c.Store.TelegramThread(chatID, threadID); err == nil && ok && thread.DisplaySlot > 0 {
		label = fmt.Sprintf("%d", thread.DisplaySlot)
	}
	return "Ignored reminder for thread " + label + ".", nil
}

func (c ThreadController) AbsorbTelegramThreadReminder(ctx context.Context, chatID int64, senderID int64, threadID int64, messageID int64) (string, error) {
	if c.Store == nil {
		return "", fmt.Errorf("thread store is unavailable")
	}
	if c.Absorb == nil {
		return "", fmt.Errorf("thread absorb is unavailable")
	}
	text, err := c.Absorb(ctx, chatID, senderID, threadID)
	if err != nil {
		return "", err
	}
	if _, _, err := c.Store.MarkTelegramThreadReminderStatus(chatID, messageID, session.TelegramThreadReminderStatusAbsorbed, time.Now().UTC()); err != nil {
		return "", err
	}
	return text, nil
}

func (c ThreadController) StartTelegramThreadTarget(_ context.Context, msg core.InboundMessage, text string) (core.InboundMessage, session.TelegramThread, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return core.InboundMessage{}, session.TelegramThread{}, telegramcommands.ThreadUserError("Usage: /thread <message>")
	}
	if c.Store == nil {
		return core.InboundMessage{}, session.TelegramThread{}, fmt.Errorf("thread store is unavailable")
	}
	thread, _, err := c.Store.CreateTelegramThreadForUpdate(msg.ChatID, msg.SenderID, msg.IngressUpdateID, msg.MessageID, text, time.Now().UTC())
	if err != nil {
		return core.InboundMessage{}, session.TelegramThread{}, err
	}
	routed := msg
	routed.TelegramThreadID = thread.ThreadID
	routed.Text = text
	if err := c.rebindTelegramIngressForMessage(routed); err != nil {
		return core.InboundMessage{}, session.TelegramThread{}, err
	}
	return routed, thread, nil
}

func (c ThreadController) TargetTelegramThreadMessage(_ context.Context, msg core.InboundMessage, threadID int64, text string) (core.InboundMessage, session.TelegramThread, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return core.InboundMessage{}, session.TelegramThread{}, telegramcommands.ThreadUserError(fmt.Sprintf("Add a message after `(thread %d)`.", threadID))
	}
	if c.Store == nil {
		return core.InboundMessage{}, session.TelegramThread{}, fmt.Errorf("thread store is unavailable")
	}
	thread, ok, err := c.Store.TelegramThread(msg.ChatID, threadID)
	if err != nil {
		return core.InboundMessage{}, session.TelegramThread{}, err
	}
	if !ok {
		return core.InboundMessage{}, session.TelegramThread{}, telegramcommands.ThreadUserError(fmt.Sprintf("Thread %d does not exist. Start a new side thread with `/thread <message>`.", threadID))
	}
	if !thread.Open() {
		return core.InboundMessage{}, session.TelegramThread{}, telegramcommands.ThreadUserError(fmt.Sprintf("Thread %d is closed. Start a new side thread with `/thread <message>`.", threadID))
	}
	routed := msg
	routed.TelegramThreadID = threadID
	routed.Text = text
	if err := c.Store.TouchTelegramThread(msg.ChatID, threadID, time.Now().UTC()); err != nil {
		return core.InboundMessage{}, session.TelegramThread{}, err
	}
	if err := c.rebindTelegramIngressForMessage(routed); err != nil {
		return core.InboundMessage{}, session.TelegramThread{}, err
	}
	return routed, thread, nil
}

func (c ThreadController) TelegramThreadForReplyMessage(chatID int64, replyMessageID int64) (session.TelegramThread, bool, error) {
	if c.Store == nil {
		return session.TelegramThread{}, false, fmt.Errorf("thread store is unavailable")
	}
	threadID, ok, err := c.Store.TelegramThreadIDForReplyMessage(chatID, replyMessageID)
	if err != nil || !ok {
		return session.TelegramThread{}, false, err
	}
	return c.Store.TelegramThread(chatID, threadID)
}

func (c ThreadController) MarkTelegramThreadReminderResumed(chatID int64, replyMessageID int64) error {
	if c.Store == nil || chatID == 0 || replyMessageID <= 0 {
		return nil
	}
	_, _, err := c.Store.MarkTelegramThreadReminderStatus(chatID, replyMessageID, session.TelegramThreadReminderStatusResumed, time.Now().UTC())
	return err
}

func (c ThreadController) TelegramThread(chatID int64, threadID int64) (session.TelegramThread, bool, error) {
	if c.Store == nil {
		return session.TelegramThread{}, false, fmt.Errorf("thread store is unavailable")
	}
	return c.Store.TelegramThread(chatID, threadID)
}

func (c ThreadController) TelegramThreads(chatID int64) ([]session.TelegramThread, error) {
	if c.Store == nil {
		return nil, fmt.Errorf("thread store is unavailable")
	}
	return c.Store.ListTelegramThreads(chatID, 20)
}

func (c ThreadController) TelegramThreadReminders(chatID int64, status session.TelegramThreadReminderStatus, limit int) ([]session.TelegramThreadReminder, error) {
	if c.Store == nil {
		return nil, fmt.Errorf("thread store is unavailable")
	}
	return c.Store.ListTelegramThreadReminders(chatID, status, limit)
}

func (c ThreadController) QueueTelegramThreadSummary(ctx context.Context, msg core.InboundMessage) (string, error) {
	if c.Store == nil {
		return "", fmt.Errorf("thread store is unavailable")
	}
	text, err := c.renderTelegramThreadSummaryQuest(msg.ChatID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "", telegramcommands.ThreadUserError("No open side threads to analyze.")
	}
	routed := msg
	routed.Text = text
	routed.TelegramThreadID = 0
	if err := c.recordTelegramThreadSummaryAccepted(routed); err != nil {
		return "", err
	}
	if err := c.routeAccepted(ctx, routed); err != nil {
		return "", err
	}
	return "Analysis queued.", nil
}

func (c ThreadController) recordTelegramThreadSummaryAccepted(msg core.InboundMessage) error {
	return recordTelegramCallbackWorkAccepted(c.Store, msg, "callback_thread_summary")
}

func (c ThreadController) renderTelegramThreadSummaryQuest(chatID int64) (string, error) {
	threads, err := c.Store.ListTelegramThreads(chatID, 20)
	if err != nil {
		return "", err
	}
	var open []session.TelegramThread
	for _, thread := range threads {
		if thread.Open() {
			open = append(open, thread)
		}
	}
	if len(open) == 0 {
		return "", nil
	}

	now := time.Now().UTC()
	var b strings.Builder
	b.WriteString("Analyze the open Telegram side threads as a thread-board triage note for the main chat.\n")
	b.WriteString("Use the operator-facing `display_thread` number in your answer. Treat `internal_thread_id` as evidence only.\n")
	b.WriteString("Do not absorb, promote, close, modify, or write memory. Do not claim actions happened unless the evidence says so.\n")
	b.WriteString("If evidence is thin, say so. Prefer uncertainty over invention.\n\n")
	b.WriteString("Output format:\n")
	b.WriteString("Quick read: one sentence on the overall thread board.\n")
	b.WriteString("Needs action: bullets for threads that need operator/system action, each with Thread N, status, why, and recommended next move.\n")
	b.WriteString("Likely stale or absorbable: bullets for threads that look safe to close or review for absorb.\n")
	b.WriteString("Blocked/waiting: bullets for approvals, credentials, external dependencies, or unclear blockers.\n")
	b.WriteString("Suggested next move: one concrete next action.\n\n")
	b.WriteString("Thread-board evidence:\n")
	for _, thread := range open {
		sess := c.loadTelegramThreadSession(chatID, thread.ThreadID)
		c.renderTelegramThreadAnalysisEvidence(&b, thread, sess, now)
	}
	return strings.TrimSpace(b.String()), nil
}

func (c ThreadController) loadTelegramThreadSession(chatID int64, threadID int64) *session.Session {
	if c.Store == nil || chatID == 0 || threadID <= 0 {
		return nil
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: session.TelegramThreadScopeRef(chatID, threadID)}
	sess, err := c.Store.Load(key)
	if err != nil {
		return nil
	}
	return sess
}

func (c ThreadController) renderTelegramThreadAnalysisEvidence(b *strings.Builder, thread session.TelegramThread, sess *session.Session, now time.Time) {
	displayID := telegramThreadAnalysisDisplayID(thread)
	fmt.Fprintf(b, "\nThread %d\n", displayID)
	fmt.Fprintf(b, "display_thread: %d\n", displayID)
	fmt.Fprintf(b, "internal_thread_id: %d\n", thread.ThreadID)
	fmt.Fprintf(b, "status: %s\n", firstNonEmptyThreadAnalysisValue(string(thread.Status), "unknown"))
	if preview := telegramcommands.CompactThreadPreview(thread.CreatedText); preview != "" {
		fmt.Fprintf(b, "created_text: %s\n", preview)
	} else {
		b.WriteString("created_text: none recorded\n")
	}
	if !thread.CreatedAt.IsZero() {
		fmt.Fprintf(b, "created_at: %s (%s)\n", formatTelegramThreadAnalysisTime(thread.CreatedAt), formatTelegramThreadAnalysisAge(thread.CreatedAt, now))
	}
	if lastActive := telegramThreadAnalysisLastActiveAt(thread, sess); !lastActive.IsZero() {
		fmt.Fprintf(b, "last_active: %s (%s)\n", formatTelegramThreadAnalysisTime(lastActive), formatTelegramThreadAnalysisAge(lastActive, now))
	} else {
		b.WriteString("last_active: unknown\n")
	}
	if sess == nil {
		b.WriteString("turn_count: unknown\n")
		b.WriteString("session_state: unavailable\n")
		b.WriteString("recent_transcript: none loaded\n")
		return
	}
	fmt.Fprintf(b, "turn_count: %d\n", sess.TurnCount)
	if plan := telegramThreadAnalysisPlanSignal(sess.PlanState); plan != "" {
		fmt.Fprintf(b, "plan_state: %s\n", plan)
	}
	if operation := telegramThreadAnalysisOperationSignal(sess.OperationState); operation != "" {
		fmt.Fprintf(b, "operation_state: %s\n", operation)
	}
	if continuation := telegramThreadAnalysisContinuationSignal(sess.ContinuationState); continuation != "" {
		fmt.Fprintf(b, "continuation_state: %s\n", continuation)
	}
	lines := telegramThreadRecentTranscriptLinesFromSession(sess, 8, now)
	if len(lines) == 0 {
		b.WriteString("recent_transcript: none recorded beyond the opener, if any\n")
		return
	}
	b.WriteString("recent_transcript:\n")
	for _, line := range lines {
		fmt.Fprintf(b, "- %s\n", line)
	}
}

func telegramThreadRecentTranscriptLinesFromSession(sess *session.Session, limit int, now time.Time) []string {
	if sess == nil || len(sess.Messages) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 8
	}
	var out []string
	for i := len(sess.Messages) - 1; i >= 0 && len(out) < limit; i-- {
		msg := sess.Messages[i]
		role := strings.TrimSpace(strings.ToLower(msg.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		content := strings.Join(strings.Fields(strings.TrimSpace(msg.Content)), " ")
		if content == "" {
			continue
		}
		when := "time unknown"
		if !msg.CreatedAt.IsZero() {
			when = formatTelegramThreadAnalysisAge(msg.CreatedAt, now)
		}
		out = append(out, fmt.Sprintf("%s [%s]: %s", role, when, truncateTelegramThreadSummaryEvidence(content, 320)))
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func telegramThreadAnalysisDisplayID(thread session.TelegramThread) int64 {
	if thread.Open() && thread.DisplaySlot > 0 {
		return thread.DisplaySlot
	}
	return thread.ThreadID
}

func telegramThreadAnalysisLastActiveAt(thread session.TelegramThread, sess *session.Session) time.Time {
	lastActive := thread.LastActivityAt
	if sess != nil {
		for _, msg := range sess.Messages {
			if msg.CreatedAt.After(lastActive) {
				lastActive = msg.CreatedAt
			}
		}
		if sess.UpdatedAt.After(lastActive) {
			lastActive = sess.UpdatedAt
		}
	}
	if lastActive.IsZero() && !thread.CreatedAt.IsZero() {
		lastActive = thread.CreatedAt
	}
	if lastActive.IsZero() && !thread.UpdatedAt.IsZero() {
		lastActive = thread.UpdatedAt
	}
	if lastActive.IsZero() {
		return time.Time{}
	}
	return lastActive.UTC()
}

func telegramThreadAnalysisPlanSignal(plan session.PlanState) string {
	plan = session.NormalizePlanState(plan)
	if !plan.Active() {
		return ""
	}
	for _, step := range plan.Steps {
		if step.Status == session.PlanStatusInProgress {
			return fmt.Sprintf("%d step(s); current [%s] %s", len(plan.Steps), step.Status, truncateTelegramThreadSummaryEvidence(step.Step, 140))
		}
	}
	for _, step := range plan.Steps {
		if step.Status == session.PlanStatusPending {
			return fmt.Sprintf("%d step(s); next [%s] %s", len(plan.Steps), step.Status, truncateTelegramThreadSummaryEvidence(step.Step, 140))
		}
	}
	if len(plan.Steps) > 0 {
		last := plan.Steps[len(plan.Steps)-1]
		return fmt.Sprintf("%d step(s); latest [%s] %s", len(plan.Steps), last.Status, truncateTelegramThreadSummaryEvidence(last.Step, 140))
	}
	return truncateTelegramThreadSummaryEvidence(plan.Explanation, 180)
}

func telegramThreadAnalysisOperationSignal(operation session.OperationState) string {
	operation = session.NormalizeOperationState(operation)
	if !operation.Active() {
		return ""
	}
	var parts []string
	if operation.Status != "" {
		parts = append(parts, "status="+string(operation.Status))
	}
	if operation.Stage != "" {
		parts = append(parts, "stage="+operation.Stage)
	}
	if operation.Objective != "" {
		parts = append(parts, "objective="+truncateTelegramThreadSummaryEvidence(operation.Objective, 160))
	}
	if operation.Summary != "" {
		parts = append(parts, "summary="+truncateTelegramThreadSummaryEvidence(operation.Summary, 180))
	}
	if operation.Proposal.Active() {
		proposal := firstNonEmptyThreadAnalysisValue(operation.Proposal.Summary, operation.Proposal.ID, operation.Proposal.Kind)
		parts = append(parts, fmt.Sprintf("proposal[%s]=%s", firstNonEmptyThreadAnalysisValue(string(operation.Proposal.Status), "unknown"), truncateTelegramThreadSummaryEvidence(proposal, 140)))
	}
	if operation.PhasePlan.Active() && operation.PhasePlan.CurrentPhaseID != "" {
		parts = append(parts, "current_phase="+operation.PhasePlan.CurrentPhaseID)
	}
	return strings.Join(parts, "; ")
}

func telegramThreadAnalysisContinuationSignal(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	if !state.Active() {
		return ""
	}
	var parts []string
	if state.Status != "" {
		parts = append(parts, "status="+string(state.Status))
	}
	if state.DecisionID != "" {
		parts = append(parts, "decision="+state.DecisionID)
	}
	if state.RemainingTurns > 0 {
		parts = append(parts, fmt.Sprintf("remaining_turns=%d", state.RemainingTurns))
	}
	if state.Objective != "" {
		parts = append(parts, "objective="+truncateTelegramThreadSummaryEvidence(state.Objective, 140))
	}
	if state.StageSummary != "" {
		parts = append(parts, "stage="+truncateTelegramThreadSummaryEvidence(state.StageSummary, 140))
	}
	if state.ActionProposal.Active() {
		parts = append(parts, fmt.Sprintf("proposal[%s]=%s", firstNonEmptyThreadAnalysisValue(string(state.ActionProposal.Status), "unknown"), truncateTelegramThreadSummaryEvidence(firstNonEmptyThreadAnalysisValue(state.ActionProposal.Summary, state.ActionProposal.ID), 140)))
	}
	if state.ContinuationLease.Active() {
		parts = append(parts, fmt.Sprintf("lease[%s]=%s", firstNonEmptyThreadAnalysisValue(string(state.ContinuationLease.Status), "unknown"), firstNonEmptyThreadAnalysisValue(state.ContinuationLease.ID, state.ContinuationLease.ProposalID)))
	}
	if state.HandshakeBlockedReason != "" {
		parts = append(parts, "blocked="+state.HandshakeBlockedReason)
	}
	return strings.Join(parts, "; ")
}

func formatTelegramThreadAnalysisTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func formatTelegramThreadAnalysisAge(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "unknown age"
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	delta := now.UTC().Sub(t.UTC())
	if delta < 0 {
		delta = -delta
		return "in " + formatTelegramThreadAnalysisDuration(delta)
	}
	if delta < time.Minute {
		return "just now"
	}
	return formatTelegramThreadAnalysisDuration(delta) + " ago"
}

func formatTelegramThreadAnalysisDuration(delta time.Duration) string {
	switch {
	case delta >= 48*time.Hour:
		days := int64(delta / (24 * time.Hour))
		return fmt.Sprintf("%d days", days)
	case delta >= 2*time.Hour:
		hours := int64(delta / time.Hour)
		return fmt.Sprintf("%d hours", hours)
	case delta >= 2*time.Minute:
		minutes := int64(delta / time.Minute)
		return fmt.Sprintf("%d minutes", minutes)
	case delta >= time.Hour:
		return "1 hour"
	default:
		return "1 minute"
	}
}

func firstNonEmptyThreadAnalysisValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func truncateTelegramThreadSummaryEvidence(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	if limit <= 3 {
		return strings.TrimSpace(string(runes[:limit]))
	}
	return strings.TrimSpace(string(runes[:limit-3])) + "..."
}

func (c ThreadController) PromoteTelegramThread(ctx context.Context, chatID int64, senderID int64, threadID int64) (session.TelegramThreadPromotionResult, error) {
	if c.Promote == nil {
		return session.TelegramThreadPromotionResult{}, fmt.Errorf("runtime is unavailable")
	}
	c.stopForMessage(core.InboundMessage{
		ChatID:           chatID,
		SenderID:         senderID,
		TelegramThreadID: threadID,
	})
	text, err := c.Promote(ctx, chatID, senderID, threadID)
	if err != nil && c.IsAbsorbUserError != nil && c.IsAbsorbUserError(err) {
		return session.TelegramThreadPromotionResult{}, telegramcommands.ThreadUserError(err.Error())
	}
	return text, err
}

func (c ThreadController) PrepareTelegramThreadPromotion(ctx context.Context, chatID int64, senderID int64, handoffID string) (session.TelegramThreadPromotionResult, error) {
	if c.PreparePromotion == nil {
		return session.TelegramThreadPromotionResult{}, fmt.Errorf("runtime is unavailable")
	}
	text, err := c.PreparePromotion(ctx, chatID, senderID, handoffID)
	if err != nil && c.IsAbsorbUserError != nil && c.IsAbsorbUserError(err) {
		return session.TelegramThreadPromotionResult{}, telegramcommands.ThreadUserError(err.Error())
	}
	return text, err
}

func (c ThreadController) CancelTelegramThreadPromotion(ctx context.Context, chatID int64, senderID int64, handoffID string) (session.TelegramThreadPromotionResult, error) {
	if c.CancelPromotion == nil {
		return session.TelegramThreadPromotionResult{}, fmt.Errorf("runtime is unavailable")
	}
	text, err := c.CancelPromotion(ctx, chatID, senderID, handoffID)
	if err != nil && c.IsAbsorbUserError != nil && c.IsAbsorbUserError(err) {
		return session.TelegramThreadPromotionResult{}, telegramcommands.ThreadUserError(err.Error())
	}
	return text, err
}

func (c ThreadController) SupersedeTelegramThreadPromotion(ctx context.Context, chatID int64, senderID int64, handoffID string) (session.TelegramThreadPromotionResult, error) {
	if c.SupersedePromotion == nil {
		return session.TelegramThreadPromotionResult{}, fmt.Errorf("runtime is unavailable")
	}
	text, err := c.SupersedePromotion(ctx, chatID, senderID, handoffID)
	if err != nil && c.IsAbsorbUserError != nil && c.IsAbsorbUserError(err) {
		return session.TelegramThreadPromotionResult{}, telegramcommands.ThreadUserError(err.Error())
	}
	return text, err
}

func (c ThreadController) AbsorbTelegramThread(ctx context.Context, chatID int64, senderID int64, threadID int64) (string, error) {
	if c.Absorb == nil {
		return "", fmt.Errorf("runtime is unavailable")
	}
	c.stopForMessage(core.InboundMessage{
		ChatID:           chatID,
		SenderID:         senderID,
		TelegramThreadID: threadID,
	})
	text, err := c.Absorb(ctx, chatID, senderID, threadID)
	if err != nil && c.IsAbsorbUserError != nil && c.IsAbsorbUserError(err) {
		return "", telegramcommands.ThreadUserError(err.Error())
	}
	return text, err
}
