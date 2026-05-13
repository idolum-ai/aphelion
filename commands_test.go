//go:build linux

package main

import (
	"context"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	"strings"
	"testing"
	"time"
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
	s.answers = append(s.answers, stubAnswerCall{
		id:   id,
		text: text,
	})
	return s.answerErr
}

type stubCommandRouter struct {
	status                       core.SessionStatus
	statusChat                   core.ChatStatusSnapshot
	statusSystem                 core.SystemStatusSnapshot
	autonomyStatus               core.AutonomyStatusSnapshot
	statusDurables               core.DurableAgentsStatusSnapshot
	statusReadableSummary        string
	tailnetStatus                core.TailnetStatusSnapshot
	tailnetStatusErr             error
	tailnetStatusSenderID        int64
	tailnetSurfaces              []core.TailnetSurfaceStatus
	tailnetSurfacesErr           error
	tailnetSurfacesSenderID      int64
	tailnetGrantBindings         []core.TailnetGrantBindingStatus
	tailnetGrantBindingsErr      error
	tailnetGrantBindingsSenderID int64
	latestDoctorReport           session.DoctorReportRecord
	latestDoctorReportOK         bool
	latestDoctorReportErr        error
	latestDoctorReportChatID     int64
	latestDoctorReportSenderID   int64
	revokeTailnetSurfaceSenderID int64
	revokeTailnetSurfaceID       string
	revokeTailnetSurfaceReason   string
	revokeTailnetSurfaceReturn   core.TailnetSurfaceStatus
	revokeTailnetSurfaceOK       bool
	revokeTailnetSurfaceErr      error
	statusChatErr                error
	statusSystemErr              error
	autonomyStatusErr            error
	statusDurablesErr            error
	stop                         core.StopResult
	stopInput                    int64
	stopCalls                    int
	streamControls               map[string]int64
	streamStopID                 string
	streamStopChatID             int64
	streamStopCalls              int
	newResult                    core.NewSessionResult
	newErr                       error
	newChatID                    int64
	newSenderID                  int64
	detach                       core.DetachResult
	detachErr                    error
	detachChatID                 int64
	detachSenderID               int64
	personaEffort                string
	governorEffort               string
	canRestart                   bool
	modelStatuses                []core.ModelSlotStatus
	modelStatusesErr             error
	validateModelSlotInput       core.ModelSlotConfig
	validateModelSlotReturn      core.ModelValidation
	setModelSlotInput            core.ModelSlotConfig
	setModelSlotActor            string
	setModelSlotReason           string
	setModelSlotTTL              time.Duration
	setModelSlotReturn           core.ModelSlotStatus
	setModelSlotErr              error
	rollbackModelSlotInput       string
	rollbackModelSlotActor       string
	rollbackModelSlotReason      string
	rollbackModelSlotReturn      core.ModelSlotStatus
	rollbackModelSlotErr         error
	clearModelSlotInput          string
	clearModelSlotActor          string
	clearModelSlotReason         string
	clearModelSlotReturn         core.ModelSlotStatus
	clearModelSlotErr            error
	modelSlotHistoryInput        string
	modelSlotHistoryLimit        int
	modelSlotHistoryReturn       []session.ModelSlotOverrideRecord
	modelSlotHistoryErr          error
	continuationState            session.ContinuationState
	continuationStateInput       int64
	continuationStateErr         error
	approveContinuationInput     int64
	approveContinuationApprover  int64
	approveContinuationReturn    session.ContinuationState
	approveContinuationErr       error
	stopContinuationInput        int64
	stopContinuationResult       core.StopResult
	stopContinuationErr          error
	triggerContinuationInput     int64
	triggerContinuationErr       error
	triggerContinuationStarted   chan struct{}
	triggerContinuationRelease   <-chan struct{}
	callbackErrorRecords         []stubCallbackErrorRecord
	refreshContinuationInput     int64
	refreshContinuationReason    string
	refreshContinuationReturn    session.ContinuationState
	refreshContinuationSent      bool
	refreshContinuationErr       error
	restartInput                 int64
	restartCalls                 int
	queuedReinstallMsg           *core.InboundMessage
	queuedDoctorMsg              *core.InboundMessage
	queueDoctorErr               error
	autoApproveChatID            int64
	autoApproveSenderID          int64
	autoApproveArgs              string
	autoApproveStatusChatID      int64
	autoApproveStatusSenderID    int64
	autoApproveStatusReturn      string
	autoApproveStatusErr         error
	autoApproveReturn            string
	autoApproveErr               error
	autonomyChatID               int64
	autonomySenderID             int64
	autonomyArgs                 string
	autonomyReturn               string
	autonomyErr                  error
	durableWizardChatID          int64
	durableWizardSenderID        int64
	durableWizardAction          string
	durableWizardAgentID         string
	durableWizardAnswers         map[string]any
	durableWizardResult          string
	durableWizardErr             error
	durableAgentsList            []core.DurableAgentStatusSnapshot
	durableAgentsListErr         error
	durableAgentsListSenderID    int64
	startDurableChatID           int64
	startDurableSenderID         int64
	startDurableAgentID          string
	startDurableResult           string
	startDurableErr              error
	missionCommandText           string
	missionCommandErr            error
	missionCommandChatID         int64
	missionCommandSenderID       int64
	missionCommandArgs           string
	missionHomeMissions          []session.MissionState
	missionHomeWorking           session.WorkingObjective
	missionHomeIsAdmin           bool
	missionHomeErr               error
	missionHomeChatID            int64
	missionHomeSenderID          int64
	missionDetailsMission        session.MissionState
	missionDetailsEvents         []session.MissionEvent
	missionDetailsErr            error
	missionDetailsChatID         int64
	missionDetailsSenderID       int64
	missionDetailsID             string
	setMissionPinnedMission      session.MissionState
	setMissionPinnedErr          error
	setMissionPinnedChatID       int64
	setMissionPinnedSenderID     int64
	setMissionPinnedID           string
	setMissionPinnedValue        bool
	updateMissionStatusMission   session.MissionState
	updateMissionStatusErr       error
	updateMissionStatusChatID    int64
	updateMissionStatusSenderID  int64
	updateMissionStatusID        string
	updateMissionStatusValue     session.MissionStatus
	missionLedgerHealth          session.MissionLedgerHealth
	missionLedgerHealthErr       error
	missionLedgerHealthSenderID  int64
	missionActionProposal        session.ActionProposal
	missionActionProposalErr     error
	missionActionProposalChatID  int64
	missionActionProposalSender  int64
	missionActionProposalID      string
	applyMissionProposalMission  session.MissionState
	applyMissionProposalChanged  bool
	applyMissionProposalErr      error
	applyMissionProposalChatID   int64
	applyMissionProposalSender   int64
	applyMissionProposalID       string
	applyMissionProposalChoice   string
	memoryReviewBySource         map[memoryReviewSource]memoryReviewSnapshot
	memoryReviewErr              error
	memoryReviewChatID           int64
	memoryReviewSenderID         int64
	memoryReviewSource           memoryReviewSource
	memoryFocusByChat            map[int64]core.MemoryFocus
	clearMemoryFocusChatID       int64
	clearMemoryFocusResult       bool
}

func (s *stubCommandRouter) Stop(chatID int64) core.StopResult {
	s.stopInput = chatID
	s.stopCalls++
	return s.stop
}

func (s *stubCommandRouter) MarkStreamControlStopping(streamID string, chatID int64) bool {
	s.streamStopID = streamID
	s.streamStopChatID = chatID
	s.streamStopCalls++
	if s.streamControls == nil {
		return false
	}
	return s.streamControls[streamID] == chatID
}

func (s *stubCommandRouter) New(chatID int64, senderID int64) (core.NewSessionResult, error) {
	s.newChatID = chatID
	s.newSenderID = senderID
	if s.newErr != nil {
		return core.NewSessionResult{}, s.newErr
	}
	return s.newResult, nil
}

func (s *stubCommandRouter) Detach(chatID int64, senderID int64) (core.DetachResult, error) {
	s.detachChatID = chatID
	s.detachSenderID = senderID
	if s.detachErr != nil {
		return core.DetachResult{}, s.detachErr
	}
	return s.detach, nil
}

func (s stubCommandRouter) Status(chatID int64) core.SessionStatus {
	return s.status
}

func (s stubCommandRouter) StatusChat(chatID int64) (core.ChatStatusSnapshot, error) {
	if s.statusChatErr != nil {
		return core.ChatStatusSnapshot{}, s.statusChatErr
	}
	snapshot := s.statusChat
	if snapshot.ChatID == 0 {
		snapshot.ChatID = chatID
	}
	return snapshot, nil
}

func (s stubCommandRouter) StatusSystem(senderID int64) (core.SystemStatusSnapshot, error) {
	_ = senderID
	if s.statusSystemErr != nil {
		return core.SystemStatusSnapshot{}, s.statusSystemErr
	}
	return s.statusSystem, nil
}

func (s *stubCommandRouter) AutonomyStatus(chatID int64, senderID int64) (core.AutonomyStatusSnapshot, error) {
	s.autonomyChatID = chatID
	s.autonomySenderID = senderID
	_ = senderID
	if s.autonomyStatusErr != nil {
		return core.AutonomyStatusSnapshot{}, s.autonomyStatusErr
	}
	if strings.TrimSpace(s.autonomyStatus.DefaultMode) != "" || strings.TrimSpace(s.autonomyStatus.Ceiling) != "" {
		return s.autonomyStatus, nil
	}
	return core.AutonomyStatusSnapshot{
		DefaultMode:         "ask_first",
		Ceiling:             "leased",
		AllowLiveOverrides:  true,
		MaxOverrideDuration: 4 * time.Hour,
		Source:              "test",
		AuthorityBehavior:   "existing proposal and approval flows",
	}, nil
}

func (s stubCommandRouter) StatusDurables(senderID int64) (core.DurableAgentsStatusSnapshot, error) {
	_ = senderID
	if s.statusDurablesErr != nil {
		return core.DurableAgentsStatusSnapshot{}, s.statusDurablesErr
	}
	return s.statusDurables, nil
}

func (s stubCommandRouter) StatusReadableSummary(ctx context.Context, view string, statusText string) string {
	_ = ctx
	_ = view
	_ = statusText
	return s.statusReadableSummary
}

func (s *stubCommandRouter) TailnetStatus(ctx context.Context, senderID int64) (core.TailnetStatusSnapshot, error) {
	_ = ctx
	s.tailnetStatusSenderID = senderID
	if s.tailnetStatusErr != nil {
		return core.TailnetStatusSnapshot{}, s.tailnetStatusErr
	}
	if strings.TrimSpace(s.tailnetStatus.Status) != "" || s.tailnetStatus.GeneratedAt.IsZero() == false {
		return s.tailnetStatus, nil
	}
	return core.TailnetStatusSnapshot{
		Enabled: false,
		Backend: "disabled",
		Status:  "disabled",
		Summary: "Tailscale integration is disabled.",
	}, nil
}

func (s *stubCommandRouter) TailnetSurfaces(senderID int64) ([]core.TailnetSurfaceStatus, error) {
	s.tailnetSurfacesSenderID = senderID
	if s.tailnetSurfacesErr != nil {
		return nil, s.tailnetSurfacesErr
	}
	return append([]core.TailnetSurfaceStatus(nil), s.tailnetSurfaces...), nil
}

func (s *stubCommandRouter) TailnetGrantBindings(senderID int64) ([]core.TailnetGrantBindingStatus, error) {
	s.tailnetGrantBindingsSenderID = senderID
	if s.tailnetGrantBindingsErr != nil {
		return nil, s.tailnetGrantBindingsErr
	}
	return append([]core.TailnetGrantBindingStatus(nil), s.tailnetGrantBindings...), nil
}

func (s *stubCommandRouter) RevokeTailnetSurface(ctx context.Context, senderID int64, surfaceID string, reason string) (core.TailnetSurfaceStatus, bool, error) {
	_ = ctx
	s.revokeTailnetSurfaceSenderID = senderID
	s.revokeTailnetSurfaceID = surfaceID
	s.revokeTailnetSurfaceReason = reason
	if s.revokeTailnetSurfaceErr != nil {
		return core.TailnetSurfaceStatus{}, false, s.revokeTailnetSurfaceErr
	}
	if strings.TrimSpace(s.revokeTailnetSurfaceReturn.SurfaceID) != "" || s.revokeTailnetSurfaceOK {
		return s.revokeTailnetSurfaceReturn, s.revokeTailnetSurfaceOK, nil
	}
	return core.TailnetSurfaceStatus{SurfaceID: surfaceID, Status: "revoked"}, true, nil
}

func (s stubCommandRouter) CurrentEfforts() (string, string) {
	return s.personaEffort, s.governorEffort
}

func (s *stubCommandRouter) ContinuationState(chatID int64) (session.ContinuationState, error) {
	s.continuationStateInput = chatID
	if s.continuationStateErr != nil {
		return session.ContinuationState{}, s.continuationStateErr
	}
	return s.continuationState, nil
}

func (s *stubCommandRouter) ApproveContinuation(chatID int64, approverID int64) (session.ContinuationState, error) {
	s.approveContinuationInput = chatID
	s.approveContinuationApprover = approverID
	if s.approveContinuationErr != nil {
		if s.approveContinuationReturn.Status != "" {
			return s.approveContinuationReturn, s.approveContinuationErr
		}
		return s.continuationState, s.approveContinuationErr
	}
	if s.continuationState.Status == "" {
		s.continuationState = session.ContinuationState{
			Status:         session.ContinuationStatusApproved,
			DecisionID:     "decision",
			RemainingTurns: 1,
			StageSummary:   "Resume the next bounded step.",
			ApprovedBy:     approverID,
		}
	} else {
		s.continuationState.ApprovedBy = approverID
		s.continuationState.Status = session.ContinuationStatusApproved
	}
	return s.continuationState, nil
}

func (s *stubCommandRouter) StopContinuation(chatID int64) (core.StopResult, error) {
	s.stopContinuationInput = chatID
	if s.stopContinuationErr != nil {
		return core.StopResult{}, s.stopContinuationErr
	}
	return s.stopContinuationResult, nil
}

func (s *stubCommandRouter) TriggerContinuation(ctx context.Context, chatID int64) error {
	s.triggerContinuationInput = chatID
	_ = ctx
	if s.triggerContinuationStarted != nil {
		close(s.triggerContinuationStarted)
		s.triggerContinuationStarted = nil
	}
	if s.triggerContinuationRelease != nil {
		<-s.triggerContinuationRelease
	}
	return s.triggerContinuationErr
}

func waitForStubContinuationTrigger(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("continuation trigger did not start")
	}
}

func (s *stubCommandRouter) RecordTelegramCallbackError(chatID int64, callbackKind string, err error) {
	s.callbackErrorRecords = append(s.callbackErrorRecords, stubCallbackErrorRecord{
		chatID:       chatID,
		callbackKind: callbackKind,
		err:          err,
	})
}

func (s *stubCommandRouter) ConfigureAutoApproval(_ context.Context, chatID int64, senderID int64, args string) (string, error) {
	s.autoApproveChatID = chatID
	s.autoApproveSenderID = senderID
	s.autoApproveArgs = args
	if s.autoApproveErr != nil {
		return "", s.autoApproveErr
	}
	if strings.TrimSpace(s.autoApproveReturn) != "" {
		return s.autoApproveReturn, nil
	}
	return "Auto approvals enabled for this chat.", nil
}

func (s *stubCommandRouter) AutoApprovalStatus(_ context.Context, chatID int64, senderID int64) (string, error) {
	s.autoApproveStatusChatID = chatID
	s.autoApproveStatusSenderID = senderID
	if s.autoApproveStatusErr != nil {
		return "", s.autoApproveStatusErr
	}
	if strings.TrimSpace(s.autoApproveStatusReturn) != "" {
		return s.autoApproveStatusReturn, nil
	}
	return "Auto approvals are inactive for this chat.", nil
}

func (s *stubCommandRouter) ConfigureAutonomy(_ context.Context, chatID int64, senderID int64, args string) (string, error) {
	s.autonomyChatID = chatID
	s.autonomySenderID = senderID
	s.autonomyArgs = args
	if s.autonomyErr != nil {
		return "", s.autonomyErr
	}
	if strings.TrimSpace(s.autonomyReturn) != "" {
		return s.autonomyReturn, nil
	}
	return "Autonomy override enabled for this chat.", nil
}

func (s *stubCommandRouter) RefreshContinuationProposal(ctx context.Context, chatID int64, reason string) (session.ContinuationState, bool, error) {
	s.refreshContinuationInput = chatID
	s.refreshContinuationReason = reason
	_ = ctx
	if s.refreshContinuationErr != nil {
		return session.ContinuationState{}, false, s.refreshContinuationErr
	}
	if s.refreshContinuationReturn.Status != "" {
		return s.refreshContinuationReturn, s.refreshContinuationSent, nil
	}
	return session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-refreshed",
		RemainingTurns: 1,
		StageSummary:   "Use the fresh approval prompt.",
	}, true, nil
}

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
