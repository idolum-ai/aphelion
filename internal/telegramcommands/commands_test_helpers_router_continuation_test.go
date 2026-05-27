//go:build linux

package telegramcommands

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
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

func (s *stubCommandRouter) CreateApprovalWindowOfferForMessage(_ context.Context, msg core.InboundMessage, sourceKind string, sourceID string, sourceDecisionKind string) (session.ApprovalWindowOffer, bool, error) {
	copied := msg
	s.approvalWindowMessage = &copied
	s.approvalWindowOfferSource = sourceKind + ":" + sourceID + ":" + sourceDecisionKind
	if s.approvalWindowErr != nil {
		return session.ApprovalWindowOffer{}, false, s.approvalWindowErr
	}
	offerID := strings.TrimSpace(s.approvalWindowOfferID)
	if offerID == "" {
		offerID = "offer-test"
	}
	return session.ApprovalWindowOffer{ID: offerID, ChatID: msg.ChatID, SourceKind: sourceKind, SourceID: sourceID, SourceDecisionKind: sourceDecisionKind}, true, nil
}

func (s *stubCommandRouter) EnableApprovalWindowForMessage(_ context.Context, msg core.InboundMessage, duration time.Duration) (string, error) {
	result, err := s.EnableApprovalWindowForMessageResult(context.Background(), msg, duration)
	return result.Text, err
}

func (s *stubCommandRouter) EnableApprovalWindowForMessageResult(_ context.Context, msg core.InboundMessage, duration time.Duration) (core.ApprovalWindowEnableResult, error) {
	copied := msg
	s.approvalWindowAction = approvalWindowActionEnable15
	s.approvalWindowMessage = &copied
	s.approvalWindowDuration = duration
	if s.approvalWindowErr != nil {
		return core.ApprovalWindowEnableResult{}, s.approvalWindowErr
	}
	if strings.TrimSpace(s.approvalWindowReturn) != "" {
		return core.ApprovalWindowEnableResult{Text: s.approvalWindowReturn, Active: s.approvalWindowActive}, nil
	}
	return core.ApprovalWindowEnableResult{Text: "Approval window active.", Active: true}, nil
}

func (s *stubCommandRouter) DoubleApprovalWindowForMessage(_ context.Context, msg core.InboundMessage) (string, error) {
	copied := msg
	s.approvalWindowAction = approvalWindowActionDouble
	s.approvalWindowMessage = &copied
	if s.approvalWindowErr != nil {
		return "", s.approvalWindowErr
	}
	if strings.TrimSpace(s.approvalWindowReturn) != "" {
		return s.approvalWindowReturn, nil
	}
	return "Approval window extended.", nil
}

func (s *stubCommandRouter) CancelApprovalWindowForMessage(_ context.Context, msg core.InboundMessage) (string, error) {
	result, err := s.CancelApprovalWindowForMessageResult(context.Background(), msg)
	return result.Text, err
}

func (s *stubCommandRouter) CancelApprovalWindowForMessageResult(_ context.Context, msg core.InboundMessage) (core.ApprovalWindowCancelResult, error) {
	copied := msg
	s.approvalWindowAction = approvalWindowActionCancel
	s.approvalWindowMessage = &copied
	if s.approvalWindowErr != nil {
		return core.ApprovalWindowCancelResult{}, s.approvalWindowErr
	}
	if strings.TrimSpace(s.approvalWindowReturn) != "" {
		return core.ApprovalWindowCancelResult{Text: s.approvalWindowReturn, Canceled: s.approvalWindowCanceled}, nil
	}
	return core.ApprovalWindowCancelResult{Text: "Approval window canceled.", Canceled: true}, nil
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

func (s *stubCommandRouter) EnableApprovalWindowOffer(_ context.Context, offerID string, senderID int64, duration time.Duration) (string, error) {
	result, err := s.EnableApprovalWindowOfferResult(context.Background(), offerID, senderID, duration)
	return result.Text, err
}

func (s *stubCommandRouter) EnableApprovalWindowOfferResult(_ context.Context, offerID string, senderID int64, duration time.Duration) (core.ApprovalWindowEnableResult, error) {
	s.approvalWindowAction = approvalWindowActionEnable15
	s.approvalWindowOfferID = offerID
	s.approvalWindowDuration = duration
	if s.approvalWindowErr != nil {
		return core.ApprovalWindowEnableResult{}, s.approvalWindowErr
	}
	if strings.TrimSpace(s.approvalWindowReturn) != "" {
		return core.ApprovalWindowEnableResult{Text: s.approvalWindowReturn, Active: s.approvalWindowActive}, nil
	}
	return core.ApprovalWindowEnableResult{Text: "Approval window active.", Active: true}, nil
}

func (s *stubCommandRouter) DoubleApprovalWindowOffer(_ context.Context, offerID string, senderID int64) (string, error) {
	s.approvalWindowAction = approvalWindowActionDouble
	s.approvalWindowOfferID = offerID
	if s.approvalWindowErr != nil {
		return "", s.approvalWindowErr
	}
	if strings.TrimSpace(s.approvalWindowReturn) != "" {
		return s.approvalWindowReturn, nil
	}
	return "Approval window extended.", nil
}

func (s *stubCommandRouter) CancelApprovalWindowOffer(_ context.Context, offerID string, senderID int64) (string, error) {
	result, err := s.CancelApprovalWindowOfferResult(context.Background(), offerID, senderID)
	return result.Text, err
}

func (s *stubCommandRouter) CancelApprovalWindowOfferResult(_ context.Context, offerID string, senderID int64) (core.ApprovalWindowCancelResult, error) {
	s.approvalWindowAction = approvalWindowActionCancel
	s.approvalWindowCancelCalls++
	s.approvalWindowOfferID = offerID
	if s.approvalWindowErr != nil {
		return core.ApprovalWindowCancelResult{}, s.approvalWindowErr
	}
	if strings.TrimSpace(s.approvalWindowReturn) != "" {
		return core.ApprovalWindowCancelResult{Text: s.approvalWindowReturn, Canceled: s.approvalWindowCanceled}, nil
	}
	return core.ApprovalWindowCancelResult{Text: "Approval window canceled.", Canceled: true}, nil
}

func (s *stubCommandRouter) CloseApprovalWindowOffer(_ context.Context, offerID string, senderID int64) error {
	s.approvalWindowAction = approvalWindowActionClose
	s.approvalWindowOfferID = offerID
	s.approvalWindowSenderID = senderID
	return s.approvalWindowErr
}

func (s *stubCommandRouter) ApprovalWindowOfferByID(offerID string) (session.ApprovalWindowOffer, bool, error) {
	s.approvalWindowOfferID = offerID
	if s.approvalWindowLookupErr != nil {
		return session.ApprovalWindowOffer{}, false, s.approvalWindowLookupErr
	}
	if s.approvalWindowLookupOK {
		return s.approvalWindowLookupOffer, true, nil
	}
	return session.ApprovalWindowOffer{}, false, nil
}

func (s *stubCommandRouter) PeekDecisionCallback(decisionID string, actor decision.CallbackActor) (decision.PendingDecision, bool) {
	s.resolvedDecisionID = decisionID
	s.resolvedDecisionActor = actor.TelegramUserID
	if s.resolvedDecisionPeekOK {
		return decision.PendingDecision{ID: decisionID, Request: decision.Request{ChatID: actor.ChatID, SenderID: actor.TelegramUserID}, Delivery: decision.Delivery{MessageID: actor.MessageID}}, true
	}
	if !s.resolvedDecisionOK {
		return decision.PendingDecision{}, false
	}
	return decision.PendingDecision{ID: decisionID, Request: decision.Request{ChatID: actor.ChatID, SenderID: actor.TelegramUserID}, Delivery: decision.Delivery{MessageID: actor.MessageID}}, true
}

func (s *stubCommandRouter) ResolveDecisionCallback(decisionID string, choice string, actor decision.CallbackActor) decision.ResolveResult {
	if !s.approvalWindowReturnBeforeResolve && !s.approvalWindowActive {
		return decision.ResolveResult{}
	}
	s.resolvedDecisionID = decisionID
	s.resolvedDecisionChoice = choice
	s.resolvedDecisionActor = actor.TelegramUserID
	if !s.resolvedDecisionOK {
		return decision.ResolveResult{}
	}
	return decision.ResolveResult{Resolved: true, Choice: choice}
}
