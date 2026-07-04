//go:build linux

package telegramcommands

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestHandleTelegramCommandFrontierShowsAdminMeter(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 3, 13, 0, 0, 0, time.UTC)
	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		canRestart: true,
		authorityFrontierStatus: core.AuthorityFrontierStatusSnapshot{
			GeneratedAt: now,
			AdminChatID: 1001,
			Budget:      5,
			Used:        2,
			Reserved:    1,
			Open:        1,
			Empty:       3,
			Slots: []core.AuthorityFrontierSlot{{
				Index:              1,
				Status:             "open",
				AllowanceID:        "lookahead:one",
				ReviewEventID:      71,
				NextActionRecordID: "next-action:issue",
				EntryID:            "ident:issue",
				ExpiresAt:          now.Add(20 * time.Minute),
				TTLSeconds:         int64((20 * time.Minute).Seconds()),
			}, {
				Index:       2,
				Status:      "reserved",
				AllowanceID: "lookahead:two",
				ExpiresAt:   now.Add(10 * time.Minute),
				TTLSeconds:  int64((10 * time.Minute).Seconds()),
			}, {
				Index:  3,
				Status: "empty",
			}, {
				Index:  4,
				Status: "empty",
			}, {
				Index:  5,
				Status: "empty",
			}},
		},
	}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    1001,
		SenderID:  1001,
		MessageID: 50,
		Text:      "/frontier",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand(/frontier) err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.authorityFrontierStatusSenderID != 1001 {
		t.Fatalf("frontier sender = %d, want 1001", router.authorityFrontierStatusSenderID)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline messages = %d, want 1", len(sender.inline))
	}
	text := sender.inline[0].text
	for _, want := range []string{
		"Authority Frontier",
		"2/5 unresolved slot(s) used",
		"1. open next-action:issue",
		"2. reserved lookahead:two",
		"3. empty",
		"No authority was approved or executed by this view.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("frontier text = %q, want %q", text, want)
		}
	}
}

func TestHandleTelegramCommandFrontierDeniesNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: false}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:   2002,
		SenderID: 2002,
		Text:     "/frontier",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand(/frontier non-admin) err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("messages = %d, want denial message", len(sender.msgs))
	}
	if !strings.Contains(sender.msgs[0].Text, "admin only") {
		t.Fatalf("denial text = %q, want admin-only message", sender.msgs[0].Text)
	}
	if router.authorityFrontierStatusSenderID != 0 {
		t.Fatalf("frontier sender = %d, want no router call", router.authorityFrontierStatusSenderID)
	}
}
