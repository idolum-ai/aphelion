//go:build linux

package doctor

import (
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) writeDoctorTelegramThreads(b *strings.Builder, key session.SessionKey) {
	if r == nil || r.store == nil || key.ChatID == 0 {
		WriteLine(b, "telegram_threads: unavailable")
		return
	}
	threads, err := r.store.ListTelegramThreads(key.ChatID, 100)
	if err != nil {
		WriteKV(b, "telegram_threads_error", err.Error())
		return
	}
	WriteKV(b, "telegram_threads_count", strconv.Itoa(len(threads)))
	handoffByThread := map[int64]session.TelegramThreadPromotionHandoff{}
	if handoffs, err := r.store.ListTelegramThreadPromotionHandoffs(key.ChatID, 100); err == nil {
		for _, handoff := range handoffs {
			if _, exists := handoffByThread[handoff.ThreadID]; !exists {
				handoffByThread[handoff.ThreadID] = handoff
			}
		}
	} else {
		WriteKV(b, "telegram_thread_promotion_handoffs_error", err.Error())
	}
	WriteKV(b, "telegram_thread_promotion_handoffs_count", strconv.Itoa(len(handoffByThread)))
	if reminders, err := r.store.ListTelegramThreadReminders(key.ChatID, session.TelegramThreadReminderStatusPending, 100); err == nil {
		WriteKV(b, "telegram_thread_reminders_pending", strconv.Itoa(len(reminders)))
	} else {
		WriteKV(b, "telegram_thread_reminders_error", err.Error())
	}
	for _, thread := range threads {
		parts := []string{
			"visible=" + strconv.FormatInt(thread.DisplaySlot, 10),
			"internal_id=" + strconv.FormatInt(thread.ThreadID, 10),
			"status=" + strings.TrimSpace(string(thread.Status)),
			"session=" + session.SessionIDForKey(session.SessionKey{ChatID: thread.ChatID, UserID: 0, Scope: session.TelegramThreadScopeRef(thread.ChatID, thread.ThreadID)}),
		}
		if thread.CreatedMessageID > 0 {
			parts = append(parts, "created_message="+strconv.FormatInt(thread.CreatedMessageID, 10))
		}
		if !thread.LastActivityAt.IsZero() {
			parts = append(parts, "last_activity="+thread.LastActivityAt.UTC().Format(TimeFormat))
		}
		eligibility := thread.ReminderEligibility(time.Now().UTC(), session.DefaultTelegramThreadReminderPolicy())
		parts = append(parts,
			"reminder_eligible="+strconv.FormatBool(eligibility.Eligible),
			"reminder_reason="+strings.TrimSpace(eligibility.Reason),
			"reminder_summary="+strings.TrimSpace(eligibility.SummaryKind),
		)
		if reminders, err := r.store.ListTelegramThreadReminders(key.ChatID, session.TelegramThreadReminderStatusPending, 100); err == nil {
			for _, reminder := range reminders {
				if reminder.ThreadID == thread.ThreadID {
					parts = append(parts, "reminder_pending_message="+strconv.FormatInt(reminder.MessageID, 10))
					break
				}
			}
		}
		if name := strings.TrimSpace(thread.ArchivedDisplayName); name != "" {
			parts = append(parts, "archived_display_name="+strconv.Quote(name))
		}
		if handoff, ok := handoffByThread[thread.ThreadID]; ok {
			parts = append(parts,
				"promotion_handoff="+strconv.Quote(strings.TrimSpace(handoff.HandoffID)),
				"promotion_status="+strings.TrimSpace(string(handoff.Status)),
				"promotion_next_action="+telegramThreadPromotionDoctorNextAction(handoff),
			)
			if memory, ok := session.DecodeTelegramThreadPromotionMemoryCandidates(handoff.MemoryDigestJSON); ok {
				parts = append(parts, "promotion_memory_candidates="+strconv.Itoa(len(memory)))
			}
			if resources, ok := session.DecodeTelegramThreadPromotionResourceCandidates(handoff.ResourceReviewJSON); ok {
				parts = append(parts, "promotion_resource_candidates="+strconv.Itoa(len(resources)))
			}
			if child, ok := session.DecodeTelegramThreadPromotionProposedChild(handoff.ProposedChildJSON); ok && strings.TrimSpace(child.AgentID) != "" {
				parts = append(parts, "promotion_proposed_child="+strconv.Quote(strings.TrimSpace(child.AgentID)))
			}
		}
		WriteLine(b, "telegram_thread "+strings.Join(parts, " "))
	}
}

func telegramThreadPromotionDoctorNextAction(handoff session.TelegramThreadPromotionHandoff) string {
	switch handoff.Status {
	case session.TelegramThreadPromotionStatusDraft:
		if strings.TrimSpace(handoff.ProposedChildJSON) == "" || strings.TrimSpace(handoff.ProposedChildJSON) == "{}" {
			return "generate_review_package"
		}
		return "review_package"
	case session.TelegramThreadPromotionStatusReady:
		return "await_apply_approval"
	case session.TelegramThreadPromotionStatusApproved:
		return "apply_with_separate_authority"
	case session.TelegramThreadPromotionStatusCancelled, session.TelegramThreadPromotionStatusSuperseded:
		return "none"
	default:
		return "inspect"
	}
}
