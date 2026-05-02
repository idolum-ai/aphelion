//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

const startupRecoveryAutoResumePrefix = "[restart recovery auto-resume]"

type startupRecoveryAutoResumeResult struct {
	Queued  int
	Skipped int
}

func (r startupRecoveryAutoResumeResult) total() int {
	return r.Queued + r.Skipped
}

func recordStartupRecoveryAutoResumePayload(result startupRecoveryAutoResumeResult) map[string]any {
	return map[string]any{
		"queued":  result.Queued,
		"skipped": result.Skipped,
	}
}

func (r *Runtime) startStartupRecoveryAutoResume(runs []session.TurnRun, now time.Time) startupRecoveryAutoResumeResult {
	result := startupRecoveryAutoResumeResult{}
	if r == nil || len(runs) == 0 {
		return result
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	for _, run := range latestAutoResumableRunsByChat(runs) {
		actor, ok := r.startupRecoveryAutoResumeActor(run)
		if !ok {
			result.Skipped++
			continue
		}
		msg := startupRecoveryAutoResumeMessage(run, actor, now)
		key := startupRecoveryRunSessionKey(run)
		payload := map[string]any{
			"run_id":           run.ID,
			"chat_id":          run.ChatID,
			"session_id":       strings.TrimSpace(run.SessionID),
			"request_preview":  truncatePreview(run.RequestText, 220),
			"auto_resume_mode": "bounded_state_verify",
		}
		r.recordExecutionEvent(key, core.ExecutionEventRecoveryAutoResume, "recovery", "queued", payload, now)
		result.Queued++
		go func(run session.TurnRun, actor principal.Principal, msg core.InboundMessage, key session.SessionKey) {
			if _, err := r.handleInternalContinuation(context.Background(), actor, msg); err != nil {
				log.Printf("WARN startup recovery auto-resume failed chat_id=%d run_id=%d err=%v", run.ChatID, run.ID, err)
				failPayload := map[string]any{
					"run_id":          run.ID,
					"chat_id":         run.ChatID,
					"request_preview": truncatePreview(run.RequestText, 220),
					"error":           trimError(err.Error()),
				}
				r.recordExecutionEvent(key, core.ExecutionEventRecoveryAutoResume, "recovery", "failed", failPayload, time.Now().UTC())
			}
		}(run, actor, msg, key)
	}
	return result
}

func latestAutoResumableRunsByChat(runs []session.TurnRun) []session.TurnRun {
	byChat := make(map[int64]session.TurnRun)
	for _, run := range runs {
		if !startupRecoveryRunAutoResumable(run) {
			continue
		}
		prior, exists := byChat[run.ChatID]
		if !exists || run.StartedAt.After(prior.StartedAt) || (run.StartedAt.Equal(prior.StartedAt) && run.ID > prior.ID) {
			byChat[run.ChatID] = run
		}
	}
	out := make([]session.TurnRun, 0, len(byChat))
	for _, run := range byChat {
		out = append(out, run)
	}
	return out
}

func startupRecoveryRunAutoResumable(run session.TurnRun) bool {
	if run.Kind != session.TurnRunKindInteractive || run.ChatID <= 0 {
		return false
	}
	scope := session.NormalizeScopeRef(run.Scope)
	if strings.TrimSpace(scope.DurableAgentID) != "" {
		return false
	}
	if scope.Kind != "" && scope.Kind != session.ScopeKindTelegramDM {
		return false
	}
	request := strings.TrimSpace(run.RequestText)
	if request == "" || strings.HasPrefix(request, startupRecoveryAutoResumePrefix) {
		return false
	}
	return true
}

func (r *Runtime) startupRecoveryAutoResumeActor(run session.TurnRun) (principal.Principal, bool) {
	if r == nil || r.resolver == nil {
		return principal.Principal{}, false
	}
	if actor, ok := r.resolver.ResolveTelegramUser(run.ChatID); ok {
		return actor, true
	}
	scope := session.NormalizeScopeRef(run.Scope)
	if run.UserID > 0 && (run.ChatID == run.UserID || (scope.Kind == session.ScopeKindTelegramDM && scope.ID == strconv.FormatInt(run.UserID, 10))) {
		return r.resolver.ResolveTelegramUser(run.UserID)
	}
	return principal.Principal{}, false
}

func startupRecoveryAutoResumeMessage(run session.TurnRun, actor principal.Principal, now time.Time) core.InboundMessage {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	text := strings.Join([]string{
		startupRecoveryAutoResumePrefix,
		"The previous process ended while this turn was running.",
		"Original request: " + fmt.Sprintf("%q", truncatePreview(run.RequestText, 500)),
		"First verify persisted state, including git/service/operation state when relevant.",
		"Then continue only the still-needed bounded work, or report clearly that the work is already complete or blocked.",
		"Do not repeat destructive or external actions unless current state proves they are still needed and already within the prior user request.",
	}, "\n")
	return core.InboundMessage{
		ChatID:       run.ChatID,
		SenderID:     actor.TelegramUserID,
		SenderName:   actorLabel(actor),
		Text:         text,
		Origin:       core.InboundOriginStartupRecovery,
		OriginDetail: "auto_resume",
		Timestamp:    now,
	}
}

func startupRecoveryRunSessionKey(run session.TurnRun) session.SessionKey {
	scope := session.NormalizeScopeRef(run.Scope)
	if scope.IsZero() && run.ChatID != 0 {
		scope = telegramDMScopeRef(run.ChatID)
	}
	return session.SessionKey{ChatID: run.ChatID, UserID: run.UserID, Scope: scope}
}
