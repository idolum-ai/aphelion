//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const telegramThreadReminderManualMax = 3

type TelegramThreadReminderSweepPolicy struct {
	ReminderPolicy    session.TelegramThreadReminderPolicy
	MaxPerSweep       int
	PerThreadCooldown time.Duration
	RecentChatWindow  time.Duration
}

type TelegramThreadReminderSweepResult struct {
	ChatID     int64
	Sent       int
	Suppressed int
	Candidates int
	Scanned    int
	ThreadIDs  []int64
}

func DefaultTelegramThreadReminderSweepPolicy() TelegramThreadReminderSweepPolicy {
	return TelegramThreadReminderSweepPolicy{
		ReminderPolicy:    session.DefaultTelegramThreadReminderPolicy(),
		MaxPerSweep:       1,
		PerThreadCooldown: 72 * time.Hour,
		RecentChatWindow:  6 * time.Hour,
	}
}

func SendTelegramThreadReminderSweep(ctx context.Context, sender Sender, router ThreadRouter, chatID int64, senderID int64, now time.Time, policy TelegramThreadReminderSweepPolicy) (TelegramThreadReminderSweepResult, error) {
	return sendTelegramThreadReminderSweep(ctx, sender, router, chatID, senderID, now, policy)
}

func sendTelegramThreadReminders(ctx context.Context, sender commandSender, router commandThreadRouter, msg core.InboundMessage) (bool, error) {
	policy := DefaultTelegramThreadReminderSweepPolicy()
	policy.MaxPerSweep = telegramThreadReminderManualMax
	result, err := sendTelegramThreadReminderSweep(ctx, sender, router, msg.ChatID, msg.SenderID, time.Now().UTC(), policy)
	if err != nil {
		return true, err
	}
	if result.Sent == 0 {
		return sendTelegramThreadText(ctx, sender, msg, "No stale side threads need reminders right now.")
	}
	return sendTelegramThreadText(ctx, sender, msg, fmt.Sprintf("Sent %d stale-thread reminder%s.", result.Sent, pluralSuffix(result.Sent)))
}

func sendTelegramThreadReminderSweep(ctx context.Context, sender commandSender, router commandThreadRouter, chatID int64, senderID int64, now time.Time, policy TelegramThreadReminderSweepPolicy) (TelegramThreadReminderSweepResult, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	policy = normalizeTelegramThreadReminderSweepPolicy(policy)
	result := TelegramThreadReminderSweepResult{ChatID: chatID}
	threads, err := router.TelegramThreads(chatID)
	if err != nil {
		return result, err
	}
	reminders, err := router.TelegramThreadReminders(chatID, "", 200)
	if err != nil {
		return result, err
	}
	selected, stats := selectTelegramThreadReminderCandidates(threads, reminders, now, policy)
	result.Scanned = stats.Scanned
	result.Candidates = stats.Candidates
	result.Suppressed = stats.Suppressed
	if len(selected) == 0 {
		return result, nil
	}
	for _, thread := range selected {
		if _, err := sendTelegramThreadReminder(ctx, sender, router, chatID, senderID, thread); err != nil {
			return result, err
		}
		result.Sent++
		result.ThreadIDs = append(result.ThreadIDs, thread.ThreadID)
	}
	return result, nil
}

func normalizeTelegramThreadReminderSweepPolicy(policy TelegramThreadReminderSweepPolicy) TelegramThreadReminderSweepPolicy {
	if policy.ReminderPolicy.StaleAfter <= 0 {
		policy.ReminderPolicy = session.DefaultTelegramThreadReminderPolicy()
	}
	if policy.MaxPerSweep <= 0 {
		policy.MaxPerSweep = 1
	}
	if policy.PerThreadCooldown <= 0 {
		policy.PerThreadCooldown = 72 * time.Hour
	}
	if policy.RecentChatWindow <= 0 {
		policy.RecentChatWindow = 6 * time.Hour
	}
	return policy
}

type telegramThreadReminderSelectionStats struct {
	Scanned    int
	Candidates int
	Suppressed int
}

type telegramThreadReminderCandidate struct {
	Thread session.TelegramThread
	Score  float64
}

func selectTelegramThreadReminderCandidates(threads []session.TelegramThread, reminders []session.TelegramThreadReminder, now time.Time, policy TelegramThreadReminderSweepPolicy) ([]session.TelegramThread, telegramThreadReminderSelectionStats) {
	policy = normalizeTelegramThreadReminderSweepPolicy(policy)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	latestByThread := latestTelegramThreadReminderByThread(reminders)
	recentChatReminder := chatHasRecentTelegramThreadReminder(reminders, now, policy.RecentChatWindow)
	stats := telegramThreadReminderSelectionStats{Scanned: len(threads)}
	candidates := make([]telegramThreadReminderCandidate, 0, len(threads))
	for _, thread := range threads {
		eligibility := thread.ReminderEligibility(now, policy.ReminderPolicy)
		if !eligibility.Eligible {
			continue
		}
		reminder := latestByThread[thread.ThreadID]
		if reminderSuppressesTelegramThreadReminder(thread, reminder, now, policy.PerThreadCooldown) {
			stats.Suppressed++
			continue
		}
		stats.Candidates++
		candidates = append(candidates, telegramThreadReminderCandidate{Thread: thread, Score: scoreTelegramThreadReminderCandidate(thread, eligibility, recentChatReminder)})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return telegramThreadLastActiveAt(candidates[i].Thread).Before(telegramThreadLastActiveAt(candidates[j].Thread))
		}
		return candidates[i].Score > candidates[j].Score
	})
	limit := policy.MaxPerSweep
	if limit > len(candidates) {
		limit = len(candidates)
	}
	selected := make([]session.TelegramThread, 0, limit)
	for i := 0; i < limit; i++ {
		selected = append(selected, candidates[i].Thread)
	}
	return selected, stats
}

func latestTelegramThreadReminderByThread(reminders []session.TelegramThreadReminder) map[int64]session.TelegramThreadReminder {
	latestByThread := map[int64]session.TelegramThreadReminder{}
	for _, reminder := range reminders {
		if reminder.ThreadID <= 0 {
			continue
		}
		current, ok := latestByThread[reminder.ThreadID]
		if !ok || reminder.UpdatedAt.After(current.UpdatedAt) || reminder.ID > current.ID && reminder.UpdatedAt.Equal(current.UpdatedAt) {
			latestByThread[reminder.ThreadID] = reminder
		}
	}
	return latestByThread
}

func chatHasRecentTelegramThreadReminder(reminders []session.TelegramThreadReminder, now time.Time, window time.Duration) bool {
	if window <= 0 {
		return false
	}
	cutoff := now.UTC().Add(-window)
	for _, reminder := range reminders {
		if reminder.CreatedAt.After(cutoff) || reminder.UpdatedAt.After(cutoff) {
			return true
		}
	}
	return false
}

func scoreTelegramThreadReminderCandidate(thread session.TelegramThread, eligibility session.TelegramThreadReminderEligibility, recentChatReminder bool) float64 {
	score := math.Min(eligibility.Age.Hours()/24, 7)
	if strings.TrimSpace(thread.CreatedText) != "" {
		score += 1
	}
	if thread.CreatedBySenderID != 0 {
		score += 1
	}
	if strings.EqualFold(strings.TrimSpace(eligibility.SummaryKind), "privacy_softened") {
		score -= 0.5
	}
	if recentChatReminder {
		score -= 2
	}
	return score
}

func reminderSuppressesTelegramThreadReminder(thread session.TelegramThread, reminder session.TelegramThreadReminder, now time.Time, cooldown time.Duration) bool {
	if reminder.ID == 0 || reminder.ThreadID != thread.ThreadID {
		return false
	}
	lastActivity := telegramThreadLastActiveAt(thread)
	if lastActivity.IsZero() {
		return false
	}
	sameActivityEpoch := !reminder.SourceLastActivityAt.IsZero() && !reminder.SourceLastActivityAt.Before(lastActivity)
	if sameActivityEpoch {
		switch reminder.Status {
		case session.TelegramThreadReminderStatusPending, session.TelegramThreadReminderStatusIgnored, session.TelegramThreadReminderStatusResumed, session.TelegramThreadReminderStatusAbsorbed:
			return true
		}
	}
	if cooldown > 0 && !reminder.CreatedAt.IsZero() && now.UTC().Sub(reminder.CreatedAt.UTC()) < cooldown {
		return true
	}
	return false
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func sendTelegramThreadReminder(ctx context.Context, sender commandSender, router commandThreadRouter, chatID int64, senderID int64, thread session.TelegramThread) (int64, error) {
	if sender == nil || router == nil || chatID == 0 || thread.ThreadID <= 0 {
		return 0, nil
	}
	rendered := renderTelegramThreadReminder(thread)
	rows := telegramThreadReminderRows(thread.ThreadID)
	messageID, err := sender.SendInlineKeyboard(ctx, chatID, rendered, rows, nil)
	if err != nil {
		return 0, err
	}
	lastActivity := telegramThreadLastActiveAt(thread)
	if err := router.RecordTelegramThreadReminderMessage(chatID, thread.ThreadID, messageID, telegramThreadReminderSummary(thread), session.TelegramThreadReminderSummaryKind(thread), lastActivity, senderID); err != nil {
		return 0, err
	}
	return messageID, nil
}

func renderTelegramThreadReminder(thread session.TelegramThread) string {
	label := telegramThreadDisplayLabel(thread)
	summary := telegramThreadReminderSummary(thread)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", label)
	if strings.TrimSpace(summary) == "" {
		summary = "this side thread"
	}
	fmt.Fprintf(&b, "Last time we chatted here, we were talking about %s.\n", summary)
	b.WriteString("Want to pick this thread up again?\n\n")
	b.WriteString("Reply to this message to continue.\n\n")
	b.WriteString("You can also ignore this reminder, or absorb the thread into the main conversation.")
	return b.String()
}

func telegramThreadReminderSummary(thread session.TelegramThread) string {
	if session.TelegramThreadReminderSummaryKind(thread) == "privacy_softened" {
		return "a personal conversation"
	}
	preview := compactThreadPreview(thread.CreatedText)
	if preview == "" {
		return "this side thread"
	}
	return preview
}

func telegramThreadReminderRows(threadID int64) [][]telegram.InlineButton {
	return [][]telegram.InlineButton{{
		{Text: "ignore", CallbackData: encodeTelegramThreadReminderIgnoreCallback(threadID)},
		{Text: "absorb", CallbackData: encodeTelegramThreadReminderAbsorbCallback(threadID)},
	}}
}
