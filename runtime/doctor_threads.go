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
		writeDoctorLine(b, "telegram_thread "+strings.Join(parts, " "))
	}
}
