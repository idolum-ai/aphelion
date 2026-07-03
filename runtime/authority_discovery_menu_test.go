//go:build linux

package runtime

import (
	"context"
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
			TokenID:       "loadout-github",
			LabelRef:      "authbundle-standing-github",
			LiveAuthority: true,
			ExpiresAt:     now.Add(time.Hour),
		}, {
			TokenID:   "loadout-github-unverified",
			LabelRef:  "authbundle-unverified-github",
			ExpiresAt: now.Add(time.Hour),
		}},
	})
	if len(menu.Tokens) != 3 {
		t.Fatalf("tokens = %#v, want frontier plus loadout", menu.Tokens)
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
