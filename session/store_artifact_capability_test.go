//go:build linux

package session

import (
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestArtifactIndexRoundTripAndSearch(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 55, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 1
	sess.LastFloorMetadata = `{"artifacts":[{"artifact_id":"doc-1","kind":"document","source_type":"document","summary":"roadmap.txt","handling":"extract_text","retention":"child_local","fetch_state":"fetched_local","materialized_path":"/tmp/roadmap.txt"}]}`
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "ok", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	hits, err := store.SearchArtifacts("roadmap", 10, nil)
	if err != nil {
		t.Fatalf("SearchArtifacts() err = %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("artifact hits len = %d, want 1", len(hits))
	}
	if hits[0].ArtifactID != "doc-1" {
		t.Fatalf("ArtifactID = %q, want doc-1", hits[0].ArtifactID)
	}
	if hits[0].Retention != "child_local" {
		t.Fatalf("Retention = %q, want child_local", hits[0].Retention)
	}
	if hits[0].MaterializedPath != "/tmp/roadmap.txt" {
		t.Fatalf("MaterializedPath = %q, want /tmp/roadmap.txt", hits[0].MaterializedPath)
	}

	scoped, err := store.SearchArtifacts("roadmap", 10, &key)
	if err != nil {
		t.Fatalf("SearchArtifacts(scoped) err = %v", err)
	}
	if len(scoped) != 1 || scoped[0].SessionID != SessionIDForKey(key) {
		t.Fatalf("scoped hits = %#v, want one hit in session", scoped)
	}
}

func TestArtifactIndexPreservesRepeatedArtifactIDsAcrossTurnsAndSessions(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	keyA := SessionKey{ChatID: 57, UserID: 0}
	sessA, err := store.Load(keyA)
	if err != nil {
		t.Fatalf("Load(keyA) err = %v", err)
	}
	sessA.TurnCount = 1
	sessA.LastFloorMetadata = `{"artifacts":[{"artifact_id":"telegram:location","kind":"structured","source_type":"location","summary":"first location","handling":"inspect_metadata","retention":"session_reference"}]}`
	if err := store.Save(sessA, []Message{{Role: "assistant", Content: "first", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(sessA first) err = %v", err)
	}
	sessA.TurnCount = 2
	sessA.LastFloorMetadata = `{"artifacts":[{"artifact_id":"telegram:location","kind":"structured","source_type":"location","summary":"second location","handling":"inspect_metadata","retention":"session_reference"}]}`
	if err := store.Save(sessA, []Message{{Role: "assistant", Content: "second", TurnIndex: 2}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(sessA second) err = %v", err)
	}

	keyB := SessionKey{ChatID: 58, UserID: 0}
	sessB, err := store.Load(keyB)
	if err != nil {
		t.Fatalf("Load(keyB) err = %v", err)
	}
	sessB.TurnCount = 1
	sessB.LastFloorMetadata = `{"artifacts":[{"artifact_id":"telegram:location","kind":"structured","source_type":"location","summary":"third location","handling":"inspect_metadata","retention":"session_reference"}]}`
	if err := store.Save(sessB, []Message{{Role: "assistant", Content: "third", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(sessB) err = %v", err)
	}

	hits, err := store.SearchArtifacts("location", 10, nil)
	if err != nil {
		t.Fatalf("SearchArtifacts() err = %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("artifact hits len = %d, want 3", len(hits))
	}

	seen := map[string]bool{}
	for _, hit := range hits {
		seen[hit.SessionID+"#"+hit.Summary] = true
	}
	if !seen[SessionIDForKey(keyA)+"#first location"] {
		t.Fatalf("missing first occurrence in hits: %#v", hits)
	}
	if !seen[SessionIDForKey(keyA)+"#second location"] {
		t.Fatalf("missing second occurrence in hits: %#v", hits)
	}
	if !seen[SessionIDForKey(keyB)+"#third location"] {
		t.Fatalf("missing third occurrence in hits: %#v", hits)
	}
}

func TestArtifactIndexIgnoresEphemeralArtifacts(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 56, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 1
	sess.LastFloorMetadata = `{"artifacts":[{"artifact_id":"img-ephemeral","kind":"image","summary":"throwaway","retention":"ephemeral"}]}`
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "ok", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	hits, err := store.SearchArtifacts("throwaway", 10, nil)
	if err != nil {
		t.Fatalf("SearchArtifacts() err = %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("artifact hits len = %d, want 0", len(hits))
	}
}

func TestMessageTurnProvenanceRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	key := SessionKey{ChatID: 991, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if err := store.Save(sess, []Message{{
		Role:              "user",
		Content:           continuationApprovedEventTextForTest,
		ActorUserID:       1002,
		ActorRole:         "approved_user",
		EventOrigin:       "turn_authorization",
		EventOriginDetail: "continuation",
		TurnIndex:         1,
	}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}
	got, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() reload err = %v", err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(got.Messages))
	}
	if got.Messages[0].ActorUserID != 1002 || got.Messages[0].ActorRole != "approved_user" {
		t.Fatalf("actor provenance = %#v, want approved user", got.Messages[0])
	}
	if got.Messages[0].EventOrigin != "turn_authorization" || got.Messages[0].EventOriginDetail != "continuation" {
		t.Fatalf("event provenance = (%q, %q), want turn_authorization/continuation", got.Messages[0].EventOrigin, got.Messages[0].EventOriginDetail)
	}
}

func TestSQLiteStoreCapabilityRequestReviewGrantInvocationRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	request, err := store.UpsertCapabilityRequest(CapabilityRequest{
		RequestID:       "cap-1",
		RequestedBy:     "child-agent",
		RequestedFor:    "child-agent",
		ParentPrincipal: "telegram:200",
		Kind:            CapabilityKindPurchase,
		TargetResource:  "amazon",
		Purpose:         "order approved school supplies",
		RiskClass:       "spend",
		Contract:        `{"max_items":3}`,
		Constraints:     `{"max_usd":50}`,
	})
	if err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	if request.ReviewStatus != CapabilityReviewStatusProposed {
		t.Fatalf("ReviewStatus = %q, want proposed", request.ReviewStatus)
	}

	if _, err := store.AppendCapabilityReview(CapabilityReview{ReviewID: "capr-1", RequestID: "cap-1", Reviewer: "telegram:200", ReviewerRole: "parent", Status: CapabilityReviewStatusParentApproved, Rationale: "bounded spend"}); err != nil {
		t.Fatalf("AppendCapabilityReview(parent) err = %v", err)
	}
	if _, err := store.AppendCapabilityReview(CapabilityReview{ReviewID: "capr-2", RequestID: "cap-1", Reviewer: "telegram:1001", ReviewerRole: "admin", Status: CapabilityReviewStatusApproved, Rationale: "parent endorsed"}); err != nil {
		t.Fatalf("AppendCapabilityReview(admin) err = %v", err)
	}
	request, ok, err := store.CapabilityRequest("cap-1")
	if err != nil {
		t.Fatalf("CapabilityRequest() err = %v", err)
	}
	if !ok || request.ReviewStatus != CapabilityReviewStatusApproved {
		t.Fatalf("CapabilityRequest() = %#v ok=%t, want approved", request, ok)
	}

	grant, err := store.UpsertCapabilityGrant(CapabilityGrant{
		GrantID:           "capg-1",
		RequestID:         "cap-1",
		GrantedBy:         "telegram:1001",
		GrantedTo:         "child-agent",
		Kind:              CapabilityKindPurchase,
		TargetResource:    "amazon",
		AllowedActions:    []string{"order", "summarize"},
		Contract:          `{"max_items":3}`,
		Constraints:       `{"max_usd":50}`,
		Status:            CapabilityGrantStatusActive,
		AnchorFingerprint: "sha256:test",
	})
	if err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	if grant.Status != CapabilityGrantStatusActive || len(grant.AllowedActions) != 2 {
		t.Fatalf("CapabilityGrant = %#v, want active with actions", grant)
	}
	request, _, err = store.CapabilityRequest("cap-1")
	if err != nil {
		t.Fatalf("CapabilityRequest(after grant) err = %v", err)
	}
	if request.GrantID != "capg-1" {
		t.Fatalf("GrantID = %q, want capg-1", request.GrantID)
	}

	active, ok, err := store.ActiveCapabilityGrant(CapabilityKindPurchase, "amazon", "child-agent", "order")
	if err != nil {
		t.Fatalf("ActiveCapabilityGrant() err = %v", err)
	}
	if !ok || active.GrantID != "capg-1" {
		t.Fatalf("ActiveCapabilityGrant() = %#v ok=%t, want capg-1", active, ok)
	}
	if _, ok, err := store.ActiveCapabilityGrant(CapabilityKindPurchase, "amazon", "child-agent", "refund"); err != nil || ok {
		t.Fatalf("ActiveCapabilityGrant(refund) ok=%t err=%v, want false nil", ok, err)
	}

	if _, err := store.RecordCapabilityInvocation(CapabilityInvocation{
		GrantID:              "capg-1",
		Principal:            "child-agent",
		Action:               "order",
		Status:               "failed",
		ErrorText:            "declined",
		SessionID:            "telegram_dm:1001",
		ContinuationLeaseID:  "lease-capability-use",
		OperationPlanLeaseID: "plan-lease-capability-use",
		AuthoritySource:      "continuation_lease",
	}); err != nil {
		t.Fatalf("RecordCapabilityInvocation() err = %v", err)
	}
	invocations, err := store.CapabilityInvocationsByGrant("capg-1", 10)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant() err = %v", err)
	}
	if len(invocations) != 1 || invocations[0].ContinuationLeaseID != "lease-capability-use" || invocations[0].OperationPlanLeaseID != "plan-lease-capability-use" || invocations[0].AuthoritySource != "continuation_lease" {
		t.Fatalf("CapabilityInvocationsByGrant() = %#v, want authority use refs", invocations)
	}
	grant, ok, err = store.CapabilityGrant("capg-1")
	if err != nil {
		t.Fatalf("CapabilityGrant(after invocation) err = %v", err)
	}
	if !ok || grant.InvocationCount != 1 || grant.FailureCount != 1 || grant.LastFailureAt.IsZero() {
		t.Fatalf("CapabilityGrant counters = %#v ok=%t, want one failed invocation", grant, ok)
	}
}

func TestDurableChildAgreementTracksCapabilityReviewStatus(t *testing.T) {
	store := newTestSQLiteStore(t)

	_, err := store.UpsertDurableChildAgreement(DurableChildAgreement{
		AgreementID:         "agree-1",
		AgentID:             "child-alpha",
		ParentPrincipal:     "telegram:1001",
		ChildPrincipal:      "durable_agent:child-alpha",
		SourceSurface:       "durable_agent.delegation_request",
		SourceRequestID:     "cap-1",
		SourceReviewEventID: 42,
		Summary:             "Child requested a bounded system capability.",
		BoundedEffect:       "Grant scoped runtime access after review.",
		ArtifactRefs:        []RecordReference{{Kind: "review_event", Ref: "42", Label: "delegation request"}},
	})
	if err != nil {
		t.Fatalf("UpsertDurableChildAgreement() err = %v", err)
	}

	if _, err := store.UpsertCapabilityRequest(CapabilityRequest{
		RequestID:      "cap-1",
		RequestedBy:    "durable_agent:child-alpha",
		RequestedFor:   "durable_agent:child-alpha",
		Kind:           CapabilityKindGenericDelegation,
		TargetResource: "runtime-capability",
		Purpose:        "Needs bounded child-local runtime support.",
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	if _, err := store.AppendCapabilityReview(CapabilityReview{
		ReviewID:     "review-1",
		RequestID:    "cap-1",
		Reviewer:     "telegram:1001",
		ReviewerRole: "admin",
		Status:       CapabilityReviewStatusApproved,
		Rationale:    "Approved as a bounded parent-child system-change agreement.",
	}); err != nil {
		t.Fatalf("AppendCapabilityReview() err = %v", err)
	}

	agreement, ok, err := store.DurableChildAgreement("agree-1")
	if err != nil {
		t.Fatalf("DurableChildAgreement() err = %v", err)
	}
	if !ok {
		t.Fatal("DurableChildAgreement() ok = false, want true")
	}
	if agreement.Status != DurableChildAgreementStatusApproved {
		t.Fatalf("agreement status = %q, want approved", agreement.Status)
	}
	if len(agreement.ArtifactRefs) != 1 || agreement.ArtifactRefs[0].Kind != "review_event" {
		t.Fatalf("agreement artifact refs = %#v, want persisted review_event ref", agreement.ArtifactRefs)
	}
}

func TestContinuationStatePersistsActionProposalAndLease(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	key := SessionKey{ChatID: 1901, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "1901"}}
	expiresAt := time.Now().UTC().Add(2 * time.Hour).Round(0)
	state := ContinuationState{
		Status:         ContinuationStatusPending,
		DecisionID:     "decision-action-proposal",
		Objective:      "Implement ActionProposal and ContinuationLease v1.",
		StageSummary:   "Wire continuation approval to a lease.",
		RemainingTurns: 1,
		ActionProposal: ActionProposal{
			ID:               "aprop-action-lease",
			OperationID:      "op-action-lease",
			MissionID:        "mission-ledger-runtime",
			Summary:          "Implement the approval-button primitive.",
			WhyNow:           "Continuation and deploy approvals need a reusable bounded contract.",
			BoundedEffect:    "Local source/docs/tests only.",
			RiskClass:        "system_change",
			AllowedActions:   []string{"edit_repo", "run_tests", "edit_repo"},
			ForbiddenActions: []string{"external_account", "purchase"},
			ValidationPlan:   []string{"go test ./session", "go test ./runtime"},
			ExpiresAt:        expiresAt,
			PlanHash:         "sha256:test-plan",
		},
		ContinuationLease: ContinuationLease{
			ID:             "lease-action-lease",
			ProposalID:     "aprop-action-lease",
			MissionID:      "mission-ledger-runtime",
			Status:         ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      expiresAt,
			PlanHash:       "sha256:test-plan",
		},
	}
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.ActionProposal.ID != "aprop-action-lease" || got.ActionProposal.Status != ProposalStatusPending {
		t.Fatalf("ActionProposal = %#v, want persisted pending proposal", got.ActionProposal)
	}
	if len(got.ActionProposal.AllowedActions) != 2 {
		t.Fatalf("AllowedActions = %#v, want deduped action list", got.ActionProposal.AllowedActions)
	}
	if got.ContinuationLease.ID != "lease-action-lease" || got.ContinuationLease.Status != ContinuationLeaseStatusPending {
		t.Fatalf("ContinuationLease = %#v, want persisted pending lease", got.ContinuationLease)
	}
	if got.ContinuationLease.MaxTurns != 1 || got.ContinuationLease.RemainingTurns != 1 {
		t.Fatalf("ContinuationLease turns = %d/%d, want 1/1", got.ContinuationLease.MaxTurns, got.ContinuationLease.RemainingTurns)
	}
	if got.ContinuationLease.ExpiresAt.IsZero() || got.ContinuationLease.ExpiresAt.UTC() != expiresAt.UTC() {
		t.Fatalf("Lease ExpiresAt = %v, want %v", got.ContinuationLease.ExpiresAt, expiresAt)
	}
}

func TestOperationPlanLeaseRoundTripAndDefaults(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 8080, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "8080"}}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.OperationState = OperationState{
		ID:        "op-plan-lease",
		Objective: "Reduce approval pings without widening authority.",
		Status:    OperationStatusBlocked,
		Stage:     "plan_lease_proposal",
		PlanLease: OperationPlanLease{
			ID:              "plan-lease-20260503",
			Summary:         "Low-risk coordination lease",
			Status:          PlanLeaseStatusProposed,
			TurnBudget:      5,
			CoveredPhaseIDs: []string{"phase-1", "phase-2"},
			Lanes: []OperationPlanLeaseLane{
				{ID: "readonly", Summary: "Read-only review", AuthorityClass: "read-only review", ExpectedTurns: 3, AllowedActions: []string{"inspect_status"}},
				{ID: "workspace", Summary: "Local patch", AuthorityClass: "workspace_write", ExpectedTurns: 2, ForbiddenActions: []string{"deploy"}},
			},
			EvidenceDigest: OperationPlanLeaseEvidenceDigest{
				TurnsSpent:   1,
				LanesUsed:    []string{"readonly"},
				Completed:    []string{"summarized status"},
				ResidualRisk: "Implementation not deployed.",
			},
		},
	}
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "plan lease proposed", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	reloaded, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	lease := reloaded.PlanLease
	if lease.ID != "plan-lease-20260503" || lease.Status != PlanLeaseStatusProposed {
		t.Fatalf("plan lease = %#v, want proposed persisted lease", lease)
	}
	if lease.TurnBudget != 5 || lease.RemainingTurns != 5 || len(lease.Lanes) != 2 {
		t.Fatalf("plan lease turns/lanes = %#v", lease)
	}
	if lease.Lanes[0].AuthorityClass != "read_only_review" || lease.Lanes[0].ExpectedTurns != 3 {
		t.Fatalf("plan lease first lane = %#v, want normalized authority and expected turns", lease.Lanes[0])
	}
	if len(lease.HardInterrupts) == 0 || len(lease.ChildInitiationLanes) == 0 {
		t.Fatalf("plan lease guardrails = hard=%#v child=%#v, want defaults", lease.HardInterrupts, lease.ChildInitiationLanes)
	}
	if !stringSliceContains(lease.HardInterrupts, "policy_or_grant_change") || !stringSliceContains(lease.ChildInitiationLanes, "capability_request") {
		t.Fatalf("plan lease guardrails = hard=%#v child=%#v, want hard gates and review lanes", lease.HardInterrupts, lease.ChildInitiationLanes)
	}
	if lease.EvidenceDigest.TurnsSpent != 1 || lease.EvidenceDigest.ResidualRisk == "" {
		t.Fatalf("evidence digest = %#v, want bounded digest persisted", lease.EvidenceDigest)
	}
}
