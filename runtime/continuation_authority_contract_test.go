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

func TestInvalidAuthorityContractDoesNotRenderApprovalButtons(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9044, UserID: 0, Scope: telegramDMScopeRef(9044)}
	now := time.Now().UTC()
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     "invalid-authority-contract",
		Objective:      "Commit contradictory local work.",
		StageSummary:   "Commit validated local slices.",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:               "aprop-invalid-authority-contract",
			Summary:          "Commit validated local slices",
			RiskClass:        "workspace_commit_then_repo_write_bounded",
			AllowedActions:   []string{"git_commit_validated_slices", "edit_repo_code"},
			ForbiddenActions: []string{"commit"},
			Status:           session.ProposalStatusPending,
			ExpiresAt:        now.Add(time.Hour),
		},
	}
	state.ContinuationLease = buildContinuationLease(state.ActionProposal, 1, now)
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := rt.sendContinuationApprovalPrompt(context.Background(), key, core.InboundMessage{ChatID: 9044, SenderID: 1001, Text: "continue", MessageID: 1}, state, "approve?"); err != nil {
		t.Fatalf("sendContinuationApprovalPrompt() err = %v", err)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sent := append([]core.OutboundMessage(nil), sender.sent...)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no approval buttons", inlineCount)
	}
	if len(sent) != 0 {
		t.Fatalf("sent = %#v, want no user-visible internal contradiction diagnostic", sent)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusRevoked || got.ActionProposal.Status != session.ProposalStatusSuperseded {
		t.Fatalf("state = %#v, want revoked/superseded invalid authority", got)
	}
}

func TestMaterializedInvalidAuthorityContractRoutesToRepairPhaseApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9046, UserID: 0, Scope: telegramDMScopeRef(9046)}
	now := time.Now().UTC()
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     "invalid-no-safe-repair",
		Objective:      "Deploy the runtime with no remaining safe action.",
		StageSummary:   "Deploy-only invalid phase",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:               "aprop-invalid-no-safe-repair",
			Summary:          "Deploy-only invalid phase",
			RiskClass:        "continuation",
			AllowedActions:   []string{"deploy"},
			ForbiddenActions: []string{"deploy"},
			Status:           session.ProposalStatusPending,
			ExpiresAt:        now.Add(time.Hour),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-invalid-no-safe-repair",
			ProposalID:     "aprop-invalid-no-safe-repair",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      now.Add(time.Hour),
		},
	}
	_, blocked, err := rt.blockInvalidMaterializedContinuationAuthority(context.Background(), key, core.InboundMessage{ChatID: 9046, SenderID: 1001, Text: "continue", MessageID: 1}, session.OperationState{ID: "invalid-no-safe-repair-op"}, state, "operation_phase_plan", now)
	if err != nil {
		t.Fatalf("blockInvalidMaterializedContinuationAuthority() err = %v", err)
	}
	if !blocked {
		t.Fatal("blocked = false, want invalid authority handled")
	}

	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[inlineCount-1].text
	}
	sent := append([]core.OutboundMessage(nil), sender.sent...)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one read-only repair approval", inlineCount)
	}
	if len(sent) != 0 {
		t.Fatalf("sent = %#v, want no generic blocked notice", sent)
	}
	for _, want := range []string{"Approve", "Clarify authority contract"} {
		if !strings.Contains(inlineText, want) {
			t.Fatalf("inline text = %q, want %q", inlineText, want)
		}
	}
	for _, notWant := range []string{"allowed_action_implies_forbidden_authority", "allowed_action_exactly_forbidden", "internally contradictory", "smaller phase"} {
		if strings.Contains(inlineText, notWant) {
			t.Fatalf("inline text = %q, want no compiler diagnostic %q", inlineText, notWant)
		}
	}
	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	opState = session.NormalizeOperationState(opState)
	if len(opState.PhasePlan.Phases) != 2 {
		t.Fatalf("phase count = %d, want repair phase + original phase", len(opState.PhasePlan.Phases))
	}
	repair := opState.PhasePlan.Phases[0]
	original := opState.PhasePlan.Phases[1]
	if !strings.HasPrefix(repair.ID, operationAuthorityContractRepairPhasePrefix) || repair.AuthorityClass != "read_only_review" || !repair.RequiresApproval {
		t.Fatalf("repair phase = %#v, want read-only authority repair approval", repair)
	}
	if got := opState.PhasePlan.CurrentPhaseID; got != repair.ID {
		t.Fatalf("current phase = %q, want repair phase %q", got, repair.ID)
	}
	if actionListContains(repair.AllowedActions, "deploy") || actionListContains(repair.ForbiddenActions, "read_only_review") {
		t.Fatalf("repair actions = allowed %#v forbidden %#v, want read-only repair without deploy conflict", repair.AllowedActions, repair.ForbiddenActions)
	}
	if !original.RequiresApproval || original.Status != session.PlanStatusPending {
		t.Fatalf("original phase = %#v, want original work preserved for later approval", original)
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ActionProposal.OperationID != operationPhaseProposalID(opState, repair) {
		t.Fatalf("continuation = %#v, want pending repair-phase approval", cont)
	}
	if compilation := continuationAuthorityCompilation(cont); compilation.Invalid() {
		t.Fatalf("repair continuation compilation = %#v, want valid read-only repair authority", compilation)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var repaired session.ExecutionEvent
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationCompileRepaired {
			repaired = event
		}
	}
	if repaired.ID == 0 {
		t.Fatalf("events = %#v, want continuation.compile_repaired", events)
	}
	payload := executionEventPayload(repaired.PayloadJSON)
	if payloadString(payload, "repair_kind") != string(continuationCompileRepairAuthorityContract) || payloadString(payload, "normalized_reason") != "invalid_authority_no_safe_repair" {
		t.Fatalf("compile repair payload = %#v, want authority repair reason", payload)
	}
	if count, ok := payloadInt64(payload, "authority_contract_contradiction_count"); !ok || count == 0 {
		t.Fatalf("compile repair payload = %#v, want contradiction count", payload)
	}
}

func TestInvalidAuthorityReconciliationDoesNotStripRequiredGitPush(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     "push-prose-forbidden-git-push",
		Objective:      "Bundle and publish the approved artifacts.",
		StageSummary:   "Bundle artifacts, commit them, and push to the imex repository.",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:               "aprop-push-prose-forbidden-git-push",
			Summary:          "Bundle artifacts, commit them, and push to the imex repository.",
			BoundedEffect:    "Create one local commit and push the current branch to origin.",
			RiskClass:        "commit",
			AllowedActions:   []string{"git_commit", "push_main_to_origin"},
			ForbiddenActions: []string{"git_push", "deploy", "restart_service"},
			Status:           session.ProposalStatusPending,
			ExpiresAt:        now.Add(time.Hour),
		},
	}
	state.ContinuationLease = buildContinuationLease(state.ActionProposal, 1, now)

	compilation := session.CompileContinuationAuthorityContract(state)
	if compilation.Valid() {
		t.Fatalf("compilation = %#v, want invalid push/prose contradiction", compilation)
	}
	var rt Runtime
	if reconciled, ok := rt.reconciledContinuationStateFromInvalidAuthority(state, compilation, now); ok {
		t.Fatalf("reconciled = %#v, want no unsafe non-push repair", reconciled)
	}
}

func TestMaterializedStaleRepairDoesNotOutrankFreshWorkingObjective(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9050, UserID: 0, Scope: telegramDMScopeRef(9050)}
	if err := store.UpdateWorkingObjective(key, session.WorkingObjective{
		Objective:  "Resume Imexx work by generating the compact Spanish executive PDF report.",
		Source:     "inbound_user_text",
		Confidence: "high",
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpdateWorkingObjective() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "aphelion-pr-220-review",
		Objective: "Review Aphelion PR #220 read-only and report findings in chat.",
		Status:    session.OperationStatusBlocked,
		Stage:     "phase_plan",
		Summary:   "Stale PR #220 review operation still has a repairable phase.",
		PhasePlan: session.OperationPhasePlan{
			ID:             "aphelion-pr-220-review",
			Goal:           "Review Aphelion PR #220 read-only and report findings in chat.",
			CurrentPhaseID: "phase-rebuild-pr-220-intent",
			Phases: []session.OperationPhase{
				{
					ID:               "phase-rebuild-pr-220-intent",
					Summary:          "Rebuild governor continuation intent for Inspect PR #220 metadata/diff/checks read-only and report review findings in chat.",
					Status:           session.PlanStatusPending,
					AuthorityClass:   "workspace_write",
					BoundedEffect:    "Only repair the stale PR #220 continuation contract.",
					AllowedActions:   []string{"deploy"},
					ForbiddenActions: []string{"deploy"},
					RequiresApproval: true,
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{
		ChatID: 9050, SenderID: 1001, SenderName: "admin", MessageID: 1,
		Text: "merged the PR with the fix. Can we continue with this?\n\nReply context:\nidolum_bot: Still blocked on the same file-write issue; no PDF exists yet and nothing was attached.",
	}, "merged the PR with the fix. Can we continue with this?", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}

	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[inlineCount-1].text
	}
	sender.mu.Unlock()
	if materialized || inlineCount != 0 {
		t.Fatalf("materialized=%v inline=%d text=%q, want stale PR repair suppressed by fresh Imexx working objective", materialized, inlineCount, inlineText)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var suppressed session.ExecutionEvent
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationCompileRepaired {
			t.Fatalf("events = %#v, want no stale PR continuation.compile_repaired event", events)
		}
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationCandidateSuppressed {
			suppressed = event
		}
	}
	if suppressed.ID == 0 {
		t.Fatalf("events = %#v, want typed suppressed continuation candidate event", events)
	}
	payload := executionEventPayload(suppressed.PayloadJSON)
	if payloadString(payload, "reason") != "stale_vs_working_objective" || !strings.Contains(payloadString(payload, "working_objective"), "Imexx") {
		t.Fatalf("suppressed payload = %#v, want stale working-objective reason", payload)
	}
}

func TestMaterializedCandidateMatchingWorkingObjectiveStillSurfaces(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9051, UserID: 0, Scope: telegramDMScopeRef(9051)}
	if err := store.UpdateWorkingObjective(key, session.WorkingObjective{
		Objective:  "Generate the compact Spanish PDF report for Imexx.",
		Source:     "inbound_user_text",
		Confidence: "high",
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpdateWorkingObjective() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "imexx-pdf-report",
		Objective: "Generate the compact Spanish PDF report for Imexx.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:             "imexx-pdf-plan",
			Goal:           "Generate the compact Spanish PDF report for Imexx.",
			CurrentPhaseID: "phase-write-imexx-pdf",
			Phases: []session.OperationPhase{{
				ID:               "phase-write-imexx-pdf",
				Summary:          "Write and validate the compact Imexx PDF report.",
				Status:           session.PlanStatusPending,
				AuthorityClass:   "workspace_write",
				BoundedEffect:    "Write report source and validate the generated PDF only.",
				AllowedActions:   []string{"write_report_source", "compile_pdf", "validate_pdf"},
				ForbiddenActions: []string{"deploy", "restart_service"},
				RequiresApproval: true,
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9051, SenderID: 1001, SenderName: "admin", Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want matching working objective to allow approval")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[inlineCount-1].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 || !strings.Contains(inlineText, "Imexx") {
		t.Fatalf("inline count/text = %d/%q, want Imexx approval prompt", inlineCount, inlineText)
	}
}

func TestMaterializedCandidateIgnoresLowConfidenceWorkingObjective(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9052, UserID: 0, Scope: telegramDMScopeRef(9052)}
	if err := store.UpdateWorkingObjective(key, session.WorkingObjective{
		Objective:  "Resume Imexx PDF generation.",
		Source:     "inferred",
		Confidence: "medium",
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpdateWorkingObjective() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "aphelion-pr-220-review",
		Objective: "Review Aphelion PR #220.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:             "aphelion-pr-220-review",
			Goal:           "Review Aphelion PR #220.",
			CurrentPhaseID: "phase-read-pr",
			Phases: []session.OperationPhase{{
				ID:               "phase-read-pr",
				Summary:          "Inspect PR #220 diff and checks.",
				Status:           session.PlanStatusPending,
				AuthorityClass:   "read_only_review",
				BoundedEffect:    "Read PR metadata and report findings.",
				AllowedActions:   []string{"inspect_pr", "report_findings"},
				ForbiddenActions: []string{"post_pr_comment", "commit", "push"},
				RequiresApproval: true,
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9052, SenderID: 1001, SenderName: "admin", Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want low-confidence working objective not to suppress approval")
	}
}

func TestMaterializedBundleAuthorityContradictionRoutesToRepairPhaseApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9047, UserID: 0, Scope: telegramDMScopeRef(9047)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "bundle-invalid-op",
		Objective: "Approve bundled local phases without leaking compiler diagnostics.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:             "bundle-invalid-plan",
			CurrentPhaseID: "phase-one",
			Phases: []session.OperationPhase{
				{
					ID:               "phase-one",
					Summary:          "Patch local files",
					Status:           session.PlanStatusPending,
					AuthorityClass:   "workspace_write",
					BoundedEffect:    "Edit local files and stop before commit.",
					AllowedActions:   []string{"edit_files"},
					ForbiddenActions: []string{"commit"},
				},
				{
					ID:               "phase-two",
					Summary:          "Run local tests",
					Status:           session.PlanStatusPending,
					AuthorityClass:   "workspace_write",
					BoundedEffect:    "Run tests only.",
					AllowedActions:   []string{"restart_aphelion_service"},
					ForbiddenActions: []string{"deploy"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9047, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want handled repair approval")
	}

	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[inlineCount-1].text
	}
	sent := append([]core.OutboundMessage(nil), sender.sent...)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one repair approval", inlineCount)
	}
	if len(sent) != 0 {
		t.Fatalf("sent = %#v, want no generic blocked notice", sent)
	}
	if !strings.Contains(inlineText, "Clarify authority contract") {
		t.Fatalf("inline text = %q, want authority repair approval", inlineText)
	}
	for _, notWant := range []string{"allowed_action_exactly_forbidden", "allowed_action_implies_forbidden_authority", "internally contradictory", "smaller phase"} {
		if strings.Contains(inlineText, notWant) {
			t.Fatalf("inline text = %q, want no compiler diagnostic %q", inlineText, notWant)
		}
	}
	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	opState = session.NormalizeOperationState(opState)
	if len(opState.PhasePlan.Phases) != 3 {
		t.Fatalf("phase count = %d, want repair phase plus original bundle phases", len(opState.PhasePlan.Phases))
	}
	repair := opState.PhasePlan.Phases[0]
	if !strings.HasPrefix(repair.ID, operationAuthorityContractRepairPhasePrefix) || repair.AuthorityClass != "read_only_review" {
		t.Fatalf("repair phase = %#v, want read-only authority repair", repair)
	}
	if opState.PhasePlan.Phases[1].ID != "phase-one" || opState.PhasePlan.Phases[2].ID != "phase-two" {
		t.Fatalf("phase order = %#v, want repair before original phases", opState.PhasePlan.Phases)
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ActionProposal.OperationID != operationPhaseProposalID(opState, repair) {
		t.Fatalf("continuation = %#v, want pending repair approval", cont)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var repaired session.ExecutionEvent
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationCompileRepaired {
			repaired = event
		}
	}
	if repaired.ID == 0 {
		t.Fatalf("events = %#v, want continuation.compile_repaired", events)
	}
	payload := executionEventPayload(repaired.PayloadJSON)
	if payloadString(payload, "repair_kind") != string(continuationCompileRepairAuthorityContract) || payloadString(payload, "normalized_reason") != "invalid_authority_no_safe_repair" {
		t.Fatalf("compile repair payload = %#v, want authority repair reason", payload)
	}
}

func TestMaterializedInvalidAuthorityContractReconcilesToFreshApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9045, UserID: 0, Scope: telegramDMScopeRef(9045)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "deploy-reconciliation-op",
		Objective: "Deploy the validated runtime without leaking compiler diagnostics.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:             "deploy-reconciliation-plan",
			CurrentPhaseID: "phase-deploy",
			Phases: []session.OperationPhase{{
				ID:             "phase-deploy",
				Summary:        "Deploy the validated runtime",
				Status:         session.PlanStatusPending,
				AuthorityClass: "deploy",
				BoundedEffect:  "Build, install, restart, and verify the service.",
				AllowedActions: []string{"inspect_readonly_state", "install_user_service", "restart_aphelion_service", "run_verify_deploy"},
				ForbiddenActions: []string{
					"deploy or restart",
					"credentials_or_tokens",
				},
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9045, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want reconciled approval prompt")
	}

	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sent := append([]core.OutboundMessage(nil), sender.sent...)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[inlineCount-1].text
	}
	sender.mu.Unlock()
	if len(sent) != 0 {
		t.Fatalf("sent = %#v, want no raw invalid authority diagnostic", sent)
	}
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one reconciled approval", inlineCount)
	}
	if strings.Contains(inlineText, "internally contradictory") || strings.Contains(inlineText, "allowed_action_implies_forbidden_authority") {
		t.Fatalf("inline text = %q, want no compiler diagnostic", inlineText)
	}
	if !strings.Contains(inlineText, "Deploy the validated runtime") {
		t.Fatalf("inline text = %q, want reconciled approval summary", inlineText)
	}

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ActionProposal.Status != session.ProposalStatusPending {
		t.Fatalf("continuation = %#v, want pending reconciled approval", cont)
	}
	if compilation := continuationAuthorityCompilation(cont); compilation.Invalid() {
		t.Fatalf("compilation = %#v, want valid reconciled authority", compilation)
	}
	for _, notWant := range []string{"install_user_service", "restart_aphelion_service", "run_verify_deploy", "deploy"} {
		if actionListContains(cont.ActionProposal.AllowedActions, notWant) {
			t.Fatalf("allowed actions = %#v, want unsafe deploy/restart action %q removed", cont.ActionProposal.AllowedActions, notWant)
		}
	}
	if cont.ActionProposal.RiskClass != "commit" || !actionListContains(cont.ActionProposal.AllowedActions, "git_commit_intended_changes") {
		t.Fatalf("action proposal = %#v, want safest remaining non-deploy approval", cont.ActionProposal)
	}
	for _, want := range []string{"deploy or restart", "credentials_or_tokens"} {
		if !actionListContains(cont.ActionProposal.ForbiddenActions, want) {
			t.Fatalf("forbidden actions = %#v, want preserved stop boundary %q", cont.ActionProposal.ForbiddenActions, want)
		}
	}

	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if opState.Stage != "deploy_approval" || opState.PhasePlan.Phases[0].LeaseID != cont.ContinuationLease.ID {
		t.Fatalf("operation state = %#v, want deploy approval linked to reconciled lease %q", opState, cont.ContinuationLease.ID)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var repaired session.ExecutionEvent
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationCompileRepaired {
			repaired = event
		}
	}
	if repaired.ID == 0 {
		t.Fatalf("events = %#v, want continuation.compile_repaired from reconciliation", events)
	}
	payload := executionEventPayload(repaired.PayloadJSON)
	if payloadString(payload, "repair_strategy") != "remove_contradictory_allowed_actions" {
		t.Fatalf("compile repair payload = %#v, want reconciliation repair strategy", payload)
	}
}
