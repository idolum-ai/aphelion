//go:build linux

package main

import (
	"context"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

type approvalWindowFacadeRouter interface {
	CreateApprovalWindowOfferForMessage(ctx context.Context, msg core.InboundMessage, sourceKind string, sourceID string, sourceDecisionKind string) (session.ApprovalWindowOffer, bool, error)
	EnableApprovalWindowForMessage(ctx context.Context, msg core.InboundMessage, duration time.Duration) (string, error)
	DoubleApprovalWindowForMessage(ctx context.Context, msg core.InboundMessage) (string, error)
	CancelApprovalWindowForMessage(ctx context.Context, msg core.InboundMessage) (string, error)
	EnableApprovalWindowOffer(ctx context.Context, offerID string, senderID int64, duration time.Duration) (string, error)
	DoubleApprovalWindowOffer(ctx context.Context, offerID string, senderID int64) (string, error)
	CancelApprovalWindowOffer(ctx context.Context, offerID string, senderID int64) (string, error)
	CloseApprovalWindowOffer(ctx context.Context, offerID string, senderID int64) error
}

var _ approvalWindowFacadeRouter = telegramCommandControl{}

func TestTelegramCommandControlSatisfiesApprovalWindowRouterContract(t *testing.T) {
	t.Parallel()

	var router any = telegramCommandControl{}
	if _, ok := router.(approvalWindowFacadeRouter); !ok {
		t.Fatal("telegramCommandControl must satisfy the approval-window callback router contract")
	}
}
