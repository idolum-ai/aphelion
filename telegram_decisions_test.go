//go:build linux

package main

import (
	"context"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

type decisionTestSender struct {
	inline    []decisionInlineCall
	edits     []decisionEditCall
	deletes   []decisionDeleteCall
	answers   []decisionAnswerCall
	answerErr error
}

type decisionInlineCall struct {
	chatID  int64
	text    string
	rows    [][]telegram.InlineButton
	replyTo *int64
}

type decisionEditCall struct {
	chatID    int64
	messageID int64
	text      string
	rows      [][]telegram.InlineButton
	at        time.Time
}

type decisionDeleteCall struct {
	chatID    int64
	messageID int64
}

type decisionAnswerCall struct {
	id   string
	text string
	at   time.Time
}

func (s *decisionTestSender) SendInlineKeyboard(_ context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error) {
	s.inline = append(s.inline, decisionInlineCall{chatID: chatID, text: text, rows: rows, replyTo: replyTo})
	return int64(len(s.inline)), nil
}

func (s *decisionTestSender) EditMessageText(_ context.Context, chatID int64, messageID int64, text string, _ string) error {
	s.edits = append(s.edits, decisionEditCall{chatID: chatID, messageID: messageID, text: text, at: time.Now().UTC()})
	return nil
}

func (s *decisionTestSender) EditMessageTextWithInlineKeyboard(_ context.Context, chatID int64, messageID int64, text string, _ string, rows [][]telegram.InlineButton) error {
	s.edits = append(s.edits, decisionEditCall{chatID: chatID, messageID: messageID, text: text, rows: rows, at: time.Now().UTC()})
	return nil
}

func (s *decisionTestSender) DeleteMessage(_ context.Context, chatID int64, messageID int64) error {
	s.deletes = append(s.deletes, decisionDeleteCall{chatID: chatID, messageID: messageID})
	return nil
}

func (s *decisionTestSender) AnswerCallbackQuery(_ context.Context, id string, text string) error {
	s.answers = append(s.answers, decisionAnswerCall{id: id, text: text, at: time.Now().UTC()})
	return s.answerErr
}

type decisionTestArtifactKeeper struct {
	messages []core.InboundMessage
	err      error
}

func (k *decisionTestArtifactKeeper) KeepTelegramArtifactsPermanently(_ context.Context, msg core.InboundMessage) error {
	k.messages = append(k.messages, msg)
	return k.err
}

type decisionTestRouter struct {
	status             core.SessionStatus
	statusForMessageFn func(core.InboundMessage) core.SessionStatus
	stopCalls          []int64
	stopForMessage     []core.InboundMessage
	routed             []core.InboundMessage
}

func (r *decisionTestRouter) Status(chatID int64) core.SessionStatus {
	return r.status
}

func (r *decisionTestRouter) StatusForMessage(msg core.InboundMessage) core.SessionStatus {
	if r.statusForMessageFn != nil {
		return r.statusForMessageFn(msg)
	}
	return r.status
}

func (r *decisionTestRouter) Stop(chatID int64) core.StopResult {
	r.stopCalls = append(r.stopCalls, chatID)
	return core.StopResult{ActiveCanceled: true}
}

func (r *decisionTestRouter) StopForMessage(msg core.InboundMessage) core.StopResult {
	r.stopForMessage = append(r.stopForMessage, msg)
	return core.StopResult{ActiveCanceled: true}
}

func (r *decisionTestRouter) Route(_ context.Context, msg core.InboundMessage) {
	r.routed = append(r.routed, msg)
}

func waitForDecisionInline(t *testing.T, sender *decisionTestSender) decisionInlineCall {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(sender.inline) > 0 {
			return sender.inline[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("inline = %#v, want at least one prompt", sender.inline)
	return decisionInlineCall{}
}

func waitForDecisionEdit(t *testing.T, sender *decisionTestSender, count int) decisionEditCall {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(sender.edits) >= count {
			return sender.edits[count-1]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("edits = %#v, want at least %d edits", sender.edits, count)
	return decisionEditCall{}
}

func callbackDataForButton(t *testing.T, rows [][]telegram.InlineButton, label string) string {
	t.Helper()
	for _, row := range rows {
		for _, button := range row {
			if button.Text == label {
				return button.CallbackData
			}
		}
	}
	t.Fatalf("rows = %#v, want button %q", rows, label)
	return ""
}

func hasInlineButton(rows [][]telegram.InlineButton, label string) bool {
	for _, row := range rows {
		for _, button := range row {
			if button.Text == label {
				return true
			}
		}
	}
	return false
}
