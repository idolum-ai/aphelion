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
			)
		}
		writeDoctorLine(b, "telegram_thread "+strings.Join(parts, " "))
	}
}
