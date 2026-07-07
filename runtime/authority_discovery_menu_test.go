//go:build linux

package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	toolpkg "github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestCompileAuthorityDiscoveryMenuScoresFrontierAndLoadout(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	entry := session.IdentificationLedgerEntry{
		EntryID:     "ident-frontier",
		PlanID:      "plan-long-run",
		PlanVersion: "v1",
		SessionID:   "telegram_dm:1001",
		StepRef:     "step:mail-triage",
		ShapeHash:   "sha256:mail-triage",
		LabelRef:    "crc-frontier",
		Status:      session.IdentificationLedgerStatusProposed,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	menu := CompileAuthorityDiscoveryMenu(AuthorityDiscoveryMenuInput{
		PlanID:      "plan-long-run",
		PlanVersion: "v1",
		SessionID:   "telegram_dm:1001",
		Now:         now,
		Entries: []session.IdentificationLedgerProjection{{
			Entry: entry,
			Observations: []session.IdentificationLedgerObservation{
				{ObservationID: "obs-static", EntryID: entry.EntryID, Method: session.IdentificationObservationStatic, Property: session.IdentificationPropertyApprovalClass, Value: "data_access", ObservedAt: now},
				{ObservationID: "obs-collision", EntryID: entry.EntryID, Method: session.IdentificationObservationCollision, Property: session.IdentificationPropertyRetryability, Value: "bounded_backoff", ObservedAt: now},
			},
		}},
		Loadout: []AuthorityDiscoveryLoadoutSlot{{
			TokenID:              "loadout-github",
			LabelRef:             "authbundle-standing-github",
			LiveAuthorityWitness: newAuthorityDiscoveryLiveSlotWitness("authbundle-standing-github", "", "test_live_join", now),
			ExpiresAt:            now.Add(time.Hour),
		}, {
			TokenID:   "loadout-github-unverified",
			LabelRef:  "authbundle-unverified-github",
			ExpiresAt: now.Add(time.Hour),
		}, {
			TokenID:              "loadout-extra-skipped",
			LabelRef:             "authbundle-extra",
			LiveAuthorityWitness: newAuthorityDiscoveryLiveSlotWitness("authbundle-extra", "", "test_live_join", now),
			ExpiresAt:            now.Add(time.Hour),
		}},
	})
	if len(menu.Tokens) != 3 {
		t.Fatalf("tokens = %#v, want frontier plus two loadout slots", menu.Tokens)
	}
	var frontier, verifiedLoadout, unverifiedLoadout AuthorityDiscoveryMenuToken
	for _, token := range menu.Tokens {
		if token.TokenID == "ident-frontier" {
			frontier = token
		}
		if token.TokenID == "loadout-github" {
			verifiedLoadout = token
		}
		if token.TokenID == "loadout-github-unverified" {
			unverifiedLoadout = token
		}
		if token.TokenID == "loadout-extra-skipped" {
			t.Fatalf("tokens = %#v, want loadout capped at %d slots", menu.Tokens, MaxAuthorityDiscoveryLoadoutSlots)
		}
	}
	if frontier.State != AuthorityDiscoveryTokenOneApprovalAway {
		t.Fatalf("frontier state = %q, want one_approval_away", frontier.State)
	}
	if verifiedLoadout.State != AuthorityDiscoveryTokenExecutable {
		t.Fatalf("verified loadout state = %q, want executable", verifiedLoadout.State)
	}
	if unverifiedLoadout.State != AuthorityDiscoveryTokenOneApprovalAway {
		t.Fatalf("unverified loadout state = %q, want one_approval_away", unverifiedLoadout.State)
	}
	if len(frontier.ResolutionCandidates) != 1 || frontier.ResolutionCandidates[0].Kind != "continuation_recovery_contract" {
		t.Fatalf("frontier candidates = %#v, want continuation recovery contract", frontier.ResolutionCandidates)
	}
	if frontier.ResolutionCandidates[0].CandidateID == "" ||
		frontier.ResolutionCandidates[0].Action != "materialize_or_use_exact_authority" ||
		frontier.ResolutionCandidates[0].State != AuthorityDiscoveryTokenOneApprovalAway {
		t.Fatalf("frontier candidate = %#v, want stable exact-authority candidate metadata", frontier.ResolutionCandidates[0])
	}
	metrics := ScoreAuthorityDiscoveryMenu(menu)
	if metrics.Interruptions != 2 {
		t.Fatalf("interruptions = %d, want frontier plus unverified loadout", metrics.Interruptions)
	}
	if metrics.OverGrantMass != 1 {
		t.Fatalf("over grant mass = %d, want one unbound standing loadout", metrics.OverGrantMass)
	}
	if metrics.IdentifiedBreadth != 3 {
		t.Fatalf("identified breadth = %d, want approval/retry/loadout properties", metrics.IdentifiedBreadth)
	}
}

func TestCompileAuthorityDiscoveryMenuRequiresLiveSlotWitness(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 3, 15, 0, 0, 0, time.UTC)
	entry := session.IdentificationLedgerEntry{
		EntryID:     "ident-approved",
		PlanID:      "plan-witness",
		PlanVersion: "v1",
		SessionID:   "telegram_dm:1001",
		StepRef:     "step:danger",
		ShapeHash:   "shape:exact",
		LabelRef:    "authbundle-witness",
		Status:      session.IdentificationLedgerStatusApproved,
		ExpiresAt:   now.Add(time.Hour),
	}
	menu := CompileAuthorityDiscoveryMenu(AuthorityDiscoveryMenuInput{
		PlanID:      "plan-witness",
		PlanVersion: "v1",
		SessionID:   "telegram_dm:1001",
		Now:         now,
		Entries:     []session.IdentificationLedgerProjection{{Entry: entry}},
		Loadout: []AuthorityDiscoveryLoadoutSlot{{
			TokenID:   "loadout-mentioned",
			LabelRef:  "authbundle-mentioned",
			ShapeHash: "shape:loadout",
			ExpiresAt: now.Add(time.Hour),
		}},
		LiveAuthorityRefs: map[string]AuthorityDiscoveryLiveSlotWitness{
			"authbundle-witness": newAuthorityDiscoveryLiveSlotWitness("authbundle-witness", "shape:wrong", "test_live_join", now),
		},
	})
	if tokenStateForTest(menu, "ident-approved") == AuthorityDiscoveryTokenExecutable {
		t.Fatalf("menu = %#v, want mismatched witness to stay non-executable", menu)
	}
	if tokenStateForTest(menu, "loadout-mentioned") == AuthorityDiscoveryTokenExecutable {
		t.Fatalf("menu = %#v, want mentioned loadout without witness to stay non-executable", menu)
	}

	menu = CompileAuthorityDiscoveryMenu(AuthorityDiscoveryMenuInput{
		PlanID:      "plan-witness",
		PlanVersion: "v1",
		SessionID:   "telegram_dm:1001",
		Now:         now,
		Entries:     []session.IdentificationLedgerProjection{{Entry: entry}},
		Loadout: []AuthorityDiscoveryLoadoutSlot{{
			TokenID:              "loadout-mentioned",
			LabelRef:             "authbundle-mentioned",
			ShapeHash:            "shape:loadout",
			LiveAuthorityWitness: newAuthorityDiscoveryLiveSlotWitness("authbundle-mentioned", "shape:loadout", "test_live_join", now),
			ExpiresAt:            now.Add(time.Hour),
		}},
		LiveAuthorityRefs: map[string]AuthorityDiscoveryLiveSlotWitness{
			"authbundle-witness": newAuthorityDiscoveryLiveSlotWitness("authbundle-witness", "shape:exact", "test_live_join", now),
		},
	})
	if tokenStateForTest(menu, "ident-approved") != AuthorityDiscoveryTokenExecutable {
		t.Fatalf("menu = %#v, want exact witness to make approved entry executable", menu)
	}
	if tokenStateForTest(menu, "loadout-mentioned") != AuthorityDiscoveryTokenExecutable {
		t.Fatalf("menu = %#v, want loadout witness to make loadout executable", menu)
	}
}

func TestAuthorityDiscoveryAdversarialNearMissesStayNonExecutable(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 3, 15, 15, 0, 0, time.UTC)
	entries := []session.IdentificationLedgerProjection{{
		Entry: session.IdentificationLedgerEntry{
			EntryID:     "ident-consumed-tail",
			PlanID:      "plan-adversarial",
			PlanVersion: "v1",
			SessionID:   "telegram_dm:1001",
			StepRef:     "step:old",
			ShapeHash:   "shape:tail",
			LabelRef:    "authbundle-tail",
			Status:      session.IdentificationLedgerStatusConsumed,
			ExpiresAt:   now.Add(time.Hour),
		},
	}, {
		Entry: session.IdentificationLedgerEntry{
			EntryID:     "ident-expired-tail",
			PlanID:      "plan-adversarial",
			PlanVersion: "v1",
			SessionID:   "telegram_dm:1001",
			StepRef:     "step:expired",
			ShapeHash:   "shape:expired",
			LabelRef:    "authbundle-expired",
			Status:      session.IdentificationLedgerStatusApproved,
			ExpiresAt:   now.Add(-time.Minute),
		},
	}, {
		Entry: session.IdentificationLedgerEntry{
			EntryID:     "ident-confused-deputy",
			PlanID:      "plan-adversarial",
			PlanVersion: "v1",
			SessionID:   "telegram_dm:1001",
			StepRef:     "step:current",
			ShapeHash:   "shape:current",
			LabelRef:    "authbundle-current",
			Status:      session.IdentificationLedgerStatusApproved,
			ExpiresAt:   now.Add(time.Hour),
		},
	}}
	menu := CompileAuthorityDiscoveryMenu(AuthorityDiscoveryMenuInput{
		PlanID:      "plan-adversarial",
		PlanVersion: "v1",
		SessionID:   "telegram_dm:1001",
		Entries:     entries,
		Now:         now,
		LiveAuthorityRefs: map[string]AuthorityDiscoveryLiveSlotWitness{
			"authbundle-tail":    newAuthorityDiscoveryLiveSlotWitness("authbundle-tail", "shape:tail", "test_live_join", now),
			"authbundle-expired": newAuthorityDiscoveryLiveSlotWitness("authbundle-expired", "shape:expired", "test_live_join", now),
			"authbundle-current": newAuthorityDiscoveryLiveSlotWitness("authbundle-current", "shape:other", "test_live_join", now),
		},
	})
	if got := tokenStateForTest(menu, "ident-consumed-tail"); got != AuthorityDiscoveryTokenSpent {
		t.Fatalf("consumed tail state = %q, want spent despite live-looking witness", got)
	}
	if got := tokenStateForTest(menu, "ident-expired-tail"); got != AuthorityDiscoveryTokenExpired {
		t.Fatalf("expired tail state = %q, want expired despite live-looking witness", got)
	}
	if got := tokenStateForTest(menu, "ident-confused-deputy"); got == AuthorityDiscoveryTokenExecutable {
		t.Fatalf("confused-deputy state = executable, want non-executable with wrong-shape witness")
	}
}

func TestReviewEventLookaheadRecordsNonExecutingLedgerObservation(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	rt := &Runtime{store: store}
	event := session.ReviewEvent{
		ID:                77,
		SourceSessionID:   "telegram_dm:1001",
		TargetSessionID:   "telegram_dm:1001",
		TargetAdminChatID: 1001,
		Summary:           "Approved current grant.",
		MetadataJSON:      `{"request_id":"cap-current"}`,
		Status:            "delivered",
		DeliveryMessageID: 44,
	}
	text, err := rt.HandleReviewEventAction(context.Background(), telegram.CallbackQuery{
		ID:      "cb-lookahead-runtime",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 44, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionLookaheadNext),
	}, event, core.ReviewEventActionLookaheadNext)
	if err != nil {
		t.Fatalf("HandleReviewEventAction(lookahead) err = %v", err)
	}
	if !strings.Contains(text, "No authority was approved or executed") {
		t.Fatalf("lookahead text = %q, want non-executing acknowledgement", text)
	}
	projections, err := store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		PlanID:      session.IdentificationPlanIDForSession("telegram_dm:1001"),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   "telegram_dm:1001",
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries() err = %v", err)
	}
	if len(projections) != 0 {
		t.Fatalf("ledger entries = %#v, want no authority-shape entry for no-frontier diagnostic", projections)
	}
	events, err := store.ExecutionEventsBySession(session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	foundNoFrontier := false
	for _, event := range events {
		if event.EventType == core.ExecutionEventAuthorityFindingReviewed && event.Status == "no_frontier" {
			foundNoFrontier = true
		}
	}
	if !foundNoFrontier {
		t.Fatalf("events = %#v, want no-frontier authority finding event", events)
	}
}

func TestReviewEventLookaheadSimulatesNextPhaseCapabilityGrant(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-lookahead-phase",
		Objective: "Continue the phase plan with one future GitHub issue step.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-lookahead-phase",
			Goal:           "Finish a long-running setup.",
			CurrentPhaseID: "phase-read",
			Phases: []session.OperationPhase{{
				ID:          "phase-read",
				Summary:     "Inspect local evidence.",
				Status:      session.PlanStatusCompleted,
				CompletedAt: now.Add(-time.Minute),
			}, {
				ID:               "phase-github-issue",
				Summary:          "Open one issue documenting the generated schema blocker.",
				Status:           session.PlanStatusPending,
				AllowedActions:   []string{"github_issue_create"},
				ForbiddenActions: []string{"github_repo_push"},
				ValidationPlan:   []string{"stop after the issue is opened or a typed blocker appears"},
				RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
					Kind:           session.CapabilityKindExternalAccount,
					TargetResource: "github:idolum-ai/CopilotKit",
					GrantedTo:      "telegram:1001",
					AllowedActions: []string{"github_issue_create"},
					Contract:       `{"bounded_effect":"Open one issue only."}`,
					Constraints:    `{"repository":"idolum-ai/CopilotKit"}`,
				}},
			}},
		},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	event := session.ReviewEvent{
		ID:                91,
		SourceSessionID:   session.SessionIDForKey(key),
		TargetSessionID:   session.SessionIDForKey(key),
		TargetAdminChatID: 1001,
		Summary:           "Approved the current grant.",
		Status:            "delivered",
		DeliveryMessageID: 91,
	}
	text, err := rt.HandleReviewEventAction(context.Background(), telegram.CallbackQuery{
		ID:      "cb-lookahead-simulated-phase",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 91, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionLookaheadNext),
	}, event, core.ReviewEventActionLookaheadNext)
	if err != nil {
		t.Fatalf("HandleReviewEventAction(simulated lookahead) err = %v", err)
	}
	if !strings.Contains(text, "approval surfaced") || !strings.Contains(text, "No authority was approved or executed") {
		t.Fatalf("lookahead text = %q, want surfaced non-executing approval", text)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one simulated approval card", inlineCount)
	}
	for _, want := range []string{"Approve:", "github_issue_create", "external_account", "github:idolum-ai/CopilotKit", "github_repo_push"} {
		if !strings.Contains(inlineText, want) {
			t.Fatalf("inline text = %q, want simulated approval card containing %q", inlineText, want)
		}
	}
	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusPending || state.ContinuationLease.LeaseClass != session.ContinuationLeaseClassCapabilityGrant {
		t.Fatalf("continuation state = %#v, want pending capability-grant carrier", state)
	}
	if len(state.ContinuationLease.RequiredCapabilityGrants) != 1 ||
		state.ContinuationLease.RequiredCapabilityGrants[0].TargetResource != "github:idolum-ai/CopilotKit" {
		t.Fatalf("required grants = %#v, want simulated phase grant copied onto approval", state.ContinuationLease.RequiredCapabilityGrants)
	}
	projections, err := store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		Limit:       20,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries() err = %v", err)
	}
	found := false
	for _, projection := range projections {
		if projection.Entry.StepRef != "operation_phase:phase-github-issue" || projection.Entry.Status != session.IdentificationLedgerStatusProposed {
			continue
		}
		for _, observation := range projection.Observations {
			if observation.Method == session.IdentificationObservationLookahead &&
				observation.Property == session.IdentificationPropertyBundleFit &&
				observation.Value == "operation_phase_required_capability" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("ledger projections = %#v, want simulated operation-phase lookahead entry", projections)
	}
	assertLookaheadLabelsWithoutApprovalHaveNoExecutionEvents(t, store, key)
}

func TestReviewEventLookaheadResurfacesSimulatedFrontierAfterExpiry(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	current := time.Now().UTC().Add(2 * time.Minute)
	rt.authorityDiscoveryClock = func() time.Time { return current }
	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	seedLookaheadOperationPhase(t, store, key, "op-lookahead-resurface", "phase-lookahead-resurface")
	event := session.ReviewEvent{
		ID:                93,
		SourceSessionID:   session.SessionIDForKey(key),
		TargetSessionID:   session.SessionIDForKey(key),
		TargetAdminChatID: 1001,
		Summary:           "Approved the current grant.",
		Status:            "delivered",
		DeliveryMessageID: 93,
	}
	text, err := rt.HandleReviewEventAction(context.Background(), telegram.CallbackQuery{
		ID:      "cb-lookahead-resurface-first",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 93, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionLookaheadNext),
	}, event, core.ReviewEventActionLookaheadNext)
	if err != nil {
		t.Fatalf("HandleReviewEventAction(first lookahead) err = %v", err)
	}
	if !strings.Contains(text, "approval surfaced") {
		t.Fatalf("first lookahead text = %q, want surfaced approval", text)
	}
	sender.mu.Lock()
	firstInlineCount := len(sender.inline)
	sender.mu.Unlock()
	if firstInlineCount != 1 {
		t.Fatalf("inline count after first lookahead = %d, want one approval card", firstInlineCount)
	}

	current = current.Add(session.DefaultLookaheadAllowanceTTL + time.Minute)
	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(first approval) err = %v", err)
	}
	if err := store.UpdateContinuationState(key, continuationStateWithLeaseExpired(state, current)); err != nil {
		t.Fatalf("UpdateContinuationState(expired first approval) err = %v", err)
	}
	if err := rt.sweepExpiredLookaheadAllowancesWithSilence(current); err != nil {
		t.Fatalf("sweepExpiredLookaheadAllowancesWithSilence() err = %v", err)
	}

	text, err = rt.HandleReviewEventAction(context.Background(), telegram.CallbackQuery{
		ID:      "cb-lookahead-resurface-second",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 94, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionLookaheadNext),
	}, event, core.ReviewEventActionLookaheadNext)
	if err != nil {
		t.Fatalf("HandleReviewEventAction(second lookahead) err = %v", err)
	}
	if !strings.Contains(text, "approval surfaced") || !strings.Contains(text, "No authority was approved or executed") {
		t.Fatalf("second lookahead text = %q, want fresh surfaced approval after expiry", text)
	}
	sender.mu.Lock()
	secondInlineCount := len(sender.inline)
	sender.mu.Unlock()
	if secondInlineCount != 2 {
		t.Fatalf("inline count after second lookahead = %d, want a fresh approval card after expiry", secondInlineCount)
	}
}

func TestReviewEventLookaheadSimulatesBoundedPhaseFrontierCluster(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	now := time.Now().UTC().Truncate(time.Second)
	rt.authorityDiscoveryClock = func() time.Time { return now }
	phases := []session.OperationPhase{{
		ID:          "phase-observe",
		Summary:     "Observe local state.",
		Status:      session.PlanStatusCompleted,
		CompletedAt: now.Add(-time.Minute),
	}}
	for i, action := range []string{"github_issue_create", "github_issue_comment", "github_issue_label"} {
		phaseID := fmt.Sprintf("phase-github-%d", i+1)
		phases = append(phases, session.OperationPhase{
			ID:             phaseID,
			Summary:        "Prepare the next bounded GitHub issue step.",
			Status:         session.PlanStatusPending,
			AllowedActions: []string{action},
			RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
				Kind:           session.CapabilityKindExternalAccount,
				TargetResource: fmt.Sprintf("github:idolum-ai/aphelion:%d", i+1),
				GrantedTo:      "telegram:1001",
				AllowedActions: []string{action},
				Contract:       `{"bounded_effect":"One issue operation only."}`,
				Constraints:    fmt.Sprintf(`{"repository":"idolum-ai/aphelion","phase":%d}`, i+1),
			}},
		})
	}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-lookahead-cluster",
		Objective: "Complete several bounded GitHub issue steps.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-lookahead-cluster",
			Goal:           "Exercise deep bounded lookahead.",
			CurrentPhaseID: "phase-observe",
			Phases:         phases,
		},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	event := session.ReviewEvent{
		ID:                92,
		SourceSessionID:   session.SessionIDForKey(key),
		TargetSessionID:   session.SessionIDForKey(key),
		TargetAdminChatID: 1001,
		Status:            "delivered",
		DeliveryMessageID: 92,
	}
	text, err := rt.HandleReviewEventAction(context.Background(), telegram.CallbackQuery{
		ID:      "cb-lookahead-cluster",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 92, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionLookaheadNext),
	}, event, core.ReviewEventActionLookaheadNext)
	if err != nil {
		t.Fatalf("HandleReviewEventAction(cluster lookahead) err = %v", err)
	}
	if !strings.Contains(text, "Next authority approval surfaced") || !strings.Contains(text, "No authority was approved or executed") {
		t.Fatalf("lookahead text = %q, want bounded cluster acknowledgement", text)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one clustered simulated approval card", inlineCount)
	}
	for _, want := range []string{"github_issue_create", "github_issue_comment", "github_issue_label"} {
		if !strings.Contains(inlineText, want) {
			t.Fatalf("inline text = %q, want clustered approval card containing %q", inlineText, want)
		}
	}
	count, err := store.OutstandingLookaheadApprovalFrontierCountAt(1001, now)
	if err != nil {
		t.Fatalf("OutstandingLookaheadApprovalFrontierCountAt() err = %v", err)
	}
	if count != 1 {
		t.Fatalf("outstanding lookahead allowances = %d, want one bound clustered approval", count)
	}
	projections, err := store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		Limit:       20,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries() err = %v", err)
	}
	var proposed int
	var operatorObservations int
	for _, projection := range projections {
		if projection.Entry.Status == session.IdentificationLedgerStatusProposed {
			proposed++
		}
		for _, observation := range projection.Observations {
			if observation.Property == session.IdentificationPropertyOperatorAction &&
				observation.ActorPrincipal == "telegram:1001" &&
				observation.ActorAction == string(core.ReviewEventActionLookaheadNext) {
				operatorObservations++
			}
		}
	}
	if proposed != 1 || operatorObservations != 1 {
		t.Fatalf("proposed=%d operatorObservations=%d projections=%#v, want one clustered ledgered lookahead approval with operator provenance", proposed, operatorObservations, projections)
	}
}

func TestAuthorityDiscoverySacrificeDemoAuditsApprovalExecutionAndRelease(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	now := time.Date(2099, 7, 3, 14, 0, 0, 0, time.UTC)
	rt.authorityDiscoveryClock = func() time.Time { return now }
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-sacrifice-demo",
		Objective: "Demonstrate a bounded local irreversible effect without external IO.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-sacrifice-demo",
			Goal:           "Show the whole identification frontier theorem.",
			CurrentPhaseID: "phase-sacrifice",
			Phases: []session.OperationPhase{{
				ID:             "phase-sacrifice",
				Summary:        "Commit one fake local irreversible effect record.",
				Status:         session.PlanStatusPending,
				AllowedActions: []string{"fake_irreversible_effect"},
				ValidationPlan: []string{"record exactly one fake effect attempt and stop"},
				RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
					Kind:           session.CapabilityKindGenericDelegation,
					TargetResource: "workspace:/tmp/aphelion-sacrifice-demo",
					GrantedTo:      "telegram:1001",
					AllowedActions: []string{"fake_irreversible_effect"},
					Contract:       `{"bounded_effect":"Record one fake local irreversible effect only."}`,
					Constraints:    `{"path":"/tmp/aphelion-sacrifice-demo","fake":true}`,
				}},
			}},
		},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	event := session.ReviewEvent{
		ID:                193,
		SourceSessionID:   session.SessionIDForKey(key),
		TargetSessionID:   session.SessionIDForKey(key),
		TargetAdminChatID: 1001,
		Status:            "delivered",
		DeliveryMessageID: 193,
	}
	text, err := rt.HandleReviewEventAction(context.Background(), telegram.CallbackQuery{
		ID:      "cb-lookahead-sacrifice-demo",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 193, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionLookaheadNext),
	}, event, core.ReviewEventActionLookaheadNext)
	if err != nil {
		t.Fatalf("HandleReviewEventAction(sacrifice lookahead) err = %v", err)
	}
	if !strings.Contains(text, "approval surfaced") || !strings.Contains(text, "No authority was approved or executed") {
		t.Fatalf("lookahead text = %q, want surfaced non-executing approval", text)
	}
	assertLookaheadLabelsWithoutApprovalHaveNoExecutionEvents(t, store, key)
	beforeApproval, err := rt.AuthorityFrontierStatus(context.Background(), 1001)
	if err != nil {
		t.Fatalf("AuthorityFrontierStatus(before approval) err = %v", err)
	}
	if beforeApproval.Used != 1 || beforeApproval.Open != 1 {
		t.Fatalf("frontier before approval = %#v, want one open slot", beforeApproval)
	}
	entry := singleLookaheadLedgerEntry(t, store, key)
	labelRef := entry.LabelRef
	if labelRef == "" {
		t.Fatalf("lookahead entry = %#v, want label ref", entry)
	}

	approvedAt := now.Add(time.Minute)
	if err := rt.recordAuthorityDiscoveryOperatorObservationForLabel(key, labelRef, "telegram:1001", "approve_continuation", approvedAt); err != nil {
		t.Fatalf("recordAuthorityDiscoveryOperatorObservationForLabel() err = %v", err)
	}
	if err := rt.markAuthorityDiscoveryLabelStatus(key, labelRef, session.IdentificationLedgerStatusApproved, now.Add(30*time.Minute), approvedAt); err != nil {
		t.Fatalf("markAuthorityDiscoveryLabelStatus(approved) err = %v", err)
	}
	afterApproval, err := rt.AuthorityFrontierStatus(context.Background(), 1001)
	if err != nil {
		t.Fatalf("AuthorityFrontierStatus(after approval) err = %v", err)
	}
	if afterApproval.Used != 0 || afterApproval.Empty != MaxOutstandingLookaheadApprovalFrontiers {
		t.Fatalf("frontier after approval = %#v, want released meter", afterApproval)
	}

	executedAt := now.Add(2 * time.Minute)
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   core.ExecutionEventWorkExecutorStarted,
		Stage:       "work",
		Status:      "started",
		PayloadJSON: fmt.Sprintf(`{"label_ref":%q,"effect":"fake_irreversible_effect"}`, labelRef),
		CreatedAt:   executedAt,
	}); err != nil {
		t.Fatalf("AppendExecutionEvent(work started) err = %v", err)
	}
	if _, err := store.UpsertEffectAttempt(session.EffectAttemptInput{
		Key:                 key,
		TurnRunID:           501,
		OperationID:         "op-sacrifice-demo",
		PhaseID:             "phase-sacrifice",
		WorkMode:            "fake_irreversible",
		Executor:            "test",
		Tool:                "effect_attempt",
		Command:             "record fake irreversible effect",
		EffectKind:          "fake_irreversible_effect",
		EffectReason:        "sacrifice demo",
		BoundaryKind:        "approved_authority_bundle",
		AuthorizationReason: labelRef,
		SubjectJSON:         fmt.Sprintf(`{"label_ref":%q}`, labelRef),
		Status:              session.EffectAttemptStatusExecuted,
		EvidenceRefs:        []string{"authority_label:" + labelRef},
		StartedAt:           executedAt,
		CompletedAt:         executedAt,
		UpdatedAt:           executedAt,
	}); err != nil {
		t.Fatalf("UpsertEffectAttempt() err = %v", err)
	}
	if err := rt.markAuthorityDiscoveryLabelStatus(key, labelRef, session.IdentificationLedgerStatusConsumed, time.Time{}, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("markAuthorityDiscoveryLabelStatus(consumed) err = %v", err)
	}
	projection := ledgerProjectionForLabel(t, store, key, labelRef)
	if projection.Entry.Status != session.IdentificationLedgerStatusConsumed {
		t.Fatalf("ledger status = %q, want consumed", projection.Entry.Status)
	}
	assertLedgerProjectionHasOperatorAction(t, projection, "telegram:1001", string(core.ReviewEventActionLookaheadNext))
	assertLedgerProjectionHasOperatorAction(t, projection, "telegram:1001", "approve_continuation")
	biography := ProjectAuthorityDiscoveryLedgerBiography(projection)
	for _, want := range []string{
		"operator:operator_action=lookahead_next actor=telegram:1001/lookahead_next",
		"operator:operator_action=approve_continuation actor=telegram:1001/approve_continuation",
		"status:consumed",
	} {
		if !authorityDiscoveryBiographyContains(biography, want) {
			t.Fatalf("biography = %#v, want line containing %q", biography, want)
		}
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !executionEventsContainLabel(events, core.ExecutionEventWorkExecutorStarted, labelRef) {
		t.Fatalf("events = %#v, want work executor event tied to label %s", events, labelRef)
	}
}

func TestLookaheadSimulationSkipsCoveredPhaseGrant(t *testing.T) {
	t.Parallel()

	_, store, _, _ := buildRuntimeFixtures(t)
	rt := &Runtime{store: store}
	now := time.Now().UTC()
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-covered-search",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github:idolum-ai/CopilotKit",
		AllowedActions: []string{"github_issue_search"},
		Status:         session.CapabilityGrantStatusActive,
		GrantedAt:      now,
		ExpiresAt:      now.Add(time.Hour),
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	opState := session.OperationState{
		ID: "op-lookahead-covered",
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-lookahead-covered",
			CurrentPhaseID: "phase-covered",
			Phases: []session.OperationPhase{{
				ID:      "phase-covered",
				Summary: "Search existing issues.",
				Status:  session.PlanStatusPending,
				RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
					Kind:           session.CapabilityKindExternalAccount,
					TargetResource: "github:idolum-ai/CopilotKit",
					GrantedTo:      "telegram:1001",
					AllowedActions: []string{"github_issue_search"},
				}},
			}, {
				ID:      "phase-missing",
				Summary: "Create follow-up issue.",
				Status:  session.PlanStatusPending,
				RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
					Kind:           session.CapabilityKindExternalAccount,
					TargetResource: "github:idolum-ai/CopilotKit",
					GrantedTo:      "telegram:1001",
					AllowedActions: []string{"github_issue_create"},
				}},
			}},
		},
	}
	phase, grants, ok, err := rt.nextLookaheadPhaseCapabilityCollision(opState, now)
	if err != nil {
		t.Fatalf("nextLookaheadPhaseCapabilityCollision() err = %v", err)
	}
	if !ok || phase.ID != "phase-missing" {
		t.Fatalf("phase = %#v ok=%v, want phase-missing after covered phase skipped", phase, ok)
	}
	if len(grants) != 1 || grants[0].AllowedActions[0] != "github_issue_create" {
		t.Fatalf("grants = %#v, want missing create grant", grants)
	}
}

func TestReviewEventLookaheadMeterBlocksAtOutstandingCap(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	adminChatID := int64(1001)
	now := time.Date(2026, 7, 3, 14, 0, 0, 0, time.UTC)
	rt := &Runtime{store: store, authorityDiscoveryClock: func() time.Time { return now }}
	for i := 0; i < MaxOutstandingLookaheadApprovalFrontiers; i++ {
		seedOutstandingLookaheadApprovalFrontier(t, store, session.SessionKey{
			ChatID: adminChatID,
			Scope:  session.TelegramThreadScopeRef(adminChatID, int64(i+1)),
		}, "full-meter", i, now.Add(time.Duration(i)*time.Second))
	}
	before, err := store.OutstandingLookaheadApprovalFrontierCountAt(adminChatID, now)
	if err != nil {
		t.Fatalf("OutstandingLookaheadApprovalFrontierCount(before) err = %v", err)
	}
	if before != MaxOutstandingLookaheadApprovalFrontiers {
		t.Fatalf("outstanding before = %d, want cap", before)
	}
	key := session.SessionKey{ChatID: adminChatID, Scope: telegramDMScopeRef(adminChatID)}
	seedLookaheadOperationPhase(t, store, key, "op-full-meter-simulatable", "phase-full-meter-simulatable")
	event := session.ReviewEvent{
		ID:                301,
		SourceSessionID:   "telegram_dm:1001",
		TargetSessionID:   "telegram_dm:1001",
		TargetAdminChatID: adminChatID,
		Status:            "delivered",
		DeliveryMessageID: 301,
	}
	text, err := rt.HandleReviewEventAction(context.Background(), telegram.CallbackQuery{
		ID:      "cb-lookahead-meter-full",
		From:    &telegram.User{ID: adminChatID},
		Message: &telegram.Message{MessageID: 301, Chat: &telegram.Chat{ID: adminChatID}},
		Data:    core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionLookaheadNext),
	}, event, core.ReviewEventActionLookaheadNext)
	if err != nil {
		t.Fatalf("HandleReviewEventAction(meter full) err = %v", err)
	}
	if !strings.Contains(text, "Next grant is paused") || !strings.Contains(text, "No authority was approved or executed") {
		t.Fatalf("lookahead text = %q, want paused non-executing meter response", text)
	}
	after, err := store.OutstandingLookaheadApprovalFrontierCountAt(adminChatID, now)
	if err != nil {
		t.Fatalf("OutstandingLookaheadApprovalFrontierCount(after) err = %v", err)
	}
	if after != before {
		t.Fatalf("outstanding after = %d, want unchanged %d", after, before)
	}
	projections, err := store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		PlanID:      session.IdentificationPlanIDForSession("telegram_dm:1001"),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   "telegram_dm:1001",
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries() err = %v", err)
	}
	if len(projections) != 0 {
		t.Fatalf("ledger projections = %#v, want no lookahead ledger write while meter is full", projections)
	}
	actions, err := store.OpenNextActionsBySessionOperation(key, session.NextActionBlockedNeedsAuthority, "request_approval", "authority_bundle_request", 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySessionOperation() err = %v", err)
	}
	if len(actions) != 0 {
		t.Fatalf("authority bundle next actions = %#v, want full meter to stop before simulated publication", actions)
	}
	events, err := store.ExecutionEventsBySession(session.SessionKey{ChatID: adminChatID, Scope: telegramDMScopeRef(adminChatID)}, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	foundMeterFull := false
	for _, event := range events {
		if event.EventType == core.ExecutionEventAuthorityFindingReviewed && event.Status == "lookahead_meter_full" {
			foundMeterFull = true
		}
	}
	if !foundMeterFull {
		t.Fatalf("events = %#v, want lookahead_meter_full event", events)
	}
}

func TestReviewEventLookaheadMeterSlotFreeingAndAdminScope(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	adminA := int64(1001)
	adminB := int64(1002)
	now := time.Now().UTC().Truncate(time.Second)
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.authorityDiscoveryClock = func() time.Time { return now }
	var seeded []seededLookaheadFrontier
	for i := 0; i < MaxOutstandingLookaheadApprovalFrontiers; i++ {
		seeded = append(seeded, seedOutstandingLookaheadApprovalFrontier(t, store, session.SessionKey{
			ChatID: adminA,
			Scope:  session.TelegramThreadScopeRef(adminA, int64(i+1)),
		}, "scope-meter", i, now.Add(time.Duration(i)*time.Second)))
	}
	seedLookaheadOperationPhase(t, store, session.SessionKey{ChatID: adminB, Scope: telegramDMScopeRef(adminB)}, "op-admin-b", "phase-admin-b")
	text, err := rt.HandleReviewEventAction(context.Background(), telegram.CallbackQuery{
		ID:      "cb-lookahead-admin-b",
		From:    &telegram.User{ID: adminB},
		Message: &telegram.Message{MessageID: 401, Chat: &telegram.Chat{ID: adminB}},
		Data:    core.EncodeReviewEventCallbackData(401, core.ReviewEventActionLookaheadNext),
	}, session.ReviewEvent{
		ID:                401,
		SourceSessionID:   session.SessionIDForKey(session.SessionKey{ChatID: adminB, Scope: telegramDMScopeRef(adminB)}),
		TargetSessionID:   session.SessionIDForKey(session.SessionKey{ChatID: adminB, Scope: telegramDMScopeRef(adminB)}),
		TargetAdminChatID: adminB,
		Status:            "delivered",
		DeliveryMessageID: 401,
	}, core.ReviewEventActionLookaheadNext)
	if err != nil {
		t.Fatalf("HandleReviewEventAction(admin B) err = %v", err)
	}
	if strings.Contains(text, "Next grant is paused") {
		t.Fatalf("admin B lookahead text = %q, want admin A cap not to apply", text)
	}
	countB, err := store.OutstandingLookaheadApprovalFrontierCountAt(adminB, now)
	if err != nil {
		t.Fatalf("OutstandingLookaheadApprovalFrontierCount(admin B) err = %v", err)
	}
	if countB != 1 {
		t.Fatalf("admin B outstanding = %d, want one newly simulated frontier", countB)
	}
	if err := store.ResolveNextAction(session.NextActionResolutionInput{
		Key:        seeded[0].Key,
		RecordID:   seeded[0].Record.RecordID,
		Owner:      "test",
		Reason:     "test_resolved",
		ResolvedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("ResolveNextAction(seed) err = %v", err)
	}
	if _, err := store.RecordIdentificationLedgerEntry(session.IdentificationLedgerEntryInput{
		EntryID:     seeded[0].EntryID,
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(seeded[0].Key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(seeded[0].Key),
		StepRef:     "next_action:" + seeded[0].Record.RecordID,
		ShapeHash: session.AuthorityShapeHash(session.AuthorityShapeInput{
			Tool:          "request_approval",
			Action:        seeded[0].Record.OperationKind,
			ResourceClass: "authority_bundle",
		}),
		LabelRef:  seeded[0].Record.SubjectRef,
		Status:    session.IdentificationLedgerStatusConsumed,
		UpdatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("RecordIdentificationLedgerEntry(consumed seed) err = %v", err)
	}
	seedLookaheadOperationPhase(t, store, session.SessionKey{ChatID: adminA, Scope: telegramDMScopeRef(adminA)}, "op-admin-a", "phase-admin-a")
	text, err = rt.HandleReviewEventAction(context.Background(), telegram.CallbackQuery{
		ID:      "cb-lookahead-admin-a-after-free",
		From:    &telegram.User{ID: adminA},
		Message: &telegram.Message{MessageID: 402, Chat: &telegram.Chat{ID: adminA}},
		Data:    core.EncodeReviewEventCallbackData(402, core.ReviewEventActionLookaheadNext),
	}, session.ReviewEvent{
		ID:                402,
		SourceSessionID:   session.SessionIDForKey(session.SessionKey{ChatID: adminA, Scope: telegramDMScopeRef(adminA)}),
		TargetSessionID:   session.SessionIDForKey(session.SessionKey{ChatID: adminA, Scope: telegramDMScopeRef(adminA)}),
		TargetAdminChatID: adminA,
		Status:            "delivered",
		DeliveryMessageID: 402,
	}, core.ReviewEventActionLookaheadNext)
	if err != nil {
		t.Fatalf("HandleReviewEventAction(admin A after free) err = %v", err)
	}
	if strings.Contains(text, "Next grant is paused") {
		t.Fatalf("admin A lookahead text = %q, want freed slot to allow one new frontier", text)
	}
	countA, err := store.OutstandingLookaheadApprovalFrontierCountAt(adminA, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("OutstandingLookaheadApprovalFrontierCount(admin A) err = %v", err)
	}
	if countA != MaxOutstandingLookaheadApprovalFrontiers {
		t.Fatalf("admin A outstanding = %d, want cap refilled after one slot freed", countA)
	}
}

func TestReviewEventLookaheadSimulationMaterializationFailureDoesNotLeaveProposedToken(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	rt := &Runtime{store: store}
	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	now := time.Date(2026, 7, 3, 13, 0, 0, 0, time.UTC)
	if err := store.UpdateOperationState(key, session.OperationState{
		ID: "op-lookahead-no-materialization",
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-lookahead-no-materialization",
			CurrentPhaseID: "phase-missing",
			Phases: []session.OperationPhase{{
				ID:      "phase-missing",
				Summary: "Create follow-up issue.",
				Status:  session.PlanStatusPending,
				RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
					Kind:           session.CapabilityKindExternalAccount,
					TargetResource: "github:idolum-ai/CopilotKit",
					GrantedTo:      "telegram:1001",
					AllowedActions: []string{"github_issue_create"},
				}},
			}},
		},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	event := session.ReviewEvent{
		ID:                92,
		SourceSessionID:   session.SessionIDForKey(key),
		TargetSessionID:   session.SessionIDForKey(key),
		TargetAdminChatID: 1001,
		Status:            "delivered",
		DeliveryMessageID: 92,
	}
	text, err := rt.HandleReviewEventAction(context.Background(), telegram.CallbackQuery{
		ID:      "cb-lookahead-simulation-no-materialization",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 92, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionLookaheadNext),
	}, event, core.ReviewEventActionLookaheadNext)
	if err != nil {
		t.Fatalf("HandleReviewEventAction(simulated no materialization) err = %v", err)
	}
	if !strings.Contains(text, "could not be materialized") || !strings.Contains(text, "No authority was approved or executed") {
		t.Fatalf("lookahead text = %q, want no-materialization acknowledgement", text)
	}
	projections, err := store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		Limit:       20,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries() err = %v", err)
	}
	for _, projection := range projections {
		if projection.Entry.StepRef == "operation_phase:phase-missing" && projection.Entry.Status == session.IdentificationLedgerStatusProposed {
			t.Fatalf("ledger projections = %#v, want no proposed simulated token without materialized card", projections)
		}
	}
	open, err := store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	for _, action := range open {
		if action.Owner == "lookahead" && action.OperationKind == "authority_bundle_request" {
			t.Fatalf("open next actions = %#v, want simulated lookahead action retired after materialization failure", open)
		}
	}
}

func TestLookaheadAuthorityFrontierSplitsSameStepDifferentAuthorityBundles(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	rt := &Runtime{store: store}
	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	now := time.Date(2026, 7, 3, 13, 15, 0, 0, time.UTC)
	actionA := recordAuthorityBundleLookaheadActionForTest(t, store, key, "same-step-a", "github_issue_create", now)
	actionB := recordAuthorityBundleLookaheadActionForTest(t, store, key, "same-step-b", "github_issue_comment", now.Add(time.Minute))
	event := session.ReviewEvent{
		ID:                93,
		SourceSessionID:   session.SessionIDForKey(key),
		TargetSessionID:   session.SessionIDForKey(key),
		TargetAdminChatID: 1001,
		Status:            "delivered",
		DeliveryMessageID: 93,
	}
	entryA, err := rt.recordLookaheadAuthorityFrontier(event, key, actionA, "telegram:1001", now)
	if err != nil {
		t.Fatalf("recordLookaheadAuthorityFrontier(A) err = %v", err)
	}
	entryB, err := rt.recordLookaheadAuthorityFrontier(event, key, actionB, "telegram:1001", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("recordLookaheadAuthorityFrontier(B) err = %v", err)
	}
	if entryA == entryB {
		t.Fatalf("entry IDs are equal for distinct authority bundles: %s", entryA)
	}
	projections, err := store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		Limit:       20,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries() err = %v", err)
	}
	var base, generated session.IdentificationLedgerEntry
	for _, projection := range projections {
		switch projection.Entry.LabelRef {
		case actionA.SubjectRef:
			base = projection.Entry
		case actionB.SubjectRef:
			generated = projection.Entry
		}
	}
	if base.EntryID == "" || generated.EntryID == "" {
		t.Fatalf("projections = %#v, want entries for both authority bundles", projections)
	}
	if base.ShapeHash == "" || generated.ShapeHash != base.ShapeHash {
		t.Fatalf("shape hashes base=%q generated=%q, want same reusable shape", base.ShapeHash, generated.ShapeHash)
	}
	if base.StepRef != "operation_phase:phase-same" {
		t.Fatalf("base step_ref = %q, want original phase step", base.StepRef)
	}
	if !strings.HasPrefix(generated.StepRef, "operation_phase:phase-same#collision:") || !strings.Contains(generated.StepRef, actionB.RecordID) {
		t.Fatalf("generated step_ref = %q, want collision generation for %s", generated.StepRef, actionB.RecordID)
	}
}

func TestBuildAuthorityDiscoveryMenuRequiresExactLiveAuthority(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	rt := &Runtime{store: store}
	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	contract, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   "menu-live-child-wake",
		SessionID:           session.SessionIDForKey(key),
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "idolum-email", "grant-idolum-email-wake", "durable_agent", "wake_once", ""),
		Principal:           "telegram:1001",
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		AllowedActions:      []string{"wake_named_child"},
		Constraints:         map[string]string{"agent_id": "idolum-email"},
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             "idolum-email",
		GrantID:             "grant-idolum-email-wake",
		GrantTargetResource: "durable_agent:idolum-email:wake_once",
		CreatedAt:           now,
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract() err = %v", err)
	}
	contract, err = store.UpsertContinuationRecoveryContract(contract)
	if err != nil {
		t.Fatalf("UpsertContinuationRecoveryContract() err = %v", err)
	}
	if _, err := store.RecordIdentificationLedgerEntry(session.IdentificationLedgerEntryInput{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		StepRef:     "step:wake-child",
		ShapeHash:   session.AuthorityShapeHashForContinuationRecoveryContract(contract),
		LabelRef:    contract.ContractID,
		Status:      session.IdentificationLedgerStatusApproved,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("RecordIdentificationLedgerEntry() err = %v", err)
	}
	menu, err := rt.BuildAuthorityDiscoveryMenu(AuthorityDiscoveryMenuBuildInput{Key: key, Now: now})
	if err != nil {
		t.Fatalf("BuildAuthorityDiscoveryMenu(no live authority) err = %v", err)
	}
	if len(menu.Tokens) != 1 {
		t.Fatalf("tokens = %#v, want one", menu.Tokens)
	}
	if menu.Tokens[0].State == AuthorityDiscoveryTokenExecutable {
		t.Fatalf("token state = executable with only ledger shape; want non-executable without live lease")
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		RemainingTurns: 1,
		ContinuationLease: session.ContinuationLease{
			ID:                 "lease-menu-live-child-wake",
			Status:             session.ContinuationLeaseStatusActive,
			LeaseClass:         session.ContinuationLeaseClassChildWake,
			AllowedActions:     []string{"wake_named_child"},
			Constraints:        map[string]string{"agent_id": "idolum-email"},
			RecoveryContractID: contract.ContractID,
			RemainingTurns:     1,
			ExpiresAt:          now.Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState(active lease) err = %v", err)
	}
	menu, err = rt.BuildAuthorityDiscoveryMenu(AuthorityDiscoveryMenuBuildInput{Key: key, Now: now})
	if err != nil {
		t.Fatalf("BuildAuthorityDiscoveryMenu(live authority) err = %v", err)
	}
	if len(menu.Tokens) != 1 || menu.Tokens[0].State != AuthorityDiscoveryTokenExecutable {
		t.Fatalf("tokens = %#v, want exact live lease to make token executable", menu.Tokens)
	}
}

func TestAuthorityDiscoveryLifecyclePromotesApprovedContinuationAndSplitsRepeatCollision(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 2003, UserID: 0, Scope: telegramDMScopeRef(2003)}
	sessionID := session.SessionIDForKey(key)
	now := time.Date(2026, 7, 2, 12, 15, 0, 0, time.UTC)
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-authority-discovery-lifecycle",
		Status:    session.OperationStatusActive,
		PhasePlan: session.OperationPhasePlan{ID: "plan-authority-discovery-lifecycle", Goal: "Recover child wake"},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	subjectRef := session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "idolum-email", "grant-idolum-email-wake", "durable_agent", "wake_once", "")
	contractA, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   "authority-discovery-lifecycle-a",
		SessionID:           sessionID,
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          subjectRef,
		Principal:           "telegram:1001",
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		AllowedActions:      []string{"wake_named_child"},
		Constraints:         map[string]string{"agent_id": "idolum-email"},
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             "idolum-email",
		GrantID:             "grant-idolum-email-wake",
		GrantTargetResource: "durable_agent:idolum-email:wake_once",
		CreatedAt:           now,
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract(A) err = %v", err)
	}
	_, actionA, err := store.RecordContinuationRecoveryContractNextAction(contractA, session.NextActionInput{
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         contractA.SubjectRef,
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		RetryPolicy:        "ask_for_grant",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: session.ContinuationRecoveryContractProjectionInput(contractA.ContractID),
		CreatedAt:          now,
	})
	if err != nil {
		t.Fatalf("RecordContinuationRecoveryContractNextAction(A) err = %v", err)
	}
	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: key.ChatID, SenderID: 1001, Text: "show approval", MessageID: 1},
		"show approval",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want recovery approval card")
	}
	approved, err := rt.ApproveContinuationForKey(key, 1001)
	if err != nil {
		t.Fatalf("ApproveContinuationForKey() err = %v", err)
	}
	if approved.Status != session.ContinuationStatusApproved || approved.ContinuationLease.Status != session.ContinuationLeaseStatusActive {
		t.Fatalf("approved continuation = %#v, want active continuation", approved)
	}
	menu, err := rt.BuildAuthorityDiscoveryMenu(AuthorityDiscoveryMenuBuildInput{Key: key, Now: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("BuildAuthorityDiscoveryMenu(approved) err = %v", err)
	}
	foundExecutable := false
	for _, token := range menu.Tokens {
		if token.LabelRef == contractA.ContractID && token.State == AuthorityDiscoveryTokenExecutable {
			foundExecutable = true
		}
	}
	if !foundExecutable {
		t.Fatalf("tokens = %#v, want approved exact contract to be executable", menu.Tokens)
	}
	_, reservation, _, _, err := rt.reserveApprovedContinuationTurn(key)
	if err != nil {
		t.Fatalf("reserveApprovedContinuationTurn() err = %v", err)
	}
	if reservation == nil {
		t.Fatal("reservation = nil, want consumed approved continuation")
	}
	menu, err = rt.BuildAuthorityDiscoveryMenu(AuthorityDiscoveryMenuBuildInput{Key: key, Now: now.Add(2 * time.Minute)})
	if err != nil {
		t.Fatalf("BuildAuthorityDiscoveryMenu(consumed) err = %v", err)
	}
	foundSpent := false
	for _, token := range menu.Tokens {
		if token.LabelRef == contractA.ContractID && token.State == AuthorityDiscoveryTokenSpent {
			foundSpent = true
		}
	}
	if !foundSpent {
		t.Fatalf("tokens = %#v, want consumed exact contract to be spent", menu.Tokens)
	}
	contractB, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   "authority-discovery-lifecycle-b",
		SessionID:           sessionID,
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          subjectRef,
		Principal:           "telegram:1001",
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		AllowedActions:      []string{"wake_named_child"},
		Constraints:         map[string]string{"agent_id": "idolum-email"},
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             "idolum-email",
		GrantID:             "grant-idolum-email-wake",
		GrantTargetResource: "durable_agent:idolum-email:wake_once",
		CreatedAt:           now.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract(B) err = %v", err)
	}
	_, actionB, err := store.RecordContinuationRecoveryContractNextAction(contractB, session.NextActionInput{
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         contractB.SubjectRef,
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		RetryPolicy:        "ask_for_grant",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: session.ContinuationRecoveryContractProjectionInput(contractB.ContractID),
		CreatedAt:          now.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("RecordContinuationRecoveryContractNextAction(B) err = %v", err)
	}
	projections, err := store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		PlanID:      session.IdentificationPlanIDForSession(sessionID),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   sessionID,
		LabelRef:    contractB.ContractID,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries(contractB) err = %v", err)
	}
	foundGenerated := false
	for _, projection := range projections {
		if projection.Entry.LabelRef == contractB.ContractID &&
			strings.HasPrefix(projection.Entry.StepRef, subjectRef+"#collision:") &&
			strings.Contains(projection.Entry.StepRef, actionB.RecordID) {
			foundGenerated = true
		}
	}
	if !foundGenerated {
		t.Fatalf("contractB projections = %#v, want new collision generation after consumed base; first action was %s", projections, actionA.RecordID)
	}
}

func TestBuildAuthorityDiscoveryMenuDoesNotTreatShapeMismatchedGrantAsExecutable(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	rt := &Runtime{store: store}
	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	now := time.Date(2026, 7, 2, 12, 30, 0, 0, time.UTC)
	grant, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-github-write",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github:idolum-ai/aphelion",
		AllowedActions: []string{"github_issue_create"},
		Status:         session.CapabilityGrantStatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
		GrantedAt:      now,
		ExpiresAt:      now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	wrongShape := session.AuthorityShapeHash(session.AuthorityShapeInput{
		Action:        "github_pr_update",
		ResourceClass: "github",
	})
	if _, err := store.RecordIdentificationLedgerEntry(session.IdentificationLedgerEntryInput{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		StepRef:     "step:update-pr",
		ShapeHash:   wrongShape,
		LabelRef:    grant.GrantID,
		Status:      session.IdentificationLedgerStatusApproved,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("RecordIdentificationLedgerEntry() err = %v", err)
	}
	menu, err := rt.BuildAuthorityDiscoveryMenu(AuthorityDiscoveryMenuBuildInput{Key: key, Now: now})
	if err != nil {
		t.Fatalf("BuildAuthorityDiscoveryMenu(wrong shape) err = %v", err)
	}
	if len(menu.Tokens) != 1 {
		t.Fatalf("tokens = %#v, want one", menu.Tokens)
	}
	if menu.Tokens[0].State == AuthorityDiscoveryTokenExecutable {
		t.Fatalf("token state = executable for shape-mismatched grant %#v", menu.Tokens[0])
	}
	rightShape := session.AuthorityShapeHash(session.AuthorityShapeInput{
		Action:        "github_issue_create",
		ResourceClass: "github",
	})
	if _, err := store.RecordIdentificationLedgerEntry(session.IdentificationLedgerEntryInput{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		StepRef:     "step:create-issue",
		ShapeHash:   rightShape,
		LabelRef:    grant.GrantID,
		Status:      session.IdentificationLedgerStatusApproved,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("RecordIdentificationLedgerEntry(right shape) err = %v", err)
	}
	menu, err = rt.BuildAuthorityDiscoveryMenu(AuthorityDiscoveryMenuBuildInput{Key: key, Now: now})
	if err != nil {
		t.Fatalf("BuildAuthorityDiscoveryMenu(right shape) err = %v", err)
	}
	foundExecutable := false
	for _, token := range menu.Tokens {
		if token.StepRef == "step:create-issue" && token.State == AuthorityDiscoveryTokenExecutable {
			foundExecutable = true
		}
	}
	if !foundExecutable {
		t.Fatalf("tokens = %#v, want shape-matched active grant executable", menu.Tokens)
	}
}

func TestAuthorityDiscoveryAuthorityBundleLivenessChecksEveryGrantSpecAction(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	rt := &Runtime{store: store}
	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	now := time.Date(2026, 7, 2, 12, 45, 0, 0, time.UTC)
	grant, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-github-second-action",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github:idolum-ai/aphelion",
		AllowedActions: []string{"pull_requests_write"},
		Status:         session.CapabilityGrantStatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
		GrantedAt:      now,
		ExpiresAt:      now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	bundle, err := session.CompileAuthorityBundleContract(session.AuthorityBundleContractInput{
		RequestInstanceID: "bundle-second-action-live",
		SessionID:         session.SessionIDForKey(key),
		Principal:         "telegram:1001",
		Summary:           "Use one bounded GitHub grant.",
		AllowedActions:    []string{"contents_write", "pull_requests_write"},
		ForbiddenActions:  []string{"deploy", "merge"},
		StopConditions:    []string{"stop after metadata update"},
		RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
			GrantID:        grant.GrantID,
			Kind:           session.CapabilityKindExternalAccount,
			TargetResource: "github:idolum-ai/aphelion",
			GrantedTo:      "telegram:1001",
			AllowedActions: []string{"contents_write", "pull_requests_write"},
		}},
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CompileAuthorityBundleContract() err = %v", err)
	}
	bundle, err = store.UpsertAuthorityBundleContract(bundle)
	if err != nil {
		t.Fatalf("UpsertAuthorityBundleContract() err = %v", err)
	}
	if _, err := store.RecordIdentificationLedgerEntry(session.IdentificationLedgerEntryInput{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		StepRef:     "step:github-pr",
		ShapeHash: session.AuthorityShapeHash(session.AuthorityShapeInput{
			Tool:          "request_approval",
			Action:        "authority_bundle_request",
			ResourceClass: "authority_bundle",
		}),
		LabelRef:  bundle.BundleID,
		Status:    session.IdentificationLedgerStatusApproved,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("RecordIdentificationLedgerEntry() err = %v", err)
	}
	menu, err := rt.BuildAuthorityDiscoveryMenu(AuthorityDiscoveryMenuBuildInput{Key: key, Now: now})
	if err != nil {
		t.Fatalf("BuildAuthorityDiscoveryMenu() err = %v", err)
	}
	if len(menu.Tokens) != 1 || menu.Tokens[0].State != AuthorityDiscoveryTokenExecutable {
		t.Fatalf("tokens = %#v, want bundle live through second required grant action", menu.Tokens)
	}
}

func TestReviewEventLookaheadSurfacesNextRecoveryApprovalCard(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 2002, UserID: 0, Scope: telegramDMScopeRef(2002)}
	now := time.Date(2026, 7, 2, 13, 0, 0, 0, time.UTC)
	contract, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   "lookahead-child-wake-instance",
		SessionID:           session.SessionIDForKey(key),
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "idolum-email", "grant-idolum-email-wake", "durable_agent", "wake_once", ""),
		Principal:           "telegram:1001",
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		AllowedActions:      []string{"wake_named_child"},
		Constraints:         map[string]string{"agent_id": "idolum-email"},
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             "idolum-email",
		GrantID:             "grant-idolum-email-wake",
		GrantTargetResource: "durable_agent:idolum-email:wake_once",
		CreatedAt:           now,
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract() err = %v", err)
	}
	_, action, err := store.RecordContinuationRecoveryContractNextAction(contract, session.NextActionInput{
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         contract.SubjectRef,
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		RetryPolicy:        "ask_for_grant",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: session.ContinuationRecoveryContractProjectionInput(contract.ContractID),
		CreatedAt:          now,
	})
	if err != nil {
		t.Fatalf("RecordContinuationRecoveryContractNextAction() err = %v", err)
	}
	event := session.ReviewEvent{
		ID:                88,
		SourceSessionID:   session.SessionIDForKey(key),
		TargetSessionID:   "telegram_dm:1001",
		TargetAdminChatID: 1001,
		Summary:           "Approved current grant.",
		Status:            "delivered",
		DeliveryMessageID: 55,
	}
	text, err := rt.HandleReviewEventAction(context.Background(), telegram.CallbackQuery{
		ID:      "cb-lookahead-frontier",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 55, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionLookaheadNext),
	}, event, core.ReviewEventActionLookaheadNext)
	if err != nil {
		t.Fatalf("HandleReviewEventAction(lookahead frontier) err = %v", err)
	}
	if !strings.Contains(text, "approval surfaced") || !strings.Contains(text, "No authority was approved or executed") {
		t.Fatalf("lookahead text = %q, want surfaced non-executing approval", text)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one approval card", inlineCount)
	}
	if !strings.Contains(inlineText, "idolum-email") || !strings.Contains(inlineText, "wake") {
		t.Fatalf("inline text = %q, want child wake approval card", inlineText)
	}
	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusPending || state.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("continuation state = %#v, want pending approval only", state)
	}
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	for _, record := range open {
		if record.RecordID == action.RecordID {
			t.Fatalf("open actions = %#v, want surfaced lookahead action resolved", open)
		}
	}
	projections, err := store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries() err = %v", err)
	}
	foundLookahead := false
	var lookaheadProjection session.IdentificationLedgerProjection
	for _, projection := range projections {
		if projection.Entry.StepRef != "next_action:"+action.RecordID {
			continue
		}
		for _, observation := range projection.Observations {
			if observation.Method == session.IdentificationObservationLookahead &&
				observation.Property == session.IdentificationPropertyContract &&
				observation.Value == contract.ContractID {
				foundLookahead = true
				lookaheadProjection = projection
			}
		}
	}
	if !foundLookahead {
		t.Fatalf("ledger projections = %#v, want lookahead contract observation for next action", projections)
	}
	trace := AuthorityDiscoveryTraceForReviewEvent(event, action, lookaheadProjection)
	wantTrace := map[string]string{
		"review_event":                      "88",
		"next_action":                       action.RecordID,
		"contract":                          contract.ContractID,
		"identification_ledger_entry":       lookaheadProjection.Entry.EntryID,
		"identification_ledger_observation": "",
	}
	for _, link := range trace.Links {
		if _, ok := wantTrace[link.Kind]; ok && (wantTrace[link.Kind] == "" || wantTrace[link.Kind] == link.Ref) {
			delete(wantTrace, link.Kind)
		}
	}
	if len(wantTrace) != 0 {
		t.Fatalf("trace links = %#v, missing %#v", trace.Links, wantTrace)
	}
}

func TestLookaheadRecoveryApprovalPropagatesContractLookupError(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	rt := &Runtime{store: store}
	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	now := time.Date(2026, 7, 2, 13, 30, 0, 0, time.UTC)
	contract, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   "lookahead-lookup-error",
		SessionID:           session.SessionIDForKey(key),
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "idolum-email", "grant-idolum-email-wake", "durable_agent", "wake_once", ""),
		Principal:           "telegram:1001",
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		AllowedActions:      []string{"wake_named_child"},
		Constraints:         map[string]string{"agent_id": "idolum-email"},
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             "idolum-email",
		GrantID:             "grant-idolum-email-wake",
		GrantTargetResource: "durable_agent:idolum-email:wake_once",
		CreatedAt:           now,
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract() err = %v", err)
	}
	_, action, err := store.RecordContinuationRecoveryContractNextAction(contract, session.NextActionInput{
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         contract.SubjectRef,
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		RetryPolicy:        "ask_for_grant",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: session.ContinuationRecoveryContractProjectionInput(contract.ContractID),
		CreatedAt:          now,
	})
	if err != nil {
		t.Fatalf("RecordContinuationRecoveryContractNextAction() err = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() err = %v", err)
	}
	if _, err := rt.lookaheadRecoveryApprovalActionExecutable(action, now); err == nil {
		t.Fatalf("lookaheadRecoveryApprovalActionExecutable() err = nil, want contract lookup error")
	}
}

func TestReviewEventLookaheadMaterializationFailureDoesNotLeaveProposedToken(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	rt := &Runtime{store: store}
	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	now := time.Date(2026, 7, 2, 13, 45, 0, 0, time.UTC)
	contract, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   "lookahead-no-materialization",
		SessionID:           session.SessionIDForKey(key),
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "idolum-email", "grant-idolum-email-wake", "durable_agent", "wake_once", ""),
		Principal:           "telegram:1001",
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		AllowedActions:      []string{"wake_named_child"},
		Constraints:         map[string]string{"agent_id": "idolum-email"},
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             "idolum-email",
		GrantID:             "grant-idolum-email-wake",
		GrantTargetResource: "durable_agent:idolum-email:wake_once",
		CreatedAt:           now,
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract() err = %v", err)
	}
	_, action, err := store.RecordContinuationRecoveryContractNextAction(contract, session.NextActionInput{
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         contract.SubjectRef,
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		RetryPolicy:        "ask_for_grant",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: session.ContinuationRecoveryContractProjectionInput(contract.ContractID),
		CreatedAt:          now,
	})
	if err != nil {
		t.Fatalf("RecordContinuationRecoveryContractNextAction() err = %v", err)
	}
	event := session.ReviewEvent{
		ID:                90,
		SourceSessionID:   session.SessionIDForKey(key),
		TargetSessionID:   session.SessionIDForKey(key),
		TargetAdminChatID: 1001,
		MetadataJSON:      `{"next_action_record_id":"` + action.RecordID + `","next_action_session_id":"` + session.SessionIDForKey(key) + `"}`,
		Status:            "delivered",
		DeliveryMessageID: 57,
	}
	text, err := rt.HandleReviewEventAction(context.Background(), telegram.CallbackQuery{
		ID:      "cb-lookahead-no-materialization",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 57, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionLookaheadNext),
	}, event, core.ReviewEventActionLookaheadNext)
	if err != nil {
		t.Fatalf("HandleReviewEventAction(no materialization) err = %v", err)
	}
	if !strings.Contains(text, "could not be materialized") || !strings.Contains(text, "No authority was approved or executed") {
		t.Fatalf("lookahead text = %q, want no-materialization acknowledgement", text)
	}
	projections, err := store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		LabelRef:    contract.ContractID,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries() err = %v", err)
	}
	for _, projection := range projections {
		if projection.Entry.StepRef == "next_action:"+action.RecordID {
			t.Fatalf("lookahead projection = %#v, want no proposed next-action token without materialized card", projection)
		}
	}
}

func TestReviewEventLookaheadLedgerFailureDoesNotLeaveUnmeteredApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	now := time.Date(2026, 7, 2, 14, 0, 0, 0, time.UTC)
	contract, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   "lookahead-ledger-conflict",
		SessionID:           session.SessionIDForKey(key),
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "idolum-email", "grant-idolum-email-wake", "durable_agent", "wake_once", ""),
		Principal:           "telegram:1001",
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		AllowedActions:      []string{"wake_named_child"},
		Constraints:         map[string]string{"agent_id": "idolum-email"},
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             "idolum-email",
		GrantID:             "grant-idolum-email-wake",
		GrantTargetResource: "durable_agent:idolum-email:wake_once",
		CreatedAt:           now,
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract() err = %v", err)
	}
	if _, err := store.UpsertContinuationRecoveryContract(contract); err != nil {
		t.Fatalf("UpsertContinuationRecoveryContract() err = %v", err)
	}
	action, err := store.RecordNextAction(session.NextActionInput{
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         contract.SubjectRef,
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		RetryPolicy:        "ask_for_grant",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: session.ContinuationRecoveryContractProjectionInput(contract.ContractID),
		CreatedAt:          now,
	})
	if err != nil {
		t.Fatalf("RecordNextAction() err = %v", err)
	}
	if _, err := store.RecordIdentificationLedgerEntry(session.IdentificationLedgerEntryInput{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		StepRef:     "next_action:" + action.RecordID,
		ShapeHash:   session.AuthorityShapeHashForContinuationRecoveryContract(contract),
		LabelRef:    "crc-conflicting-existing-label",
		Status:      session.IdentificationLedgerStatusProposed,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("RecordIdentificationLedgerEntry(conflict seed) err = %v", err)
	}
	if _, err := store.RecordIdentificationLedgerEntry(session.IdentificationLedgerEntryInput{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		StepRef:     "next_action:" + action.RecordID + "#collision:" + action.RecordID,
		ShapeHash:   session.AuthorityShapeHashForContinuationRecoveryContract(contract),
		LabelRef:    "crc-conflicting-collision-label",
		Status:      session.IdentificationLedgerStatusProposed,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("RecordIdentificationLedgerEntry(collision conflict seed) err = %v", err)
	}

	event := session.ReviewEvent{
		ID:                94,
		SourceSessionID:   session.SessionIDForKey(key),
		TargetSessionID:   session.SessionIDForKey(key),
		TargetAdminChatID: 1001,
		MetadataJSON:      `{"next_action_record_id":"` + action.RecordID + `","next_action_session_id":"` + session.SessionIDForKey(key) + `"}`,
		Status:            "delivered",
		DeliveryMessageID: 94,
	}
	_, err = rt.HandleReviewEventAction(context.Background(), telegram.CallbackQuery{
		ID:      "cb-lookahead-ledger-conflict",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 94, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionLookaheadNext),
	}, event, core.ReviewEventActionLookaheadNext)
	if err == nil {
		t.Fatal("HandleReviewEventAction(ledger conflict) err = nil, want ledger conflict")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count after ledger conflict = %d, want no unmetered approval card after failed ledger bind", inlineCount)
	}
	if state, exists, stateErr := store.ContinuationStateIfExists(key); stateErr != nil {
		t.Fatalf("ContinuationStateIfExists() err = %v", stateErr)
	} else if exists && state.Status == session.ContinuationStatusPending {
		t.Fatalf("continuation state = %#v, want no pending approval after failed ledger bind", state)
	}
}

func TestReviewEventLookaheadDoesNotFallForwardFromStaleExactFrontier(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	rt := &Runtime{store: store}
	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	now := time.Date(2026, 7, 2, 14, 0, 0, 0, time.UTC)
	contractA, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   "lookahead-stale-a",
		SessionID:           session.SessionIDForKey(key),
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "idolum-email", "grant-idolum-email-wake", "durable_agent", "wake_once", ""),
		Principal:           "telegram:1001",
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		AllowedActions:      []string{"wake_named_child"},
		Constraints:         map[string]string{"agent_id": "idolum-email"},
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             "idolum-email",
		GrantID:             "grant-idolum-email-wake",
		GrantTargetResource: "durable_agent:idolum-email:wake_once",
		CreatedAt:           now,
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract(A) err = %v", err)
	}
	_, actionA, err := store.RecordContinuationRecoveryContractNextAction(contractA, session.NextActionInput{
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         contractA.SubjectRef,
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		RetryPolicy:        "ask_for_grant",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: session.ContinuationRecoveryContractProjectionInput(contractA.ContractID),
		CreatedAt:          now,
	})
	if err != nil {
		t.Fatalf("RecordContinuationRecoveryContractNextAction(A) err = %v", err)
	}
	contractB, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   "lookahead-stale-b",
		SessionID:           session.SessionIDForKey(key),
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          contractA.SubjectRef,
		Principal:           "telegram:1001",
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		AllowedActions:      []string{"wake_named_child"},
		Constraints:         map[string]string{"agent_id": "idolum-email"},
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             "idolum-email",
		GrantID:             "grant-idolum-email-wake",
		GrantTargetResource: "durable_agent:idolum-email:wake_once",
		CreatedAt:           now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract(B) err = %v", err)
	}
	_, actionB, err := store.RecordContinuationRecoveryContractNextAction(contractB, session.NextActionInput{
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         contractB.SubjectRef,
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		RetryPolicy:        "ask_for_grant",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: session.ContinuationRecoveryContractProjectionInput(contractB.ContractID),
		CreatedAt:          now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("RecordContinuationRecoveryContractNextAction(B) err = %v", err)
	}
	if actionA.RecordID == actionB.RecordID {
		t.Fatalf("test setup produced same action record id %q", actionA.RecordID)
	}
	event := session.ReviewEvent{
		ID:                89,
		SourceSessionID:   session.SessionIDForKey(key),
		TargetSessionID:   session.SessionIDForKey(key),
		TargetAdminChatID: 1001,
		Summary:           "Old frontier card.",
		MetadataJSON:      `{"next_action_record_id":"` + actionA.RecordID + `","next_action_session_id":"` + session.SessionIDForKey(key) + `"}`,
		Status:            "delivered",
		DeliveryMessageID: 56,
	}
	text, err := rt.HandleReviewEventAction(context.Background(), telegram.CallbackQuery{
		ID:      "cb-lookahead-stale",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 56, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionLookaheadNext),
	}, event, core.ReviewEventActionLookaheadNext)
	if err != nil {
		t.Fatalf("HandleReviewEventAction(stale exact lookahead) err = %v", err)
	}
	if !strings.Contains(text, "No unresolved authority frontier") {
		t.Fatalf("lookahead text = %q, want stale exact frontier to no-op", text)
	}
	projections, err := store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		Limit:       20,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries() err = %v", err)
	}
	for _, projection := range projections {
		if projection.Entry.StepRef != "next_action:"+actionB.RecordID {
			continue
		}
		for _, observation := range projection.Observations {
			if observation.Method == session.IdentificationObservationLookahead {
				t.Fatalf("newer action %s gained lookahead observation from stale card: %#v", actionB.RecordID, projection)
			}
		}
	}
}

func TestReviewEventLookaheadRejectsMismatchedAdminWithoutLedgerMutation(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	rt := &Runtime{store: store}
	event := session.ReviewEvent{
		ID:                78,
		SourceSessionID:   "telegram_dm:1001",
		TargetSessionID:   "telegram_dm:1001",
		TargetAdminChatID: 1001,
		Status:            "delivered",
		DeliveryMessageID: 45,
	}
	text, err := rt.HandleReviewEventAction(context.Background(), telegram.CallbackQuery{
		ID:      "cb-lookahead-denied",
		From:    &telegram.User{ID: 2002},
		Message: &telegram.Message{MessageID: 45, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionLookaheadNext),
	}, event, core.ReviewEventActionLookaheadNext)
	if err != nil {
		t.Fatalf("HandleReviewEventAction(lookahead denied) err = %v", err)
	}
	if !strings.Contains(text, "Only the target admin") {
		t.Fatalf("denied text = %q, want target-admin response", text)
	}
	projections, err := store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		SessionID: "telegram_dm:1001",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries() err = %v", err)
	}
	if len(projections) != 0 {
		t.Fatalf("ledger entries = %#v, want no mutation for unauthorized callback", projections)
	}
}

type seededLookaheadFrontier struct {
	Key         session.SessionKey
	Record      session.NextActionRecord
	EntryID     string
	AllowanceID string
}

func seedOutstandingLookaheadApprovalFrontier(t *testing.T, store *session.SQLiteStore, key session.SessionKey, prefix string, index int, at time.Time) seededLookaheadFrontier {
	t.Helper()
	operationKind := "authority_bundle_request"
	if index%2 == 1 {
		operationKind = "continuation_lease_request"
	}
	record, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           prefix + "-lookahead-next-" + strings.ReplaceAll(time.Unix(int64(index), 0).UTC().Format("150405"), ":", "") + "-" + operationKind,
		Key:                key,
		Owner:              "lookahead",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        operationKind,
		SubjectRef:         prefix + "-subject-" + operationKind + "-" + strings.ReplaceAll(time.Unix(int64(index), 0).UTC().Format(time.RFC3339), ":", "-"),
		NextAction:         "review the metered lookahead frontier",
		RequiredAuthority:  "authority_bundle",
		ResourceBlocker:    "authority_bundle_approval",
		RetryPolicy:        "manual_review",
		OperationKind:      operationKind,
		OperationTool:      "request_approval",
		OperationInputJSON: `{"action":"request_authority_bundle","contract_id":"authbundle-meter-seed"}`,
		OperatorProjection: "Metered lookahead frontier",
		CreatedAt:          at,
	})
	if err != nil {
		t.Fatalf("RecordNextAction(seed %d) err = %v", index, err)
	}
	shapeHash := session.AuthorityShapeHash(session.AuthorityShapeInput{
		Tool:          "request_approval",
		Action:        operationKind,
		ResourceClass: "authority_bundle",
	})
	entry, err := store.RecordIdentificationLedgerEntry(session.IdentificationLedgerEntryInput{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		StepRef:     "next_action:" + record.RecordID,
		ShapeHash:   shapeHash,
		LabelRef:    record.SubjectRef,
		Status:      session.IdentificationLedgerStatusProposed,
		UpdatedAt:   at,
		ExpiresAt:   at.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("RecordIdentificationLedgerEntry(seed %d) err = %v", index, err)
	}
	allowance, reserved, err := store.ReserveLookaheadAllowance(key.ChatID, int64(9000+index), session.SessionIDForKey(key), session.SessionIDForKey(key), MaxOutstandingLookaheadApprovalFrontiers, at, at.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReserveLookaheadAllowance(seed %d) err = %v", index, err)
	}
	if !reserved {
		t.Fatalf("ReserveLookaheadAllowance(seed %d) reserved = false", index)
	}
	if err := store.BindLookaheadAllowance(allowance.AllowanceID, record.RecordID, entry.EntryID, at); err != nil {
		t.Fatalf("BindLookaheadAllowance(seed %d) err = %v", index, err)
	}
	return seededLookaheadFrontier{Key: key, Record: record, EntryID: entry.EntryID, AllowanceID: allowance.AllowanceID}
}

func recordAuthorityBundleLookaheadActionForTest(t *testing.T, store *session.SQLiteStore, key session.SessionKey, requestSuffix string, allowedAction string, at time.Time) session.NextActionRecord {
	t.Helper()

	sessionID := session.SessionIDForKey(key)
	bundle, err := session.CompileAuthorityBundleContract(session.AuthorityBundleContractInput{
		RequestInstanceID: "lookahead-" + requestSuffix,
		SessionID:         sessionID,
		Principal:         "telegram:1001",
		Objective:         "Continue the same operation phase.",
		Summary:           "Review one bounded operation-phase grant.",
		AllowedActions:    []string{allowedAction},
		ForbiddenActions:  []string{"github_repo_push"},
		StopConditions:    []string{"stop after one bounded operation"},
		RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
			Kind:           session.CapabilityKindExternalAccount,
			TargetResource: "github:idolum-ai/aphelion",
			GrantedTo:      "telegram:1001",
			AllowedActions: []string{allowedAction},
			Contract:       `{"bounded_effect":"One operation only."}`,
			Constraints:    `{"repository":"idolum-ai/aphelion"}`,
		}},
		Components: []session.AuthorityBundleComponent{{
			Kind:       session.AuthorityBundleComponentKindOperationPhase,
			RefID:      "phase-same",
			Subject:    "operation",
			SubjectRef: "op-same",
		}},
		ExpiresAt: at.Add(30 * time.Minute),
		CreatedAt: at,
	})
	if err != nil {
		t.Fatalf("CompileAuthorityBundleContract(%s) err = %v", requestSuffix, err)
	}
	inputJSON, err := promotedDurableChildAuthorityBundleOperationInput(bundle.BundleID)
	if err != nil {
		t.Fatalf("promotedDurableChildAuthorityBundleOperationInput(%s) err = %v", requestSuffix, err)
	}
	_, action, err := store.RecordAuthorityBundleContractNextAction(bundle, session.NextActionInput{
		RecordID:           session.NextActionRecordID(sessionID, "authority_bundle_request", bundle.BundleID, session.NextActionBlockedNeedsAuthority, at),
		Key:                key,
		Owner:              "lookahead",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "authority_bundle_request",
		SubjectRef:         bundle.BundleID,
		CausalRefs:         []string{"operation:op-same", "operation_phase:phase-same", "authority_bundle:" + bundle.BundleID},
		NextAction:         "review the next plan-phase authority bundle",
		RequiredAuthority:  "authority_bundle",
		ResourceBlocker:    "authority_bundle_approval",
		RetryPolicy:        "retry_after_bundle_approval",
		OperationKind:      "authority_bundle_request",
		OperationTool:      "request_approval",
		OperationInputJSON: inputJSON,
		OperatorProjection: "Review next grant for operation phase phase-same",
		CreatedAt:          at,
	})
	if err != nil {
		t.Fatalf("RecordAuthorityBundleContractNextAction(%s) err = %v", requestSuffix, err)
	}
	return action
}

func seedLookaheadOperationPhase(t *testing.T, store *session.SQLiteStore, key session.SessionKey, opID string, phaseID string) {
	t.Helper()
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        opID,
		Objective: "Continue the next bounded operation phase.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:             opID + "-plan",
			Goal:           "Exercise metered lookahead.",
			CurrentPhaseID: phaseID,
			Phases: []session.OperationPhase{{
				ID:      phaseID,
				Summary: "Open one bounded follow-up issue.",
				Status:  session.PlanStatusPending,
				RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
					Kind:           session.CapabilityKindExternalAccount,
					TargetResource: "github:idolum-ai/aphelion",
					GrantedTo:      "telegram:" + strings.TrimPrefix(session.SessionIDForKey(key), "telegram_dm:"),
					AllowedActions: []string{"github_issue_create"},
					Contract:       `{"bounded_effect":"Open one issue only."}`,
					Constraints:    `{"repository":"idolum-ai/aphelion"}`,
				}},
			}},
		},
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpdateOperationState(%s) err = %v", opID, err)
	}
}

func assertLookaheadLabelsWithoutApprovalHaveNoExecutionEvents(t *testing.T, store *session.SQLiteStore, key session.SessionKey) {
	t.Helper()
	projections, err := store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		Limit:       100,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries() err = %v", err)
	}
	var unapprovedLookaheadLabels []string
	for _, projection := range projections {
		if strings.TrimSpace(projection.Entry.LabelRef) == "" || !ledgerProjectionHasLookaheadObservation(projection) {
			continue
		}
		if ledgerProjectionHasApprovalObservation(projection) {
			continue
		}
		unapprovedLookaheadLabels = append(unapprovedLookaheadLabels, projection.Entry.LabelRef)
	}
	if len(unapprovedLookaheadLabels) == 0 {
		t.Fatal("no unapproved lookahead labels found for fail-closed assertion")
	}
	events, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	for _, event := range events {
		if !authorityDiscoveryExecutionBearingEvent(event.EventType) {
			continue
		}
		for _, labelRef := range unapprovedLookaheadLabels {
			if strings.Contains(event.PayloadJSON, labelRef) {
				t.Fatalf("execution-bearing event references unapproved lookahead label = %#v label=%s", event, labelRef)
			}
		}
	}
}

func ledgerProjectionHasLookaheadObservation(projection session.IdentificationLedgerProjection) bool {
	for _, observation := range projection.Observations {
		if observation.Method == session.IdentificationObservationLookahead {
			return true
		}
	}
	return false
}

func ledgerProjectionHasApprovalObservation(projection session.IdentificationLedgerProjection) bool {
	for _, observation := range projection.Observations {
		if observation.Property != session.IdentificationPropertyOperatorAction {
			continue
		}
		if strings.Contains(strings.ToLower(strings.TrimSpace(observation.ActorAction)), "approve") ||
			strings.Contains(strings.ToLower(strings.TrimSpace(observation.Value)), "approve") {
			return true
		}
	}
	return false
}

func authorityDiscoveryExecutionBearingEvent(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case core.ExecutionEventContinuationApproved,
		core.ExecutionEventContinuationConsumed,
		core.ExecutionEventToolStarted,
		core.ExecutionEventToolBatchStarted,
		core.ExecutionEventWorkExecutorStarted,
		core.ExecutionEventWorkExecutorSucceeded,
		core.ExecutionEventAutoApprovalUsed,
		core.ExecutionEventDurableWakeStarted,
		core.ExecutionEventCapabilityInvocation:
		return true
	default:
		return false
	}
}

func singleLookaheadLedgerEntry(t *testing.T, store *session.SQLiteStore, key session.SessionKey) session.IdentificationLedgerEntry {
	t.Helper()
	projections, err := store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		Limit:       100,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries() err = %v", err)
	}
	var matches []session.IdentificationLedgerEntry
	for _, projection := range projections {
		if ledgerProjectionHasLookaheadObservation(projection) {
			matches = append(matches, projection.Entry)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("lookahead entries = %#v, want exactly one", matches)
	}
	return matches[0]
}

func ledgerProjectionForLabel(t *testing.T, store *session.SQLiteStore, key session.SessionKey, labelRef string) session.IdentificationLedgerProjection {
	t.Helper()
	projections, err := store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		LabelRef:    labelRef,
		Limit:       100,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries(label) err = %v", err)
	}
	for _, projection := range projections {
		if projection.Entry.LabelRef == labelRef {
			return projection
		}
	}
	t.Fatalf("no ledger projection found for label %s in %#v", labelRef, projections)
	return session.IdentificationLedgerProjection{}
}

func assertLedgerProjectionHasOperatorAction(t *testing.T, projection session.IdentificationLedgerProjection, actorPrincipal string, actorAction string) {
	t.Helper()
	for _, observation := range projection.Observations {
		if observation.Property == session.IdentificationPropertyOperatorAction &&
			observation.ActorPrincipal == actorPrincipal &&
			observation.ActorAction == actorAction {
			return
		}
	}
	t.Fatalf("projection observations = %#v, want operator action %s by %s", projection.Observations, actorAction, actorPrincipal)
}

func executionEventsContainLabel(events []session.ExecutionEvent, eventType string, labelRef string) bool {
	for _, event := range events {
		if event.EventType == eventType && strings.Contains(event.PayloadJSON, labelRef) {
			return true
		}
	}
	return false
}

func tokenStateForTest(menu AuthorityDiscoveryMenu, tokenID string) AuthorityDiscoveryTokenState {
	for _, token := range menu.Tokens {
		if token.TokenID == tokenID {
			return token.State
		}
	}
	return ""
}

func authorityDiscoveryBiographyContains(bio AuthorityDiscoveryLedgerBiography, needle string) bool {
	for _, line := range bio.Lines {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}
