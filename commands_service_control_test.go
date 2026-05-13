//go:build linux

package main

import (
	"context"
	"testing"

	"github.com/idolum-ai/aphelion/core"
)

func TestHandleTelegramCommandReinstallQueuesRequest(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{}
	msg := core.InboundMessage{ChatID: 7, SenderID: 1001, SenderName: "admin", MessageID: 11, Text: "/reinstall"}
	handled, err := handleTelegramCommand(context.Background(), sender, router, msg)
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.queuedReinstallMsg == nil {
		t.Fatal("queuedReinstallMsg = nil, want queued message")
	}
	if router.queuedReinstallMsg.ChatID != msg.ChatID || router.queuedReinstallMsg.SenderID != msg.SenderID {
		t.Fatalf("queued reinstall msg = %#v, want original routing identity", router.queuedReinstallMsg)
	}
	if router.queuedReinstallMsg.Text != msg.Text {
		t.Fatalf("queued reinstall text = %q, want original command text at command-router boundary", router.queuedReinstallMsg.Text)
	}
	if len(sender.msgs) != 1 || sender.msgs[0].Text != "Queued a reinstall request as a normal turn in this chat." {
		t.Fatalf("sender msgs = %#v, want queued reinstall ack", sender.msgs)
	}
}

func TestHandleTelegramCommandRestartForcesRestart(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true}
	msg := core.InboundMessage{ChatID: 7, SenderID: 1001, SenderName: "admin", MessageID: 12, Text: "/restart"}
	handled, err := handleTelegramCommand(context.Background(), sender, router, msg)
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.restartCalls != 1 || router.restartInput != msg.ChatID {
		t.Fatalf("restart calls/input = (%d,%d), want (1,%d)", router.restartCalls, router.restartInput, msg.ChatID)
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("sender msgs = %#v, want one restart ack", sender.msgs)
	}
	if sender.msgs[0].Text != "Restarting the gateway now. Active work and continuation leases will be parked for startup recovery." {
		t.Fatalf("restart ack text = %q, want restart confirmation", sender.msgs[0].Text)
	}
}

func TestHandleTelegramCommandRestartDeniedForNonAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: false}
	msg := core.InboundMessage{ChatID: 7, SenderID: 2002, SenderName: "approved", MessageID: 13, Text: "/restart"}
	handled, err := handleTelegramCommand(context.Background(), sender, router, msg)
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.restartCalls != 0 {
		t.Fatalf("restart calls = %d, want 0 for denied restart", router.restartCalls)
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("sender msgs = %#v, want one deny ack", sender.msgs)
	}
	if sender.msgs[0].Text != "Restart denied. Only Telegram admins can run /restart." {
		t.Fatalf("deny ack text = %q, want denied confirmation", sender.msgs[0].Text)
	}
}
