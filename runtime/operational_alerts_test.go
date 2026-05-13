//go:build linux

package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func TestReportOperationalIssueDeliversAndPersistsForAdminChat(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	now := time.Date(2026, time.April, 21, 12, 30, 0, 0, time.UTC)
	rt.operationalAlertClock = func() time.Time { return now }
	rt.operationalAlertWindow = 10 * time.Minute

	rt.reportOperationalIssue(context.Background(), "durable_wake", errors.New("durable child wake runner failed: user_workspace_root is required"))

	sender.mu.Lock()
	if got := len(sender.sent); got != 1 {
		sender.mu.Unlock()
		t.Fatalf("sent len = %d, want 1", got)
	}
	sent := sender.sent[0]
	sender.mu.Unlock()
	if sent.ChatID != 1001 {
		t.Fatalf("sent chat_id = %d, want 1001", sent.ChatID)
	}
	if !strings.Contains(sent.Text, "System warning") {
		t.Fatalf("sent text = %q, want warning header", sent.Text)
	}
	if !strings.Contains(sent.Text, "Component: durable_wake") {
		t.Fatalf("sent text = %q, want component label", sent.Text)
	}
	if !strings.Contains(sent.Text, "Error: durable child wake runner failed: user_workspace_root is required") {
		t.Fatalf("sent text = %q, want error detail", sent.Text)
	}

	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(admin session) err = %v", err)
	}
	if sess.TurnCount != 1 {
		t.Fatalf("admin session turn_count = %d, want 1", sess.TurnCount)
	}
	if len(sess.Messages) != 1 {
		t.Fatalf("admin session messages len = %d, want 1", len(sess.Messages))
	}
	if !strings.Contains(sess.Messages[0].Content, "System warning") {
		t.Fatalf("admin session message = %q, want warning text", sess.Messages[0].Content)
	}
}

func TestReportOperationalIssueThrottlesAndSummarizesSuppressedRepeats(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	now := time.Date(2026, time.April, 21, 12, 40, 0, 0, time.UTC)
	rt.operationalAlertClock = func() time.Time { return now }
	rt.operationalAlertWindow = 10 * time.Minute

	reportErr := errors.New("child wake runner failed: user_workspace_root is required")
	rt.reportOperationalIssue(context.Background(), "durable_wake", reportErr)
	now = now.Add(2 * time.Minute)
	rt.reportOperationalIssue(context.Background(), "durable_wake", reportErr)
	now = now.Add(11 * time.Minute)
	rt.reportOperationalIssue(context.Background(), "durable_wake", reportErr)

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if got := len(sender.sent); got != 2 {
		t.Fatalf("sent len = %d, want 2 (first + post-window replay)", got)
	}
	if strings.Contains(sender.sent[0].Text, "Suppressed repeats:") {
		t.Fatalf("first alert text = %q, want no suppressed summary", sender.sent[0].Text)
	}
	if !strings.Contains(sender.sent[1].Text, "Suppressed repeats: 1") {
		t.Fatalf("second alert text = %q, want suppressed repeat count", sender.sent[1].Text)
	}
}
