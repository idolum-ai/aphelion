//go:build linux

package main

import (
	"context"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"strings"
	"time"
)

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

func (s *stubCommandRouter) MissionHome(ctx context.Context, chatID int64, senderID int64) ([]session.MissionState, session.WorkingObjective, bool, error) {
	_ = ctx
	s.missionHomeChatID = chatID
	s.missionHomeSenderID = senderID
	if s.missionHomeErr != nil {
		return nil, session.WorkingObjective{}, false, s.missionHomeErr
	}
	isAdmin := s.missionHomeIsAdmin || s.canRestart
	return append([]session.MissionState(nil), s.missionHomeMissions...), s.missionHomeWorking, isAdmin, nil
}

func (s *stubCommandRouter) MissionDetails(ctx context.Context, chatID int64, senderID int64, missionID string) (session.MissionState, []session.MissionEvent, error) {
	_ = ctx
	s.missionDetailsChatID = chatID
	s.missionDetailsSenderID = senderID
	s.missionDetailsID = missionID
	if s.missionDetailsErr != nil {
		return session.MissionState{}, nil, s.missionDetailsErr
	}
	if strings.TrimSpace(s.missionDetailsMission.ID) != "" {
		return s.missionDetailsMission, append([]session.MissionEvent(nil), s.missionDetailsEvents...), nil
	}
	return stubMissionState(missionID, session.MissionStatusCandidate), nil, nil
}

func (s *stubCommandRouter) SetMissionPinned(ctx context.Context, chatID int64, senderID int64, missionID string, pinned bool) (session.MissionState, error) {
	_ = ctx
	s.setMissionPinnedChatID = chatID
	s.setMissionPinnedSenderID = senderID
	s.setMissionPinnedID = missionID
	s.setMissionPinnedValue = pinned
	if s.setMissionPinnedErr != nil {
		return session.MissionState{}, s.setMissionPinnedErr
	}
	if strings.TrimSpace(s.setMissionPinnedMission.ID) != "" {
		return s.setMissionPinnedMission, nil
	}
	mission := stubMissionState(missionID, session.MissionStatusCandidate)
	mission.Pinned = pinned
	return mission, nil
}

func (s *stubCommandRouter) UpdateMissionStatus(ctx context.Context, chatID int64, senderID int64, missionID string, status session.MissionStatus) (session.MissionState, error) {
	_ = ctx
	s.updateMissionStatusChatID = chatID
	s.updateMissionStatusSenderID = senderID
	s.updateMissionStatusID = missionID
	s.updateMissionStatusValue = status
	if s.updateMissionStatusErr != nil {
		return session.MissionState{}, s.updateMissionStatusErr
	}
	if strings.TrimSpace(s.updateMissionStatusMission.ID) != "" {
		return s.updateMissionStatusMission, nil
	}
	return stubMissionState(missionID, status), nil
}

func (s *stubCommandRouter) MissionLedgerHealth(ctx context.Context, senderID int64) (session.MissionLedgerHealth, error) {
	_ = ctx
	s.missionLedgerHealthSenderID = senderID
	if s.missionLedgerHealthErr != nil {
		return session.MissionLedgerHealth{}, s.missionLedgerHealthErr
	}
	return s.missionLedgerHealth, nil
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

func stubMissionState(id string, status session.MissionStatus) session.MissionState {
	id = strings.TrimSpace(id)
	if id == "" {
		id = "mission-test"
	}
	status = session.NormalizeMissionStatus(status)
	if status == "" {
		status = session.MissionStatusCandidate
	}
	return session.MissionState{
		ID:        id,
		Title:     "Mission",
		Objective: "Test mission",
		Status:    status,
		Owner:     "telegram:1001",
		Authority: session.DefaultMissionAuthority(),
	}
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

func (s *stubCommandRouter) MemoryReviewSnapshotForMessage(ctx context.Context, msg core.InboundMessage, source memoryReviewSource) (memoryReviewSnapshot, error) {
	s.memoryReviewMessage = msg
	return s.MemoryReviewSnapshot(ctx, msg.ChatID, msg.SenderID, source)
}

func (s *stubCommandRouter) MemoryFocus(chatID int64) (core.MemoryFocus, bool) {
	if s.memoryFocusByChat == nil {
		return core.MemoryFocus{}, false
	}
	focus, ok := s.memoryFocusByChat[chatID]
	return focus, ok
}

func (s *stubCommandRouter) MemoryFocusForMessage(msg core.InboundMessage) (core.MemoryFocus, bool) {
	if s.memoryFocusByThread == nil || msg.TelegramThreadID <= 0 {
		return core.MemoryFocus{}, false
	}
	focus, ok := s.memoryFocusByThread[msg.TelegramThreadID]
	return focus, ok
}

func (s *stubCommandRouter) SetMemoryFocus(chatID int64, focus core.MemoryFocus) {
	if s.memoryFocusByChat == nil {
		s.memoryFocusByChat = make(map[int64]core.MemoryFocus)
	}
	s.memoryFocusByChat[chatID] = focus
}

func (s *stubCommandRouter) SetMemoryFocusForMessage(msg core.InboundMessage, focus core.MemoryFocus) {
	s.setMemoryFocusMessage = msg
	if msg.TelegramThreadID <= 0 {
		s.SetMemoryFocus(msg.ChatID, focus)
		return
	}
	if s.memoryFocusByThread == nil {
		s.memoryFocusByThread = make(map[int64]core.MemoryFocus)
	}
	s.memoryFocusByThread[msg.TelegramThreadID] = focus
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

func (s *stubCommandRouter) ClearMemoryFocusForMessage(msg core.InboundMessage) bool {
	s.clearMemoryFocusMessage = msg
	if msg.TelegramThreadID <= 0 {
		return s.ClearMemoryFocus(msg.ChatID)
	}
	if s.memoryFocusByThread != nil {
		if _, ok := s.memoryFocusByThread[msg.TelegramThreadID]; ok {
			delete(s.memoryFocusByThread, msg.TelegramThreadID)
			return true
		}
	}
	return s.clearMemoryFocusResult
}
