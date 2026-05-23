//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestPromoteTelegramThreadCreatesReviewPackageOnly(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	now := time.Now().UTC()
	thread, _, err := store.CreateTelegramThreadForUpdate(9106, 1001, 901, 101, "promote this work lane", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	threadKey := session.SessionKey{ChatID: 9106, UserID: 0, Scope: telegramThreadScopeRef(9106, thread.ThreadID)}
	sess, err := store.Load(threadKey)
	if err != nil {
		t.Fatalf("Load(thread session) err = %v", err)
	}
	sess.TurnCount = 2
	if err := store.Save(sess, []session.Message{
		{Role: "user", Content: "We need this lane to become durable but safe.", TurnIndex: 1, CreatedAt: now.Add(time.Minute)},
		{Role: "assistant", Content: "I will preserve context but stop before grants or child creation.", TurnIndex: 1, CreatedAt: now.Add(2 * time.Minute)},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(thread messages) err = %v", err)
	}

	result, err := rt.PromoteTelegramThread(context.Background(), 9106, 1001, thread.ThreadID)
	if err != nil {
		t.Fatalf("PromoteTelegramThread() err = %v", err)
	}
	if result.HandoffID == "" || result.ThreadID != thread.ThreadID || result.Status != session.TelegramThreadPromotionStatusDraft {
		t.Fatalf("promotion result = %#v, want typed draft handoff for thread %d", result, thread.ThreadID)
	}

	for _, want := range []string{
		"Promotion draft created for thread 1.",
		"Handoff: thread-promotion:9106:1:",
		"Proposed child:",
		"Context digest:",
		"Memory candidates:",
		"Resource candidates:",
		"Policy: review_before_reply / parent_relay_only / shared_context=isolated",
		"does not create a durable child, transfer memory, grant resources, or run work",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("promotion text missing %q:\n%s", want, result.Text)
		}
	}

	handoff, ok, err := store.LatestTelegramThreadPromotionHandoff(9106, thread.ThreadID)
	if err != nil || !ok {
		t.Fatalf("LatestTelegramThreadPromotionHandoff() ok=%t err=%v", ok, err)
	}
	if handoff.Status != session.TelegramThreadPromotionStatusDraft || handoff.SourceSessionID != "telegram_thread:9106:1" {
		t.Fatalf("handoff = %#v, want draft typed source", handoff)
	}
	if handoff.ProposedChildJSON == "{}" || handoff.MemoryDigestJSON == "[]" || handoff.ResourceReviewJSON == "[]" || handoff.PolicyPatchJSON == "{}" {
		t.Fatalf("handoff package = %#v, want review package without applying", handoff)
	}
	if !strings.Contains(handoff.ContextSummary, "Promotion candidate") || !strings.Contains(handoff.FirstTask, "stop for parent review") {
		t.Fatalf("context/first task = %q / %q", handoff.ContextSummary, handoff.FirstTask)
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
	if !strings.Contains(again.Text, "Promotion draft already exists for thread 1.") || !strings.Contains(again.Text, handoff.HandoffID) {
		t.Fatalf("second promotion text = %q, want existing handoff", again.Text)
	}
	if again.HandoffID != handoff.HandoffID || again.ThreadID != handoff.ThreadID || again.Status != session.TelegramThreadPromotionStatusDraft {
		t.Fatalf("second promotion result = %#v, want existing typed draft handoff", again)
	}
}

func TestPrepareAndCancelTelegramThreadPromotionStayInsideReviewBoundary(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	thread, _, err := store.CreateTelegramThreadForUpdate(9116, 1001, 901, 101, "make this production ready", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	if _, err := rt.PromoteTelegramThread(context.Background(), 9116, 1001, thread.ThreadID); err != nil {
		t.Fatalf("PromoteTelegramThread() err = %v", err)
	}
	handoff, ok, err := store.LatestTelegramThreadPromotionHandoff(9116, thread.ThreadID)
	if err != nil || !ok {
		t.Fatalf("LatestTelegramThreadPromotionHandoff() ok=%t err=%v", ok, err)
	}
	readyResult, err := rt.PrepareTelegramThreadPromotion(context.Background(), 9116, 1001, handoff.HandoffID)
	if err != nil {
		t.Fatalf("PrepareTelegramThreadPromotion() err = %v", err)
	}
	if !strings.Contains(readyResult.Text, "Promotion handoff ready") || !strings.Contains(readyResult.Text, "No durable child, memory write, capability grant, or first run happened") {
		t.Fatalf("ready text = %q", readyResult.Text)
	}
	if readyResult.HandoffID != handoff.HandoffID || readyResult.ThreadID != handoff.ThreadID || readyResult.Status != session.TelegramThreadPromotionStatusReady {
		t.Fatalf("ready result = %#v, want typed ready handoff", readyResult)
	}
	ready, _, err := store.LatestTelegramThreadPromotionHandoff(9116, thread.ThreadID)
	if err != nil {
		t.Fatalf("LatestTelegramThreadPromotionHandoff(after ready) err = %v", err)
	}
	if ready.Status != session.TelegramThreadPromotionStatusReady {
		t.Fatalf("status = %s, want ready", ready.Status)
	}
	repromote, err := rt.PromoteTelegramThread(context.Background(), 9116, 1001, thread.ThreadID)
	if err != nil {
		t.Fatalf("PromoteTelegramThread(after ready) err = %v", err)
	}
	if repromote.HandoffID != handoff.HandoffID || repromote.ThreadID != handoff.ThreadID || repromote.Status != session.TelegramThreadPromotionStatusReady {
		t.Fatalf("ready repromote result = %#v, want existing typed ready handoff", repromote)
	}
	if !strings.Contains(repromote.Text, "Promotion handoff ready") || !strings.Contains(repromote.Text, "Next gate: approve/apply") {
		t.Fatalf("ready repromote text = %q, want ready next-gate copy", repromote.Text)
	}
	for _, forbidden := range []string{"Promotion draft already exists", "tap Ready"} {
		if strings.Contains(repromote.Text, forbidden) {
			t.Fatalf("ready repromote text contains %q:\n%s", forbidden, repromote.Text)
		}
	}
	cancelResult, err := rt.CancelTelegramThreadPromotion(context.Background(), 9116, 1001, handoff.HandoffID)
	if err != nil {
		t.Fatalf("CancelTelegramThreadPromotion() err = %v", err)
	}
	if !strings.Contains(cancelResult.Text, "Promotion cancelled") || !strings.Contains(cancelResult.Text, "No durable child") {
		t.Fatalf("cancel text = %q", cancelResult.Text)
	}
	if cancelResult.HandoffID != handoff.HandoffID || cancelResult.ThreadID != handoff.ThreadID || cancelResult.Status != session.TelegramThreadPromotionStatusCancelled {
		t.Fatalf("cancel result = %#v, want typed cancelled handoff", cancelResult)
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
		"promotion_next_action=review_package",
		"promotion_memory_candidates=1",
		"promotion_resource_candidates=1",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("doctor thread report missing %q:\n%s", want, report)
		}
	}
}
