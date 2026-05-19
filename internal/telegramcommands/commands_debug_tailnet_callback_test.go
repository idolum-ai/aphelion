//go:build linux

package telegramcommands

import (
	"context"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
	"strings"
	"testing"
)

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

func TestHandleTelegramCommandCallbackTailnetTokenRevokeResolvesSurface(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		tailnetSurfaces: []core.TailnetSurfaceStatus{{
			SurfaceID: "parent:tsnet_http:status",
			Status:    "active",
		}},
		revokeTailnetSurfaceReturn: core.TailnetSurfaceStatus{
			SurfaceID: "parent:tsnet_http:status",
			Status:    "revoked",
		},
		revokeTailnetSurfaceOK: true,
	}
	ask, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-tailnet-token-ask",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: encodeTailnetRevokeTokenCallbackData(tailnetRevokeCallbackAsk, "parent:tsnet_http:status"),
		Message: &telegram.Message{
			MessageID: 97,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("ask callback err = %v", err)
	}
	if !ask {
		t.Fatal("ask handled = false, want true")
	}
	if len(sender.editInline) != 1 || !strings.Contains(sender.editInline[0].text, "Revoke tailnet surface?") {
		t.Fatalf("editInline = %#v, want token confirmation", sender.editInline)
	}
	if !strings.HasPrefix(sender.editInline[0].rows[0][1].CallbackData, tailnetRevokeTokenCallbackPrefix+tailnetRevokeCallbackConfirm+":") {
		t.Fatalf("confirmation rows = %#v, want token confirm callback", sender.editInline[0].rows)
	}

	confirmed, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-tailnet-token-confirm",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: encodeTailnetRevokeTokenCallbackData(tailnetRevokeCallbackConfirm, "parent:tsnet_http:status"),
		Message: &telegram.Message{
			MessageID: 97,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("confirm callback err = %v", err)
	}
	if !confirmed {
		t.Fatal("confirm handled = false, want true")
	}
	if router.revokeTailnetSurfaceSenderID != 1001 || router.revokeTailnetSurfaceID != "parent:tsnet_http:status" {
		t.Fatalf("revoke call sender=%d surface=%q, want token-resolved surface", router.revokeTailnetSurfaceSenderID, router.revokeTailnetSurfaceID)
	}
	if len(sender.answers) != 2 {
		t.Fatalf("answers = %#v, want ask and confirm acknowledgements", sender.answers)
	}
	if len(sender.editClear) != 1 || !strings.Contains(sender.editClear[0].text, "Tailnet surface revoked") {
		t.Fatalf("editClear = %#v, want revoke result", sender.editClear)
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
	if got := sender.editInline[0].text; !strings.Contains(got, "System Status") || strings.Contains(got, "Status Scope: system") {
		t.Fatalf("system status text = %q, want human system status without raw scope", got)
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
	if got := sender.editInline[0].text; !strings.Contains(got, "Durable Agents") || strings.Contains(got, "Status Scope: durables") {
		t.Fatalf("durables status text = %q, want human durable status without raw scope", got)
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
