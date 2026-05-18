//go:build linux

package main

import (
	"context"
	"fmt"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func (c telegramCommandControl) ContinuationState(chatID int64) (session.ContinuationState, error) {
	if c.rt == nil {
		return session.ContinuationState{}, nil
	}
	return c.rt.ContinuationState(chatID)
}

func (c telegramCommandControl) ContinuationStateForMessage(msg core.InboundMessage) (session.ContinuationState, error) {
	if c.rt == nil {
		return session.ContinuationState{}, nil
	}
	return c.rt.ContinuationStateForKey(session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramCommandMessageScope(msg)})
}

func (c telegramCommandControl) ApproveContinuation(chatID int64, approverID int64) (session.ContinuationState, error) {
	return c.rt.ApproveContinuation(chatID, approverID)
}

func (c telegramCommandControl) ApproveContinuationForMessage(msg core.InboundMessage, approverID int64) (session.ContinuationState, error) {
	return c.rt.ApproveContinuationForKey(session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramCommandMessageScope(msg)}, approverID)
}

func (c telegramCommandControl) StopContinuation(chatID int64) (core.StopResult, error) {
	revoke, err := c.rt.RevokeContinuation(chatID)
	if err != nil {
		return core.StopResult{}, err
	}
	return core.StopResult{ContinuationRevoked: revoke.Revoked, ContinuationLabel: revoke.ContinuationLabel}, nil
}

func (c telegramCommandControl) StopContinuationForMessage(msg core.InboundMessage) (core.StopResult, error) {
	revoke, err := c.rt.RevokeContinuationForKey(session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramCommandMessageScope(msg)})
	if err != nil {
		return core.StopResult{}, err
	}
	return core.StopResult{ContinuationRevoked: revoke.Revoked, ContinuationLabel: revoke.ContinuationLabel}, nil
}

func (c telegramCommandControl) TriggerContinuation(ctx context.Context, chatID int64) error {
	if c.rt == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.rt.TriggerContinuation(ctx, chatID)
}

func (c telegramCommandControl) TriggerContinuationForMessage(ctx context.Context, msg core.InboundMessage) error {
	if c.rt == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.rt.TriggerContinuationForKey(ctx, session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramCommandMessageScope(msg)})
}

func (c telegramCommandControl) RecordTelegramCallbackError(chatID int64, callbackKind string, err error) {
	if c.rt == nil || err == nil {
		return
	}
	c.rt.RecordTelegramCallbackError(chatID, callbackKind, err)
}

func (c telegramCommandControl) ToggleProgressView(ctx context.Context, chatID int64, senderID int64, runID int64, details bool) (bool, string, error) {
	if c.rt == nil {
		return false, "", nil
	}
	return c.rt.ToggleProgressView(ctx, chatID, senderID, runID, details)
}

func (c telegramCommandControl) ConfigureAutoApproval(ctx context.Context, chatID int64, senderID int64, args string) (string, error) {
	if c.rt == nil {
		return "Auto approvals are unavailable.", nil
	}
	return c.rt.ConfigureAutoApproval(ctx, chatID, senderID, args)
}

func (c telegramCommandControl) ConfigureAutoApprovalForMessage(ctx context.Context, msg core.InboundMessage, args string) (string, error) {
	if c.rt == nil {
		return "Auto approvals are unavailable.", nil
	}
	return c.rt.ConfigureAutoApprovalForKey(ctx, session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramCommandMessageScope(msg)}, msg.SenderID, args)
}

func (c telegramCommandControl) AutoApprovalStatus(ctx context.Context, chatID int64, senderID int64) (string, error) {
	if c.rt == nil {
		return "Auto approvals are unavailable.", nil
	}
	return c.rt.AutoApprovalStatus(ctx, chatID, senderID)
}

func (c telegramCommandControl) AutoApprovalStatusForMessage(ctx context.Context, msg core.InboundMessage) (string, error) {
	if c.rt == nil {
		return "Auto approvals are unavailable.", nil
	}
	return c.rt.AutoApprovalStatusForKey(ctx, session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramCommandMessageScope(msg)}, msg.SenderID)
}

func (c telegramCommandControl) RefreshContinuationProposal(ctx context.Context, chatID int64, reason string) (session.ContinuationState, bool, error) {
	if c.rt == nil {
		return session.ContinuationState{}, false, fmt.Errorf("runtime is unavailable")
	}
	return c.rt.RefreshContinuationProposal(ctx, chatID, reason)
}

func (c telegramCommandControl) RefreshContinuationProposalForMessage(ctx context.Context, msg core.InboundMessage, reason string) (session.ContinuationState, bool, error) {
	if c.rt == nil {
		return session.ContinuationState{}, false, fmt.Errorf("runtime is unavailable")
	}
	return c.rt.RefreshContinuationProposalForKey(ctx, session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramCommandMessageScope(msg)}, reason)
}
