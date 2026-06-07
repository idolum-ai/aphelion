//go:build linux

package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/session"
)

func TestCronJobNoneStoresDedicatedSessionWithoutOutbound(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "cron canonical"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := rt.runCronJobOnce(context.Background(), config.CronJobConfig{
		ID:       "sample",
		Every:    "2h",
		Prompt:   "Summarize pending maintenance state.",
		Delivery: "none",
		Enabled:  true,
	}); err != nil {
		t.Fatalf("runCronJobOnce() err = %v", err)
	}

	sender.mu.Lock()
	if len(sender.sent) != 0 {
		t.Fatalf("sent len = %d, want 0", len(sender.sent))
	}
	sender.mu.Unlock()

	cronSession, err := store.Load(session.SessionKey{ChatID: cronSessionChatID("sample"), UserID: 0, Scope: cronScopeRef("sample")})
	if err != nil {
		t.Fatalf("Load(cron session) err = %v", err)
	}
	if cronSession.LastFloorText != "cron canonical" {
		t.Fatalf("cron floor = %q, want cron canonical", cronSession.LastFloorText)
	}
	if len(cronSession.Messages) == 0 || cronSession.Messages[len(cronSession.Messages)-1].Content != "cron canonical" {
		t.Fatalf("cron messages = %#v, want canonical cron entry", cronSession.Messages)
	}
	if len(cronSession.Messages) != 2 || cronSession.Messages[0].Role != "user" || cronSession.Messages[1].Role != "assistant" {
		t.Fatalf("cron message roles = %#v, want synthetic user + assistant", cronSession.Messages)
	}
}

func TestCronJobAnnounceUsesFloorFallbackAndUpdatesAdminSession(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "cron canonical"
	provider.faceReplyText = "unexpected cron face render"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := rt.runCronJobOnce(context.Background(), config.CronJobConfig{
		ID:       "announce",
		Every:    "1h",
		Prompt:   "Tell the admin something useful.",
		Delivery: "announce",
		Enabled:  true,
	}); err != nil {
		t.Fatalf("runCronJobOnce() err = %v", err)
	}

	sender.mu.Lock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].ChatID != 1001 || sender.sent[0].Text != "cron canonical" {
		t.Fatalf("sent = %#v, want cron floor to admin", sender.sent[0])
	}
	sender.mu.Unlock()

	adminSession, err := store.Load(session.SessionKey{ChatID: 1001, UserID: 0})
	if err != nil {
		t.Fatalf("Load(admin session) err = %v", err)
	}
	if adminSession.LastFloorText != "cron canonical" {
		t.Fatalf("admin floor = %q, want cron canonical", adminSession.LastFloorText)
	}
	if len(adminSession.Messages) == 0 || adminSession.Messages[len(adminSession.Messages)-1].Content != "cron canonical" {
		t.Fatalf("admin messages = %#v, want cron floor entry", adminSession.Messages)
	}
	if adminSession.Messages[len(adminSession.Messages)-1].FloorContent != "cron canonical" {
		t.Fatalf("admin floor content = %q, want cron canonical", adminSession.Messages[len(adminSession.Messages)-1].FloorContent)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenFaceSystem) != 0 {
		t.Fatalf("seenFaceSystem = %#v, want no face render for cron maintenance delivery", provider.seenFaceSystem)
	}
}

func TestCronJobAnnounceFaceFailureUsesSerializedFloorFallback(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = strings.Join([]string{
		"FACTS:",
		"- The nightly maintenance summary is ready.",
		"ALLOWED_ACTIONS:",
		"- Review the pending maintenance queue.",
		"SCENE_CONSTRAINTS:",
		"- Keep the tone spare.",
	}, "\n")
	provider.faceErr = errors.New("face unavailable")

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := rt.runCronJobOnce(context.Background(), config.CronJobConfig{
		ID:       "announce-fallback",
		Every:    "1h",
		Prompt:   "Tell the admin something useful.",
		Delivery: "announce",
		Enabled:  true,
	}); err != nil {
		t.Fatalf("runCronJobOnce() err = %v", err)
	}

	want := strings.Join([]string{
		"What matters:",
		"- The nightly maintenance summary is ready.",
		"",
		"Next:",
		"- Review the pending maintenance queue.",
	}, "\n")

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != want {
		t.Fatalf("sent = %#v, want serialized cron fallback %q", sender.sent[0], want)
	}

	adminSession, err := store.Load(session.SessionKey{ChatID: 1001, UserID: 0})
	if err != nil {
		t.Fatalf("Load(admin session) err = %v", err)
	}
	if adminSession.Messages[len(adminSession.Messages)-1].Content != want {
		t.Fatalf("admin visible content = %q, want serialized cron fallback", adminSession.Messages[len(adminSession.Messages)-1].Content)
	}
}
