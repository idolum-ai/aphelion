//go:build linux

package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

// ToggleProgressView re-renders the active Working/progress card for runID in
// summary or details mode. It is a presentation-only control: it does not stop,
// detach, approve, queue, or otherwise mutate turn authority.
func (r *Runtime) ToggleProgressView(ctx context.Context, chatID int64, senderID int64, runID int64, details bool) (bool, string, error) {
	if r == nil || r.store == nil || r.outbound == nil || chatID == 0 || runID <= 0 {
		return false, "", nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if details && !r.IsTelegramAdmin(senderID) {
		return false, "", nil
	}
	run, ok, err := r.activeProgressTurnRun(chatID, runID)
	if err != nil {
		return false, "", err
	}
	if !ok || run.ProgressMessageID == 0 {
		return false, "", nil
	}
	key := progressTurnRunExecutionKey(run)
	keyboardEditor, ok := r.outbound.(messageKeyboardEditor)
	if !ok {
		return false, "", fmt.Errorf("progress detail toggle unavailable: outbound sender cannot edit inline keyboards")
	}
	reporter := &toolProgressReporter{
		runtime:        r,
		executionKey:   key,
		sender:         r.outbound,
		keyboardEditor: keyboardEditor,
		chatID:         chatID,
		messageID:      run.ProgressMessageID,
		mode:           "all",
		style:          "semantic",
		window:         r.toolProgressWindow,
		runID:          runID,
		controls:       deliberationControlRows(runID, details),
		seenKeys:       make(map[string]struct{}),
		validateText:   r.filterProgressText,
		displayPrefix:  r.telegramPresentationForTurnRun(run).Prefix,
	}
	if reporter.window <= 0 {
		reporter.window = 4
	}
	view := session.TurnProgressViewSummary
	if details {
		view = session.TurnProgressViewDetails
	}
	pair := reporter.renderProgressTextPairLocked(false)
	text := reporter.selectProgressTextLocked(pair, details)
	text = reporter.prefixProgressText(text)
	if strings.TrimSpace(text) == "" {
		return false, "", nil
	}
	if err := keyboardEditor.EditMessageTextWithInlineKeyboard(ctx, chatID, run.ProgressMessageID, text, "", reporter.controls); err != nil {
		return false, "", err
	}
	if err := r.store.SetTurnProgressSelectedView(runID, run.ProgressMessageID, view); err != nil {
		return false, "", err
	}
	reporter.saveProgressRenderCache(details, pair)
	r.recordExecutionEvent(key, core.ExecutionEventDeliveryProgressEdited, "progress", "edited", map[string]any{
		"method":           "edit_inline",
		"message_id":       run.ProgressMessageID,
		"run_id":           runID,
		"view":             progressViewName(details),
		"source_class":     "canonical",
		"source_surface":   "outbound_transport_ledger",
		"visibility":       "human_render_unknown",
		"transport_status": "acknowledged",
	}, time.Now().UTC())
	return true, text, nil
}

func (r *Runtime) activeProgressTurnRun(chatID int64, runID int64) (session.TurnRun, bool, error) {
	if r == nil || r.store == nil || chatID == 0 || runID <= 0 {
		return session.TurnRun{}, false, nil
	}
	run, err := r.store.TurnRun(runID)
	if errors.Is(err, sql.ErrNoRows) {
		return session.TurnRun{}, false, nil
	}
	if err != nil {
		return session.TurnRun{}, false, err
	}
	if run == nil || run.ChatID != chatID || run.Status != session.TurnRunStatusRunning || run.ProgressMessageID == 0 {
		return session.TurnRun{}, false, nil
	}
	return *run, true, nil
}

func progressToggleScopeRef(chatID int64) session.ScopeRef {
	if chatID < 0 {
		return telegramGroupScopeRef(chatID)
	}
	return telegramDMScopeRef(chatID)
}

func progressTurnRunExecutionKey(run session.TurnRun) session.SessionKey {
	scope := session.NormalizeScopeRef(run.Scope)
	if scope.IsZero() {
		scope = progressToggleScopeRef(run.ChatID)
	}
	return session.SessionKey{
		ChatID: run.ChatID,
		UserID: run.UserID,
		Scope:  scope,
	}
}

func progressViewName(details bool) string {
	if details {
		return session.TurnProgressViewDetails
	}
	return session.TurnProgressViewSummary
}
