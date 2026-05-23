//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func TestPromoteTelegramThreadCreatesDraftOnly(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	thread, _, err := store.CreateTelegramThreadForUpdate(9106, 1001, 901, 101, "promote this work lane", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}

	text, err := rt.PromoteTelegramThread(context.Background(), 9106, 1001, thread.ThreadID)
	if err != nil {
		t.Fatalf("PromoteTelegramThread() err = %v", err)
	}
	for _, want := range []string{
		"Promotion draft created for thread 1.",
		"Handoff: thread-promotion:9106:1:",
		"memory candidates: review required; no child memory written",
		"resources/capabilities: review required; no grants created",
		"does not create a durable child, transfer memory, grant resources, or run work",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("promotion text missing %q:\n%s", want, text)
		}
	}

	handoff, ok, err := store.LatestTelegramThreadPromotionHandoff(9106, thread.ThreadID)
	if err != nil || !ok {
		t.Fatalf("LatestTelegramThreadPromotionHandoff() ok=%t err=%v", ok, err)
	}
	if handoff.Status != session.TelegramThreadPromotionStatusDraft || handoff.SourceSessionID != "telegram_thread:9106:1" {
		t.Fatalf("handoff = %#v, want draft typed source", handoff)
	}
	agents, err := store.ListDurableAgents()
	if err != nil {
		t.Fatalf("ListDurableAgents() err = %v", err)
	}
	if len(agents) != 0 {
		t.Fatalf("durable agents = %#v, want no child creation", agents)
	}
	grants, err := store.CapabilityGrants(10, "", "", "")
	if err != nil {
		t.Fatalf("CapabilityGrants() err = %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("capability grants = %#v, want no grants", grants)
	}

	again, err := rt.PromoteTelegramThread(context.Background(), 9106, 1001, thread.ThreadID)
	if err != nil {
		t.Fatalf("PromoteTelegramThread(second) err = %v", err)
	}
	if !strings.Contains(again, "Promotion draft already exists for thread 1.") || !strings.Contains(again, handoff.HandoffID) {
		t.Fatalf("second promotion text = %q, want existing handoff", again)
	}
}

func TestPromoteTelegramThreadRejectsClosedThread(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	thread, _, err := store.CreateTelegramThreadForUpdate(9107, 1001, 901, 101, "closed lane", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	if _, closed, err := store.CloseTelegramThread(9107, thread.ThreadID, "done", time.Now().UTC()); err != nil || !closed {
		t.Fatalf("CloseTelegramThread() closed=%t err=%v", closed, err)
	}
	if _, err := rt.PromoteTelegramThread(context.Background(), 9107, 1001, thread.ThreadID); !IsTelegramThreadUserError(err) || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("PromoteTelegramThread(closed) err = %v, want user-facing closed-thread error", err)
	}
}

func TestDoctorTelegramThreadsShowsPromotionHandoff(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	thread, _, err := store.CreateTelegramThreadForUpdate(9108, 1001, 901, 101, "doctor-visible promotion", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	if _, err := rt.PromoteTelegramThread(context.Background(), 9108, 1001, thread.ThreadID); err != nil {
		t.Fatalf("PromoteTelegramThread() err = %v", err)
	}

	var b strings.Builder
	rt.writeDoctorTelegramThreads(&b, session.SessionKey{ChatID: 9108, UserID: 0, Scope: telegramDMScopeRef(9108)})
	report := b.String()
	for _, want := range []string{
		`telegram_thread_promotion_handoffs_count="1"`,
		`promotion_handoff="thread-promotion:9108:1:`,
		"promotion_status=draft",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("doctor thread report missing %q:\n%s", want, report)
		}
	}
}
