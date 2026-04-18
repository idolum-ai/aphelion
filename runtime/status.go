//go:build linux

package runtime

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

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
