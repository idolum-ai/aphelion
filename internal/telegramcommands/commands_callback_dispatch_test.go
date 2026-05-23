//go:build linux

package telegramcommands

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestHandleTelegramCommandCallbackDispatchesSplitCallbackFamilies(t *testing.T) {
	t.Parallel()

	t.Run("tailnet refresh", func(t *testing.T) {
		sender := &stubCommandSender{}
		router := &stubCommandRouter{
			canRestart:    true,
			tailnetStatus: core.TailnetStatusSnapshot{Status: "running"},
		}
		handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
			ID:   "cb-tailnet-refresh-dispatch",
			From: &telegram.User{ID: 1001},
			Data: encodeTailnetCallbackData(tailnetCallbackRefresh),
			Message: &telegram.Message{
				MessageID: 97,
				Chat:      &telegram.Chat{ID: 7, Type: "private"},
			},
		})
		if err != nil {
			t.Fatalf("handleTelegramCommandCallback() err = %v", err)
		}
		if !handled || router.tailnetStatusSenderID != 1001 || len(sender.editInline) != 1 {
			t.Fatalf("tailnet dispatch handled=%v sender=%d edits=%d, want routed refresh edit", handled, router.tailnetStatusSenderID, len(sender.editInline))
		}
	})

	t.Run("tailnet token revoke ask", func(t *testing.T) {
		sender := &stubCommandSender{}
		router := &stubCommandRouter{
			canRestart: true,
			tailnetSurfaces: []core.TailnetSurfaceStatus{{
				SurfaceID: "parent:tsnet_http:status",
				Status:    "active",
			}},
		}
		handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
			ID:   "cb-tailnet-token-dispatch",
			From: &telegram.User{ID: 1001},
			Data: encodeTailnetRevokeTokenCallbackData(tailnetRevokeCallbackAsk, "parent:tsnet_http:status"),
			Message: &telegram.Message{
				MessageID: 97,
				Chat:      &telegram.Chat{ID: 7, Type: "private"},
			},
		})
		if err != nil {
			t.Fatalf("handleTelegramCommandCallback() err = %v", err)
		}
		if !handled || router.tailnetSurfacesSenderID != 1001 || len(sender.editInline) != 1 || !strings.Contains(sender.editInline[0].text, "Revoke tailnet surface?") {
			t.Fatalf("tailnet token dispatch handled=%v sender=%d edits=%#v, want routed revoke ask", handled, router.tailnetSurfacesSenderID, sender.editInline)
		}
	})

	t.Run("continuation status", func(t *testing.T) {
		sender := &stubCommandSender{}
		router := &stubCommandRouter{
			canRestart: true,
			continuationState: session.ContinuationState{
				Status:         session.ContinuationStatusPending,
				DecisionID:     "decision-dispatch",
				RemainingTurns: 1,
				Objective:      "inspect callback routing",
			},
		}
		handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
			ID:   "cb-continuation-dispatch",
			From: &telegram.User{ID: 1001},
			Data: encodeContinuationCallbackData("decision-dispatch", continuationActionStatusOnly),
			Message: &telegram.Message{
				MessageID: 97,
				Chat:      &telegram.Chat{ID: 7, Type: "private"},
			},
		})
		if err != nil {
			t.Fatalf("handleTelegramCommandCallback() err = %v", err)
		}
		if !handled || router.approveContinuationInput != 0 || router.stopContinuationInput != 0 || router.triggerContinuationInput != 0 || len(sender.editInline) != 1 {
			t.Fatalf("continuation dispatch handled=%v approve/stop/trigger=%d/%d/%d edits=%d, want status-only route", handled, router.approveContinuationInput, router.stopContinuationInput, router.triggerContinuationInput, len(sender.editInline))
		}
	})
}
