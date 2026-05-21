//go:build linux

package runtime

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

type streamEditor struct {
	sender          OutboundSender
	editor          messageEditor
	keyboardEditor  messageKeyboardEditor
	keyboardClearer messageKeyboardClearer
	deleter         messageDeleter
	chatID          int64
	replyTo         *int64
	interval        time.Duration
	cursor          string
	displayPrefix   string
	controlRows     [][]telegram.InlineButton
	onMessageID     func(int64)
	messageID       int64
	buffer          strings.Builder
	lastEdit        time.Time
}

type mutableMessageMarker interface {
	MarkMessageMutable(messageID int64)
}

func (r *Runtime) newStreamEditor(msg core.InboundMessage) *streamEditor {
	if r == nil || r.outbound == nil {
		return nil
	}
	editor, ok := r.outbound.(messageEditor)
	if !ok {
		return nil
	}

	stream := &streamEditor{
		sender:        r.outbound,
		editor:        editor,
		chatID:        msg.ChatID,
		replyTo:       replyToMessageID(msg.MessageID),
		interval:      r.streamEditInterval,
		cursor:        r.streamCursor,
		displayPrefix: r.telegramPresentationForMessage(msg).Prefix,
	}
	if keyboardEditor, ok := r.outbound.(messageKeyboardEditor); ok {
		stream.keyboardEditor = keyboardEditor
	}
	if keyboardClearer, ok := r.outbound.(messageKeyboardClearer); ok {
		stream.keyboardClearer = keyboardClearer
	}
	if deleter, ok := r.outbound.(messageDeleter); ok {
		stream.deleter = deleter
	}
	return stream
}

func (s *streamEditor) OnChunk(ctx context.Context, text string) error {
	if s == nil || text == "" {
		return nil
	}
	s.buffer.WriteString(text)
	if s.messageID == 0 || time.Since(s.lastEdit) >= s.interval {
		return s.flush(ctx, false)
	}
	return nil
}

func (s *streamEditor) Finish(ctx context.Context) (int64, error) {
	if s == nil {
		return 0, nil
	}
	if s.buffer.Len() == 0 {
		return 0, nil
	}
	if err := s.flush(ctx, true); err != nil {
		return 0, err
	}
	return s.messageID, nil
}

func (s *streamEditor) FinishStopped(ctx context.Context) (int64, error) {
	if s == nil {
		return 0, nil
	}
	text := strings.TrimSpace(s.buffer.String())
	if text == "" {
		return 0, nil
	}
	if !strings.HasSuffix(text, "Stopped.") {
		s.buffer.Reset()
		s.buffer.WriteString(text)
		s.buffer.WriteString("\n\nStopped.")
	}
	if err := s.flush(ctx, true); err != nil {
		return 0, err
	}
	return s.messageID, nil
}

func (s *streamEditor) Abort(ctx context.Context) {
	if s == nil || s.messageID == 0 || s.deleter == nil {
		return
	}
	if err := s.deleter.DeleteMessage(ctx, s.chatID, s.messageID); err != nil {
		log.Printf("WARN delete streamed message chat_id=%d msg_id=%d err=%v", s.chatID, s.messageID, err)
	}
}

func (s *streamEditor) flush(ctx context.Context, done bool) error {
	text := s.buffer.String()
	if !done {
		text += s.cursor
	}
	text = s.prefixText(text)
	rows := s.activeControlRows(done)
	if s.messageID == 0 {
		msgID, err := s.sendInitial(ctx, text)
		if err != nil {
			return err
		}
		s.messageID = msgID
		if marker, ok := s.sender.(mutableMessageMarker); ok {
			marker.MarkMessageMutable(msgID)
		}
		if s.onMessageID != nil {
			s.onMessageID(msgID)
		}
		s.lastEdit = time.Now()
		return nil
	}

	if err := s.editExisting(ctx, text, rows, done); err != nil {
		msgID, sendErr := s.sendInitial(ctx, text)
		if sendErr != nil {
			return err
		}
		if s.deleter != nil {
			if delErr := s.deleter.DeleteMessage(ctx, s.chatID, s.messageID); delErr != nil {
				log.Printf("WARN delete superseded streamed message chat_id=%d msg_id=%d err=%v", s.chatID, s.messageID, delErr)
			}
		}
		s.messageID = msgID
		if marker, ok := s.sender.(mutableMessageMarker); ok {
			marker.MarkMessageMutable(msgID)
		}
		if s.onMessageID != nil {
			s.onMessageID(msgID)
		}
		s.lastEdit = time.Now()
		return nil
	}

	s.lastEdit = time.Now()
	return nil
}

func (s *streamEditor) prefixText(text string) string {
	text = strings.TrimSpace(text)
	prefix := strings.TrimSpace(s.displayPrefix)
	if prefix == "" || text == "" {
		return text
	}
	if strings.HasPrefix(strings.ToLower(text), strings.ToLower(prefix)) {
		return text
	}
	return prefix + "\n\n" + text
}

func (s *streamEditor) activeControlRows(done bool) [][]telegram.InlineButton {
	if s == nil || done || len(s.controlRows) == 0 {
		return nil
	}
	return s.controlRows
}

func (s *streamEditor) sendInitial(ctx context.Context, text string) (int64, error) {
	if s == nil {
		return 0, nil
	}
	return s.sender.SendMessage(ctx, core.OutboundMessage{
		ChatID:  s.chatID,
		Text:    text,
		ReplyTo: s.replyTo,
	})
}

func (s *streamEditor) editExisting(ctx context.Context, text string, rows [][]telegram.InlineButton, done bool) error {
	if s == nil {
		return nil
	}
	if len(rows) > 0 && s.keyboardEditor != nil {
		return s.keyboardEditor.EditMessageTextWithInlineKeyboard(ctx, s.chatID, s.messageID, text, "", rows)
	}
	if done && s.keyboardClearer != nil {
		return s.keyboardClearer.EditMessageTextWithoutInlineKeyboard(ctx, s.chatID, s.messageID, text, "")
	}
	return s.editor.EditMessageText(ctx, s.chatID, s.messageID, text, "")
}

func streamStopControlRows(streamID string) [][]telegram.InlineButton {
	data := core.EncodeStreamControlCallbackData(streamID, core.StreamControlActionStop)
	if data == "" {
		return nil
	}
	return [][]telegram.InlineButton{{
		{Text: "Stop", CallbackData: data},
	}}
}
