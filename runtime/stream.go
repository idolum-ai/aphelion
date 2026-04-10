//go:build linux

package runtime

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type streamEditor struct {
	sender    OutboundSender
	editor    messageEditor
	deleter   messageDeleter
	chatID    int64
	replyTo   *int64
	interval  time.Duration
	cursor    string
	messageID int64
	buffer    strings.Builder
	lastEdit  time.Time
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
		sender:   r.outbound,
		editor:   editor,
		chatID:   msg.ChatID,
		replyTo:  replyToMessageID(msg.MessageID),
		interval: r.streamEditInterval,
		cursor:   r.streamCursor,
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

func (s *streamEditor) Abort(ctx context.Context) {
	if s == nil || s.messageID == 0 || s.deleter == nil {
		return
	}
	if err := s.deleter.DeleteMessage(ctx, s.chatID, s.messageID); err != nil {
		log.Printf("WARN delete streamed message chat_id=%d msg_id=%d err=%v", s.chatID, s.messageID, err)
	}
}

func (s *streamEditor) Started() bool {
	if s == nil {
		return false
	}
	return s.messageID != 0 || s.buffer.Len() > 0
}

func (s *streamEditor) flush(ctx context.Context, done bool) error {
	text := s.buffer.String()
	if !done {
		text += s.cursor
	}
	if s.messageID == 0 {
		msgID, err := s.sender.SendMessage(ctx, core.OutboundMessage{
			ChatID:  s.chatID,
			Text:    text,
			ReplyTo: s.replyTo,
		})
		if err != nil {
			return err
		}
		s.messageID = msgID
		s.lastEdit = time.Now()
		return nil
	}

	if err := s.editor.EditMessageText(ctx, s.chatID, s.messageID, text, ""); err != nil {
		msgID, sendErr := s.sender.SendMessage(ctx, core.OutboundMessage{
			ChatID:  s.chatID,
			Text:    text,
			ReplyTo: s.replyTo,
		})
		if sendErr != nil {
			return err
		}
		if s.deleter != nil {
			if delErr := s.deleter.DeleteMessage(ctx, s.chatID, s.messageID); delErr != nil {
				log.Printf("WARN delete superseded streamed message chat_id=%d msg_id=%d err=%v", s.chatID, s.messageID, delErr)
			}
		}
		s.messageID = msgID
		s.lastEdit = time.Now()
		return nil
	}

	s.lastEdit = time.Now()
	return nil
}
