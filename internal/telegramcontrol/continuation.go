//go:build linux

package telegramcontrol

import (
	"context"
	"fmt"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
)

func (c CommandControl) ContinuationState(chatID int64) (session.ContinuationState, error) {
	if c.Runtime == nil {
		return session.ContinuationState{}, nil
	}
	return c.Runtime.ContinuationState(chatID)
}

func (c CommandControl) ContinuationStateForMessage(msg core.InboundMessage) (session.ContinuationState, error) {
	if c.Runtime == nil {
		return session.ContinuationState{}, nil
	}
	return c.Runtime.ContinuationStateForKey(SessionKeyForMessage(msg))
}

func (c CommandControl) ApproveContinuation(chatID int64, approverID int64) (session.ContinuationState, error) {
	return c.Runtime.ApproveContinuation(chatID, approverID)
}

func (c CommandControl) ApproveContinuationBundle(chatID int64, approverID int64, phaseIDs []string) (session.ContinuationState, error) {
	return c.Runtime.ApproveContinuationBundle(chatID, approverID, phaseIDs)
}

func (c CommandControl) ApproveContinuationForMessage(msg core.InboundMessage, approverID int64) (session.ContinuationState, error) {
	return c.Runtime.ApproveContinuationForKey(SessionKeyForMessage(msg), approverID)
}

func (c CommandControl) ApproveContinuationBundleForMessage(msg core.InboundMessage, approverID int64, phaseIDs []string) (session.ContinuationState, error) {
	return c.Runtime.ApproveContinuationBundleForKey(SessionKeyForMessage(msg), approverID, phaseIDs)
}

func (c CommandControl) StopContinuation(chatID int64) (core.StopResult, error) {
	if c.RevokeContinuation == nil {
		return core.StopResult{}, nil
	}
	return c.RevokeContinuation(chatID)
}

func (c CommandControl) StopContinuationForMessage(msg core.InboundMessage) (core.StopResult, error) {
	if c.RevokeContinuationForMessage == nil {
		return core.StopResult{}, nil
	}
	return c.RevokeContinuationForMessage(msg)
}

func (c CommandControl) TriggerContinuation(ctx context.Context, chatID int64) error {
	if c.Runtime == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.Runtime.TriggerContinuation(ctx, chatID)
}

func (c CommandControl) TriggerContinuationForMessage(ctx context.Context, msg core.InboundMessage) error {
	if c.Runtime == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.Runtime.TriggerContinuationForKey(ctx, SessionKeyForMessage(msg))
}

func (c CommandControl) RecordTelegramCallbackError(chatID int64, callbackKind string, err error) {
	if c.Runtime == nil || err == nil {
		return
	}
	c.Runtime.RecordTelegramCallbackError(chatID, callbackKind, err)
}

func (c CommandControl) RetireTelegramCallbackMessage(chatID int64, messageID int64, surface string) error {
	if c.Store == nil {
		return nil
	}
	return c.Store.MarkTelegramCallbackMessageSurface(chatID, messageID, surface, time.Now().UTC())
}

func (c CommandControl) ToggleProgressView(ctx context.Context, chatID int64, senderID int64, runID int64, details bool) (bool, string, error) {
	if c.Runtime == nil {
		return false, "", nil
	}
	return c.Runtime.ToggleProgressView(ctx, chatID, senderID, runID, details)
}

func (c CommandControl) ConfigureAutoApproval(ctx context.Context, chatID int64, senderID int64, args string) (string, error) {
	if c.Runtime == nil {
		return "Auto approvals are unavailable.", nil
	}
	return c.Runtime.ConfigureAutoApproval(ctx, chatID, senderID, args)
}

func (c CommandControl) ConfigureAutoApprovalForMessage(ctx context.Context, msg core.InboundMessage, args string) (string, error) {
	if c.Runtime == nil {
		return "Auto approvals are unavailable.", nil
	}
	return c.Runtime.ConfigureAutoApprovalForKey(ctx, SessionKeyForMessage(msg), msg.SenderID, args)
}

func (c CommandControl) AutoApprovalStatus(ctx context.Context, chatID int64, senderID int64) (string, error) {
	if c.Runtime == nil {
		return "Auto approvals are unavailable.", nil
	}
	return c.Runtime.AutoApprovalStatus(ctx, chatID, senderID)
}

func (c CommandControl) AutoApprovalStatusForMessage(ctx context.Context, msg core.InboundMessage) (string, error) {
	if c.Runtime == nil {
		return "Auto approvals are unavailable.", nil
	}
	return c.Runtime.AutoApprovalStatusForKey(ctx, SessionKeyForMessage(msg), msg.SenderID)
}

func (c CommandControl) CreateApprovalWindowOfferForMessage(ctx context.Context, msg core.InboundMessage, sourceKind string, sourceID string, sourceDecisionKind string) (session.ApprovalWindowOffer, bool, error) {
	if c.Runtime == nil {
		return session.ApprovalWindowOffer{}, false, nil
	}
	return c.Runtime.CreateApprovalWindowOfferForKey(ctx, SessionKeyForMessage(msg), msg.SenderID, sourceKind, sourceID, sourceDecisionKind)
}

func (c CommandControl) SuppressPostApprovalDefaultWindowOfferForMessage(ctx context.Context, msg core.InboundMessage, sourceKind string, sourceID string, sourceDecisionKind string) (bool, error) {
	if c.Runtime == nil {
		return false, nil
	}
	return c.Runtime.SuppressPostApprovalDefaultWindowOfferForKey(ctx, SessionKeyForMessage(msg), msg.SenderID, sourceKind, sourceID, sourceDecisionKind)
}

func (c CommandControl) EnableApprovalWindowForMessage(ctx context.Context, msg core.InboundMessage, duration time.Duration) (string, error) {
	result, err := c.EnableApprovalWindowForMessageResult(ctx, msg, duration)
	return result.Text, err
}

func (c CommandControl) EnableApprovalWindowForMessageResult(ctx context.Context, msg core.InboundMessage, duration time.Duration) (core.ApprovalWindowEnableResult, error) {
	if c.Runtime == nil {
		return core.ApprovalWindowEnableResult{Text: "Approval windows are unavailable."}, nil
	}
	return c.Runtime.EnableApprovalWindowForKeyResult(ctx, SessionKeyForMessage(msg), msg.SenderID, duration)
}

func (c CommandControl) DefaultApprovalWindowDuration() time.Duration {
	if c.Runtime == nil {
		return 15 * time.Minute
	}
	return c.Runtime.DefaultApprovalWindowDuration()
}

func (c CommandControl) DoubleApprovalWindowForMessage(ctx context.Context, msg core.InboundMessage) (string, error) {
	if c.Runtime == nil {
		return "Approval windows are unavailable.", nil
	}
	return c.Runtime.DoubleApprovalWindowForKey(ctx, SessionKeyForMessage(msg), msg.SenderID)
}

func (c CommandControl) CancelApprovalWindowForMessage(ctx context.Context, msg core.InboundMessage) (string, error) {
	result, err := c.CancelApprovalWindowForMessageResult(ctx, msg)
	return result.Text, err
}

func (c CommandControl) CancelApprovalWindowForMessageResult(ctx context.Context, msg core.InboundMessage) (core.ApprovalWindowCancelResult, error) {
	if c.Runtime == nil {
		return core.ApprovalWindowCancelResult{Text: "Approval windows are unavailable."}, nil
	}
	return c.Runtime.CancelApprovalWindowForKeyResult(ctx, SessionKeyForMessage(msg), msg.SenderID)
}

func (c CommandControl) EnableApprovalWindowOffer(ctx context.Context, offerID string, senderID int64, duration time.Duration) (string, error) {
	result, err := c.EnableApprovalWindowOfferResult(ctx, offerID, senderID, duration)
	return result.Text, err
}

func (c CommandControl) EnableApprovalWindowOfferResult(ctx context.Context, offerID string, senderID int64, duration time.Duration) (core.ApprovalWindowEnableResult, error) {
	if c.Runtime == nil {
		return core.ApprovalWindowEnableResult{Text: "Approval windows are unavailable."}, nil
	}
	return c.Runtime.EnableApprovalWindowOfferResult(ctx, offerID, senderID, duration)
}

func (c CommandControl) DoubleApprovalWindowOffer(ctx context.Context, offerID string, senderID int64) (string, error) {
	if c.Runtime == nil {
		return "Approval windows are unavailable.", nil
	}
	return c.Runtime.DoubleApprovalWindowOffer(ctx, offerID, senderID)
}

func (c CommandControl) CancelApprovalWindowOffer(ctx context.Context, offerID string, senderID int64) (string, error) {
	result, err := c.CancelApprovalWindowOfferResult(ctx, offerID, senderID)
	return result.Text, err
}

func (c CommandControl) CancelApprovalWindowOfferResult(ctx context.Context, offerID string, senderID int64) (core.ApprovalWindowCancelResult, error) {
	if c.Runtime == nil {
		return core.ApprovalWindowCancelResult{Text: "Approval windows are unavailable."}, nil
	}
	return c.Runtime.CancelApprovalWindowOfferResult(ctx, offerID, senderID)
}

func (c CommandControl) CloseApprovalWindowOffer(ctx context.Context, offerID string, senderID int64) error {
	if c.Runtime == nil {
		return nil
	}
	return c.Runtime.CloseApprovalWindowOffer(ctx, offerID, senderID)
}

func (c CommandControl) ApprovalWindowOfferByID(offerID string) (session.ApprovalWindowOffer, bool, error) {
	if c.Runtime == nil {
		return session.ApprovalWindowOffer{}, false, nil
	}
	return c.Runtime.ApprovalWindowOfferByID(offerID)
}

type decisionCallbackResolver interface {
	ResolveCallbackDetailed(id string, choice string, actor decision.CallbackActor) decision.ResolveResult
}

type decisionCallbackPeeker interface {
	PeekCallback(id string, actor decision.CallbackActor) (decision.PendingDecision, bool)
}

func (c CommandControl) PeekDecisionCallback(decisionID string, actor decision.CallbackActor) (decision.PendingDecision, bool) {
	peeker, ok := c.DecisionDetacher.(decisionCallbackPeeker)
	if !ok || peeker == nil {
		return decision.PendingDecision{}, false
	}
	return peeker.PeekCallback(decisionID, actor)
}

func (c CommandControl) ResolveDecisionCallback(decisionID string, choice string, actor decision.CallbackActor) decision.ResolveResult {
	resolver, ok := c.DecisionDetacher.(decisionCallbackResolver)
	if !ok || resolver == nil {
		return decision.ResolveResult{}
	}
	return resolver.ResolveCallbackDetailed(decisionID, choice, actor)
}

func (c CommandControl) RefreshContinuationProposal(ctx context.Context, chatID int64, reason string) (session.ContinuationState, bool, error) {
	if c.Runtime == nil {
		return session.ContinuationState{}, false, fmt.Errorf("runtime is unavailable")
	}
	return c.Runtime.RefreshContinuationProposal(ctx, chatID, reason)
}

func (c CommandControl) RefreshContinuationProposalForMessage(ctx context.Context, msg core.InboundMessage, reason string) (session.ContinuationState, bool, error) {
	if c.Runtime == nil {
		return session.ContinuationState{}, false, fmt.Errorf("runtime is unavailable")
	}
	return c.Runtime.RefreshContinuationProposalForKey(ctx, SessionKeyForMessage(msg), reason)
}
