//go:build linux

package telegramcommands

import (
	"context"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

type stubCommandSender struct {
	msgs       []core.OutboundMessage
	inline     []stubInlineCall
	edits      []stubEditCall
	editClear  []stubEditCall
	editInline []stubEditInlineCall
	editErr    error
	answers    []stubAnswerCall
	answerErr  error
	order      *[]string
}

type stubInlineCall struct {
	chatID  int64
	text    string
	rows    [][]telegram.InlineButton
	replyTo *int64
}

type stubEditCall struct {
	chatID    int64
	messageID int64
	text      string
	parseMode string
}

type stubEditInlineCall struct {
	chatID    int64
	messageID int64
	text      string
	parseMode string
	rows      [][]telegram.InlineButton
}

type stubAnswerCall struct {
	id   string
	text string
}

type stubCallbackErrorRecord struct {
	chatID       int64
	callbackKind string
	err          error
}

func (s *stubCommandSender) SendMessage(_ context.Context, msg core.OutboundMessage) (int64, error) {
	s.msgs = append(s.msgs, msg)
	return int64(len(s.msgs)), nil
}

func (s *stubCommandSender) SendInlineKeyboard(_ context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error) {
	s.inline = append(s.inline, stubInlineCall{
		chatID:  chatID,
		text:    text,
		rows:    rows,
		replyTo: replyTo,
	})
	return int64(len(s.inline)), nil
}

func (s *stubCommandSender) EditMessageText(_ context.Context, chatID int64, messageID int64, text string, parseMode string) error {
	s.edits = append(s.edits, stubEditCall{
		chatID:    chatID,
		messageID: messageID,
		text:      text,
		parseMode: parseMode,
	})
	return s.editErr
}

func (s *stubCommandSender) EditMessageTextWithoutInlineKeyboard(_ context.Context, chatID int64, messageID int64, text string, parseMode string) error {
	s.editClear = append(s.editClear, stubEditCall{
		chatID:    chatID,
		messageID: messageID,
		text:      text,
		parseMode: parseMode,
	})
	return s.editErr
}

func (s *stubCommandSender) EditMessageTextWithInlineKeyboard(_ context.Context, chatID int64, messageID int64, text string, parseMode string, rows [][]telegram.InlineButton) error {
	s.editInline = append(s.editInline, stubEditInlineCall{
		chatID:    chatID,
		messageID: messageID,
		text:      text,
		parseMode: parseMode,
		rows:      rows,
	})
	return s.editErr
}

func (s *stubCommandSender) AnswerCallbackQuery(_ context.Context, id string, text string) error {
	if s.order != nil {
		*s.order = append(*s.order, "answer:"+text)
	}
	s.answers = append(s.answers, stubAnswerCall{
		id:   id,
		text: text,
	})
	return s.answerErr
}
