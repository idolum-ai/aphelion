//go:build linux

package runtime

import (
	"context"
	"errors"
	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestApproveContinuationActivatesContinuationLease(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8107, UserID: 0, Scope: telegramDMScopeRef(8107)}
	expiresAt := time.Now().UTC().Add(time.Hour)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:            session.ContinuationStatusPending,
		DecisionID:        "decision-lease-approve",
		RemainingTurns:    1,
		ActionProposal:    session.ActionProposal{ID: "aprop-lease-approve", Summary: "Approve lease", ExpiresAt: expiresAt, PlanHash: "sha256:lease"},
		ContinuationLease: session.ContinuationLease{ID: "lease-approve", ProposalID: "aprop-lease-approve", Status: session.ContinuationLeaseStatusPending, MaxTurns: 1, RemainingTurns: 1, ExpiresAt: expiresAt, PlanHash: "sha256:lease"},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	state, err := rt.ApproveContinuation(8107, 1002)
	if err != nil {
		t.Fatalf("ApproveContinuation() err = %v", err)
	}
	if state.ActionProposal.Status != session.ProposalStatusApproved {
		t.Fatalf("proposal status = %q, want approved", state.ActionProposal.Status)
	}
	if state.ContinuationLease.Status != session.ContinuationLeaseStatusActive {
		t.Fatalf("lease status = %q, want active", state.ContinuationLease.Status)
	}
	if state.ContinuationLease.ApprovedBy != 1002 || state.ContinuationLease.ApprovedAt.IsZero() {
		t.Fatalf("lease approval = by %d at %v, want recorded approver", state.ContinuationLease.ApprovedBy, state.ContinuationLease.ApprovedAt)
	}
}

func TestHandleInboundTypedApprovalConsumesPendingContinuation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "continued"}}
	rt.interactiveDMAssembler = recorder

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 8116, UserID: 0, Scope: telegramDMScopeRef(8116)}
	action := session.ActionProposal{
		ID:            "aprop-typed-approval",
		Summary:       "Run the approved typed continuation.",
		BoundedEffect: "Run one bounded follow-up and report evidence.",
		Status:        session.ProposalStatusPending,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	action.PlanHash = actionProposalHash(action)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:              session.TurnAuthorizationKindContinuation,
		Status:            session.ContinuationStatusPending,
		DecisionID:        "typed-approval",
		Objective:         "Continue a pending plan.",
		StageSummary:      "Run the approved typed continuation.",
		RemainingTurns:    1,
		ActionProposal:    action,
		ContinuationLease: buildContinuationLease(action, 1, now),
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8116, SenderID: 1001, SenderName: "admin", Text: "approved", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if !recorder.called {
		t.Fatal("interactive assembler not called for approved continuation")
	}
	if recorder.input.Msg.Origin != core.InboundOriginTurnAuthorization {
		t.Fatalf("origin = %q, want turn authorization", recorder.input.Msg.Origin)
	}
	if recorder.input.Msg.Text == "approved" || !strings.Contains(recorder.input.Msg.Text, "Next:\nRun the approved typed continuation") {
		t.Fatalf("continuation text = %q, want machine-authored approved step", recorder.input.Msg.Text)
	}
	for _, notWant := range []string{"approved_step:", "proposal_id:", "lease_id:", "risk_class:"} {
		if strings.Contains(recorder.input.Msg.Text, notWant) {
			t.Fatalf("continuation text = %q, did not want internal fragment %q", recorder.input.Msg.Text, notWant)
		}
	}
	if recorder.input.Actor.Role != principal.RoleAdmin {
		t.Fatalf("actor role = %q, want admin", recorder.input.Actor.Role)
	}

	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle || got.RemainingTurns != 0 || got.ApprovedBy != 0 || got.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed {
		t.Fatalf("continuation = %#v, want idle state with consumed lease after typed approval", got)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventContinuationApproved) || !hasExecutionEvent(events, core.ExecutionEventContinuationConsumed) {
		t.Fatalf("events = %#v, want approved and consumed events", events)
	}
}

func TestTriggerContinuationLoopsWhileApprovedLeaseHasTurns(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "continued"}}
	rt.interactiveDMAssembler = recorder

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 8121, UserID: 0, Scope: telegramDMScopeRef(8121)}
	action := session.ActionProposal{
		ID:               "aprop-loop-approved",
		Summary:          "Run the next approved continuation turn.",
		BoundedEffect:    "Use only the active approved lease and report evidence.",
		RiskClass:        "continuation",
		AllowedActions:   []string{"continue_one_turn", "use_existing_authority_only", "report_evidence"},
		ForbiddenActions: []string{"expand_authority_without_new_approval"},
		Status:           session.ProposalStatusApproved,
		ExpiresAt:        now.Add(time.Hour),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	action.PlanHash = actionProposalHash(action)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "loop-approved",
		Objective:      "Finish all approved continuation turns.",
		StageSummary:   "Run approved follow-up work.",
		RemainingTurns: 3,
		ApprovedBy:     1001,
		ActionProposal: action,
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-loop-approved",
			ProposalID:       action.ID,
			Status:           session.ContinuationLeaseStatusActive,
			MaxTurns:         3,
			RemainingTurns:   3,
			ApprovedBy:       1001,
			AllowedActions:   action.AllowedActions,
			ForbiddenActions: action.ForbiddenActions,
			ExpiresAt:        now.Add(time.Hour),
			PlanHash:         action.PlanHash,
			ApprovedAt:       now,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if err := rt.TriggerContinuationForKey(context.Background(), key); err != nil {
		t.Fatalf("TriggerContinuationForKey() err = %v", err)
	}
	if recorder.callCount != 3 {
		t.Fatalf("assembler calls = %d, want all 3 approved turns consumed", recorder.callCount)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle || got.RemainingTurns != 0 || got.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed {
		t.Fatalf("continuation = %#v, want consumed idle continuation", got)
	}
	sender.mu.Lock()
	progressCount := len(sender.sent)
	sender.mu.Unlock()
	if progressCount != 2 {
		t.Fatalf("progress messages = %d, want one compact progress line before each automatic follow-up turn", progressCount)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if got := countEventsByType(events, core.ExecutionEventContinuationConsumed); got != 3 {
		t.Fatalf("consumed events = %d, want 3", got)
	}
	if got := countEventsByType(events, core.ExecutionEventMissionProgressAssessed); got != 3 {
		t.Fatalf("mission progress assessments = %d, want 3", got)
	}
	if !hasExecutionEvent(events, core.ExecutionEventContinuationBoundaryReached) {
		t.Fatalf("events = %#v, want continuation boundary event after loop exhausts", events)
	}
}

func TestConcurrentContinuationReservationsConsumeSingleLeaseTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 8122, UserID: 0, Scope: telegramDMScopeRef(8122)}
	action := session.ActionProposal{
		ID:            "aprop-concurrent-reservation",
		Summary:       "Run one reserved continuation turn.",
		BoundedEffect: "Consume exactly one approved turn.",
		Status:        session.ProposalStatusApproved,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	action.PlanHash = actionProposalHash(action)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "concurrent-reservation",
		Objective:      "Prove one-turn leases cannot be double-spent.",
		StageSummary:   action.Summary,
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: action,
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-concurrent-reservation",
			ProposalID:     action.ID,
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			ApprovedBy:     1001,
			ApprovedAt:     now,
			ExpiresAt:      now.Add(time.Hour),
			PlanHash:       action.PlanHash,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	type result struct {
		reserved bool
		repair   bool
		err      error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, reservation, _, repair, err := rt.reserveApprovedContinuationTurn(key)
			results <- result{reserved: reservation != nil, repair: repair != nil, err: err}
		}()
	}
	wg.Wait()
	close(results)

	reserved := 0
	for got := range results {
		if got.err != nil {
			t.Fatalf("reserveApprovedContinuationTurn() err = %v", got.err)
		}
		if got.repair {
			t.Fatal("reserveApprovedContinuationTurn() repair = true, want no lease repair")
		}
		if got.reserved {
			reserved++
		}
	}
	if reserved != 1 {
		t.Fatalf("reserved turns = %d, want exactly one reservation for one approved turn", reserved)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle || got.RemainingTurns != 0 || got.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed {
		t.Fatalf("continuation = %#v, want single consumed idle lease", got)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if got := countEventsByType(events, core.ExecutionEventContinuationConsumed); got != 1 {
		t.Fatalf("consumed events = %d, want 1", got)
	}
}

func TestConcurrentTriggerContinuationExecutesSingleLeaseTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "continued"}}
	rt.interactiveDMAssembler = recorder

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 8123, UserID: 0, Scope: telegramDMScopeRef(8123)}
	action := session.ActionProposal{
		ID:            "aprop-concurrent-trigger",
		Summary:       "Run one public trigger continuation turn.",
		BoundedEffect: "Execute exactly one approved turn.",
		Status:        session.ProposalStatusApproved,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	action.PlanHash = actionProposalHash(action)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "concurrent-trigger",
		Objective:      "Prove concurrent triggers cannot execute more than the lease allows.",
		StageSummary:   action.Summary,
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: action,
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-concurrent-trigger",
			ProposalID:     action.ID,
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			ApprovedBy:     1001,
			ApprovedAt:     now,
			ExpiresAt:      now.Add(time.Hour),
			PlanHash:       action.PlanHash,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- rt.TriggerContinuationForKey(context.Background(), key)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("TriggerContinuationForKey() err = %v", err)
		}
	}
	if got := recorder.CallCount(); got != 1 {
		t.Fatalf("assembler calls = %d, want exactly one executed continuation turn", got)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if got := countEventsByType(events, core.ExecutionEventContinuationConsumed); got != 1 {
		t.Fatalf("consumed events = %d, want 1", got)
	}
}

func TestApproveContinuationReturnsTypedExpiredErrorAndRecordsBlocked(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8109, UserID: 0, Scope: telegramDMScopeRef(8109)}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-expired-approval",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:        "aprop-expired-approval",
			Summary:   "Expired approval",
			Status:    session.ProposalStatusPending,
			ExpiresAt: expiredAt,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-expired-approval",
			ProposalID:     "aprop-expired-approval",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      expiredAt,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	state, err := rt.ApproveContinuation(8109, 1002)
	if !errors.Is(err, core.ErrContinuationExpired) {
		t.Fatalf("ApproveContinuation() err = %v, want ErrContinuationExpired", err)
	}
	if state.Status != session.ContinuationStatusIdle || state.RemainingTurns != 0 {
		t.Fatalf("state status/turns = %q/%d, want idle/0", state.Status, state.RemainingTurns)
	}
	if state.ActionProposal.Status != session.ProposalStatusExpired || state.ContinuationLease.Status != session.ContinuationLeaseStatusExpired {
		t.Fatalf("state proposal/lease status = %q/%q, want expired/expired", state.ActionProposal.Status, state.ContinuationLease.Status)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle || got.ActionProposal.Status != session.ProposalStatusExpired || got.ContinuationLease.Status != session.ContinuationLeaseStatusExpired {
		t.Fatalf("persisted continuation = %#v, want expired idle state", got)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventContinuationBlocked) {
		t.Fatalf("events = %#v, want continuation blocked event", events)
	}
}

func TestRefreshContinuationProposalCreatesFreshLeaseForSameBoundedAction(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback
	key := session.SessionKey{ChatID: 8110, UserID: 0, Scope: telegramDMScopeRef(8110)}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	prior := session.ContinuationState{
		Status:         session.ContinuationStatusIdle,
		Objective:      "Finish the bounded local patch.",
		StageSummary:   "Patch and test the callback flow.",
		RemainingTurns: 0,
		ActionProposal: session.ActionProposal{
			ID:               "aprop-old-expired",
			OperationID:      "op-refresh-v1",
			Summary:          "Refresh the expired lease",
			WhyNow:           "The previous prompt expired.",
			BoundedEffect:    "Patch only continuation callback refresh behavior.",
			RiskClass:        "system_change",
			AllowedActions:   []string{"patch_code", "run_tests"},
			ForbiddenActions: []string{"deploy", "restart"},
			ValidationPlan:   []string{"go test ./..."},
			Status:           session.ProposalStatusExpired,
			ExpiresAt:        expiredAt,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-old-expired",
			ProposalID:     "aprop-old-expired",
			Status:         session.ContinuationLeaseStatusExpired,
			MaxTurns:       1,
			RemainingTurns: 0,
			ExpiresAt:      expiredAt,
		},
	}
	if err := store.UpdateContinuationState(key, prior); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	state, sent, err := rt.RefreshContinuationProposal(context.Background(), 8110, "expired approval callback")
	if err != nil {
		t.Fatalf("RefreshContinuationProposal() err = %v", err)
	}
	if !sent {
		t.Fatal("sent = false, want fresh inline prompt")
	}
	if state.Status != session.ContinuationStatusPending || state.RemainingTurns != 1 {
		t.Fatalf("state status/turns = %q/%d, want pending/1", state.Status, state.RemainingTurns)
	}
	if state.ActionProposal.ID == prior.ActionProposal.ID || state.ContinuationLease.ID == prior.ContinuationLease.ID {
		t.Fatalf("fresh ids reused old proposal/lease: proposal=%q lease=%q", state.ActionProposal.ID, state.ContinuationLease.ID)
	}
	if state.ActionProposal.OperationID != prior.ActionProposal.OperationID || state.ActionProposal.BoundedEffect != prior.ActionProposal.BoundedEffect {
		t.Fatalf("fresh proposal = %#v, want same operation and bounded effect", state.ActionProposal)
	}
	if state.ActionProposal.Status != session.ProposalStatusPending || !state.ActionProposal.ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("fresh proposal status/expires = %q/%v, want pending future expiry", state.ActionProposal.Status, state.ActionProposal.ExpiresAt)
	}
	if state.ContinuationLease.Status != session.ContinuationLeaseStatusPending || state.ContinuationLease.ProposalID != state.ActionProposal.ID {
		t.Fatalf("fresh lease = %#v, want pending lease tied to fresh proposal", state.ContinuationLease)
	}

	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.ActionProposal.ID != state.ActionProposal.ID || got.Status != session.ContinuationStatusPending {
		t.Fatalf("persisted state = %#v, want fresh pending state", got)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want one fresh prompt", len(sender.inline))
	}
	if !strings.Contains(sender.inline[0].text, "Patch only continuation callback refresh behavior") {
		t.Fatalf("inline text = %q, want refreshed bounded effect", sender.inline[0].text)
	}
	oldCallback := core.EncodeContinuationCallbackData(prior.ActionProposal.ID, "approve_lease")
	newCallback := sender.inline[0].rows[0][0].CallbackData
	if newCallback == "" || newCallback == oldCallback {
		t.Fatalf("fresh callback = %q old = %q, want distinct non-empty callback", newCallback, oldCallback)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventContinuationOffered) {
		t.Fatalf("events = %#v, want continuation offered event", events)
	}
}

func TestRefreshContinuationProposalSanitizesLiveNegatedDeployAuthority(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback
	key := session.SessionKey{ChatID: 81100, UserID: 0, Scope: telegramDMScopeRef(81100)}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	prior := session.ContinuationState{
		Status:         session.ContinuationStatusIdle,
		Objective:      "Diagnose and recover the blocked mail-child credentials without mailbox access.",
		StageSummary:   "Repair child-scoped mailbox adapter credential materialization, then run only a non-mailbox auth-status smoke.",
		RemainingTurns: 0,
		ActionProposal: session.ActionProposal{
			ID:        "aprop-live-corrupt",
			Summary:   "Repair child-scoped mailbox adapter credential materialization",
			RiskClass: "credential_recovery",
			BoundedEffect: "May create or adjust child-scoped mailbox adapter credential materialization, wrapper/env, or grant contract. " +
				"No mailbox content/label/inbox/message query, no OAuth, no account mutation, no public/external contact, no email actions, no deploy/restart unless separately approved.",
			AllowedActions: []string{
				"create_child_scoped_mailbox_adapter_materialization_if_approved",
				"copy_or_bind_existing_host_mailbox_credentials_without_printing_values",
				"adjust_child_mailbox_adapter_wrapper_or_grant_contract_if_needed",
				"run_child_sandbox_external_account_auth_status_only",
				"report_repair_evidence",
				"deploy",
				"prepare_release_handoff",
			},
			ForbiddenActions: []string{
				"read_or_print_secret_values",
				"run_mailbox_adapter_query",
				"read_mailbox_contents",
				"deploy",
				"restart",
			},
			Status:    session.ProposalStatusExpired,
			ExpiresAt: expiredAt,
		},
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-live-corrupt",
			ProposalID:       "aprop-live-corrupt",
			Status:           session.ContinuationLeaseStatusExpired,
			MaxTurns:         1,
			RemainingTurns:   0,
			LeaseClass:       session.ContinuationLeaseClassDeployRestart,
			AllowedActions:   []string{"deploy", "prepare_release_handoff"},
			ForbiddenActions: []string{"deploy", "restart"},
			ExpiresAt:        expiredAt,
		},
	}
	if err := store.UpdateContinuationState(key, prior); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	state, sent, err := rt.RefreshContinuationProposal(context.Background(), 81100, "expired approval callback")
	if err != nil {
		t.Fatalf("RefreshContinuationProposal() err = %v", err)
	}
	if !sent {
		t.Fatal("sent = false, want fresh prompt")
	}
	if actionListContains(state.ActionProposal.AllowedActions, "deploy") ||
		actionListContains(state.ContinuationLease.AllowedActions, "deploy") ||
		state.ContinuationLease.LeaseClass == session.ContinuationLeaseClassDeployRestart {
		t.Fatalf("refreshed state = %#v, want deploy authority stripped from negated credential recovery", state)
	}
	if !actionListContains(state.ActionProposal.ForbiddenActions, "deploy") || !actionListContains(state.ActionProposal.ForbiddenActions, "restart") {
		t.Fatalf("forbidden actions = %#v, want deploy/restart preserved", state.ActionProposal.ForbiddenActions)
	}
}

func TestTriggerContinuationExpiresStaleLease(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8108, UserID: 0, Scope: telegramDMScopeRef(8108)}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:            session.ContinuationStatusApproved,
		DecisionID:        "decision-expired-lease",
		RemainingTurns:    1,
		ApprovedBy:        1002,
		ActionProposal:    session.ActionProposal{ID: "aprop-expired-lease", Summary: "Expired lease", Status: session.ProposalStatusApproved, ExpiresAt: expiredAt},
		ContinuationLease: session.ContinuationLease{ID: "lease-expired", ProposalID: "aprop-expired-lease", Status: session.ContinuationLeaseStatusActive, MaxTurns: 1, RemainingTurns: 1, ApprovedBy: 1002, ExpiresAt: expiredAt},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := rt.TriggerContinuation(context.Background(), 8108); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.ContinuationLease.Status != session.ContinuationLeaseStatusExpired {
		t.Fatalf("lease status = %q, want expired", got.ContinuationLease.Status)
	}
	if got.ActionProposal.Status != session.ProposalStatusExpired {
		t.Fatalf("proposal status = %q, want expired", got.ActionProposal.Status)
	}
	if got.Status != session.ContinuationStatusIdle || got.RemainingTurns != 0 {
		t.Fatalf("continuation state = %q/%d, want idle/0", got.Status, got.RemainingTurns)
	}
}

func TestTriggerContinuationRunsAsApprovedUser(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &toolRequestingProvider{}
	tools := &principalRecordingTools{defs: []agent.ToolDef{testExecToolDef()}, supportsPrincipal: true}
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	key := session.SessionKey{ChatID: 8103, UserID: 0, Scope: telegramDMScopeRef(8103)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{Status: session.ContinuationStatusApproved, RemainingTurns: 1, ApprovedBy: 1002}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := rt.TriggerContinuation(context.Background(), 8103); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	if tools.lastPrincipal.Role != principal.RoleApprovedUser {
		t.Fatalf("last principal role = %q, want approved_user", tools.lastPrincipal.Role)
	}
	if tools.lastPrincipal.TelegramUserID != 1002 {
		t.Fatalf("last principal user id = %d, want 1002", tools.lastPrincipal.TelegramUserID)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle {
		t.Fatalf("status = %q, want idle when continuation consensus is missing", got.Status)
	}
	if got.ApprovedBy != 0 {
		t.Fatalf("ApprovedBy = %d, want cleared after approved continuation turn", got.ApprovedBy)
	}
	if got.HandshakeBlockedReason == "" {
		t.Fatal("HandshakeBlockedReason empty, want explicit reason when continuation is not offered again")
	}
}

func TestTriggerSandboxedOrganicProposalContinuationDowngradesAdminToApprovedUserSandbox(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{}}
	rt.interactiveDMAssembler = recorder

	expiresAt := time.Now().UTC().Add(time.Hour)
	key := session.SessionKey{ChatID: 8104, UserID: 0, Scope: telegramDMScopeRef(8104)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "organic-proposal-sandbox",
		Objective:      "Run one Organic proposal system-change step.",
		StageSummary:   "Patch one local file and report evidence.",
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-organic-proposal-sandbox",
			Summary:        "Patch one local file",
			RiskClass:      "system_change",
			AllowedActions: []string{organicProposalSandboxAction, organicProposalSandboxWriteBoundary},
			Status:         session.ProposalStatusApproved,
			ExpiresAt:      expiresAt,
			PlanHash:       "sha256:organic-proposal-sandbox",
		},
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-organic-proposal-sandbox",
			ProposalID:       "aprop-organic-proposal-sandbox",
			Status:           session.ContinuationLeaseStatusActive,
			MaxTurns:         1,
			RemainingTurns:   1,
			AllowedActions:   []string{organicProposalSandboxAction, organicProposalSandboxWriteBoundary},
			ForbiddenActions: []string{"deploy"},
			ExpiresAt:        expiresAt,
			PlanHash:         "sha256:organic-proposal-sandbox",
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if err := rt.TriggerContinuation(context.Background(), 8104); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	if !recorder.called {
		t.Fatal("interactive assembler not called")
	}
	if recorder.input.Actor.Role != principal.RoleApprovedUser {
		t.Fatalf("actor role = %q, want approved_user sandbox execution", recorder.input.Actor.Role)
	}
	if recorder.input.Actor.TelegramUserID != 1001 {
		t.Fatalf("actor user id = %d, want admin approver preserved", recorder.input.Actor.TelegramUserID)
	}
	if recorder.input.Scope.Profile.Mode != sandbox.ModeIsolated || recorder.input.Scope.Profile.Network != sandbox.NetworkDeny {
		t.Fatalf("scope profile = %#v, want isolated network-deny approved_user sandbox", recorder.input.Scope.Profile)
	}
	if !strings.Contains(recorder.input.Scope.WorkingRoot, "isolated/workspaces/1001") {
		t.Fatalf("working root = %q, want admin approver isolated user workspace", recorder.input.Scope.WorkingRoot)
	}

	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var consumed session.ExecutionEvent
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationConsumed {
			consumed = event
		}
	}
	if consumed.ID == 0 {
		t.Fatalf("events = %#v, want continuation consumed event", events)
	}
	payload := executionEventPayload(consumed.PayloadJSON)
	if payloadString(payload, "execution_principal_role") != string(principal.RoleApprovedUser) {
		t.Fatalf("execution role payload = %q, want approved_user", payloadString(payload, "execution_principal_role"))
	}
	if payloadString(payload, "sandbox_profile") != organicProposalSandboxProfile || payloadString(payload, "sandboxed_from_role") != string(principal.RoleAdmin) {
		t.Fatalf("sandbox payload = profile %q from %q, want approved_user_isolated from admin", payloadString(payload, "sandbox_profile"), payloadString(payload, "sandboxed_from_role"))
	}
}

func TestTriggerContinuationFailsClosedWithoutRecordedApprover(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8104, UserID: 0, Scope: telegramDMScopeRef(8104)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{Status: session.ContinuationStatusApproved, RemainingTurns: 1}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	err = rt.TriggerContinuation(context.Background(), 8104)
	if err == nil || !strings.Contains(err.Error(), "approver is not recorded") {
		t.Fatalf("TriggerContinuation() err = %v, want missing approver error", err)
	}
}

func TestTriggerContinuationUsesMachineAuthoredContinuationEventText(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8105, UserID: 0, Scope: telegramDMScopeRef(8105)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		StageSummary:   "Bundled Phase 4B: one bounded mail-child read-only adapter proof",
		RemainingTurns: 1,
		ApprovedBy:     1002,
		ActionProposal: session.ActionProposal{
			ID:            "aprop-phase-4b-rebundled-email-proof",
			OperationID:   "phase-4b-rebundled-email-proof",
			Summary:       "Bundled Phase 4B: one bounded mail-child read-only adapter proof",
			BoundedEffect: "Inspect current email due/backoff state, run at most one bounded read-only proof, then report.",
			RiskClass:     "status_check",
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-phase-4b-rebundled-email-proof",
			ProposalID:     "aprop-phase-4b-rebundled-email-proof",
			Status:         session.ContinuationLeaseStatusActive,
			LeaseClass:     session.ContinuationLeaseClassLocalWorkspace,
			AllowedActions: []string{string(WorkModeReadOnly)},
			MaxTurns:       1,
			RemainingTurns: 1,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := rt.TriggerContinuation(context.Background(), 8105); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.lastGovernorMsgs) == 0 {
		t.Fatal("lastGovernorMsgs empty, want continuation turn input")
	}
	last := provider.lastGovernorMsgs[len(provider.lastGovernorMsgs)-1]
	if last.Role != "user" {
		t.Fatalf("last role = %q, want provider user-role input", last.Role)
	}
	for _, want := range []string{
		approvedContinuationEventText,
		"Approved work:",
		"Next:\nBundled Phase 4B: one bounded mail-child read-only adapter proof",
		"Scope:\nInspect current email due/backoff state, run at most one bounded read-only proof, then report.",
	} {
		if !strings.Contains(last.Content, want) {
			t.Fatalf("last content = %q, want substring %q", last.Content, want)
		}
	}
	for _, notWant := range []string{"proposal_id:", "operation_id:", "lease_id:", "risk_class:", "aprop-", "lease-phase-4b"} {
		if strings.Contains(last.Content, notWant) {
			t.Fatalf("last content = %q, did not want internal fragment %q", last.Content, notWant)
		}
	}
}

func testPersonaContinuationProposal(decision session.ContinuationIntentDecision, rationale string) string {
	lines := []string{
		"INSPECT: no",
		"QUESTION: no",
		"ANSWER: yes",
		"CONTINUATION_SCHEMA_VERSION: 1",
		"CONTINUATION_INTENT: " + string(decision),
		"CONTINUATION_NEXT_STEP: Resume the next bounded step.",
		"CONTINUATION_CONFIDENCE: medium",
	}
	if strings.TrimSpace(rationale) != "" {
		lines = append(lines, "CONTINUATION_RATIONALE: "+strings.TrimSpace(rationale))
	}
	return strings.Join(lines, "\n")
}

func testGovernorContinuationRatification(decision session.ContinuationIntentDecision, rationale string, ratified bool) string {
	ratifiedToken := "no"
	if ratified {
		ratifiedToken = "yes"
	}
	lines := []string{
		"INSPECT: no",
		"QUESTION: no",
		"ANSWER: yes",
		"RATIFICATION: accept",
		"PLAN:",
		"- Continue with the next bounded step.",
		"CONTINUATION_SCHEMA_VERSION: 1",
		"CONTINUATION_INTENT: " + string(decision),
		"CONTINUATION_RATIFIED: " + ratifiedToken,
		"CONTINUATION_NEXT_STEP: Continue with the next bounded step.",
		"CONTINUATION_CONSTRAINTS: Stay within the current objective and local repo scope.",
		"CONTINUATION_CONFIDENCE: high",
	}
	if strings.TrimSpace(rationale) != "" {
		lines = append(lines, "CONTINUATION_RATIONALE: "+strings.TrimSpace(rationale))
	}
	return strings.Join(lines, "\n")
}
