//go:build linux

package main

import (
	"context"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"strings"
	"testing"
	"time"
)

func (s *stubCommandRouter) ContinuationState(chatID int64) (session.ContinuationState, error) {
	s.continuationStateInput = chatID
	if s.continuationStateErr != nil {
		return session.ContinuationState{}, s.continuationStateErr
	}
	return s.continuationState, nil
}

func (s *stubCommandRouter) ContinuationStateForMessage(msg core.InboundMessage) (session.ContinuationState, error) {
	s.continuationStateMessage = msg
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

func (s *stubCommandRouter) ApproveContinuationForMessage(msg core.InboundMessage, approverID int64) (session.ContinuationState, error) {
	s.approveContinuationMessage = msg
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

func (s *stubCommandRouter) ApproveContinuationBundle(chatID int64, approverID int64, phaseIDs []string) (session.ContinuationState, error) {
	s.approveContinuationInput = chatID
	s.approveContinuationApprover = approverID
	s.approveContinuationPhaseIDs = append([]string(nil), phaseIDs...)
	if s.approveContinuationErr != nil {
		if s.approveContinuationReturn.Status != "" {
			return s.approveContinuationReturn, s.approveContinuationErr
		}
		return s.continuationState, s.approveContinuationErr
	}
	if s.approveContinuationReturn.Status != "" {
		return s.approveContinuationReturn, nil
	}
	return s.ApproveContinuation(chatID, approverID)
}

func (s *stubCommandRouter) ApproveContinuationBundleForMessage(msg core.InboundMessage, approverID int64, phaseIDs []string) (session.ContinuationState, error) {
	s.approveContinuationMessage = msg
	s.approveContinuationApprover = approverID
	s.approveContinuationPhaseIDs = append([]string(nil), phaseIDs...)
	if s.approveContinuationErr != nil {
		if s.approveContinuationReturn.Status != "" {
			return s.approveContinuationReturn, s.approveContinuationErr
		}
		return s.continuationState, s.approveContinuationErr
	}
	if s.approveContinuationReturn.Status != "" {
		return s.approveContinuationReturn, nil
	}
	return s.ApproveContinuationForMessage(msg, approverID)
}

func (s *stubCommandRouter) StopContinuation(chatID int64) (core.StopResult, error) {
	s.stopContinuationInput = chatID
	if s.stopContinuationErr != nil {
		return core.StopResult{}, s.stopContinuationErr
	}
	return s.stopContinuationResult, nil
}

func (s *stubCommandRouter) StopContinuationForMessage(msg core.InboundMessage) (core.StopResult, error) {
	s.stopContinuationMessage = msg
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

func (s *stubCommandRouter) TriggerContinuationForMessage(ctx context.Context, msg core.InboundMessage) error {
	s.triggerContinuationMessage = msg
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

func (s *stubCommandRouter) ConfigureAutoApprovalForMessage(_ context.Context, msg core.InboundMessage, args string) (string, error) {
	copied := msg
	s.autoApproveMessage = &copied
	s.autoApproveChatID = msg.ChatID
	s.autoApproveSenderID = msg.SenderID
	s.autoApproveArgs = args
	if s.autoApproveErr != nil {
		return "", s.autoApproveErr
	}
	if strings.TrimSpace(s.autoApproveReturn) != "" {
		return s.autoApproveReturn, nil
	}
	return "Auto approvals enabled for this thread.", nil
}

func (s *stubCommandRouter) AutoApprovalStatusForMessage(_ context.Context, msg core.InboundMessage) (string, error) {
	copied := msg
	s.autoApproveStatusMessage = &copied
	s.autoApproveStatusChatID = msg.ChatID
	s.autoApproveStatusSenderID = msg.SenderID
	if s.autoApproveStatusErr != nil {
		return "", s.autoApproveStatusErr
	}
	if strings.TrimSpace(s.autoApproveStatusReturn) != "" {
		return s.autoApproveStatusReturn, nil
	}
	return "Auto approvals are inactive for this thread.", nil
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

func (s *stubCommandRouter) ConfigureAutonomyForMessage(_ context.Context, msg core.InboundMessage, args string) (string, error) {
	copied := msg
	s.autonomyMessage = &copied
	s.autonomyChatID = msg.ChatID
	s.autonomySenderID = msg.SenderID
	s.autonomyArgs = args
	if s.autonomyErr != nil {
		return "", s.autonomyErr
	}
	if strings.TrimSpace(s.autonomyReturn) != "" {
		return s.autonomyReturn, nil
	}
	return "Autonomy override enabled for this thread.", nil
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

func (s *stubCommandRouter) RefreshContinuationProposalForMessage(ctx context.Context, msg core.InboundMessage, reason string) (session.ContinuationState, bool, error) {
	s.refreshContinuationMessage = msg
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
