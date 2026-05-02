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

func TestParkActiveWorkForRestartRefreshesAndReoffersPendingContinuation(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8701, UserID: 0, Scope: telegramDMScopeRef(8701)}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "old-decision",
		Objective:      "Keep deploy-safe continuation alive.",
		StageSummary:   "Finish the restart parking implementation.",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:            "aprop-old-decision",
			Summary:       "Finish restart parking",
			WhyNow:        "The old prompt was created before deploy.",
			BoundedEffect: "Patch continuation parking and report evidence.",
			Status:        session.ProposalStatusPending,
			ExpiresAt:     expiredAt,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-old-decision",
			ProposalID:     "aprop-old-decision",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      expiredAt,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	before := time.Now().UTC()
	result, err := rt.ParkActiveWorkForRestart(context.Background(), "test_deploy")
	if err != nil {
		t.Fatalf("ParkActiveWorkForRestart() err = %v", err)
	}
	if result.ContinuationsParked != 1 || result.PendingContinuationsParked != 1 {
		t.Fatalf("park result = %#v, want one pending continuation parked", result)
	}
	parked, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if parked.Status != session.ContinuationStatusPending {
		t.Fatalf("status = %q, want pending", parked.Status)
	}
	if parked.DecisionID == "old-decision" || parked.ActionProposal.ID == "aprop-old-decision" || parked.ContinuationLease.ID == "lease-old-decision" {
		t.Fatalf("parked state kept stale ids: %#v", parked)
	}
	if parked.ParkedAt.IsZero() || parked.ParkedSource != "test_deploy" {
		t.Fatalf("park marker = at %v source %q, want set from test_deploy", parked.ParkedAt, parked.ParkedSource)
	}
	if !parked.ContinuationLease.ExpiresAt.After(before) {
		t.Fatalf("lease expires_at = %v, want refreshed after %v", parked.ContinuationLease.ExpiresAt, before)
	}
	if !strings.Contains(parked.ActionProposal.WhyNow, "Restart/deploy parked") {
		t.Fatalf("why_now = %q, want restart parking reason", parked.ActionProposal.WhyNow)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	assertHasEventType(t, events, core.ExecutionEventContinuationParked)

	resume, err := rt.resumeRestartParkedContinuations(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("resumeRestartParkedContinuations() err = %v", err)
	}
	if resume.PendingContinuationsReoffered != 1 {
		t.Fatalf("resume result = %#v, want one reoffered pending continuation", resume)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	var inlineText string
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want 1", inlineCount)
	}
	if !strings.Contains(inlineText, "Keep deploy-safe continuation alive.") || !strings.Contains(inlineText, "Restart/deploy parked") {
		t.Fatalf("inline text = %q, want parked continuation context", inlineText)
	}
	reoffered, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(reoffered) err = %v", err)
	}
	if continuationStateRestartParked(reoffered) {
		t.Fatalf("park marker still set after reoffer: %#v", reoffered)
	}
	events, err = store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession(reoffered) err = %v", err)
	}
	assertHasEventType(t, events, core.ExecutionEventContinuationResumed)
}

func TestParkActiveWorkForRestartInterruptsRunsAndResumesApprovedContinuation(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{}}
	rt.interactiveDMAssembler = recorder

	key := session.SessionKey{ChatID: 8702, UserID: 0, Scope: telegramDMScopeRef(8702)}
	if _, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "deploy while a turn is running"); err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "approved-decision",
		Objective:      "Resume approved deploy continuation.",
		StageSummary:   "Run exactly one approved follow-up.",
		RemainingTurns: 1,
		ApprovedBy:     1002,
		ActionProposal: session.ActionProposal{
			ID:            "aprop-approved-decision",
			Summary:       "Run approved continuation",
			BoundedEffect: "Resume one bounded turn after restart.",
			Status:        session.ProposalStatusApproved,
			ExpiresAt:     expiresAt,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-approved-decision",
			ProposalID:     "aprop-approved-decision",
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			ApprovedBy:     1002,
			ExpiresAt:      expiresAt,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	result, err := rt.ParkActiveWorkForRestart(context.Background(), "telegram_restart")
	if err != nil {
		t.Fatalf("ParkActiveWorkForRestart() err = %v", err)
	}
	if result.TurnRunsInterrupted != 1 || result.ApprovedContinuationsParked != 1 {
		t.Fatalf("park result = %#v, want interrupted turn and approved continuation", result)
	}
	pendingRuns, err := store.PendingRecoveryTurnRuns(10)
	if err != nil {
		t.Fatalf("PendingRecoveryTurnRuns() err = %v", err)
	}
	if len(pendingRuns) != 1 || pendingRuns[0].Status != session.TurnRunStatusInterrupted {
		t.Fatalf("pending recovery runs = %#v, want one interrupted run", pendingRuns)
	}
	parked, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(parked) err = %v", err)
	}
	if parked.Status != session.ContinuationStatusApproved || parked.ContinuationLease.Status != session.ContinuationLeaseStatusActive {
		t.Fatalf("parked state = %#v, want approved active lease", parked)
	}
	if !continuationStateRestartParked(parked) {
		t.Fatalf("park marker missing from approved continuation: %#v", parked)
	}

	resume, err := rt.resumeRestartParkedContinuations(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("resumeRestartParkedContinuations() err = %v", err)
	}
	if resume.ApprovedContinuationsResumed != 1 {
		t.Fatalf("resume result = %#v, want one approved continuation resumed", resume)
	}
	deadline := time.Now().Add(time.Second)
	for !recorder.called && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !recorder.called {
		t.Fatal("approved parked continuation was not triggered after resume")
	}
	if recorder.input.Msg.Text != approvedContinuationEventText {
		t.Fatalf("continuation message = %q, want approved continuation event text", recorder.input.Msg.Text)
	}
	consumed, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(consumed) err = %v", err)
	}
	if consumed.Status != session.ContinuationStatusIdle {
		t.Fatalf("status after auto resume = %q, want idle after one turn consumed", consumed.Status)
	}
	if continuationStateRestartParked(consumed) {
		t.Fatalf("park marker still set after approved resume: %#v", consumed)
	}
}
