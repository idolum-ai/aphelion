//go:build linux

package runtime

import (
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) writeDoctorTelegramThreads(b *strings.Builder, key session.SessionKey) {
	if r == nil || r.store == nil || key.ChatID == 0 {
		writeDoctorLine(b, "telegram_threads: unavailable")
		return
	}
	threads, err := r.store.ListTelegramThreads(key.ChatID, 100)
	if err != nil {
		writeDoctorKV(b, "telegram_threads_error", err.Error())
		return
	}
	writeDoctorKV(b, "telegram_threads_count", strconv.Itoa(len(threads)))
	handoffByThread := map[int64]session.TelegramThreadPromotionHandoff{}
	if handoffs, err := r.store.ListTelegramThreadPromotionHandoffs(key.ChatID, 100); err == nil {
		for _, handoff := range handoffs {
			if _, exists := handoffByThread[handoff.ThreadID]; !exists {
				handoffByThread[handoff.ThreadID] = handoff
			}
		}
	} else {
		writeDoctorKV(b, "telegram_thread_promotion_handoffs_error", err.Error())
	}
	writeDoctorKV(b, "telegram_thread_promotion_handoffs_count", strconv.Itoa(len(handoffByThread)))
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
			parts = append(parts, "last_activity="+thread.LastActivityAt.UTC().Format(doctorTimeFormat))
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
		writeDoctorLine(b, "telegram_thread "+strings.Join(parts, " "))
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
