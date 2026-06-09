//go:build linux

package main

import (
	"context"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"strconv"
	"strings"
	"time"
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

func (s *stubCommandRouter) ReentryRecommendation(ctx context.Context, senderID int64, recommendationID string) (session.ReentryRecommendation, bool, error) {
	_ = ctx
	_ = senderID
	_ = recommendationID
	return session.ReentryRecommendation{}, false, nil
}

func (s *stubCommandRouter) IgnoreReentryRecommendation(ctx context.Context, senderID int64, recommendationID string) (session.ReentryRecommendation, error) {
	_ = ctx
	_ = senderID
	_ = recommendationID
	return session.ReentryRecommendation{}, nil
}

func (s *stubCommandRouter) QueueReentryRecommendation(ctx context.Context, msg core.InboundMessage, recommendationID string, candidateID string) (session.ReentryRecommendation, session.ReentryRecommendationCandidate, bool, error) {
	_ = ctx
	_ = msg
	_ = recommendationID
	_ = candidateID
	return session.ReentryRecommendation{}, session.ReentryRecommendationCandidate{}, false, nil
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

func (s *stubCommandRouter) RecordTelegramThreadReminderMessage(chatID int64, threadID int64, messageID int64, summary string, summaryKind string, _ time.Time, createdBySenderID int64) error {
	s.threadReminderChatID = chatID
	s.threadReminderID = threadID
	s.threadReminderMessageID = messageID
	s.threadReminderSummary = summary
	s.threadReminderSummaryKind = summaryKind
	s.threadReminderSenderID = createdBySenderID
	return nil
}

func (s *stubCommandRouter) IgnoreTelegramThreadReminder(_ context.Context, chatID int64, senderID int64, threadID int64, messageID int64) (string, error) {
	s.ignoreReminderChatID = chatID
	s.ignoreReminderSenderID = senderID
	s.ignoreReminderThreadID = threadID
	s.ignoreReminderMessageID = messageID
	if s.ignoreReminderErr != nil {
		return "", s.ignoreReminderErr
	}
	if strings.TrimSpace(s.ignoreReminderReturn) != "" {
		return s.ignoreReminderReturn, nil
	}
	return "Ignored reminder for thread.", nil
}

func (s *stubCommandRouter) AbsorbTelegramThreadReminder(_ context.Context, chatID int64, senderID int64, threadID int64, messageID int64) (string, error) {
	s.absorbReminderChatID = chatID
	s.absorbReminderSenderID = senderID
	s.absorbReminderThreadID = threadID
	s.absorbReminderMessageID = messageID
	if s.absorbReminderErr != nil {
		return "", s.absorbReminderErr
	}
	if strings.TrimSpace(s.absorbReminderReturn) != "" {
		return s.absorbReminderReturn, nil
	}
	return "Absorbed thread from reminder.", nil
}

func (s *stubCommandRouter) RecordTelegramThreadCallbackMessage(chatID int64, threadID int64, messageID int64, surface string) error {
	s.threadCallbackChatID = chatID
	s.threadCallbackID = threadID
	s.threadCallbackMessageID = messageID
	s.threadCallbackSurface = surface
	return s.threadCallbackErr
}

func (s *stubCommandRouter) ClearTelegramThreadCallbackMessage(chatID int64, messageID int64, surface string) error {
	s.threadCallbackClearChatID = chatID
	s.threadCallbackClearMessageID = messageID
	s.threadCallbackClearSurface = surface
	return s.threadCallbackClearErr
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

func (s *stubCommandRouter) MarkTelegramThreadReminderResumed(chatID int64, replyMessageID int64) error {
	s.threadReminderChatID = chatID
	s.threadReminderMessageID = replyMessageID
	return nil
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

func (s *stubCommandRouter) TelegramThreadReminders(chatID int64, status session.TelegramThreadReminderStatus, limit int) ([]session.TelegramThreadReminder, error) {
	s.threadRemindersChatID = chatID
	s.threadRemindersStatus = status
	s.threadRemindersLimit = limit
	if s.threadRemindersErr != nil {
		return nil, s.threadRemindersErr
	}
	return append([]session.TelegramThreadReminder(nil), s.threadRemindersReturn...), nil
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

func (s *stubCommandRouter) PromoteTelegramThread(_ context.Context, chatID int64, senderID int64, threadID int64) (session.TelegramThreadPromotionResult, error) {
	if s.order != nil {
		*s.order = append(*s.order, "promote")
	}
	s.promoteThreadChatID = chatID
	s.promoteThreadSenderID = senderID
	s.promoteThreadID = threadID
	if s.promoteThreadErr != nil {
		return session.TelegramThreadPromotionResult{}, s.promoteThreadErr
	}
	if strings.TrimSpace(s.promoteThreadReturn.Text) != "" || strings.TrimSpace(s.promoteThreadReturn.HandoffID) != "" {
		return s.promoteThreadReturn, nil
	}
	return session.TelegramThreadPromotionResult{Text: "Promotion draft created for thread.", ThreadID: threadID, Status: session.TelegramThreadPromotionStatusDraft}, nil
}

func (s *stubCommandRouter) PrepareTelegramThreadPromotion(_ context.Context, chatID int64, senderID int64, handoffID string) (session.TelegramThreadPromotionResult, error) {
	if s.order != nil {
		*s.order = append(*s.order, "promotion_ready")
	}
	s.preparePromotionChatID = chatID
	s.preparePromotionSenderID = senderID
	s.preparePromotionHandoffID = handoffID
	if s.preparePromotionErr != nil {
		return session.TelegramThreadPromotionResult{}, s.preparePromotionErr
	}
	if strings.TrimSpace(s.preparePromotionReturn.Text) != "" || strings.TrimSpace(s.preparePromotionReturn.HandoffID) != "" {
		return s.preparePromotionReturn, nil
	}
	return session.TelegramThreadPromotionResult{Text: "Promotion handoff ready.", HandoffID: handoffID, Status: session.TelegramThreadPromotionStatusReady}, nil
}

func (s *stubCommandRouter) CancelTelegramThreadPromotion(_ context.Context, chatID int64, senderID int64, handoffID string) (session.TelegramThreadPromotionResult, error) {
	if s.order != nil {
		*s.order = append(*s.order, "promotion_cancel")
	}
	s.cancelPromotionChatID = chatID
	s.cancelPromotionSenderID = senderID
	s.cancelPromotionHandoffID = handoffID
	if s.cancelPromotionErr != nil {
		return session.TelegramThreadPromotionResult{}, s.cancelPromotionErr
	}
	if strings.TrimSpace(s.cancelPromotionReturn.Text) != "" || strings.TrimSpace(s.cancelPromotionReturn.HandoffID) != "" {
		return s.cancelPromotionReturn, nil
	}
	return session.TelegramThreadPromotionResult{Text: "Promotion cancelled.", HandoffID: handoffID, Status: session.TelegramThreadPromotionStatusCancelled}, nil
}

func (s *stubCommandRouter) SupersedeTelegramThreadPromotion(_ context.Context, chatID int64, senderID int64, handoffID string) (session.TelegramThreadPromotionResult, error) {
	if s.order != nil {
		*s.order = append(*s.order, "promotion_refresh")
	}
	s.supersedePromotionChatID = chatID
	s.supersedePromotionSenderID = senderID
	s.supersedePromotionHandoffID = handoffID
	if s.supersedePromotionErr != nil {
		return session.TelegramThreadPromotionResult{}, s.supersedePromotionErr
	}
	if strings.TrimSpace(s.supersedePromotionReturn.Text) != "" || strings.TrimSpace(s.supersedePromotionReturn.HandoffID) != "" {
		return s.supersedePromotionReturn, nil
	}
	return session.TelegramThreadPromotionResult{Text: "Previous promotion handoff superseded.", HandoffID: "thread-promotion:1001:3:9", ThreadID: 3, Status: session.TelegramThreadPromotionStatusDraft}, nil
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

func (s *stubCommandRouter) RecordTelegramMediaThreadPicker(chatID int64, pickerMessageID int64, inbound core.InboundMessage) error {
	s.mediaPickerRecordChatID = chatID
	s.mediaPickerRecordMessageID = pickerMessageID
	s.mediaPickerRecordInbound = inbound
	return s.mediaPickerRecordErr
}

func (s *stubCommandRouter) TelegramMediaThreadPicker(chatID int64, pickerMessageID int64) (core.InboundMessage, bool, error) {
	s.mediaPickerGetChatID = chatID
	s.mediaPickerGetMessageID = pickerMessageID
	if s.mediaPickerErr != nil {
		return core.InboundMessage{}, false, s.mediaPickerErr
	}
	return s.mediaPickerReturn, s.mediaPickerOK, nil
}

func (s *stubCommandRouter) MarkTelegramMediaThreadPickerRouted(chatID int64, pickerMessageID int64) error {
	s.mediaPickerMarkChatID = chatID
	s.mediaPickerMarkMessageID = pickerMessageID
	return s.mediaPickerMarkErr
}

func (s *stubCommandRouter) RouteAccepted(_ context.Context, msg core.InboundMessage) error {
	copied := msg
	s.routeAcceptedMsg = &copied
	return s.routeAcceptedErr
}
