//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
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
	personaModel                 string
	personaModelOptions          []string
	governorEffortOptions        []string
	setPersonaModelInput         string
	setGovernorEffortInput       string
	setPersonaModelReturn        string
	setGovernorEffortReturn      string
	setPersonaModelErr           error
	setGovernorEffortErr         error
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
	autoApproveReturn            string
	autoApproveErr               error
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

func (s stubCommandRouter) AutonomyStatus(senderID int64) (core.AutonomyStatusSnapshot, error) {
	_ = senderID
	if s.autonomyStatusErr != nil {
		return core.AutonomyStatusSnapshot{}, s.autonomyStatusErr
	}
	if strings.TrimSpace(s.autonomyStatus.DefaultMode) != "" || strings.TrimSpace(s.autonomyStatus.Ceiling) != "" {
		return s.autonomyStatus, nil
	}
	return core.AutonomyStatusSnapshot{
		DefaultMode:         "ask_first",
		Ceiling:             "ask_first",
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
	return "Auto-approval enabled for this chat.", nil
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

func (s *stubCommandRouter) Restart(chatID int64) error {
	s.restartInput = chatID
	s.restartCalls++
	return nil
}

func (s stubCommandRouter) CanRestart(senderID int64) bool {
	_ = senderID
	return s.canRestart
}

func (s stubCommandRouter) CurrentPersonaModel() string {
	return s.personaModel
}

func (s stubCommandRouter) PersonaModelOptions() []string {
	if len(s.personaModelOptions) == 0 {
		return []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-opus-4-7", "gpt-5.5"}
	}
	return append([]string(nil), s.personaModelOptions...)
}

func (s *stubCommandRouter) SetPersonaModel(model string) (string, error) {
	s.setPersonaModelInput = model
	if s.setPersonaModelErr != nil {
		return "", s.setPersonaModelErr
	}
	if s.setPersonaModelReturn != "" {
		return s.setPersonaModelReturn, nil
	}
	return model, nil
}

func (s stubCommandRouter) GovernorEffortOptions() []string {
	if len(s.governorEffortOptions) == 0 {
		return []string{"low", "medium", "high", "xhigh"}
	}
	return append([]string(nil), s.governorEffortOptions...)
}

func (s *stubCommandRouter) SetGovernorEffort(effort string) (string, error) {
	s.setGovernorEffortInput = effort
	if s.setGovernorEffortErr != nil {
		return "", s.setGovernorEffortErr
	}
	if s.setGovernorEffortReturn != "" {
		return s.setGovernorEffortReturn, nil
	}
	return effort, nil
}

func (s *stubCommandRouter) ModelSlotStatuses() ([]core.ModelSlotStatus, error) {
	if s.modelStatusesErr != nil {
		return nil, s.modelStatusesErr
	}
	return append([]core.ModelSlotStatus(nil), s.modelStatuses...), nil
}

func (s *stubCommandRouter) ValidateModelSlotConfig(cfg core.ModelSlotConfig) core.ModelValidation {
	s.validateModelSlotInput = cfg
	if s.validateModelSlotReturn.Config.Slot != "" || s.validateModelSlotReturn.Error != "" || s.validateModelSlotReturn.Valid {
		return s.validateModelSlotReturn
	}
	return core.ModelValidation{Valid: true, Config: core.NormalizeModelSlotConfig(cfg), ResolvedTransport: core.ModelTransportAnthropicMessages}
}

func (s *stubCommandRouter) SetModelSlotConfig(cfg core.ModelSlotConfig, actor string, reason string, ttl time.Duration) (core.ModelSlotStatus, error) {
	s.setModelSlotInput = cfg
	s.setModelSlotActor = actor
	s.setModelSlotReason = reason
	s.setModelSlotTTL = ttl
	if s.setModelSlotErr != nil {
		return core.ModelSlotStatus{}, s.setModelSlotErr
	}
	if s.setModelSlotReturn.Slot != "" {
		return s.setModelSlotReturn, nil
	}
	normalized := core.NormalizeModelSlotConfig(cfg)
	return core.ModelSlotStatus{
		Slot:      normalized.Slot,
		Effective: normalized,
		Source:    "override",
		Validation: core.ModelValidation{
			Valid:             true,
			Config:            normalized,
			ResolvedTransport: core.ResolveModelTransport(normalized, core.ModelSlotUsesTools(normalized.Slot)),
		},
	}, nil
}

func (s *stubCommandRouter) RollbackModelSlot(slot string, actor string, reason string) (core.ModelSlotStatus, error) {
	s.rollbackModelSlotInput = slot
	s.rollbackModelSlotActor = actor
	s.rollbackModelSlotReason = reason
	if s.rollbackModelSlotErr != nil {
		return core.ModelSlotStatus{}, s.rollbackModelSlotErr
	}
	return s.rollbackModelSlotReturn, nil
}

func (s *stubCommandRouter) ClearModelSlot(slot string, actor string, reason string) (core.ModelSlotStatus, error) {
	s.clearModelSlotInput = slot
	s.clearModelSlotActor = actor
	s.clearModelSlotReason = reason
	if s.clearModelSlotErr != nil {
		return core.ModelSlotStatus{}, s.clearModelSlotErr
	}
	return s.clearModelSlotReturn, nil
}

func (s *stubCommandRouter) ModelSlotHistory(slot string, limit int) ([]session.ModelSlotOverrideRecord, error) {
	s.modelSlotHistoryInput = slot
	s.modelSlotHistoryLimit = limit
	if s.modelSlotHistoryErr != nil {
		return nil, s.modelSlotHistoryErr
	}
	return append([]session.ModelSlotOverrideRecord(nil), s.modelSlotHistoryReturn...), nil
}

func (s *stubCommandRouter) RunDurableWizard(ctx context.Context, chatID int64, senderID int64, action string, agentID string, wizardAnswers map[string]any) (string, error) {
	_ = ctx
	s.durableWizardChatID = chatID
	s.durableWizardSenderID = senderID
	s.durableWizardAction = action
	s.durableWizardAgentID = agentID
	if wizardAnswers != nil {
		copied := make(map[string]any, len(wizardAnswers))
		for key, value := range wizardAnswers {
			copied[key] = value
		}
		s.durableWizardAnswers = copied
	} else {
		s.durableWizardAnswers = nil
	}
	if s.durableWizardErr != nil {
		return "", s.durableWizardErr
	}
	if strings.TrimSpace(s.durableWizardResult) != "" {
		return s.durableWizardResult, nil
	}
	return "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: in_progress\ncurrent_step: adapter\nmissing: adapter,autonomy\nnext_question: Which channel adapter should be named for this channel profile?\naddress: child-endpoint\nadapter: \nautonomy: \nwakeup_mode: poll\npoll_interval: 5m\nsynthesis_cadence: 4h\ncharter:\n", nil
}

func (s *stubCommandRouter) DurableAgentsList(senderID int64) ([]core.DurableAgentStatusSnapshot, error) {
	s.durableAgentsListSenderID = senderID
	if s.durableAgentsListErr != nil {
		return nil, s.durableAgentsListErr
	}
	return append([]core.DurableAgentStatusSnapshot(nil), s.durableAgentsList...), nil
}

func (s *stubCommandRouter) StartDurableAgentConversation(ctx context.Context, chatID int64, senderID int64, agentID string) (string, error) {
	_ = ctx
	s.startDurableChatID = chatID
	s.startDurableSenderID = senderID
	s.startDurableAgentID = agentID
	if s.startDurableErr != nil {
		return "", s.startDurableErr
	}
	if strings.TrimSpace(s.startDurableResult) != "" {
		return s.startDurableResult, nil
	}
	return "Started background conversation with durable agent " + strings.TrimSpace(agentID) + ".", nil
}

func (s *stubCommandRouter) MissionCommand(ctx context.Context, chatID int64, senderID int64, args string) (string, error) {
	_ = ctx
	s.missionCommandChatID = chatID
	s.missionCommandSenderID = senderID
	s.missionCommandArgs = args
	if s.missionCommandErr != nil {
		return "", s.missionCommandErr
	}
	if strings.TrimSpace(s.missionCommandText) != "" {
		return s.missionCommandText, nil
	}
	return "Mission Ledger\n- none", nil
}

func (s *stubCommandRouter) MissionActionProposal(ctx context.Context, chatID int64, senderID int64, missionID string) (session.ActionProposal, error) {
	_ = ctx
	s.missionActionProposalChatID = chatID
	s.missionActionProposalSender = senderID
	s.missionActionProposalID = missionID
	if s.missionActionProposalErr != nil {
		return session.ActionProposal{}, s.missionActionProposalErr
	}
	if strings.TrimSpace(s.missionActionProposal.ID) != "" {
		return s.missionActionProposal, nil
	}
	return session.ActionProposal{ID: "aprop-" + missionID, MissionID: missionID, Summary: "Activate mission", BoundedEffect: "Mark active only.", Status: session.ProposalStatusPending}, nil
}

func (s *stubCommandRouter) ApplyMissionActionProposalDecision(ctx context.Context, chatID int64, senderID int64, missionID string, choice string) (session.MissionState, bool, error) {
	_ = ctx
	s.applyMissionProposalChatID = chatID
	s.applyMissionProposalSender = senderID
	s.applyMissionProposalID = missionID
	s.applyMissionProposalChoice = choice
	if s.applyMissionProposalErr != nil {
		return session.MissionState{}, false, s.applyMissionProposalErr
	}
	if strings.TrimSpace(s.applyMissionProposalMission.ID) != "" {
		return s.applyMissionProposalMission, s.applyMissionProposalChanged, nil
	}
	return session.MissionState{ID: missionID, Title: "Mission", Status: session.MissionStatusActive}, true, nil
}

func (s *stubCommandRouter) MemoryReviewSnapshot(ctx context.Context, chatID int64, senderID int64, source memoryReviewSource) (memoryReviewSnapshot, error) {
	_ = ctx
	s.memoryReviewChatID = chatID
	s.memoryReviewSenderID = senderID
	s.memoryReviewSource = source
	if s.memoryReviewErr != nil {
		return memoryReviewSnapshot{}, s.memoryReviewErr
	}
	if s.memoryReviewBySource == nil {
		return memoryReviewSnapshot{
			Source: source,
			Query:  "default seed",
		}, nil
	}
	if snapshot, ok := s.memoryReviewBySource[source]; ok {
		return snapshot, nil
	}
	return memoryReviewSnapshot{
		Source: source,
		Query:  "default seed",
	}, nil
}

func (s *stubCommandRouter) MemoryFocus(chatID int64) (core.MemoryFocus, bool) {
	if s.memoryFocusByChat == nil {
		return core.MemoryFocus{}, false
	}
	focus, ok := s.memoryFocusByChat[chatID]
	return focus, ok
}

func (s *stubCommandRouter) SetMemoryFocus(chatID int64, focus core.MemoryFocus) {
	if s.memoryFocusByChat == nil {
		s.memoryFocusByChat = make(map[int64]core.MemoryFocus)
	}
	s.memoryFocusByChat[chatID] = focus
}

func (s *stubCommandRouter) ClearMemoryFocus(chatID int64) bool {
	s.clearMemoryFocusChatID = chatID
	if s.memoryFocusByChat != nil {
		if _, ok := s.memoryFocusByChat[chatID]; ok {
			delete(s.memoryFocusByChat, chatID)
			return true
		}
	}
	return s.clearMemoryFocusResult
}

func TestParseTelegramCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text string
		want string
		ok   bool
	}{
		{text: "/stop", want: "stop", ok: true},
		{text: "/new", want: "new", ok: true},
		{text: "/detach", want: "detach", ok: true},
		{text: "/help extra", want: "help", ok: true},
		{text: "/status@my_bot", want: "status", ok: true},
		{text: "/restart", want: "restart", ok: true},
		{text: "/reinstall", want: "reinstall", ok: true},
		{text: "/debug", want: "debug", ok: true},
		{text: "/doctor", want: "doctor", ok: true},
		{text: "/tailnet", want: "tailnet", ok: true},
		{text: "/agents", want: "agents", ok: true},
		{text: "/memory", want: "memory", ok: true},
		{text: "/mission", want: "mission", ok: true},
		{text: "/model status", want: "model", ok: true},
		{text: "/set_persona_model", want: "set_persona_model", ok: true},
		{text: "/set_governor_effort", want: "set_governor_effort", ok: true},
		{text: "/stop\n\nReply context:\nidolum: Please confirm.", want: "stop", ok: true},
		{text: "/tmp/file", ok: false},
		{text: " /start ", want: "start", ok: true},
		{text: "hello", ok: false},
	}

	for _, tt := range tests {
		got, ok := parseTelegramCommand(tt.text)
		if ok != tt.ok || got != tt.want {
			t.Fatalf("parseTelegramCommand(%q) = (%q, %v), want (%q, %v)", tt.text, got, ok, tt.want, tt.ok)
		}
	}
}

func TestDefaultTelegramCommandsIncludeMemory(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "memory" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /memory command entry", defaultTelegramCommands)
	}
}

func TestDefaultTelegramCommandsIncludeMission(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "mission" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /mission command entry", defaultTelegramCommands)
	}
}

func TestDefaultTelegramCommandsIncludeModel(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "model" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /model command entry", defaultTelegramCommands)
	}
}

func TestDefaultTelegramCommandsIncludeAgents(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "agents" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /agents command entry", defaultTelegramCommands)
	}
}

func TestDefaultTelegramCommandsIncludeDebug(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "debug" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /debug command entry", defaultTelegramCommands)
	}
}

func TestDefaultTelegramCommandsIncludeDoctor(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "doctor" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /doctor command entry", defaultTelegramCommands)
	}
}

func TestDefaultTelegramCommandsIncludeNew(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "new" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /new command entry", defaultTelegramCommands)
	}
}

func TestDefaultTelegramCommandsIncludeTailnet(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "tailnet" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /tailnet command entry", defaultTelegramCommands)
	}
}

func TestDefaultTelegramCommandsIncludeAutoApprove(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "autoapprove" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /autoapprove command entry", defaultTelegramCommands)
	}
}

func TestDefaultTelegramCommandsIncludeAutonomy(t *testing.T) {
	t.Parallel()

	found := false
	for _, cmd := range defaultTelegramCommands {
		if cmd.Command == "autonomy" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("defaultTelegramCommands = %#v, want /autonomy command entry", defaultTelegramCommands)
	}
}

func TestHandleTelegramCommandAutonomyAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart: true,
		autonomyStatus: core.AutonomyStatusSnapshot{
			DefaultMode:         "ask_first",
			Ceiling:             "leased",
			AllowLiveOverrides:  true,
			MaxOverrideDuration: 2 * time.Hour,
			Source:              "config",
			AuthorityBehavior:   "existing proposal and approval flows",
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 14,
		Text:      "/autonomy",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("messages = %#v, want one autonomy response", sender.msgs)
	}
	for _, want := range []string{"Autonomy policy", "Default: Ask first", "Ceiling: Leased", "Live changes: enabled", "Authority behavior: existing proposal and approval flows."} {
		if !strings.Contains(sender.msgs[0].Text, want) {
			t.Fatalf("autonomy response = %q, want %q", sender.msgs[0].Text, want)
		}
	}
}

func TestHandleTelegramCommandAutonomyDeniedForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: false}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1002,
		Text:     "/autonomy",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.msgs) != 1 || !strings.Contains(strings.ToLower(sender.msgs[0].Text), "admin only") {
		t.Fatalf("messages = %#v, want admin-only response", sender.msgs)
	}
}

func TestHandleTelegramCommandAutoApproveAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true, autoApproveReturn: "Auto-approval enabled for this chat."}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 14,
		Text:      "/autoapprove 15m all uses=2",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.autoApproveChatID != 7 || router.autoApproveSenderID != 1001 || router.autoApproveArgs != "15m all uses=2" {
		t.Fatalf("autoapprove inputs = chat:%d sender:%d args:%q, want 7/1001/15m all uses=2", router.autoApproveChatID, router.autoApproveSenderID, router.autoApproveArgs)
	}
	if len(sender.msgs) != 1 || !strings.Contains(sender.msgs[0].Text, "enabled") {
		t.Fatalf("messages = %#v, want enabled response", sender.msgs)
	}
}

func TestHandleTelegramCommandAutoApproveValidationErrorRepliesWithoutFatalError(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true, autoApproveErr: errors.New("auto-approval duration is capped at 48h0m0s")}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 14,
		Text:      "/autoapprove 24h all",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v, want nil so poller can advance the update offset", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.autoApproveArgs != "24h all" {
		t.Fatalf("autoApproveArgs = %q, want command args recorded", router.autoApproveArgs)
	}
	if len(sender.msgs) != 1 || !strings.Contains(sender.msgs[0].Text, "not applied") || !strings.Contains(sender.msgs[0].Text, "capped") {
		t.Fatalf("messages = %#v, want validation reply", sender.msgs)
	}
}

func TestHandleTelegramCommandAutoApproveDeniedForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: false}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1002,
		Text:     "/autoapprove 15m all",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.autoApproveChatID != 0 {
		t.Fatalf("autoApproveChatID = %d, want not called", router.autoApproveChatID)
	}
	if len(sender.msgs) != 1 || !strings.Contains(strings.ToLower(sender.msgs[0].Text), "admin only") {
		t.Fatalf("messages = %#v, want admin-only denial", sender.msgs)
	}
}

func TestHandleTelegramCommandStop(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		stop: core.StopResult{ActiveCanceled: true, QueuedDropped: true, ContinuationRevoked: true},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:    7,
		MessageID: 11,
		Text:      "/stop",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(sender.msgs))
	}
	if sender.msgs[0].ReplyTo == nil || *sender.msgs[0].ReplyTo != 11 {
		t.Fatalf("reply_to = %#v, want 11", sender.msgs[0].ReplyTo)
	}
}

func TestHandleTelegramCommandNew(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		newResult: core.NewSessionResult{
			ActiveCanceled:           true,
			QueuedDropped:            true,
			ContinuationRevoked:      true,
			PendingDecisionsDetached: 1,
			ContextCleared:           true,
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  99,
		MessageID: 13,
		Text:      "/new",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.newChatID != 7 || router.newSenderID != 99 {
		t.Fatalf("new inputs = (%d,%d), want (7,99)", router.newChatID, router.newSenderID)
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(sender.msgs))
	}
	if got := sender.msgs[0].Text; !strings.Contains(got, "Started a new session for this chat") || !strings.Contains(got, "Memories were not changed") {
		t.Fatalf("new text = %q, want new-session summary", got)
	}
	if sender.msgs[0].ReplyTo == nil || *sender.msgs[0].ReplyTo != 13 {
		t.Fatalf("reply_to = %#v, want 13", sender.msgs[0].ReplyTo)
	}
}

func TestHandleTelegramCommandDetach(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		detach: core.DetachResult{
			ActiveCanceled:           true,
			QueuedDropped:            true,
			ContinuationRevoked:      true,
			PendingDecisionsDetached: 2,
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  99,
		MessageID: 12,
		Text:      "/detach",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.detachChatID != 7 || router.detachSenderID != 99 {
		t.Fatalf("detach inputs = (%d,%d), want (7,99)", router.detachChatID, router.detachSenderID)
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(sender.msgs))
	}
	if got := sender.msgs[0].Text; !strings.Contains(got, "Detached") || !strings.Contains(got, "2 pending") {
		t.Fatalf("detach text = %q, want detach summary including pending count", got)
	}
	if sender.msgs[0].ReplyTo == nil || *sender.msgs[0].ReplyTo != 12 {
		t.Fatalf("reply_to = %#v, want 12", sender.msgs[0].ReplyTo)
	}
}

func TestHandleTelegramCommandStatus(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID:        7,
			ActiveTurnIDs: []uint64{91},
			QueueDepth:    2,
			PendingItems: []core.PendingItem{
				{Kind: core.PendingItemKindQueue, ChatID: 7, Summary: "queue_depth=2"},
			},
		},
		personaEffort:  "sonnet",
		governorEffort: "medium",
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID: 7,
		Text:   "/status",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Status Scope: chat") {
		t.Fatalf("status text = %q, want chat scope status", got)
	}
	foundThisChat := false
	foundPending := false
	foundRefresh := false
	for _, row := range sender.inline[0].rows {
		for _, button := range row {
			switch button.Text {
			case "This Chat":
				foundThisChat = true
			case "Pending Only":
				foundPending = true
			case "Refresh":
				foundRefresh = true
			}
		}
	}
	if !foundThisChat || !foundPending || !foundRefresh {
		t.Fatalf("status keyboard rows = %#v, want user status controls", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandAgentsShowsButtons(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart: true,
		durableAgentsList: []core.DurableAgentStatusSnapshot{
			{
				AgentID:     "idolum-daily-review",
				ChannelKind: "daily_review",
				Status:      "active",
				Health:      "ok",
			},
			{
				AgentID:          "ops-child",
				ChannelKind:      "telegram_dm",
				Status:           "active",
				Health:           "dormant",
				TailnetMode:      "tsnet",
				TailnetHostname:  "ops-child",
				TailnetSurfaceID: "durable_agent:ops-child:tsnet_http:status",
			},
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 55,
		Text:      "/agents",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.durableAgentsListSenderID != 1001 {
		t.Fatalf("durableAgentsListSenderID = %d, want 1001", router.durableAgentsListSenderID)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Durable Agents") {
		t.Fatalf("agents text = %q, want Durable Agents heading", got)
	}
	if got := sender.inline[0].text; !strings.Contains(got, "ops-child (telegram_dm | active | dormant | tailnet:tsnet)") {
		t.Fatalf("agents text = %q, want tailnet declaration marker", got)
	}
	foundStart := false
	foundRefresh := false
	for _, row := range sender.inline[0].rows {
		for _, button := range row {
			if strings.Contains(button.CallbackData, "agents:start:idolum-daily-review") {
				foundStart = true
			}
			if button.CallbackData == "agents:refresh" {
				foundRefresh = true
			}
		}
	}
	if !foundStart || !foundRefresh {
		t.Fatalf("agents rows = %#v, want start and refresh callbacks", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandMemoryShowsButtons(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		memoryReviewBySource: map[memoryReviewSource]memoryReviewSnapshot{
			memoryReviewSourceSession: {
				Source: memoryReviewSourceSession,
				Query:  "investigation thread",
				Items: []memoryReviewItem{
					{
						ID:      "session:12:user",
						Label:   "turn=12 role=user",
						Excerpt: "Can you investigate alternatives for the architecture?",
					},
					{
						ID:      "session:13:assistant",
						Label:   "turn=13 role=assistant",
						Excerpt: "I identified three options with different trade-offs.",
					},
				},
			},
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 21,
		Text:      "/memory",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.memoryReviewChatID != 7 || router.memoryReviewSenderID != 1001 || router.memoryReviewSource != memoryReviewSourceSession {
		t.Fatalf("memory review routing = chat:%d sender:%d source:%q", router.memoryReviewChatID, router.memoryReviewSenderID, router.memoryReviewSource)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Memory Review") {
		t.Fatalf("memory text = %q, want Memory Review heading", got)
	}
	foundFocus := false
	for _, row := range sender.inline[0].rows {
		for _, button := range row {
			if strings.Contains(button.CallbackData, "memory:focus:session:1") {
				foundFocus = true
				break
			}
		}
	}
	if !foundFocus {
		t.Fatalf("rows = %#v, want focus callback button", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandCallbackAgentsStartInvokesBackgroundConversation(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart:         true,
		startDurableResult: "Started background conversation with durable agent idolum-daily-review (wake requested).",
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-agents-start",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "agents:start:idolum-daily-review",
		Message: &telegram.Message{
			MessageID: 88,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.startDurableChatID != 7 || router.startDurableSenderID != 1001 {
		t.Fatalf("start durable routing = chat:%d sender:%d, want chat:7 sender:1001", router.startDurableChatID, router.startDurableSenderID)
	}
	if router.startDurableAgentID != "idolum-daily-review" {
		t.Fatalf("startDurableAgentID = %q, want idolum-daily-review", router.startDurableAgentID)
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(sender.msgs))
	}
	if got := sender.msgs[0].Text; !strings.Contains(got, "wake requested") {
		t.Fatalf("ack text = %q, want wake status", got)
	}
}

func TestHandleTelegramCommandCallbackMemoryFocusSetsFocus(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		memoryReviewBySource: map[memoryReviewSource]memoryReviewSnapshot{
			memoryReviewSourceSession: {
				Source: memoryReviewSourceSession,
				Query:  "investigation thread",
				Items: []memoryReviewItem{
					{
						ID:      "session:12:user",
						Label:   "turn=12 role=user",
						Excerpt: "Can you investigate alternatives for the architecture?",
					},
				},
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-memory-focus",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "memory:focus:session:1",
		Message: &telegram.Message{
			MessageID: 95,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	focus, ok := router.MemoryFocus(7)
	if !ok {
		t.Fatal("memory focus not set")
	}
	if focus.ItemID != "session:12:user" {
		t.Fatalf("focus item id = %q, want session:12:user", focus.ItemID)
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	if got := sender.editInline[0].text; !strings.Contains(got, "Active Focus") {
		t.Fatalf("memory panel text = %q, want Active Focus section", got)
	}
}

func TestHandleTelegramCommandStatusIncludesReadableSummary(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
		},
		statusReadableSummary: "Chat 7 is idle right now; no blocking pending items.",
		personaEffort:         "sonnet",
		governorEffort:        "medium",
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID: 7,
		Text:   "/status",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Quick Read: Chat 7 is idle right now; no blocking pending items.") {
		t.Fatalf("status text = %q, want readable quick summary", got)
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Status Scope: chat") {
		t.Fatalf("status text = %q, want machine status body", got)
	}
}

func TestHandleTelegramCommandStatusRewritesInconsistentReadableSummary(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID:          7,
			OperationStatus: "blocked",
			PendingItems: []core.PendingItem{
				{Kind: core.PendingItemKindDecision, ChatID: 7, ID: "decision-1", Summary: "kind=proposal_approval"},
			},
		},
		statusReadableSummary: "Chat 7 is idle right now; no pending items.",
		personaEffort:         "sonnet",
		governorEffort:        "medium",
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID: 7,
		Text:   "/status",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	text := strings.ToLower(sender.inline[0].text)
	if strings.Contains(text, "idle right now; no pending items") {
		t.Fatalf("status text = %q, do not want inconsistent readable summary", sender.inline[0].text)
	}
	if !strings.Contains(text, "quick read: chat is blocked") {
		t.Fatalf("status text = %q, want grounded blocked quick summary", sender.inline[0].text)
	}
}

func TestHandleTelegramCommandStatusShowsAdminButtonsForAdmins(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat:     core.ChatStatusSnapshot{ChatID: 7},
		statusSystem:   core.SystemStatusSnapshot{},
		personaEffort:  "opus",
		governorEffort: "high",
		canRestart:     true,
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1001,
		Text:     "/status",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	foundSystem := false
	foundHot := false
	foundFind := false
	foundDurables := false
	for _, row := range sender.inline[0].rows {
		for _, button := range row {
			switch button.Text {
			case "System Overview":
				foundSystem = true
			case "Hot Chats":
				foundHot = true
			case "Find Chat":
				foundFind = true
			case "Durables":
				foundDurables = true
			}
		}
	}
	if !foundSystem || !foundHot || !foundFind || !foundDurables {
		t.Fatalf("admin status keyboard rows = %#v, want admin controls", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandStatusShowsBlockedOperationSignal(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID:           7,
			OperationStatus:  "blocked",
			OperationStage:   "approval_wait",
			OperationSummary: "Waiting for admin review",
			PlanStepStatus:   "in_progress",
			PlanStep:         "Await admin approval",
		},
		personaEffort:  "opus",
		governorEffort: "high",
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID: 7,
		Text:   "/status",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Status: blocked") {
		t.Fatalf("status text = %q, want blocked status", got)
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Why: Waiting for admin review") {
		t.Fatalf("status text = %q, want blocked operation reason", got)
	}
}

func TestHandleTelegramCommandStatusUsesReadableCardInsteadOfRawDump(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID:           7,
			OperationStatus:  "blocked",
			OperationStage:   "approval_wait",
			OperationSummary: "Waiting for admin review",
			PlanStepStatus:   "in_progress",
			PlanStep:         "Await admin approval",
			ToolLifecycle: []core.ToolLifecycleStatusSnapshot{{
				ToolName:      "browse_page",
				InstallStatus: "verified",
				ProbeStatus:   "passed",
				AuditStatus:   "passed",
			}},
			CapabilityGrants: []core.CapabilityGrantStatusSnapshot{{
				GrantID:        "capg-status",
				Kind:           "purchase",
				Status:         "active",
				GrantedTo:      "family-child",
				AllowedActions: []string{"order"},
			}},
		},
		personaEffort:  "opus",
		governorEffort: "high",
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID: 7,
		Text:   "/status",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	text := sender.inline[0].text
	for _, needle := range []string{
		"Status: blocked",
		"Why: Waiting for admin review",
		"Now: Await admin approval",
		"Details: /debug has the full execution trace and source attribution.",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("status text = %q, want readable substring %q", text, needle)
		}
	}
	for _, forbidden := range []string{
		"Tool Lifecycle: Source:",
		"Capability Grants: Source:",
		"Source Attribution:",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("status text = %q, should not include raw diagnostic block %q", text, forbidden)
		}
	}
}

func TestHandleTelegramCommandDebugForNonAdminShowsChatDebugOnly(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:                    91,
				Status:                "running",
				Kind:                  "interactive",
				RequestText:           "debug this run",
				LastToolName:          "exec",
				LastToolPreview:       `{"command":"curl -fsS https://api.github.com/zen"}`,
				LastToolResultPreview: "stdout: Keep it logically awesome.",
			},
		},
		statusReadableSummary: "Chat 7 is working and currently running exec.",
		personaEffort:         "sonnet",
		governorEffort:        "medium",
		canRestart:            false,
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1002,
		Text:     "/debug",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Quick Read: Chat 7 is working and currently running exec.") {
		t.Fatalf("debug text = %q, want quick summary", got)
	}
	if got := sender.inline[0].text; strings.Contains(got, "status_scope=chat") {
		t.Fatalf("debug text = %q, do not want full chat section before read more", got)
	}
	if got := sender.inline[0].text; strings.Contains(got, "status_scope=system") {
		t.Fatalf("debug text = %q, do not want admin system section for non-admin", got)
	}
	if len(sender.inline[0].rows) != 1 || len(sender.inline[0].rows[0]) != 1 {
		t.Fatalf("rows = %#v, want one Read More button", sender.inline[0].rows)
	}
	if got := sender.inline[0].rows[0][0].Text; got != "Read More" {
		t.Fatalf("button text = %q, want Read More", got)
	}
	if got := sender.inline[0].rows[0][0].CallbackData; got != "debug:more" {
		t.Fatalf("callback = %q, want debug:more", got)
	}
}

func TestHandleTelegramCommandDebugForAdminIncludesSystemAndDurables(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
		},
		statusSystem: core.SystemStatusSnapshot{
			ActiveTurnCount: 1,
		},
		statusDurables: core.DurableAgentsStatusSnapshot{
			TotalAgents: 1,
			Agents: []core.DurableAgentStatusSnapshot{
				{
					AgentID:     "family-group",
					ChannelKind: "telegram_group",
					Status:      "active",
					Health:      "ok",
				},
			},
		},
		personaEffort:         "opus",
		governorEffort:        "high",
		canRestart:            true,
		statusReadableSummary: "Admin debug snapshot ready.",
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1001,
		Text:     "/debug",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Quick Read: Admin debug snapshot ready.") {
		t.Fatalf("debug text = %q, want quick summary in collapsed view", got)
	}
	if got := sender.inline[0].text; strings.Contains(got, "status_scope=chat") {
		t.Fatalf("debug text = %q, do not want full snapshot in collapsed view", got)
	}
	if len(sender.inline[0].rows) != 1 || len(sender.inline[0].rows[0]) != 1 {
		t.Fatalf("rows = %#v, want one Read More button", sender.inline[0].rows)
	}
	if got := sender.inline[0].rows[0][0].CallbackData; got != "debug:more" {
		t.Fatalf("callback = %q, want debug:more", got)
	}
}

func TestHandleTelegramCommandDoctorQueuesAdminDiagnosis(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: true}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:    1001,
		SenderID:  1001,
		MessageID: 44,
		Text:      "/doctor",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.queuedDoctorMsg == nil {
		t.Fatal("queuedDoctorMsg = nil, want doctor request")
	}
	if router.queuedDoctorMsg.ChatID != 1001 || router.queuedDoctorMsg.SenderID != 1001 {
		t.Fatalf("queued doctor msg = %#v, want original admin routing identity", router.queuedDoctorMsg)
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(sender.msgs))
	}
	if got := sender.msgs[0].Text; !strings.Contains(got, "Doctor diagnostics started") {
		t.Fatalf("doctor ack = %q, want started acknowledgement", got)
	}
	if sender.msgs[0].ReplyTo == nil || *sender.msgs[0].ReplyTo != 44 {
		t.Fatalf("reply_to = %#v, want 44", sender.msgs[0].ReplyTo)
	}
}

func TestHandleTelegramCommandDoctorDeniesNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: false}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1002,
		Text:     "/doctor",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.queuedDoctorMsg != nil {
		t.Fatalf("queuedDoctorMsg = %#v, want nil for non-admin", router.queuedDoctorMsg)
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(sender.msgs))
	}
	if got := sender.msgs[0].Text; !strings.Contains(got, "admin only") {
		t.Fatalf("doctor denial = %q, want admin-only denial", got)
	}
}

func TestHandleTelegramCommandDoctorRequiresPrivateAdminChat(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: true}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   -1007,
		SenderID: 1001,
		ChatType: "group",
		Text:     "/doctor",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.queuedDoctorMsg != nil {
		t.Fatalf("queuedDoctorMsg = %#v, want nil for group chat", router.queuedDoctorMsg)
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(sender.msgs))
	}
	if got := sender.msgs[0].Text; !strings.Contains(got, "private chat") {
		t.Fatalf("doctor denial = %q, want private-chat denial", got)
	}
}

func TestHandleTelegramCommandTailnetShowsReadOnlyStatus(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		tailnetStatus: core.TailnetStatusSnapshot{
			Enabled:           true,
			Backend:           "cli",
			Status:            "healthy",
			HostName:          "aphelion",
			DNSName:           "aphelion.example.ts.net",
			TailnetName:       "example.ts.net",
			TailscaleIPs:      []string{"100.64.0.10"},
			Tags:              []string{"tag:admin"},
			NetcheckAvailable: true,
			NetcheckSummary:   "UDP: true",
			Summary:           "aphelion is healthy.",
			Parent: &core.TailnetParentStatus{
				Enabled:     true,
				Running:     true,
				Hostname:    "aphelion",
				ListenAddr:  ":8765",
				MagicDNSURL: "http://aphelion.example.ts.net:8765",
			},
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1001,
		Text:     "/tailnet",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.tailnetStatusSenderID != 1001 {
		t.Fatalf("tailnet sender = %d, want 1001", router.tailnetStatusSenderID)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Tailnet") || !strings.Contains(got, "Status: healthy") || !strings.Contains(got, "aphelion.example.ts.net") || !strings.Contains(got, "Parent tsnet") || !strings.Contains(got, "http://aphelion.example.ts.net:8765") {
		t.Fatalf("tailnet text = %q, want compact status", got)
	}
	if len(sender.inline[0].rows) != 1 || len(sender.inline[0].rows[0]) != 3 || sender.inline[0].rows[0][0].CallbackData != "tailnet:refresh" || sender.inline[0].rows[0][1].CallbackData != "tailnet:surfaces" || sender.inline[0].rows[0][2].URL != "http://aphelion.example.ts.net:8765/status" {
		t.Fatalf("tailnet rows = %#v, want refresh", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandTailnetSurfacesShowsRegistry(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		tailnetSurfaces: []core.TailnetSurfaceStatus{{
			SurfaceID:   "parent:tsnet_http:status",
			OwnerKind:   "parent",
			OwnerID:     "aphelion",
			SurfaceKind: "tsnet_http",
			Name:        "status",
			Hostname:    "aphelion",
			TailnetName: "example.ts.net",
			URL:         "http://aphelion.example.ts.net:8765/status",
			Status:      "active",
		}},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1001,
		Text:     "/tailnet surfaces",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.tailnetSurfacesSenderID != 1001 || router.tailnetStatusSenderID != 0 {
		t.Fatalf("tailnet calls surfaces=%d status=%d, want surfaces only", router.tailnetSurfacesSenderID, router.tailnetStatusSenderID)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Tailnet Surfaces") || !strings.Contains(got, "active status") || !strings.Contains(got, "http://aphelion.example.ts.net:8765/status") {
		t.Fatalf("tailnet surfaces text = %q, want registry surface", got)
	}
	if len(sender.inline[0].rows) != 1 || sender.inline[0].rows[0][0].CallbackData != "tailnet:refresh" || sender.inline[0].rows[0][1].CallbackData != "tailnet:surfaces" {
		t.Fatalf("tailnet surfaces rows = %#v, want status/refresh", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandTailnetRevokeShowsConfirmation(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: true}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		Text:      "/tailnet revoke parent:tsnet_http:status",
		MessageID: 55,
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Revoke tailnet surface?") || !strings.Contains(got, "parent:tsnet_http:status") {
		t.Fatalf("revoke prompt = %q, want explicit surface confirmation", got)
	}
	if len(sender.inline[0].rows) != 1 || sender.inline[0].rows[0][0].Text != "Cancel" || sender.inline[0].rows[0][1].Text != "Revoke" {
		t.Fatalf("revoke rows = %#v, want cancel/revoke", sender.inline[0].rows)
	}
	if router.revokeTailnetSurfaceID != "" {
		t.Fatalf("revokeTailnetSurfaceID = %q, want no revoke before confirmation", router.revokeTailnetSurfaceID)
	}
}

func TestHandleTelegramCommandTailnetDeniesNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: false}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1002,
		Text:     "/tailnet",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.tailnetStatusSenderID != 0 {
		t.Fatalf("tailnet sender = %d, want no status lookup", router.tailnetStatusSenderID)
	}
	if len(sender.msgs) != 1 || !strings.Contains(sender.msgs[0].Text, "admin only") {
		t.Fatalf("tailnet denial messages = %#v, want admin-only denial", sender.msgs)
	}
}

func TestHandleTelegramCommandDebugRewritesInconsistentQuickSummary(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:     91,
				Status: "failed",
				Kind:   "interactive",
			},
		},
		statusReadableSummary: "Chat 7 is idle and all done.",
		personaEffort:         "sonnet",
		governorEffort:        "medium",
		canRestart:            false,
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1002,
		Text:     "/debug",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	got := strings.ToLower(sender.inline[0].text)
	if !strings.Contains(got, "quick read: chat 7 is failed") {
		t.Fatalf("debug text = %q, want grounded failed quick summary", sender.inline[0].text)
	}
	if strings.Contains(got, "idle and all done") {
		t.Fatalf("debug text = %q, do not want inconsistent readable summary", sender.inline[0].text)
	}
}

func TestHandleTelegramCommandCallbackDebugReadMoreExpandsFullSnapshot(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:                    91,
				Status:                "running",
				Kind:                  "interactive",
				RequestText:           "debug this run",
				LastToolName:          "exec",
				LastToolPreview:       `{"command":"curl -fsS https://api.github.com/zen"}`,
				LastToolResultPreview: "stdout: Keep it logically awesome.",
			},
		},
		statusReadableSummary: "Chat 7 is working and currently running exec.",
		personaEffort:         "sonnet",
		governorEffort:        "medium",
		canRestart:            false,
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-debug-more",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: "debug:more",
		Message: &telegram.Message{
			MessageID: 201,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 plain edits", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
	full := sender.editClear[0].text
	for _, msg := range sender.msgs {
		full += "\n" + msg.Text
	}
	if !strings.Contains(full, "Status Scope: chat") {
		t.Fatalf("full debug text = %q, want chat section", full)
	}
	if !strings.Contains(full, "Debug Chat:") {
		t.Fatalf("full debug text = %q, want debug_chat section", full)
	}
	if !strings.Contains(full, "Last Exec Command: \"curl -fsS https://api.github.com/zen\"") {
		t.Fatalf("full debug text = %q, want decoded last exec command", full)
	}
	if strings.Contains(full, "status_scope=system") {
		t.Fatalf("full debug text = %q, do not want admin sections for non-admin", full)
	}
}

func TestHandleTelegramCommandHelpHidesAdminRestartForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		personaEffort:  "sonnet",
		governorEffort: "medium",
		canRestart:     false,
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1002,
		Text:     "/help",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(sender.msgs))
	}
	if strings.Contains(sender.msgs[0].Text, "\n/restart - ") {
		t.Fatalf("help text = %q, want admin-only /restart hidden for non-admins", sender.msgs[0].Text)
	}
}

func TestHandleTelegramCommandHelpShowsAdminRestartForAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		personaEffort:  "sonnet",
		governorEffort: "medium",
		canRestart:     true,
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1001,
		Text:     "/help",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(sender.msgs))
	}
	if !strings.Contains(sender.msgs[0].Text, "\n/restart - ") {
		t.Fatalf("help text = %q, want admin /restart command listed", sender.msgs[0].Text)
	}
	if !strings.Contains(sender.msgs[0].Text, "\n/debug - ") {
		t.Fatalf("help text = %q, want /debug command listed", sender.msgs[0].Text)
	}
}

func TestHandleTelegramCommandStartHidesAdminRestartForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		personaEffort:  "sonnet",
		governorEffort: "medium",
		canRestart:     false,
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1002,
		Text:     "/start",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(sender.msgs))
	}
	if strings.Contains(sender.msgs[0].Text, "\n/restart - ") {
		t.Fatalf("start text = %q, want admin-only /restart hidden for non-admins", sender.msgs[0].Text)
	}
	if !strings.Contains(sender.msgs[0].Text, "\n/debug - ") {
		t.Fatalf("start text = %q, want /debug command listed", sender.msgs[0].Text)
	}
}

func TestHandleTelegramCommandSetPersonaModel(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		personaModel:        "claude-sonnet-4-6",
		personaModelOptions: []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-opus-4-7", "gpt-5.5"},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:    7,
		MessageID: 19,
		Text:      "/set_persona_model",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if sender.inline[0].replyTo == nil || *sender.inline[0].replyTo != 19 {
		t.Fatalf("reply_to = %#v, want 19", sender.inline[0].replyTo)
	}
	if len(sender.inline[0].rows) == 0 || len(sender.inline[0].rows[0]) == 0 {
		t.Fatalf("rows = %#v, want non-empty", sender.inline[0].rows)
	}
	if sender.inline[0].rows[0][0].CallbackData == "" {
		t.Fatalf("callback data empty in rows %#v", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandSetGovernorEffort(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		governorEffortOptions: []string{"medium", "high"},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:    7,
		MessageID: 20,
		Text:      "/set_governor_effort",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if len(sender.inline[0].rows) == 0 {
		t.Fatalf("rows = %#v, want non-empty", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandModelStatus(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		modelStatuses: []core.ModelSlotStatus{{
			Slot: core.ModelSlotGovernor,
			Effective: core.ModelSlotConfig{
				Slot:      core.ModelSlotGovernor,
				Provider:  core.ModelProviderOpenAI,
				Model:     "gpt-5.5",
				Effort:    "high",
				Transport: core.ModelTransportAuto,
			},
			Source: "override",
			Validation: core.ModelValidation{
				Valid:             true,
				ResolvedTransport: core.ModelTransportOpenAIResponses,
			},
		}},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 21,
		Text:      "/model status",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if !strings.Contains(sender.inline[0].text, "Governor: openai/gpt-5.5 effort=high") {
		t.Fatalf("model status text = %q", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "Transport: responses") {
		t.Fatalf("model status text = %q, want resolved transport", sender.inline[0].text)
	}
	if len(sender.inline[0].rows) == 0 || sender.inline[0].rows[0][0].CallbackData != "model:slot:p" {
		t.Fatalf("rows = %#v, want model slot buttons", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandModelSetParsesSlotConfig(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: true}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 22,
		Text:      "/model set governor anthropic/claude-opus-4.7 effort=max ttl=2h reason=debug swap",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.setModelSlotInput.Slot != core.ModelSlotGovernor {
		t.Fatalf("slot = %q, want governor", router.setModelSlotInput.Slot)
	}
	if router.setModelSlotInput.Provider != core.ModelProviderAnthropic || router.setModelSlotInput.Model != "claude-opus-4.7" {
		t.Fatalf("provider/model = %s/%s", router.setModelSlotInput.Provider, router.setModelSlotInput.Model)
	}
	if router.setModelSlotInput.Effort != "xhigh" {
		t.Fatalf("effort = %q, want xhigh", router.setModelSlotInput.Effort)
	}
	if router.setModelSlotTTL != 2*time.Hour {
		t.Fatalf("ttl = %s, want 2h", router.setModelSlotTTL)
	}
	if router.setModelSlotReason != "debug swap" {
		t.Fatalf("reason = %q, want debug swap", router.setModelSlotReason)
	}
}

func TestHandleTelegramCommandModelValidateRejectsBadTransport(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		validateModelSlotReturn: core.ModelValidation{
			Valid: false,
			Config: core.ModelSlotConfig{
				Slot:      core.ModelSlotGovernor,
				Provider:  core.ModelProviderOpenAI,
				Model:     "gpt-5.5",
				Effort:    "high",
				Transport: core.ModelTransportOpenAIChat,
			},
			Error: "openai gpt-5.5 with tools and effort requires responses transport",
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1001,
		MessageID: 23,
		Text:      "/model validate governor openai/gpt-5.5 effort=high transport=chat_completions",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(sender.msgs))
	}
	if !strings.Contains(sender.msgs[0].Text, "requires responses transport") {
		t.Fatalf("validation text = %q", sender.msgs[0].Text)
	}
}

func TestHandleTelegramCommandModelDeniedForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: false}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:    7,
		SenderID:  1002,
		MessageID: 24,
		Text:      "/model status",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.msgs) != 1 || !strings.Contains(sender.msgs[0].Text, "admin only") {
		t.Fatalf("message = %#v, want admin denial", sender.msgs)
	}
}

func TestHandleTelegramCommandCallbackModelSlotDetail(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		modelStatuses: []core.ModelSlotStatus{{
			Slot: core.ModelSlotGovernor,
			Effective: core.ModelSlotConfig{
				Slot:      core.ModelSlotGovernor,
				Provider:  core.ModelProviderAnthropic,
				Model:     "claude-sonnet-4-6",
				Effort:    "medium",
				Transport: core.ModelTransportAuto,
			},
			Default: core.ModelSlotConfig{
				Slot:      core.ModelSlotGovernor,
				Provider:  core.ModelProviderAnthropic,
				Model:     "claude-sonnet-4-6",
				Effort:    "medium",
				Transport: core.ModelTransportAuto,
			},
			Source: "default",
			Validation: core.ModelValidation{
				Valid:             true,
				ResolvedTransport: core.ModelTransportAnthropicMessages,
			},
		}},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "model-slot",
		Data: encodeModelCallbackData(modelCallbackSlot, core.ModelSlotGovernor, ""),
		From: &telegram.User{ID: 1001},
		Message: &telegram.Message{
			MessageID: 31,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	if !strings.Contains(sender.editInline[0].text, "Governor") {
		t.Fatalf("edit text = %q, want Governor detail", sender.editInline[0].text)
	}
	if len(sender.editInline[0].rows) < 3 {
		t.Fatalf("rows = %#v, want slot controls", sender.editInline[0].rows)
	}
}

func TestHandleTelegramCommandCallbackModelEffortSetsSlot(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		modelStatuses: []core.ModelSlotStatus{{
			Slot: core.ModelSlotGovernor,
			Effective: core.ModelSlotConfig{
				Slot:      core.ModelSlotGovernor,
				Provider:  core.ModelProviderAnthropic,
				Model:     "claude-sonnet-4-6",
				Effort:    "medium",
				Transport: core.ModelTransportAuto,
			},
			Source: "default",
			Validation: core.ModelValidation{
				Valid:             true,
				ResolvedTransport: core.ModelTransportAnthropicMessages,
			},
		}},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "model-effort",
		Data: encodeModelCallbackData(modelCallbackEffort, core.ModelSlotGovernor, "xhigh"),
		From: &telegram.User{ID: 1001},
		Message: &telegram.Message{
			MessageID: 32,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.setModelSlotInput.Effort != "xhigh" {
		t.Fatalf("set effort = %q, want xhigh", router.setModelSlotInput.Effort)
	}
	if router.setModelSlotActor != "telegram:1001" {
		t.Fatalf("actor = %q, want telegram:1001", router.setModelSlotActor)
	}
	if router.setModelSlotTTL != modelButtonOverrideTTL {
		t.Fatalf("ttl = %s, want %s", router.setModelSlotTTL, modelButtonOverrideTTL)
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
}

func TestHandleTelegramCommandCallbackModelPresetDoctorGPTUsesCodexWithTTL(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		modelStatuses: []core.ModelSlotStatus{{
			Slot: core.ModelSlotDoctor,
			Effective: core.ModelSlotConfig{
				Slot:      core.ModelSlotDoctor,
				Provider:  core.ModelProviderAnthropic,
				Model:     "claude-sonnet-4-6",
				Effort:    "xhigh",
				Transport: core.ModelTransportAuto,
			},
			Source: "default",
			Validation: core.ModelValidation{
				Valid:             true,
				ResolvedTransport: core.ModelTransportAnthropicMessages,
			},
		}},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "model-preset-doctor",
		Data: encodeModelCallbackData(modelCallbackPreset, core.ModelSlotDoctor, "gpt55"),
		From: &telegram.User{ID: 1001},
		Message: &telegram.Message{
			MessageID: 33,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.setModelSlotInput.Slot != core.ModelSlotDoctor {
		t.Fatalf("slot = %q, want doctor", router.setModelSlotInput.Slot)
	}
	if router.setModelSlotInput.Provider != core.ModelProviderCodex || router.setModelSlotInput.Model != "gpt-5.5" {
		t.Fatalf("provider/model = %s/%s, want codex/gpt-5.5", router.setModelSlotInput.Provider, router.setModelSlotInput.Model)
	}
	if router.setModelSlotInput.Effort != "xhigh" {
		t.Fatalf("effort = %q, want inherited xhigh", router.setModelSlotInput.Effort)
	}
	if router.setModelSlotTTL != modelButtonOverrideTTL {
		t.Fatalf("ttl = %s, want %s", router.setModelSlotTTL, modelButtonOverrideTTL)
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
}

func TestRenderModelSlotRowsHidesMaxForDoctorDirectOpenAI(t *testing.T) {
	t.Parallel()

	rows := renderModelSlotRows(core.ModelSlotStatus{
		Slot: core.ModelSlotDoctor,
		Effective: core.ModelSlotConfig{
			Slot:     core.ModelSlotDoctor,
			Provider: core.ModelProviderOpenAI,
			Model:    "gpt-5.5",
			Effort:   "high",
		},
	})
	var labels []string
	for _, row := range rows {
		for _, button := range row {
			labels = append(labels, button.Text)
		}
	}
	if !slices.Contains(labels, "Codex GPT-5.5") {
		t.Fatalf("labels = %#v, want doctor GPT preset labeled as Codex", labels)
	}
	if slices.Contains(labels, "Max") {
		t.Fatalf("labels = %#v, should hide Max for direct OpenAI doctor slot", labels)
	}
}

func TestHandleTelegramCommandCallbackModelDeniedForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: false}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "model-denied",
		Data: encodeModelCallbackData(modelCallbackStatus, "", ""),
		From: &telegram.User{ID: 1002},
		Message: &telegram.Message{
			MessageID: 33,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.answers) != 1 || !strings.Contains(sender.answers[0].text, "admin only") {
		t.Fatalf("answers = %#v, want admin denial", sender.answers)
	}
	if len(sender.editInline) != 0 {
		t.Fatalf("editInline count = %d, want 0", len(sender.editInline))
	}
}

func TestHandleTelegramCommandCallbackTailnetRefreshForAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		tailnetStatus: core.TailnetStatusSnapshot{
			Enabled:     true,
			Backend:     "cli",
			Status:      "degraded",
			HostName:    "aphelion",
			TailnetName: "example.ts.net",
			Issues: []core.TailnetIssue{{
				Code:     "magicdns_missing",
				Severity: "warning",
				Summary:  "no MagicDNS name was observed.",
			}},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-tailnet-refresh",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "tailnet:refresh",
		Message: &telegram.Message{
			MessageID: 97,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	if got := sender.editInline[0].text; !strings.Contains(got, "Status: degraded") || !strings.Contains(got, "magicdns_missing") {
		t.Fatalf("tailnet callback text = %q, want refreshed tailnet status", got)
	}
}

func TestHandleTelegramCommandCallbackTailnetSurfacesForAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		tailnetSurfaces: []core.TailnetSurfaceStatus{{
			SurfaceID:   "parent:tsnet_http:status",
			OwnerKind:   "parent",
			OwnerID:     "aphelion",
			SurfaceKind: "tsnet_http",
			Name:        "status",
			URL:         "http://aphelion.example.ts.net:8765/status",
			Status:      "active",
		}},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-tailnet-surfaces",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "tailnet:surfaces",
		Message: &telegram.Message{
			MessageID: 97,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	if got := sender.editInline[0].text; !strings.Contains(got, "Tailnet Surfaces") || !strings.Contains(got, "active status") {
		t.Fatalf("tailnet surfaces callback text = %q, want refreshed surfaces", got)
	}
	if router.tailnetSurfacesSenderID != 1001 || router.tailnetStatusSenderID != 0 {
		t.Fatalf("tailnet calls surfaces=%d status=%d, want surfaces only", router.tailnetSurfacesSenderID, router.tailnetStatusSenderID)
	}
}

func TestHandleTelegramCommandCallbackTailnetRevokeForAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		revokeTailnetSurfaceReturn: core.TailnetSurfaceStatus{
			SurfaceID: "parent:tsnet_http:status",
			Status:    "revoked",
		},
		revokeTailnetSurfaceOK: true,
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-tailnet-revoke",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "tailnet_revoke:confirm:parent:tsnet_http:status",
		Message: &telegram.Message{
			MessageID: 97,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.revokeTailnetSurfaceSenderID != 1001 || router.revokeTailnetSurfaceID != "parent:tsnet_http:status" {
		t.Fatalf("revoke call sender=%d surface=%q, want admin surface revoke", router.revokeTailnetSurfaceSenderID, router.revokeTailnetSurfaceID)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want confirmation edit", len(sender.editClear))
	}
	if got := sender.editClear[0].text; !strings.Contains(got, "Tailnet surface revoked") || !strings.Contains(got, "parent:tsnet_http:status") {
		t.Fatalf("revoke edit = %q, want revoked confirmation", got)
	}
}

func TestHandleTelegramCommandCallbackTailnetRevokeDeniedForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: false}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-tailnet-revoke-denied",
		From: &telegram.User{ID: 1002},
		Data: "tailnet_revoke:confirm:parent:tsnet_http:status",
		Message: &telegram.Message{
			MessageID: 97,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.answers) != 1 || !strings.Contains(sender.answers[0].text, "admin") {
		t.Fatalf("answers = %#v, want admin denial", sender.answers)
	}
	if router.revokeTailnetSurfaceID != "" {
		t.Fatalf("revokeTailnetSurfaceID = %q, want no revoke", router.revokeTailnetSurfaceID)
	}
}

func TestHandleTelegramCommandCallbackTailnetDeniedForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: false}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-tailnet-denied",
		From: &telegram.User{ID: 1002},
		Data: "tailnet:refresh",
		Message: &telegram.Message{
			MessageID: 97,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.answers) != 1 || !strings.Contains(sender.answers[0].text, "admin") {
		t.Fatalf("answers = %#v, want admin denial", sender.answers)
	}
	if len(sender.editInline) != 0 {
		t.Fatalf("editInline count = %d, want 0", len(sender.editInline))
	}
}

func TestHandleTelegramCommandCallbackStatusSystemForAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		statusSystem: core.SystemStatusSnapshot{
			ActiveChatIDs: []int64{7, 8},
			HotChats: []core.ChatStatusRollup{
				{ChatID: 7, PendingCount: 2},
				{ChatID: 8, PendingCount: 1},
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-status-system",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "status:system",
		Message: &telegram.Message{
			MessageID: 96,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	if got := sender.editInline[0].text; !strings.Contains(got, "Status Scope: system") {
		t.Fatalf("system status text = %q, want system scope", got)
	}
}

func TestHandleTelegramCommandCallbackStatusDurablesForAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		statusDurables: core.DurableAgentsStatusSnapshot{
			TotalAgents: 1,
			Agents: []core.DurableAgentStatusSnapshot{
				{
					AgentID:            "family-group",
					ChannelKind:        "telegram_group",
					Status:             "active",
					Health:             "ok",
					PolicyVersion:      2,
					PolicyHash:         "abc123",
					PolicyDrift:        "admin_review",
					PolicyOutboundMode: "read_only",
				},
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-status-durables",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "status:durables",
		Message: &telegram.Message{
			MessageID: 196,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	if got := sender.editInline[0].text; !strings.Contains(got, "Status Scope: durables") {
		t.Fatalf("durables status text = %q, want durables scope", got)
	}
}

func TestHandleTelegramCommandCallbackStatusSystemDeniedForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: false}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-status-system-denied",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: "status:system",
		Message: &telegram.Message{
			MessageID: 97,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if !strings.Contains(strings.ToLower(sender.answers[0].text), "admin") {
		t.Fatalf("answer text = %q, want admin-only denial", sender.answers[0].text)
	}
	if len(sender.editInline) != 0 {
		t.Fatalf("editInline count = %d, want 0 for denied callback", len(sender.editInline))
	}
}

func TestHandleTelegramCommandCallbackStatusFindChatShowsChatButtons(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		statusSystem: core.SystemStatusSnapshot{
			HotChats: []core.ChatStatusRollup{
				{ChatID: 9001, PendingCount: 3},
				{ChatID: 9002, PendingCount: 1},
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-status-find",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "status:find",
		Message: &telegram.Message{
			MessageID: 98,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	foundChatButton := false
	for _, row := range sender.editInline[0].rows {
		for _, button := range row {
			if strings.Contains(button.CallbackData, "status:chat:9001") {
				foundChatButton = true
			}
		}
	}
	if !foundChatButton {
		t.Fatalf("rows = %#v, want chat drill-down callback", sender.editInline[0].rows)
	}
}

func TestHandleTelegramCommandCallbackStatusChunksOverflowDeterministically(t *testing.T) {
	t.Parallel()

	pending := make([]core.PendingItem, 0, 120)
	for i := 0; i < 120; i++ {
		pending = append(pending, core.PendingItem{
			Kind:    core.PendingItemKindDecision,
			ChatID:  int64(7000 + i%3),
			ID:      "decision-overflow-" + strings.Repeat("x", 20),
			Summary: strings.Repeat("very long pending summary ", 4),
		})
	}

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		statusReadableSummary: "System status overflow probe. " +
			strings.Repeat("This deliberately long quick read verifies deterministic Telegram chunking. ", 80),
		statusSystem: core.SystemStatusSnapshot{
			PendingItems: pending,
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-status-overflow",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "status:system",
		Message: &telegram.Message{
			MessageID: 99,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	if len([]rune(sender.editInline[0].text)) > 3800 {
		t.Fatalf("edited text rune length = %d, want <= 3800", len([]rune(sender.editInline[0].text)))
	}
	if len(sender.msgs) == 0 {
		t.Fatalf("follow-up messages = %#v, want overflow chunks", sender.msgs)
	}
}

func TestHandleTelegramCommandCallbackDeliberationStop(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		stop: core.StopResult{ActiveCanceled: true, QueuedDropped: true},
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:     501,
				ChatID: 7,
				Status: string(session.TurnRunStatusRunning),
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-delib-stop",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: core.EncodeDeliberationControlCallbackData(501, core.DeliberationControlActionStop),
		Message: &telegram.Message{
			MessageID: 240,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.stopCalls != 1 || router.stopInput != 7 {
		t.Fatalf("stop calls/input = (%d,%d), want (1,7)", router.stopCalls, router.stopInput)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 plain edits", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
	if got := sender.editClear[0].text; !strings.Contains(got, "Stopped the current turn and cleared queued work for this chat.") {
		t.Fatalf("edited text = %q, want stop summary", got)
	}
}

func TestHandleTelegramCommandCallbackStreamStop(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		stop:           core.StopResult{ActiveCanceled: true},
		streamControls: map[string]int64{"stream-abc": 7},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-stream-stop",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: core.EncodeStreamControlCallbackData("stream-abc", core.StreamControlActionStop),
		Message: &telegram.Message{
			MessageID: 241,
			Text:      "partial streamed reply...",
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.streamStopCalls != 1 || router.streamStopID != "stream-abc" || router.streamStopChatID != 7 {
		t.Fatalf("stream stop = calls:%d id:%q chat:%d, want stream-abc/7", router.streamStopCalls, router.streamStopID, router.streamStopChatID)
	}
	if router.stopCalls != 1 || router.stopInput != 7 {
		t.Fatalf("stop calls/input = (%d,%d), want (1,7)", router.stopCalls, router.stopInput)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "Stopping stream." {
		t.Fatalf("answers = %#v, want stopping answer", sender.answers)
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
	if got := sender.editClear[0].text; !strings.Contains(got, "partial streamed reply") || !strings.Contains(got, "Stopping.") {
		t.Fatalf("edited text = %q, want partial reply with stopping marker", got)
	}
}

func TestHandleTelegramCommandCallbackStreamStopRejectsStale(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-stream-stale",
		Data: core.EncodeStreamControlCallbackData("stream-missing", core.StreamControlActionStop),
		Message: &telegram.Message{
			MessageID: 242,
			Text:      "already done",
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.stopCalls != 0 {
		t.Fatalf("stop calls = %d, want 0", router.stopCalls)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != staleStreamCallbackText {
		t.Fatalf("answers = %#v, want stale stream answer", sender.answers)
	}
	if len(sender.editClear) != 1 || sender.editClear[0].text != "already done" {
		t.Fatalf("editClear = %#v, want keyboard clear with original text", sender.editClear)
	}
}

func TestHandleTelegramCommandCallbackDeliberationDetach(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		detach: core.DetachResult{
			ActiveCanceled:           true,
			QueuedDropped:            true,
			ContinuationRevoked:      true,
			PendingDecisionsDetached: 1,
		},
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:     777,
				ChatID: 7,
				Status: string(session.TurnRunStatusRunning),
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-delib-detach",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: core.EncodeDeliberationControlCallbackData(777, core.DeliberationControlActionDetach),
		Message: &telegram.Message{
			MessageID: 241,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.detachChatID != 7 || router.detachSenderID != 1002 {
		t.Fatalf("detach inputs = (%d,%d), want (7,1002)", router.detachChatID, router.detachSenderID)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 plain edits", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
	if got := sender.editClear[0].text; !strings.Contains(got, "Detached this chat from pending work") {
		t.Fatalf("edited text = %q, want detach summary", got)
	}
}

func TestHandleTelegramCommandCallbackDeliberationRejectsStaleRun(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:     700,
				ChatID: 7,
				Status: string(session.TurnRunStatusCompleted),
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-delib-stale",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: core.EncodeDeliberationControlCallbackData(701, core.DeliberationControlActionStop),
		Message: &telegram.Message{
			MessageID: 242,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
			Text:      "Done.\n- Finished earlier.",
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.stopCalls != 0 {
		t.Fatalf("stopCalls = %d, want 0 for stale callback", router.stopCalls)
	}
	if router.detachChatID != 0 {
		t.Fatalf("detachChatID = %d, want 0 for stale callback", router.detachChatID)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if sender.answers[0].text != staleDeliberationCallbackText {
		t.Fatalf("answer text = %q, want stale callback warning", sender.answers[0].text)
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 for stale callback", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1 stale cleanup edit", len(sender.editClear))
	}
	if sender.editClear[0].text != "Done.\n- Finished earlier." {
		t.Fatalf("stale cleanup text = %q, want existing message text", sender.editClear[0].text)
	}
}

func TestHandleTelegramCommandCallbackPersonaModel(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		setPersonaModelReturn: "claude-opus-4-6",
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-1",
		Data: "recipe:persona_model:claude-opus-4-6",
		Message: &telegram.Message{
			MessageID: 91,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.setPersonaModelInput != "claude-opus-4-6" {
		t.Fatalf("setPersonaModel input = %q, want claude-opus-4-6", router.setPersonaModelInput)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 plain edits", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
}

func TestHandleTelegramCommandCallbackGovernorEffort(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		setGovernorEffortReturn: "high",
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-2",
		Data: "recipe:governor_effort:high",
		Message: &telegram.Message{
			MessageID: 92,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.setGovernorEffortInput != "high" {
		t.Fatalf("setGovernorEffort input = %q, want high", router.setGovernorEffortInput)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 plain edits", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
}

func TestHandleTelegramCommandCallbackContinuationApprove(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	triggerStarted := make(chan struct{})
	router := stubCommandRouter{continuationState: session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-1",
		RemainingTurns: 1,
		StageSummary:   "Resume the next bounded step.",
	}, triggerContinuationStarted: triggerStarted}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-continue",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-1", "approve"),
		Message: &telegram.Message{MessageID: 93, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approveContinuationInput != 7 {
		t.Fatalf("approveContinuationInput = %d, want 7", router.approveContinuationInput)
	}
	if router.approveContinuationApprover != 1002 {
		t.Fatalf("approveContinuationApprover = %d, want 1002", router.approveContinuationApprover)
	}
	waitForStubContinuationTrigger(t, triggerStarted)
	if router.triggerContinuationInput != 7 {
		t.Fatalf("triggerContinuationInput = %d, want 7", router.triggerContinuationInput)
	}
	if router.continuationStateInput != 7 {
		t.Fatalf("continuationStateInput = %d, want 7", router.continuationStateInput)
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 plain edits", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
}

func TestHandleTelegramCommandCallbackContinuationApproveContinuesWhenEditFails(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{editErr: errors.New("telegram editMessageText failed: message is not modified")}
	triggerStarted := make(chan struct{})
	router := stubCommandRouter{continuationState: session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-2",
		RemainingTurns: 1,
		StageSummary:   "Resume the next bounded step.",
	}, triggerContinuationStarted: triggerStarted}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-continue-edit-fail",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-2", "approve"),
		Message: &telegram.Message{MessageID: 193, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	waitForStubContinuationTrigger(t, triggerStarted)
	if router.triggerContinuationInput != 7 {
		t.Fatalf("triggerContinuationInput = %d, want 7", router.triggerContinuationInput)
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 plain edits", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
}

func TestHandleTelegramCommandCallbackContinuationApproveContainsExpiredLease(t *testing.T) {
	t.Parallel()

	pending := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-expired",
		RemainingTurns: 1,
		StageSummary:   "Resume the expired bounded step.",
	}
	expired := pending
	expired.Status = session.ContinuationStatusIdle
	expired.RemainingTurns = 0
	expired.ActionProposal = session.ActionProposal{ID: "aprop-expired", Status: session.ProposalStatusExpired}
	expired.ContinuationLease = session.ContinuationLease{ID: "lease-expired", ProposalID: "aprop-expired", Status: session.ContinuationLeaseStatusExpired}

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		continuationState:         pending,
		approveContinuationReturn: expired,
		approveContinuationErr:    fmt.Errorf("approve continuation: %w", core.ErrContinuationExpired),
		refreshContinuationReturn: session.ContinuationState{
			Status:         session.ContinuationStatusPending,
			DecisionID:     "decision-refreshed",
			RemainingTurns: 1,
			StageSummary:   "Resume the expired bounded step.",
		},
		refreshContinuationSent: true,
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-expired",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-expired", "approve"),
		Message: &telegram.Message{MessageID: 194, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v, want nil for expired continuation", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approveContinuationInput != 7 || router.approveContinuationApprover != 1002 {
		t.Fatalf("approve input/approver = %d/%d, want 7/1002", router.approveContinuationInput, router.approveContinuationApprover)
	}
	if router.triggerContinuationInput != 0 {
		t.Fatalf("triggerContinuationInput = %d, want 0 after expired approval", router.triggerContinuationInput)
	}
	if router.refreshContinuationInput != 7 || !strings.Contains(router.refreshContinuationReason, "expired") {
		t.Fatalf("refresh input/reason = %d/%q, want 7 expired reason", router.refreshContinuationInput, router.refreshContinuationReason)
	}
	if len(sender.answers) != 1 || !strings.Contains(strings.ToLower(sender.answers[0].text), "fresh approval prompt") {
		t.Fatalf("answers = %#v, want fresh approval prompt callback answer", sender.answers)
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear = %#v, want one expired refresh message update", sender.editClear)
	}
	editText := strings.ToLower(sender.editClear[0].text)
	if !strings.Contains(editText, "expired") || !strings.Contains(editText, "fresh approval prompt") {
		t.Fatalf("editClear = %#v, want expired refresh message update", sender.editClear)
	}
	if len(router.callbackErrorRecords) != 0 {
		t.Fatalf("callbackErrorRecords = %#v, want no callback failure when expired approval refreshes successfully", router.callbackErrorRecords)
	}
}

func TestHandleTelegramCommandCallbackContinuationApproveRecordsAckErrorWithoutFailing(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{answerErr: errors.New("telegram answerCallbackQuery failed: Bad Request: chat not found")}
	triggerStarted := make(chan struct{})
	router := stubCommandRouter{continuationState: session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-ack-error",
		RemainingTurns: 1,
		StageSummary:   "Resume despite callback ack failure.",
	}, triggerContinuationStarted: triggerStarted}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-ack-error",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-ack-error", "approve"),
		Message: &telegram.Message{MessageID: 195, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v, want nil for callback ack failure", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	waitForStubContinuationTrigger(t, triggerStarted)
	if router.triggerContinuationInput != 7 {
		t.Fatalf("triggerContinuationInput = %d, want 7", router.triggerContinuationInput)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(router.callbackErrorRecords) != 1 {
		t.Fatalf("callbackErrorRecords = %#v, want one ack record", router.callbackErrorRecords)
	}
	if router.callbackErrorRecords[0].chatID != 7 || router.callbackErrorRecords[0].callbackKind != "continuation.approve.answer" {
		t.Fatalf("callback error record = %#v, want continuation.approve.answer", router.callbackErrorRecords[0])
	}
}

func TestPersonaModelButtonLabelIncludesOpus47(t *testing.T) {
	t.Parallel()
	if got := personaModelButtonLabel("claude-opus-4-7"); got != "Opus 4.7" {
		t.Fatalf("personaModelButtonLabel() = %q, want Opus 4.7", got)
	}
	if got := personaModelButtonLabel("gpt-5.5"); got != "GPT-5.5" {
		t.Fatalf("personaModelButtonLabel() = %q, want GPT-5.5", got)
	}
}

func TestHandleTelegramCommandCallbackContinuationStopRendersCombinedStopResult(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		continuationState:      session.ContinuationState{Status: session.ContinuationStatusPending, DecisionID: "decision-3", RemainingTurns: 1},
		stopContinuationResult: core.StopResult{ContinuationRevoked: true, ContinuationLabel: "Plan: Mada's Job Agent (Phase J1)"},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-stop",
		Data:    encodeContinuationCallbackData("decision-3", "stop"),
		Message: &telegram.Message{MessageID: 94, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.stopContinuationInput != 7 {
		t.Fatalf("stopContinuationInput = %d, want 7", router.stopContinuationInput)
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 plain edits", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
	if sender.editClear[0].text != "Stopped Plan: Mada's Job Agent (Phase J1)." {
		t.Fatalf("edit text = %q, want continuation revoke text", sender.editClear[0].text)
	}
}

func TestHandleTelegramCommandCallbackContinuationStopRendersNoOpStopResult(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		continuationState:      session.ContinuationState{Status: session.ContinuationStatusPending, DecisionID: "decision-4", RemainingTurns: 1},
		stopContinuationResult: core.StopResult{},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-stop-none",
		Data:    encodeContinuationCallbackData("decision-4", "stop"),
		Message: &telegram.Message{MessageID: 95, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 plain edits", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
	if sender.editClear[0].text != "Continuation approval was already inactive for this chat." {
		t.Fatalf("edit text = %q, want inactive continuation text", sender.editClear[0].text)
	}
}

func TestHandleTelegramCommandCallbackContinuationRejectsStaleDecisionID(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		continuationState: session.ContinuationState{
			Status:         session.ContinuationStatusPending,
			DecisionID:     "decision-current",
			RemainingTurns: 1,
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-stale",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-old", "approve"),
		Message: &telegram.Message{MessageID: 196, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approveContinuationInput != 0 {
		t.Fatalf("approveContinuationInput = %d, want 0 for stale callback", router.approveContinuationInput)
	}
	if router.triggerContinuationInput != 0 {
		t.Fatalf("triggerContinuationInput = %d, want 0 for stale callback", router.triggerContinuationInput)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if sender.answers[0].text != staleContinuationCallbackText {
		t.Fatalf("answer text = %q, want stale callback warning", sender.answers[0].text)
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 for stale callback", len(sender.edits))
	}
}

func TestDurableWizardInlineRowsFromTextInProgress(t *testing.T) {
	t.Parallel()

	text := "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: in_progress\ncurrent_step: autonomy\nmissing: autonomy,surface_rules\nnext_question: Should the child be observe_only, local_drafts, review_before_reply, or reply_within_charter?\naddress: child-endpoint\nadapter: child_adapter\nautonomy: \nwakeup_mode: poll\npoll_interval: 5m\nsynthesis_cadence: 4h\ncharter:\n"
	rows := durableWizardInlineRowsFromText(text)
	if len(rows) < 2 {
		t.Fatalf("rows len = %d, want at least option row and controls", len(rows))
	}
	foundObserveOnly := false
	for _, row := range rows {
		for _, button := range row {
			if strings.EqualFold(button.Text, "Observe only") {
				foundObserveOnly = true
			}
		}
	}
	if !foundObserveOnly {
		t.Fatalf("rows = %#v, want Observe only button", rows)
	}
	last := rows[len(rows)-1]
	if len(last) != 2 || last[0].Text != "Cancel" || last[1].Text != "Refresh" {
		t.Fatalf("last row = %#v, want [Cancel|Refresh] controls", last)
	}
}

func TestDurableWizardInlineRowsFromTextBootstrapProfile(t *testing.T) {
	t.Parallel()

	text := "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: in_progress\ncurrent_step: bootstrap_profile\nmissing: bootstrap_profile,autonomy\nnext_question: Should this child inherit the parent bootstrap defaults or pin a child-custom bootstrap profile?\naddress: child-endpoint\nadapter: child_adapter\nbootstrap_profile: \nbootstrap_backend: native\nbootstrap_native_provider: anthropic\nbootstrap_model: claude-parent\n"
	rows := durableWizardInlineRowsFromText(text)
	if len(rows) < 2 {
		t.Fatalf("rows len = %d, want at least option row and controls", len(rows))
	}
	foundInherit := false
	foundCustom := false
	for _, row := range rows {
		for _, button := range row {
			if strings.EqualFold(button.Text, "Inherit parent") {
				foundInherit = true
			}
			if strings.EqualFold(button.Text, "Child custom") {
				foundCustom = true
			}
		}
	}
	if !foundInherit || !foundCustom {
		t.Fatalf("rows = %#v, want bootstrap profile buttons", rows)
	}
}

func TestDurableWizardInlineRowsFromTextBootstrapProfileCodex(t *testing.T) {
	t.Parallel()

	text := "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: in_progress\ncurrent_step: bootstrap_profile\nmissing: bootstrap_profile,autonomy\nnext_question: This child uses a codex bootstrap backend; keep parent bootstrap defaults?\naddress: child-endpoint\nadapter: child_adapter\nbootstrap_profile: \nbootstrap_backend: codex\nbootstrap_model: \n"
	rows := durableWizardInlineRowsFromText(text)
	if len(rows) < 2 {
		t.Fatalf("rows len = %d, want at least option row and controls", len(rows))
	}
	optionButtons := rows[0]
	if len(optionButtons) != 1 || optionButtons[0].Text != "Inherit parent" {
		t.Fatalf("option row = %#v, want only Inherit parent for codex", optionButtons)
	}
}

func TestDurableWizardInlineRowsFromTextBootstrapModel(t *testing.T) {
	t.Parallel()

	text := "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: in_progress\ncurrent_step: bootstrap_model\nmissing: bootstrap_model\nnext_question: Which model should this child pin for child-custom bootstrap?\naddress: child-endpoint\nadapter: child_adapter\nbootstrap_profile: child_custom\nbootstrap_backend: native\nbootstrap_native_provider: anthropic\nbootstrap_model: claude-parent\n"
	rows := durableWizardInlineRowsFromText(text)
	if len(rows) < 2 {
		t.Fatalf("rows len = %d, want option rows plus controls", len(rows))
	}
	foundKeepParent := false
	foundSonnet := false
	for _, row := range rows {
		for _, button := range row {
			if strings.EqualFold(button.Text, "Keep parent model") {
				foundKeepParent = true
			}
			if strings.EqualFold(button.Text, "Sonnet 4.6") {
				foundSonnet = true
			}
		}
	}
	if !foundKeepParent || !foundSonnet {
		t.Fatalf("rows = %#v, want bootstrap model buttons", rows)
	}
}

func TestDurableWizardInlineRowsFromTextReady(t *testing.T) {
	t.Parallel()

	text := "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: ready\ncurrent_step: -\nmissing: -\naddress: child-endpoint\nadapter: child_adapter\nautonomy: observe_only\nwakeup_mode: poll\npoll_interval: 5m\nsynthesis_cadence: 4h\ncharter: Read-only child.\n"
	rows := durableWizardInlineRowsFromText(text)
	if len(rows) != 1 {
		t.Fatalf("rows len = %d, want 1 finalize control row", len(rows))
	}
	if len(rows[0]) != 2 || rows[0][0].Text != "Cancel" || rows[0][1].Text != "Finalize" {
		t.Fatalf("row = %#v, want [Cancel|Finalize]", rows[0])
	}
}

func TestHandleTelegramCommandCallbackDurableWizardAnswer(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart:          true,
		durableWizardResult: "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: ready\ncurrent_step: -\nmissing: -\naddress: child-endpoint\nadapter: child_adapter\nautonomy: observe_only\nwakeup_mode: poll\npoll_interval: 5m\nsynthesis_cadence: 4h\ncharter: Read-only child.\n",
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-durable-answer",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: encodeDurableWizardAnswerCallbackData("autonomy", "observe_only"),
		Message: &telegram.Message{
			MessageID: 210,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
			Text:      "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: in_progress\ncurrent_step: autonomy\nmissing: autonomy,surface_rules\naddress: child-endpoint\nadapter: child_adapter\nautonomy: \nwakeup_mode: poll\npoll_interval: 5m\nsynthesis_cadence: 4h\ncharter:\n",
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.durableWizardChatID != 7 || router.durableWizardSenderID != 1001 {
		t.Fatalf("wizard callback routing = (%d,%d), want (7,1001)", router.durableWizardChatID, router.durableWizardSenderID)
	}
	if router.durableWizardAction != "wizard_answer" {
		t.Fatalf("durableWizardAction = %q, want wizard_answer", router.durableWizardAction)
	}
	if router.durableWizardAgentID != "child-alpha" {
		t.Fatalf("durableWizardAgentID = %q, want child-alpha", router.durableWizardAgentID)
	}
	if got := router.durableWizardAnswers["autonomy"]; got != "observe_only" {
		t.Fatalf("durableWizardAnswers[autonomy] = %#v, want observe_only", got)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	if len(sender.editInline[0].rows) != 1 || len(sender.editInline[0].rows[0]) != 2 {
		t.Fatalf("rows = %#v, want finalize controls", sender.editInline[0].rows)
	}
	if sender.editInline[0].rows[0][0].Text != "Cancel" || sender.editInline[0].rows[0][1].Text != "Finalize" {
		t.Fatalf("row = %#v, want [Cancel|Finalize]", sender.editInline[0].rows[0])
	}
}

func TestHandleTelegramCommandCallbackDurableWizardBootstrapModelKeepParent(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart:          true,
		durableWizardResult: "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: ready\ncurrent_step: -\nmissing: -\nbootstrap_profile: child_custom\nbootstrap_model: claude-parent\n",
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-durable-bootstrap-model",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: encodeDurableWizardAnswerCallbackData("bootstrap_model", "keep_parent_model"),
		Message: &telegram.Message{
			MessageID: 212,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
			Text:      "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: in_progress\ncurrent_step: bootstrap_model\nmissing: bootstrap_model\nbootstrap_profile: child_custom\nbootstrap_model: claude-parent\n",
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.durableWizardAction != "wizard_answer" {
		t.Fatalf("durableWizardAction = %q, want wizard_answer", router.durableWizardAction)
	}
	if got := router.durableWizardAnswers["bootstrap_model"]; got != "claude-parent" {
		t.Fatalf("durableWizardAnswers[bootstrap_model] = %#v, want claude-parent", got)
	}
}

func TestHandleTelegramCommandCallbackDurableWizardRejectsCodexChildCustom(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: true}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-durable-codex-child-custom",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: encodeDurableWizardAnswerCallbackData("bootstrap_profile", "child_custom"),
		Message: &telegram.Message{
			MessageID: 213,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
			Text:      "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: in_progress\ncurrent_step: bootstrap_profile\nmissing: bootstrap_profile,autonomy\nbootstrap_backend: codex\nbootstrap_model: \n",
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.durableWizardAction != "" {
		t.Fatalf("durableWizardAction = %q, want no wizard execution for invalid codex child_custom", router.durableWizardAction)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != staleDurableWizardCallbackText {
		t.Fatalf("answers = %#v, want stale durable wizard callback text", sender.answers)
	}
}

func TestHandleTelegramCommandCallbackDurableWizardRejectsStaleStep(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: true}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-durable-stale",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: encodeDurableWizardAnswerCallbackData("autonomy", "observe_only"),
		Message: &telegram.Message{
			MessageID: 211,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
			Text:      "action: durable-agent wizard show\nagent_id: child-alpha\nchannel_kind: external_channel\nwizard_status: in_progress\ncurrent_step: adapter\nmissing: adapter,autonomy\naddress: child-endpoint\nadapter: \nautonomy: \n",
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.durableWizardAction != "" {
		t.Fatalf("durableWizardAction = %q, want no wizard execution for stale callback", router.durableWizardAction)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if sender.answers[0].text != staleDurableWizardCallbackText {
		t.Fatalf("answer text = %q, want stale wizard callback warning", sender.answers[0].text)
	}
	if len(sender.editInline) != 0 {
		t.Fatalf("editInline count = %d, want 0 for stale callback", len(sender.editInline))
	}
}

func TestHandleTelegramCommandReinstallQueuesRequest(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{}
	msg := core.InboundMessage{ChatID: 7, SenderID: 1001, SenderName: "admin", MessageID: 11, Text: "/reinstall"}
	handled, err := handleTelegramCommand(context.Background(), sender, router, msg)
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.queuedReinstallMsg == nil {
		t.Fatal("queuedReinstallMsg = nil, want queued message")
	}
	if router.queuedReinstallMsg.ChatID != msg.ChatID || router.queuedReinstallMsg.SenderID != msg.SenderID {
		t.Fatalf("queued reinstall msg = %#v, want original routing identity", router.queuedReinstallMsg)
	}
	if router.queuedReinstallMsg.Text != msg.Text {
		t.Fatalf("queued reinstall text = %q, want original command text at command-router boundary", router.queuedReinstallMsg.Text)
	}
	if len(sender.msgs) != 1 || sender.msgs[0].Text != "Queued a reinstall request as a normal turn in this chat." {
		t.Fatalf("sender msgs = %#v, want queued reinstall ack", sender.msgs)
	}
}

func TestHandleTelegramCommandRestartForcesRestart(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true}
	msg := core.InboundMessage{ChatID: 7, SenderID: 1001, SenderName: "admin", MessageID: 12, Text: "/restart"}
	handled, err := handleTelegramCommand(context.Background(), sender, router, msg)
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.restartCalls != 1 || router.restartInput != msg.ChatID {
		t.Fatalf("restart calls/input = (%d,%d), want (1,%d)", router.restartCalls, router.restartInput, msg.ChatID)
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("sender msgs = %#v, want one restart ack", sender.msgs)
	}
	if sender.msgs[0].Text != "Restarting the gateway now. Active work and continuation leases will be parked for startup recovery." {
		t.Fatalf("restart ack text = %q, want restart confirmation", sender.msgs[0].Text)
	}
}

func TestHandleTelegramCommandRestartDeniedForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: false}
	msg := core.InboundMessage{ChatID: 7, SenderID: 2002, SenderName: "approved", MessageID: 13, Text: "/restart"}
	handled, err := handleTelegramCommand(context.Background(), sender, router, msg)
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.restartCalls != 0 {
		t.Fatalf("restart calls = %d, want 0 for denied restart", router.restartCalls)
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("sender msgs = %#v, want one deny ack", sender.msgs)
	}
	if sender.msgs[0].Text != "Restart denied. Only Telegram admins can run /restart." {
		t.Fatalf("deny ack text = %q, want denied confirmation", sender.msgs[0].Text)
	}
}

func TestRenderStatusSourceAttributionLifecycleFieldScope(t *testing.T) {
	t.Parallel()

	system := renderStatusSourceAttribution(statusViewSystem)
	if !strings.Contains(system, "field=tool_authority_lifecycle") {
		t.Fatalf("system source attribution = %q, want tool_authority_lifecycle field", system)
	}

	hot := renderStatusSourceAttribution(statusViewHotChats)
	if strings.Contains(hot, "field=tool_authority_lifecycle") {
		t.Fatalf("hot source attribution = %q, do not want tool_authority_lifecycle field", hot)
	}

	find := renderStatusSourceAttribution(statusViewFindChat)
	if strings.Contains(find, "field=tool_authority_lifecycle") {
		t.Fatalf("find source attribution = %q, do not want tool_authority_lifecycle field", find)
	}
}

func TestMissionProposeCommandSendsActionProposalButtons(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		missionActionProposal: session.ActionProposal{
			ID:               "aprop-mission-action-ui",
			MissionID:        "mission-action-ui",
			Summary:          "Implement the generic approval UI.",
			WhyNow:           "Mission Control surfaced this candidate.",
			BoundedEffect:    "Mark active only; do not self-continue.",
			RiskClass:        "mission_control",
			AllowedActions:   []string{"mark_mission_active"},
			ForbiddenActions: []string{"self_continue_without_lease"},
			ValidationPlan:   []string{"record approval"},
			Status:           session.ProposalStatusPending,
		},
	}
	msg := core.InboundMessage{ChatID: 7, SenderID: 1001, MessageID: 42, Text: "/mission propose mission-action-ui"}

	handled, err := handleTelegramCommand(context.Background(), sender, router, msg)
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.missionActionProposalID != "mission-action-ui" || router.missionCommandArgs != "" {
		t.Fatalf("mission proposal id = %q mission args = %q, want proposal path only", router.missionActionProposalID, router.missionCommandArgs)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline len = %d, want 1", len(sender.inline))
	}
	text := sender.inline[0].text
	for _, needle := range []string{"ActionProposal", "Implement the generic approval UI.", "Bounded effect:", "do not self-continue", "Forbidden:"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("inline text = %q, want substring %q", text, needle)
		}
	}
	if len(sender.inline[0].rows) != 1 || len(sender.inline[0].rows[0]) != 3 {
		t.Fatalf("rows = %#v, want one Deny/Ask edit/Approve row", sender.inline[0].rows)
	}
	row := sender.inline[0].rows[0]
	if row[0].Text != "Deny" || row[1].Text != "Ask edit" || row[2].Text != "Approve" {
		t.Fatalf("button row = %#v, want Deny / Ask edit / Approve", row)
	}
}

func TestActionProposalApproveCallbackAppliesMissionDecision(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		missionActionProposal: session.ActionProposal{
			ID:            "aprop-mission-action-ui",
			MissionID:     "mission-action-ui",
			Summary:       "Implement the generic approval UI.",
			BoundedEffect: "Mark active only.",
			Status:        session.ProposalStatusPending,
		},
		applyMissionProposalMission: session.MissionState{ID: "mission-action-ui", Title: "Generic ActionProposal approval UI", Status: session.MissionStatusActive},
		applyMissionProposalChanged: true,
	}
	cb := telegram.CallbackQuery{
		ID:      "cb-approve-action-proposal",
		Data:    encodeActionProposalCallbackData("aprop-mission-action-ui", "approve"),
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 7}},
	}

	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, cb)
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.applyMissionProposalID != "mission-action-ui" || router.applyMissionProposalChoice != "approve" || router.applyMissionProposalSender != 1001 {
		t.Fatalf("applied id=%q choice=%q sender=%d, want mission-action-ui approve 1001", router.applyMissionProposalID, router.applyMissionProposalChoice, router.applyMissionProposalSender)
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear len = %d, want approval message edit", len(sender.editClear))
	}
	if !strings.Contains(sender.editClear[0].text, "ActionProposal approved") || !strings.Contains(sender.editClear[0].text, "No self-continuation") {
		t.Fatalf("edit text = %q, want approval and authority boundary", sender.editClear[0].text)
	}
}

func TestContinuationControlsV2DecodeAliases(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"approve", continuationActionApprove},
		{"approve_lease", continuationActionApproveLease},
		{"continue_once", continuationActionContinueOnce},
		{"ask-edit", continuationActionAskEdit},
		{"park", continuationActionStopPark},
		{"resume", continuationActionResumeEdge},
		{"next-lease", continuationActionAskNextLease},
		{"status-only", continuationActionStatusOnly},
	} {
		id, action, ok := decodeContinuationCallbackData(encodeContinuationCallbackData("decision-v2", tc.raw))
		if !ok || id != "decision-v2" || action != tc.want {
			t.Fatalf("decode %q = id=%q action=%q ok=%t, want decision-v2/%q/true", tc.raw, id, action, ok, tc.want)
		}
	}
}

func TestContinuationCallbackCompactsLongIDsAndMatchesState(t *testing.T) {
	t.Parallel()

	longID := "button-backed-materialization-live-test-v1"
	data := encodeContinuationCallbackData(longID, continuationActionAskNextLease)
	if data == "" || len(data) > core.TelegramCallbackDataMaxBytes {
		t.Fatalf("callback data = %q len=%d, want non-empty <= %d", data, len(data), core.TelegramCallbackDataMaxBytes)
	}
	decodedID, action, ok := decodeContinuationCallbackData(data)
	if !ok || action != continuationActionAskNextLease || decodedID == longID {
		t.Fatalf("decode = id=%q action=%q ok=%t, want compact id/%q/true", decodedID, action, ok, continuationActionAskNextLease)
	}
	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     longID,
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{ID: "aprop-" + longID},
		ContinuationLease: session.ContinuationLease{
			ID:         "lease-" + longID,
			ProposalID: "aprop-" + longID,
		},
	}
	if !continuationCallbackMatchesState(state, decodedID, action) {
		t.Fatalf("continuationCallbackMatchesState() = false for compact id %q", decodedID)
	}
}

func TestHandleTelegramCommandCallbackContinuationApproveLease(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	triggerStarted := make(chan struct{})
	router := stubCommandRouter{continuationState: session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-approve-lease",
		RemainingTurns: 1,
		StageSummary:   "Resume the next bounded step.",
	}, triggerContinuationStarted: triggerStarted}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-approve-lease",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-approve-lease", continuationActionApproveLease),
		Message: &telegram.Message{MessageID: 293, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approveContinuationInput != 7 || router.approveContinuationApprover != 1002 {
		t.Fatalf("approve input/approver = %d/%d, want 7/1002", router.approveContinuationInput, router.approveContinuationApprover)
	}
	waitForStubContinuationTrigger(t, triggerStarted)
	if router.triggerContinuationInput != 7 {
		t.Fatalf("triggerContinuationInput = %d, want 7", router.triggerContinuationInput)
	}
	if len(sender.editClear) != 1 || !strings.Contains(sender.editClear[0].text, "Continuation lease approved") {
		t.Fatalf("editClear = %#v, want lease approval confirmation", sender.editClear)
	}
}

func TestHandleTelegramCommandCallbackContinuationDetailsKeepsPendingPlanButtons(t *testing.T) {
	t.Parallel()

	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-plan-details",
		Objective:      "Finish the governed onboarding plan.",
		StageSummary:   "Approve the bounded plan budget.",
		RemainingTurns: 3,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-plan-details",
			RiskClass:      "plan_lease",
			Summary:        "Approve three bounded setup steps.",
			AllowedActions: []string{"approve_operation_plan_lease"},
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-plan-details",
			ProposalID:     "aprop-plan-details",
			Status:         session.ContinuationLeaseStatusPending,
			AllowedActions: []string{"approve_operation_plan_lease"},
		},
	}
	sender := &stubCommandSender{}
	router := stubCommandRouter{continuationState: state}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-plan-details",
		Data:    encodeContinuationCallbackData("aprop-plan-details", continuationActionStatusOnly),
		Message: &telegram.Message{MessageID: 393, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.editClear) != 0 {
		t.Fatalf("editClear = %#v, want no keyboard-clearing edit for details", sender.editClear)
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline = %#v, want details edit with buttons retained", sender.editInline)
	}
	if !strings.Contains(sender.editInline[0].text, "Budget remaining: 3 turn(s)") || !strings.Contains(sender.editInline[0].text, "This details view does not change permissions.") {
		t.Fatalf("details text = %q, want expanded plan details", sender.editInline[0].text)
	}
	var labels []string
	for _, row := range sender.editInline[0].rows {
		for _, button := range row {
			labels = append(labels, button.Text)
		}
	}
	for _, want := range []string{"Start", "Details", "Change", "Pause", "Stop"} {
		if !slices.Contains(labels, want) {
			t.Fatalf("retained labels = %#v, missing %q", labels, want)
		}
	}
}

func TestHandleTelegramCommandCallbackContinuationApproveDoesNotWaitForTrigger(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	triggerStarted := make(chan struct{})
	triggerRelease := make(chan struct{})
	defer close(triggerRelease)
	router := stubCommandRouter{continuationState: session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-blocked-trigger",
		RemainingTurns: 1,
		StageSummary:   "Run a bounded continuation.",
	}, triggerContinuationStarted: triggerStarted, triggerContinuationRelease: triggerRelease}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-blocked-trigger",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-blocked-trigger", continuationActionApproveLease),
		Message: &telegram.Message{MessageID: 294, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.editClear) != 1 || !strings.Contains(sender.editClear[0].text, "Continuation lease approved") {
		t.Fatalf("editClear = %#v, want immediate lease approval confirmation", sender.editClear)
	}
	waitForStubContinuationTrigger(t, triggerStarted)
	if router.triggerContinuationInput != 7 {
		t.Fatalf("triggerContinuationInput = %d, want 7", router.triggerContinuationInput)
	}
}

func TestHandleTelegramCommandCallbackContinuationStatusOnlyDoesNotMutateOrTrigger(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{continuationState: session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-status",
		RemainingTurns: 1,
		Objective:      "Keep the edge visible.",
		StageSummary:   "Report status only.",
		ActionProposal: session.ActionProposal{
			Summary:          "Inspect the proposed scope",
			BoundedEffect:    "Inspect local state and report only.",
			AllowedActions:   []string{"inspect_readonly_state"},
			ForbiddenActions: []string{"edit_files", "deploy"},
			ValidationPlan:   []string{"report evidence"},
		},
	}}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-status-only",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-status", continuationActionStatusOnly),
		Message: &telegram.Message{MessageID: 294, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.approveContinuationInput != 0 || router.triggerContinuationInput != 0 || router.stopContinuationInput != 0 {
		t.Fatalf("router mutated approve/trigger/stop = %d/%d/%d, want 0/0/0", router.approveContinuationInput, router.triggerContinuationInput, router.stopContinuationInput)
	}
	if len(sender.editClear) != 0 {
		t.Fatalf("editClear = %#v, want status-only details to retain buttons", sender.editClear)
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline = %#v, want status-only no-authority text with buttons", sender.editInline)
	}
	if !strings.Contains(sender.editInline[0].text, "Lease scope details") ||
		!strings.Contains(sender.editInline[0].text, "Bounded effect: Inspect local state and report only.") ||
		!strings.Contains(sender.editInline[0].text, "Forbidden actions: edit_files, deploy") ||
		!strings.Contains(sender.editInline[0].text, "No new authority was granted") {
		t.Fatalf("editInline = %#v, want detailed scope no-authority text", sender.editInline)
	}
	var labels []string
	for _, row := range sender.editInline[0].rows {
		for _, button := range row {
			labels = append(labels, button.Text)
		}
	}
	for _, want := range []string{"Start", "Details", "Change", "Pause", "Stop"} {
		if !slices.Contains(labels, want) {
			t.Fatalf("retained labels = %#v, missing %q", labels, want)
		}
	}
}

func TestHandleTelegramCommandCallbackContinuationAskNextLeaseRefreshesProposal(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		continuationState: session.ContinuationState{
			Status:         session.ContinuationStatusIdle,
			DecisionID:     "decision-expired-refresh",
			RemainingTurns: 0,
			ActionProposal: session.ActionProposal{ID: "aprop-expired-refresh", Status: session.ProposalStatusExpired},
			ContinuationLease: session.ContinuationLease{
				ID:         "lease-expired-refresh",
				ProposalID: "aprop-expired-refresh",
				Status:     session.ContinuationLeaseStatusExpired,
			},
		},
		refreshContinuationReturn: session.ContinuationState{
			Status:         session.ContinuationStatusPending,
			DecisionID:     "decision-refreshed",
			RemainingTurns: 1,
			StageSummary:   "Use the fresh approval prompt.",
		},
		refreshContinuationSent: true,
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-next-lease",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("aprop-expired-refresh", continuationActionAskNextLease),
		Message: &telegram.Message{MessageID: 296, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.refreshContinuationInput != 7 || !strings.Contains(router.refreshContinuationReason, "requested") {
		t.Fatalf("refresh input/reason = %d/%q, want requested refresh", router.refreshContinuationInput, router.refreshContinuationReason)
	}
	if router.approveContinuationInput != 0 || router.triggerContinuationInput != 0 || router.stopContinuationInput != 0 {
		t.Fatalf("router approve/trigger/stop = %d/%d/%d, want 0/0/0", router.approveContinuationInput, router.triggerContinuationInput, router.stopContinuationInput)
	}
	if len(sender.editClear) != 1 || !strings.Contains(sender.editClear[0].text, "fresh approval prompt") {
		t.Fatalf("editClear = %#v, want refreshed prompt status", sender.editClear)
	}
}

func TestHandleTelegramCommandCallbackContinuationAskEditParksWithoutTrigger(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		continuationState:      session.ContinuationState{Status: session.ContinuationStatusPending, DecisionID: "decision-edit", RemainingTurns: 1},
		stopContinuationResult: core.StopResult{ContinuationRevoked: true},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:      "cb-ask-edit",
		From:    &telegram.User{ID: 1002, Username: "approved"},
		Data:    encodeContinuationCallbackData("decision-edit", continuationActionAskEdit),
		Message: &telegram.Message{MessageID: 295, Chat: &telegram.Chat{ID: 7, Type: "private"}},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.stopContinuationInput != 7 {
		t.Fatalf("stopContinuationInput = %d, want 7", router.stopContinuationInput)
	}
	if router.triggerContinuationInput != 0 || router.approveContinuationInput != 0 {
		t.Fatalf("trigger/approve = %d/%d, want 0/0", router.triggerContinuationInput, router.approveContinuationInput)
	}
	if len(sender.editClear) != 1 || !strings.Contains(sender.editClear[0].text, "needs edits") {
		t.Fatalf("editClear = %#v, want ask-edit confirmation", sender.editClear)
	}
}
