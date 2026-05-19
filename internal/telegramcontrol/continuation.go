//go:build linux

package telegramcontrol

import (
	"context"
	"fmt"
	"github.com/idolum-ai/aphelion/core"
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

func (c CommandControl) ApproveContinuationForMessage(msg core.InboundMessage, approverID int64) (session.ContinuationState, error) {
	return c.Runtime.ApproveContinuationForKey(SessionKeyForMessage(msg), approverID)
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
