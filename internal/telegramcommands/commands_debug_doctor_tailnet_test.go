//go:build linux

package telegramcommands

import (
	"context"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
	"strings"
	"testing"
)

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
		Text:     "/health trace",
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
	if len(sender.inline[0].rows) == 0 || len(sender.inline[0].rows[0]) != 1 {
		t.Fatalf("rows = %#v, want Read More button", sender.inline[0].rows)
	}
	if got := sender.inline[0].rows[0][0].Text; got != "Read More" {
		t.Fatalf("button text = %q, want Read More", got)
	}
	if got := sender.inline[0].rows[0][0].CallbackData; got != encodeHealthCallbackData(healthActionTraceMore) {
		t.Fatalf("callback = %q, want health trace read-more", got)
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
		statusReadableSummary: "Admin trace snapshot ready.",
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1001,
		Text:     "/health trace",
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
	if got := sender.inline[0].text; !strings.Contains(got, "Quick Read: Admin trace snapshot ready.") {
		t.Fatalf("debug text = %q, want quick summary in collapsed view", got)
	}
	if got := sender.inline[0].text; strings.Contains(got, "status_scope=chat") {
		t.Fatalf("debug text = %q, do not want full snapshot in collapsed view", got)
	}
	if len(sender.inline[0].rows) == 0 || len(sender.inline[0].rows[0]) != 1 {
		t.Fatalf("rows = %#v, want Read More button", sender.inline[0].rows)
	}
	if got := sender.inline[0].rows[0][0].CallbackData; got != encodeHealthCallbackData(healthActionTraceMore) {
		t.Fatalf("callback = %q, want health trace read-more", got)
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
		Text:      "/health diagnose",
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
	if got := sender.msgs[0].Text; !strings.Contains(got, "Health diagnosis started") {
		t.Fatalf("doctor ack = %q, want started acknowledgement", got)
	}
	if sender.msgs[0].ReplyTo == nil || *sender.msgs[0].ReplyTo != 44 {
		t.Fatalf("reply_to = %#v, want 44", sender.msgs[0].ReplyTo)
	}
}

func TestHandleHealthDiagnoseCallbackQueuesRecoverableDoctorWork(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: true}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:       "cb-health-diagnose",
		UpdateID: 808,
		From:     &telegram.User{ID: 1001, Username: "admin"},
		Data:     encodeHealthCallbackData(healthActionDiagnose),
		Message: &telegram.Message{
			MessageID: 77,
			Chat:      &telegram.Chat{ID: 1001, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.queuedDoctorMsg == nil {
		t.Fatal("queuedDoctorMsg = nil, want doctor request")
	}
	if router.queuedDoctorMsg.IngressSurface != telegramDoctorIngressSurface || router.queuedDoctorMsg.IngressUpdateID != 808 {
		t.Fatalf("queued doctor ingress = %s/%d, want doctor callback surface/update", router.queuedDoctorMsg.IngressSurface, router.queuedDoctorMsg.IngressUpdateID)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want callback acknowledgement", len(sender.answers))
	}
	if len(sender.editInline) != 1 || !strings.Contains(sender.editInline[0].text, "Health diagnosis started") {
		t.Fatalf("editInline = %#v, want health panel updated with queued doctor work", sender.editInline)
	}
}

func TestHandleTelegramCommandDoctorDeniesNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{canRestart: false}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1002,
		Text:     "/health diagnose",
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
		Text:     "/health diagnose",
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
	if len(sender.inline[0].rows) != 1 || len(sender.inline[0].rows[0]) != 4 || sender.inline[0].rows[0][0].CallbackData != "tailnet:refresh" || sender.inline[0].rows[0][1].CallbackData != "tailnet:surfaces" || sender.inline[0].rows[0][2].CallbackData != "tailnet:grants" || sender.inline[0].rows[0][3].URL != "http://aphelion.example.ts.net:8765/status" {
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
	if len(sender.inline[0].rows) != 2 || sender.inline[0].rows[0][0].CallbackData != "tailnet:refresh" || sender.inline[0].rows[0][1].CallbackData != "tailnet:surfaces" || sender.inline[0].rows[0][2].CallbackData != "tailnet:grants" || !strings.HasPrefix(sender.inline[0].rows[1][0].CallbackData, tailnetRevokeTokenCallbackPrefix+tailnetRevokeCallbackAsk+":") {
		t.Fatalf("tailnet surfaces rows = %#v, want status/refresh and revoke button", sender.inline[0].rows)
	}
}

func TestHandleTelegramCommandTailnetGrantsShowsBindings(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		tailnetGrantBindings: []core.TailnetGrantBindingStatus{{
			BindingID:      "tailnet-bind-capg-mail-status",
			GrantID:        "capg-mail",
			SurfaceID:      "durable_agent:mail-helper:tsnet_http:status",
			CapabilityKind: "network_access",
			TargetResource: "tailnet:mail-helper",
			Status:         "applied",
		}},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, &router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1001,
		Text:     "/tailnet grants",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.tailnetGrantBindingsSenderID != 1001 || router.tailnetStatusSenderID != 0 || router.tailnetSurfacesSenderID != 0 {
		t.Fatalf("tailnet calls bindings=%d status=%d surfaces=%d, want bindings only", router.tailnetGrantBindingsSenderID, router.tailnetStatusSenderID, router.tailnetSurfacesSenderID)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if got := sender.inline[0].text; !strings.Contains(got, "Tailnet Grants") || !strings.Contains(got, "applied tailnet-bind-capg-mail-status") || !strings.Contains(got, "Target: network_access/tailnet:mail-helper") {
		t.Fatalf("tailnet grants text = %q, want grant binding projection", got)
	}
	if len(sender.inline[0].rows) != 1 || sender.inline[0].rows[0][0].CallbackData != "tailnet:refresh" || sender.inline[0].rows[0][1].CallbackData != "tailnet:surfaces" || sender.inline[0].rows[0][2].CallbackData != "tailnet:grants" {
		t.Fatalf("tailnet grants rows = %#v, want status/surfaces/refresh", sender.inline[0].rows)
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
		Text:     "/health trace",
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

func TestHandleTelegramCommandCallbackDebugReadMoreOpensPagedSnapshot(t *testing.T) {
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
		Data: encodeHealthCallbackData(healthActionTraceMore),
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
	if len(sender.edits) != 0 || len(sender.editClear) != 0 || len(sender.msgs) != 0 {
		t.Fatalf("edits=%#v editClear=%#v msgs=%#v, want paged inline edit only", sender.edits, sender.editClear, sender.msgs)
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	page := sender.editInline[0].text
	if !strings.Contains(page, "Health Trace") || !strings.Contains(page, "section 1 of 2") || !strings.Contains(page, "Status Scope: chat") {
		t.Fatalf("debug page text = %q, want first paged chat section", page)
	}
	if strings.Contains(page, "Trace Chat:") || strings.Contains(page, "status_scope=system") {
		t.Fatalf("debug page text = %q, want only first non-admin section", page)
	}
	if !commandRowsContain(sender.editInline[0].rows, "Next", "page:health:trace:2") {
		t.Fatalf("debug page rows = %#v, want next page control", sender.editInline[0].rows)
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

func TestHandleTelegramCommandCallbackTailnetGrantsForAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		tailnetGrantBindings: []core.TailnetGrantBindingStatus{{
			BindingID:      "tailnet-bind-capg-status",
			GrantID:        "capg-status",
			SurfaceID:      "parent:tsnet_http:status",
			CapabilityKind: "network_access",
			TargetResource: "tailnet:status",
			Status:         "drifted",
			DriftReason:    "observed policy hash changed",
		}},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-tailnet-grants",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: "tailnet:grants",
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
	if got := sender.editInline[0].text; !strings.Contains(got, "Tailnet Grants") || !strings.Contains(got, "drifted tailnet-bind-capg-status") || !strings.Contains(got, "observed policy hash changed") {
		t.Fatalf("tailnet grants callback text = %q, want refreshed bindings", got)
	}
	if router.tailnetGrantBindingsSenderID != 1001 || router.tailnetStatusSenderID != 0 || router.tailnetSurfacesSenderID != 0 {
		t.Fatalf("tailnet calls bindings=%d status=%d surfaces=%d, want bindings only", router.tailnetGrantBindingsSenderID, router.tailnetStatusSenderID, router.tailnetSurfacesSenderID)
	}
}
