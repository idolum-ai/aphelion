//go:build linux

package telegramcommands

import (
	"context"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"strconv"
	"strings"
)

func (s *stubCommandRouter) QueueReinstall(ctx context.Context, msg core.InboundMessage) error {
	copied := msg
	s.queuedReinstallMsg = &copied
	_ = ctx
	return nil
}

func (s *stubCommandRouter) QueueDoctor(ctx context.Context, msg core.InboundMessage) error {
	copied := msg
	s.queuedDoctorMsg = &copied
	_ = ctx
	return s.queueDoctorErr
}

func (s *stubCommandRouter) LatestDoctorReport(ctx context.Context, chatID int64, senderID int64) (session.DoctorReportRecord, bool, error) {
	_ = ctx
	s.latestDoctorReportChatID = chatID
	s.latestDoctorReportSenderID = senderID
	if s.latestDoctorReportErr != nil {
		return session.DoctorReportRecord{}, false, s.latestDoctorReportErr
	}
	return s.latestDoctorReport, s.latestDoctorReportOK, nil
}

func (s *stubCommandRouter) Restart(chatID int64) error {
	s.restartInput = chatID
	s.restartCalls++
	return nil
}

func (s stubCommandRouter) CanRestart(senderID int64) bool {
	_ = senderID
	return s.canRestart
}

func (s *stubCommandRouter) ModelSlotStatuses() ([]core.ModelSlotStatus, error) {
	if s.modelStatusesErr != nil {
		return nil, s.modelStatusesErr
	}
	return append([]core.ModelSlotStatus(nil), s.modelStatuses...), nil
}

func (s *stubCommandRouter) CreateTelegramThread(_ context.Context, msg core.InboundMessage) (session.TelegramThread, error) {
	copied := msg
	s.threadCreateMsg = &copied
	if s.threadCreateErr != nil {
		return session.TelegramThread{}, s.threadCreateErr
	}
	if s.threadCreateReturn.ThreadID != 0 {
		return s.threadCreateReturn, nil
	}
	return session.TelegramThread{ChatID: msg.ChatID, ThreadID: 1, Status: session.TelegramThreadStatusOpen}, nil
}

func (s *stubCommandRouter) RecordTelegramThreadGuideMessage(chatID int64, threadID int64, messageID int64) error {
	s.threadGuideChatID = chatID
	s.threadGuideID = threadID
	s.threadGuideMessageID = messageID
	return nil
}

func (s *stubCommandRouter) RecordTelegramThreadCallbackMessage(chatID int64, threadID int64, messageID int64, surface string) error {
	s.threadCallbackChatID = chatID
	s.threadCallbackID = threadID
	s.threadCallbackMessageID = messageID
	s.threadCallbackSurface = surface
	return s.threadCallbackErr
}

func (s *stubCommandRouter) StartTelegramThreadTarget(_ context.Context, msg core.InboundMessage, text string) (core.InboundMessage, session.TelegramThread, error) {
	copied := msg
	s.threadStartMsg = &copied
	s.threadStartText = text
	if s.threadStartErr != nil {
		return core.InboundMessage{}, session.TelegramThread{}, s.threadStartErr
	}
	thread := session.TelegramThread{ChatID: msg.ChatID, ThreadID: 1, Status: session.TelegramThreadStatusOpen}
	if s.threadStartReturn.ThreadID != 0 {
		thread = s.threadStartReturn
	}
	routed := msg
	routed.TelegramThreadID = thread.ThreadID
	routed.Text = text
	return routed, thread, nil
}

func (s *stubCommandRouter) TargetTelegramThreadMessage(_ context.Context, msg core.InboundMessage, threadID int64, text string) (core.InboundMessage, session.TelegramThread, error) {
	copied := msg
	s.threadRouteMsg = &copied
	s.threadRouteID = threadID
	s.threadRouteText = text
	if s.threadRouteErr != nil {
		return core.InboundMessage{}, session.TelegramThread{}, s.threadRouteErr
	}
	thread := session.TelegramThread{ChatID: msg.ChatID, ThreadID: threadID, Status: session.TelegramThreadStatusOpen}
	if s.threadRouteReturn.ThreadID != 0 {
		thread = s.threadRouteReturn
	}
	routed := msg
	routed.TelegramThreadID = threadID
	routed.Text = text
	return routed, thread, nil
}

func (s *stubCommandRouter) TelegramThread(chatID int64, threadID int64) (session.TelegramThread, bool, error) {
	s.threadReplyChatID = chatID
	s.threadReplyMessageID = threadID
	if s.threadReplyErr != nil {
		return session.TelegramThread{}, false, s.threadReplyErr
	}
	if s.threadReplyOK && s.threadReplyReturn.ThreadID == threadID {
		return s.threadReplyReturn, true, nil
	}
	return session.TelegramThread{ChatID: chatID, ThreadID: threadID, Status: session.TelegramThreadStatusOpen}, true, nil
}

func (s *stubCommandRouter) TelegramThreadForReplyMessage(chatID int64, replyMessageID int64) (session.TelegramThread, bool, error) {
	s.threadReplyChatID = chatID
	s.threadReplyMessageID = replyMessageID
	if s.threadReplyErr != nil {
		return session.TelegramThread{}, false, s.threadReplyErr
	}
	if s.threadReplyOK {
		return s.threadReplyReturn, true, nil
	}
	return session.TelegramThread{}, false, nil
}

func (s *stubCommandRouter) TelegramThreads(chatID int64) ([]session.TelegramThread, error) {
	s.threadsChatID = chatID
	if s.threadsErr != nil {
		return nil, s.threadsErr
	}
	return append([]session.TelegramThread(nil), s.threadsReturn...), nil
}

func (s *stubCommandRouter) QueueTelegramThreadSummary(_ context.Context, msg core.InboundMessage) (string, error) {
	copied := msg
	s.threadSummaryMsg = &copied
	if s.threadSummaryErr != nil {
		return "", s.threadSummaryErr
	}
	if strings.TrimSpace(s.threadSummaryReturn) != "" {
		return s.threadSummaryReturn, nil
	}
	return "Summary queued.", nil
}

func (s *stubCommandRouter) AbsorbTelegramThread(_ context.Context, chatID int64, senderID int64, threadID int64) (string, error) {
	if s.order != nil {
		*s.order = append(*s.order, "absorb")
	}
	s.absorbThreadChatID = chatID
	s.absorbThreadSenderID = senderID
	s.absorbThreadID = threadID
	if s.absorbThreadErr != nil {
		return "", s.absorbThreadErr
	}
	if strings.TrimSpace(s.absorbThreadReturn) != "" {
		return s.absorbThreadReturn, nil
	}
	return "Absorbed thread " + strconv.FormatInt(threadID, 10) + ".", nil
}
