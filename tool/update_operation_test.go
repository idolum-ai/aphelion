//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func TestDefinitionsIncludeUpdateOperationToolWhenStoreConfigured(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), time.Second)
	names := make([]string, 0, len(registry.Definitions()))
	for _, def := range registry.Definitions() {
		names = append(names, def.Name)
	}
	if containsString(names, "update_operation") {
		t.Fatalf("definitions without store = %#v, do not want update_operation", names)
	}

	store := newToolTestStore(t)
	registry = NewRegistry(t.TempDir(), time.Second).WithSessionStore(store)
	names = names[:0]
	for _, def := range registry.Definitions() {
		names = append(names, def.Name)
	}
	if !containsString(names, "update_operation") {
		t.Fatalf("definitions with store = %#v, want update_operation", names)
	}
}

func TestUpdateOperationDefinitionDocumentsEmptyInputInspection(t *testing.T) {
	t.Parallel()

	store := newToolTestStore(t)
	registry := NewRegistry(t.TempDir(), time.Second).WithSessionStore(store)
	var found bool
	for _, def := range registry.Definitions() {
		if def.Name != "update_operation" {
			continue
		}
		found = true
		desc := strings.ToLower(def.Description)
		for _, want := range []string{"empty input", "inspect", "full persisted operation state", "compact acknowledgement"} {
			if !strings.Contains(desc, want) {
				t.Fatalf("description = %q, want guidance containing %q", def.Description, want)
			}
		}
	}
	if !found {
		t.Fatal("update_operation definition not found")
	}
}

func TestUpdateOperationToolPersistsAndShowsOperationState(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()

	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		json.RawMessage(`{
			"objective":"Investigate my internet footprint.",
			"status":"active",
			"stage":"assessment",
			"summary":"Collecting public traces before requesting a browser install proposal.",
			"proposal":{
				"kind":"capability_acquisition",
				"summary":"Acquire browser automation",
				"why_now":"A screenshot requires browser automation in this operation.",
				"bounded_effect":"Install Playwright locally and capture one screenshot.",
				"status":"pending"
			},
			"findings":[
				{"claim":"Browser automation is not currently available.","confidence":"high","basis":"No browser tool is exposed in the manifest."}
			],
			"artifacts":[
				{"label":"working-note","ref":"tmp/notes.md"}
			]
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(update_operation) err = %v", err)
	}
	if !strings.Contains(out, "[OPERATION_UPDATED]") || !strings.Contains(out, "received_fields:") {
		t.Fatalf("update output = %q, want compact update ack", out)
	}
	if strings.Contains(out, "applied_fields:") {
		t.Fatalf("update output = %q, want received_fields naming, not applied_fields", out)
	}
	if got, max := len(out), 500; got > max {
		t.Fatalf("compact update ack length = %d, want <= %d: %q", got, max, out)
	}
	if strings.Contains(out, "Browser automation is not currently available") || strings.Contains(out, "A screenshot requires browser automation") {
		t.Fatalf("update output = %q, want compact ack without full operation echo", out)
	}

	state, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if state.Status != session.OperationStatusActive {
		t.Fatalf("Status = %q, want active", state.Status)
	}
	if state.Proposal.Status != session.ProposalStatusPending {
		t.Fatalf("Proposal status = %q, want pending", state.Proposal.Status)
	}
	if len(state.Findings) != 1 || state.Findings[0].Confidence != session.FindingConfidenceHigh {
		t.Fatalf("Findings = %#v, want persisted high-confidence finding", state.Findings)
	}

	showOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		json.RawMessage(`{}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(show update_operation) err = %v", err)
	}
	if !strings.Contains(showOut, "[OPERATION]") || !strings.Contains(showOut, "Acquire browser automation") {
		t.Fatalf("show output = %q, want current operation state", showOut)
	}
}

func TestDurableOperationToolsDecodeStringWrappedObjectInputs(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	updateInput := stringWrappedJSON(t, `{"objective":"Keep continuation state durable.","status":"active","stage":"wrapped-input","summary":"Decoded from a JSON string."}`)
	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		updateInput,
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(update_operation wrapped) err = %v", err)
	}
	if !strings.Contains(out, "[OPERATION_UPDATED]") {
		t.Fatalf("update_operation output = %q, want updated marker", out)
	}
	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if opState.Stage != "wrapped_input" {
		t.Fatalf("operation stage = %q, want wrapped_input", opState.Stage)
	}

	approvalInput := stringWrappedJSON(t, `{"objective":"Ask before the next slice.","phase":{"id":"phase-wrapped-approval","summary":"Run read-only release readiness check","authority_class":"read_only_review","why_now":"The operator asked to continue after completion.","bounded_effect":"Inspect repo state and report release readiness only.","allowed_actions":["inspect_status"],"forbidden_actions":["edit_files","commit","push_remote","deploy"],"validation_plan":["report findings"]}}`)
	out, err = registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"request_approval",
		approvalInput,
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(request_approval wrapped) err = %v", err)
	}
	if !strings.Contains(out, "[APPROVAL_REQUESTED]") {
		t.Fatalf("request_approval output = %q, want approval marker", out)
	}
	opState, err = store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() after approval err = %v", err)
	}
	if opState.Status != session.OperationStatusBlocked || opState.Proposal.Status != session.ProposalStatusPending {
		t.Fatalf("operation = %#v, want blocked pending proposal", opState)
	}
}

func TestDurableOperationToolsRejectStringWrappedNonObjectInputs(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	for _, tc := range []struct {
		name  string
		tool  string
		input json.RawMessage
	}{
		{name: "update array", tool: "update_operation", input: stringWrappedJSON(t, `[]`)},
		{name: "approval prose", tool: "request_approval", input: stringWrappedJSON(t, `not json`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := registry.ExecuteForSessionPrincipal(
				context.Background(),
				principal.Principal{Role: principal.RoleAdmin},
				key,
				tc.tool,
				tc.input,
			)
			if err == nil || !strings.Contains(err.Error(), "decode "+tc.tool+" input") {
				t.Fatalf("%s err = %v, want decode error", tc.tool, err)
			}
		})
	}
}

func stringWrappedJSON(t *testing.T, raw string) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal wrapped input: %v", err)
	}
	return json.RawMessage(encoded)
}

func TestUpdateOperationAckOmitsEmptyReceivedFieldsLine(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		json.RawMessage(`{"id":"   ","objective":"\t "}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(update_operation whitespace) err = %v", err)
	}
	if strings.Contains(out, "received_fields:") {
		t.Fatalf("update output = %q, want no received_fields line when received fields trim empty/invalid", out)
	}
	if strings.Contains(out, "applied_fields:") {
		t.Fatalf("update output = %q, want no legacy applied_fields line", out)
	}
}

func TestUpdateOperationToolMergeAppendsFindingsAndAdvancesProposal(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()

	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-1",
		Objective: "Investigate my internet footprint.",
		Status:    session.OperationStatusBlocked,
		Stage:     "proposal",
		Summary:   "Waiting on capability approval.",
		Proposal: session.OperationProposal{
			ID:            "proposal-1",
			Kind:          "capability_acquisition",
			Summary:       "Acquire browser automation",
			WhyNow:        "A screenshot requires browser automation in this operation.",
			BoundedEffect: "Install Playwright locally and capture one screenshot.",
			Status:        session.ProposalStatusPending,
		},
		Findings: []session.OperationFinding{
			{Claim: "Browser automation is not currently available.", Confidence: session.FindingConfidenceHigh, Basis: "No browser tool is exposed."},
		},
		Artifacts: []session.OperationArtifact{
			{Label: "working-note", Ref: "tmp/notes.md"},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState(seed) err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		json.RawMessage(`{
			"merge":true,
			"status":"active",
			"stage":"execution",
			"summary":"Proposal approved and screenshot capture is underway.",
			"proposal":{"status":"approved"},
			"findings":[
				{"claim":"Browser automation can be acquired locally.","confidence":"high","basis":"Admin execution can install workspace dependencies."}
			],
			"artifacts":[
				{"label":"screenshot","ref":"tmp/reddit.png"}
			]
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(update_operation merge) err = %v", err)
	}
	if !strings.Contains(out, "[OPERATION_UPDATED]") || !strings.Contains(out, "received_fields:") {
		t.Fatalf("merge output = %q, want compact update ack", out)
	}
	if strings.Contains(out, "tmp/reddit.png") || strings.Contains(out, "Browser automation can be acquired locally") {
		t.Fatalf("merge output = %q, want compact ack without appended state echo", out)
	}

	state, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if state.Stage != "execution" {
		t.Fatalf("Stage = %q, want execution", state.Stage)
	}
	if state.Proposal.Status != session.ProposalStatusApproved {
		t.Fatalf("Proposal status = %q, want approved", state.Proposal.Status)
	}
	if len(state.Findings) != 2 {
		t.Fatalf("findings len = %d, want 2", len(state.Findings))
	}
	if len(state.Artifacts) != 2 {
		t.Fatalf("artifacts len = %d, want 2", len(state.Artifacts))
	}
}

func TestUpdateOperationToolPersistsDurablePhasePlan(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()

	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		json.RawMessage(`{
			"id":"op-phase-plan",
			"objective":"Deliver the Lighthouse inbox workflow end to end.",
			"status":"blocked",
			"stage":"phase_plan",
			"summary":"The broad goal is split into durable approval phases.",
			"phase_plan":{
				"id":"lighthouse-inbox-plan",
				"goal":"Deliver the Lighthouse inbox workflow end to end.",
				"phases":[
					{
						"id":"phase-1-contract",
						"summary":"Write the read-only integration contract",
						"status":"completed",
						"authority_class":"read_only_review",
						"bounded_effect":"Inspect runtime state and write down the contract only.",
						"validation_plan":["contract references live evidence"]
					},
					{
						"id":"phase-2-implementation",
						"summary":"Implement the local inbox bridge",
						"status":"pending",
						"authority_class":"workspace_write",
						"why_now":"The contract is complete and implementation is the next bounded phase.",
						"bounded_effect":"Edit local files, run tests, and stop before deploy.",
						"allowed_actions":["edit_files","run_tests"],
						"forbidden_actions":["deploy","restart_service"]
					}
				]
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(update_operation phase_plan) err = %v", err)
	}
	if !strings.Contains(out, "[OPERATION_UPDATED]") || !strings.Contains(out, "current_phase: phase-2-implementation") {
		t.Fatalf("update output = %q, want compact ack with current phase", out)
	}
	showOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "update_operation", nil)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(show update_operation) err = %v", err)
	}
	if !strings.Contains(showOut, "phase_plan:") || !strings.Contains(showOut, "phase-2-implementation") {
		t.Fatalf("show output = %q, want rendered phase plan", showOut)
	}

	state, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if state.PhasePlan.ID != "lighthouse-inbox-plan" || state.PhasePlan.CurrentPhaseID != "phase-2-implementation" {
		t.Fatalf("PhasePlan = %#v, want durable current pending phase", state.PhasePlan)
	}
	if len(state.PhasePlan.Phases) != 2 {
		t.Fatalf("phase count = %d, want 2", len(state.PhasePlan.Phases))
	}
	phase := state.PhasePlan.Phases[1]
	if phase.Status != session.PlanStatusPending || phase.AuthorityClass != "workspace_write" {
		t.Fatalf("phase 2 = %#v, want pending workspace_write phase", phase)
	}
	if !phase.RequiresApproval {
		t.Fatalf("phase 2 RequiresApproval = false, want default approval gate")
	}
}

func TestUpdateOperationRejectsExecutablePhaseCompletionWithoutWorkEvidence(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-evidence-gate",
		Objective: "Patch the runtime.",
		Status:    session.OperationStatusActive,
		Stage:     "execution",
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-evidence-gate",
			CurrentPhaseID: "implementation",
			Phases: []session.OperationPhase{{
				ID:             "implementation",
				Summary:        "Patch runtime files",
				Status:         session.PlanStatusInProgress,
				AuthorityClass: "workspace_write",
				BoundedEffect:  "Edit runtime files and run focused tests.",
				AllowedActions: []string{"workspace_write", "run_tests"},
				LeaseID:        "lease-implementation",
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState(seed) err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		json.RawMessage(`{"merge":true,"phase_plan":{"phases":[{"id":"implementation","status":"completed"}]}}`),
	)
	if err == nil || !strings.Contains(err.Error(), "matching successful work evidence") {
		t.Fatalf("ExecuteForSessionPrincipal(update_operation) err = %v, want work evidence rejection", err)
	}
	state, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if state.PhasePlan.Phases[0].Status != session.PlanStatusInProgress {
		t.Fatalf("phase status = %q, want in_progress after rejected completion", state.PhasePlan.Phases[0].Status)
	}
}

func TestUpdateOperationAllowsExecutablePhaseCompletionWithMatchingWorkEvidence(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	now := time.Now().UTC()
	opState := session.OperationState{
		ID:        "op-evidence-gate",
		Objective: "Patch the runtime.",
		Status:    session.OperationStatusActive,
		Stage:     "execution",
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-evidence-gate",
			CurrentPhaseID: "implementation",
			Phases: []session.OperationPhase{{
				ID:             "implementation",
				Summary:        "Patch runtime files",
				Status:         session.PlanStatusInProgress,
				AuthorityClass: "workspace_write",
				BoundedEffect:  "Edit runtime files and run focused tests.",
				AllowedActions: []string{"workspace_write", "run_tests"},
				LeaseID:        "lease-implementation",
			}},
		},
	}
	proposalID := session.OperationPhaseProposalID(opState, opState.PhasePlan.Phases[0])
	opState.Work = session.WorkOperationMetadata{
		LastOperationID:       "op-evidence-gate",
		LastActionOperationID: proposalID,
		LastActionProposalID:  "aprop-" + proposalID,
		LastLeaseID:           "lease-implementation",
		LastWorkMode:          "workspace_write",
		LastSummary:           "Runtime patch completed with tests.",
		LastCompletedAt:       now,
		LastExecutorUpdatedAt: now,
	}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState(seed) err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		json.RawMessage(`{"merge":true,"phase_plan":{"phases":[{"id":"implementation","status":"completed"}]}}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(update_operation) err = %v", err)
	}
	if !strings.Contains(out, "[OPERATION_UPDATED]") {
		t.Fatalf("update output = %q, want operation update ack", out)
	}
	state, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if state.PhasePlan.Phases[0].Status != session.PlanStatusCompleted {
		t.Fatalf("phase status = %q, want completed with work evidence", state.PhasePlan.Phases[0].Status)
	}
	if state.Work.LastLeaseID != "lease-implementation" {
		t.Fatalf("work metadata = %#v, want preserved completion evidence", state.Work)
	}
}

func TestUpdateOperationCompletionIgnoresStaleSupersededExecutablePhase(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	now := time.Now().UTC()
	opState := session.OperationState{
		ID:        "op-superseded-evidence-gate",
		Objective: "Commit and push the accepted recovery package.",
		Status:    session.OperationStatusActive,
		Stage:     "execution",
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-superseded-evidence-gate",
			CurrentPhaseID: "whitespace-fix-commit-push",
			Phases: []session.OperationPhase{
				{
					ID:                "stage-scan-before-commit-push",
					Summary:           "Stage and scan repo-safe files before commit/push.",
					Status:            session.PlanStatusPending,
					AuthorityClass:    "workspace_write",
					BoundedEffect:     "Stage and scan files only.",
					AllowedActions:    []string{"edit_workspace", "run_checks"},
					StaleAuthority:    true,
					BlockedReasonCode: "superseded_phase",
				},
				{
					ID:                 "whitespace-fix-commit-push",
					Summary:            "Fix whitespace, re-scan, commit, and push.",
					Status:             session.PlanStatusInProgress,
					AuthorityClass:     "workspace_write_commit_push",
					BoundedEffect:      "Fix two whitespace-only issues, re-scan, commit, and push.",
					AllowedActions:     []string{"edit_workspace", "run_checks", "git_commit", "git_push"},
					LeaseID:            "lease-whitespace-fix-commit-push",
					SupersedesPhaseIDs: []string{"stage-scan-before-commit-push"},
				},
			},
		},
	}
	proposalID := session.OperationPhaseProposalID(opState, opState.PhasePlan.Phases[1])
	opState.Work = session.WorkOperationMetadata{
		LastOperationID:       opState.ID,
		LastActionOperationID: proposalID,
		LastActionProposalID:  "aprop-" + proposalID,
		LastLeaseID:           "lease-whitespace-fix-commit-push",
		LastWorkMode:          session.OperationPhaseWorkAction(opState.PhasePlan.Phases[1]),
		LastSummary:           "Committed and pushed the accepted recovery package.",
		LastCompletedAt:       now,
		LastExecutorUpdatedAt: now,
		Commands:              []string{"git commit -m evidence", "git push origin main"},
	}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState(seed) err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		json.RawMessage(`{"merge":true,"status":"completed","phase_plan":{"phases":[{"id":"whitespace-fix-commit-push","status":"completed"}]}}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(update_operation) err = %v", err)
	}
	state, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if state.Status != session.OperationStatusCompleted {
		t.Fatalf("status = %q, want completed", state.Status)
	}
	if !state.PhasePlan.Phases[0].StaleAuthority || state.PhasePlan.Phases[0].Status == session.PlanStatusCompleted {
		t.Fatalf("stale phase = %#v, want stale non-completed non-blocking phase", state.PhasePlan.Phases[0])
	}
	if state.PhasePlan.Phases[1].Status != session.PlanStatusCompleted {
		t.Fatalf("active phase = %#v, want completed", state.PhasePlan.Phases[1])
	}
}

func TestUpdateOperationRejectsModelMarkedStalePhaseCompletionWithoutSupersedingEvidence(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	opState := updateOperationExecutableEvidenceGateState()
	opState.PhasePlan.Phases[0].StaleAuthority = true
	opState.PhasePlan.Phases[0].BlockedReasonCode = "stale_authority"
	opState.Work = session.WorkOperationMetadata{}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState(seed) err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		json.RawMessage(`{"merge":true,"status":"completed","phase_plan":{"phases":[{"id":"implementation","status":"completed"}]}}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(update_operation) err = nil, want stale flag without superseding evidence rejected")
	}
	if !strings.Contains(err.Error(), "matching successful work evidence") {
		t.Fatalf("err = %v, want work evidence rejection", err)
	}
}

func TestUpdateOperationRejectsModelAuthoredPhaseLeaseID(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	opState := updateOperationExecutableEvidenceGateState()
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState(seed) err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		json.RawMessage(`{"merge":true,"phase_plan":{"phases":[{"id":"implementation","lease_id":"lease-forged"}]}}`),
	)
	if err == nil || !strings.Contains(err.Error(), "lease_id is runtime-owned") {
		t.Fatalf("ExecuteForSessionPrincipal(update_operation) err = %v, want runtime-owned lease_id rejection", err)
	}
}

func TestUpdateOperationRejectsExecutablePhaseCompletionWithStaleWorkEvidence(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		mutate func(*session.WorkOperationMetadata)
	}{
		{
			name: "work mode mismatch",
			mutate: func(work *session.WorkOperationMetadata) {
				work.LastWorkMode = session.AuthorityWorkActionReadOnly
			},
		},
		{
			name: "phase proposal mismatch",
			mutate: func(work *session.WorkOperationMetadata) {
				work.LastActionOperationID = "phase-other-operation"
			},
		},
		{
			name: "action proposal mismatch",
			mutate: func(work *session.WorkOperationMetadata) {
				work.LastActionProposalID = "aprop-other-operation"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, store := newDurableAgentToolRegistry(t)
			key := adminSessionKey()
			now := time.Now().UTC()
			opState := updateOperationExecutableEvidenceGateState()
			work := updateOperationMatchingWorkEvidence(opState, now)
			tc.mutate(&work)
			opState.Work = work
			if err := store.UpdateOperationState(key, opState); err != nil {
				t.Fatalf("UpdateOperationState(seed) err = %v", err)
			}

			_, err := registry.ExecuteForSessionPrincipal(
				context.Background(),
				principal.Principal{Role: principal.RoleAdmin},
				key,
				"update_operation",
				json.RawMessage(`{"merge":true,"phase_plan":{"phases":[{"id":"implementation","status":"completed"}]}}`),
			)
			if err == nil || !strings.Contains(err.Error(), "matching successful work evidence") {
				t.Fatalf("ExecuteForSessionPrincipal(update_operation) err = %v, want stale work evidence rejection", err)
			}
		})
	}
}

func TestUpdateOperationRejectsExecutablePhasePlanRewriteBypass(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	opState := updateOperationExecutableEvidenceGateState()
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState(seed) err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		json.RawMessage(`{
			"status":"completed",
			"phase_plan":{
				"phases":[{
					"id":"harmless-summary",
					"summary":"Summarize completed findings",
					"status":"completed",
					"authority_class":"read_only_review",
					"allowed_actions":["read_only_review","report_evidence"]
				}]
			}
		}`),
	)
	if err == nil || !strings.Contains(err.Error(), "cannot remove in-progress executable phase") {
		t.Fatalf("ExecuteForSessionPrincipal(update_operation) err = %v, want phase rewrite bypass rejection", err)
	}
}

func TestUpdateOperationRejectsPendingExecutablePhaseCompletionWithoutWorkEvidence(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	opState := updateOperationPendingExecutableEvidenceGateState()
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState(seed) err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		json.RawMessage(`{"merge":true,"phase_plan":{"phases":[{"id":"implementation","status":"completed"}]}}`),
	)
	if err == nil || !strings.Contains(err.Error(), "matching successful work evidence") {
		t.Fatalf("ExecuteForSessionPrincipal(update_operation) err = %v, want pending work evidence rejection", err)
	}
	state, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if state.PhasePlan.Phases[0].Status != session.PlanStatusPending {
		t.Fatalf("phase status = %q, want pending after rejected completion", state.PhasePlan.Phases[0].Status)
	}
}

func TestUpdateOperationRejectsPendingExecutablePhaseDowngradeCompletionWithoutWorkEvidence(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	opState := updateOperationPendingExecutableEvidenceGateState()
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState(seed) err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		json.RawMessage(`{
			"merge":true,
			"phase_plan":{"phases":[{
				"id":"implementation",
				"status":"completed",
				"authority_class":"read_only_review",
				"bounded_effect":"Inspect runtime files and report findings only.",
				"allowed_actions":["read_only_review","report_evidence"]
			}]}
		}`),
	)
	if err == nil || !strings.Contains(err.Error(), "cannot rewrite executable phase") {
		t.Fatalf("ExecuteForSessionPrincipal(update_operation) err = %v, want executable downgrade completion rejection", err)
	}
	state, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	phase := state.PhasePlan.Phases[0]
	if phase.Status != session.PlanStatusPending || phase.AuthorityClass != "workspace_write" {
		t.Fatalf("phase = %#v, want original pending workspace_write phase after rejected downgrade", phase)
	}
}

func TestUpdateOperationAllowsReadOnlyPhaseCompletionWithoutWorkEvidence(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-readonly-evidence-gate",
		Objective: "Inspect PR readiness.",
		Status:    session.OperationStatusActive,
		Stage:     "inspection",
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-readonly-evidence-gate",
			CurrentPhaseID: "inspect",
			Phases: []session.OperationPhase{{
				ID:             "inspect",
				Summary:        "Inspect PR readiness",
				Status:         session.PlanStatusInProgress,
				AuthorityClass: "read_only_review",
				BoundedEffect:  "Inspect local files and report findings.",
				AllowedActions: []string{"read_only_review", "report_evidence"},
				LeaseID:        "lease-inspect",
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState(seed) err = %v", err)
	}

	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		json.RawMessage(`{"merge":true,"phase_plan":{"phases":[{"id":"inspect","status":"completed"}]}}`),
	); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(update_operation) err = %v", err)
	}
	state, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if state.PhasePlan.Phases[0].Status != session.PlanStatusCompleted {
		t.Fatalf("phase status = %q, want read-only phase completed", state.PhasePlan.Phases[0].Status)
	}
}

func TestUpdateOperationAllowsReadOnlyPhaseWithWritePatchWordsWithoutWorkEvidence(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-readonly-prose-evidence-gate",
		Objective: "Inspect PR readiness and write findings.",
		Status:    session.OperationStatusActive,
		Stage:     "inspection",
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-readonly-prose-evidence-gate",
			CurrentPhaseID: "inspect",
			Phases: []session.OperationPhase{{
				ID:             "inspect",
				Summary:        "Write patch-readiness findings",
				Status:         session.PlanStatusInProgress,
				AuthorityClass: "read_only_review",
				BoundedEffect:  "Write a patch-readiness note from inspected files only.",
				AllowedActions: []string{"read_only_review", "report_evidence"},
				LeaseID:        "lease-inspect",
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState(seed) err = %v", err)
	}

	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		json.RawMessage(`{"merge":true,"phase_plan":{"phases":[{"id":"inspect","status":"completed"}]}}`),
	); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(update_operation) err = %v", err)
	}
	state, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if state.PhasePlan.Phases[0].Status != session.PlanStatusCompleted {
		t.Fatalf("phase status = %q, want read-only prose phase completed", state.PhasePlan.Phases[0].Status)
	}
}

func updateOperationExecutableEvidenceGateState() session.OperationState {
	return session.OperationState{
		ID:        "op-evidence-gate",
		Objective: "Patch the runtime.",
		Status:    session.OperationStatusActive,
		Stage:     "execution",
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-evidence-gate",
			CurrentPhaseID: "implementation",
			Phases: []session.OperationPhase{{
				ID:             "implementation",
				Summary:        "Patch runtime files",
				Status:         session.PlanStatusInProgress,
				AuthorityClass: "workspace_write",
				BoundedEffect:  "Edit runtime files and run focused tests.",
				AllowedActions: []string{"workspace_write", "run_tests"},
				LeaseID:        "lease-implementation",
			}},
		},
	}
}

func updateOperationPendingExecutableEvidenceGateState() session.OperationState {
	state := updateOperationExecutableEvidenceGateState()
	state.PhasePlan.Phases[0].Status = session.PlanStatusPending
	state.PhasePlan.Phases[0].LeaseID = ""
	return state
}

func updateOperationMatchingWorkEvidence(opState session.OperationState, now time.Time) session.WorkOperationMetadata {
	opState = session.NormalizeOperationState(opState)
	proposalID := session.OperationPhaseProposalID(opState, opState.PhasePlan.Phases[0])
	return session.WorkOperationMetadata{
		LastOperationID:       opState.ID,
		LastActionOperationID: proposalID,
		LastActionProposalID:  "aprop-" + proposalID,
		LastLeaseID:           opState.PhasePlan.Phases[0].LeaseID,
		LastWorkMode:          session.AuthorityWorkActionWorkspaceWrite,
		LastSummary:           "Runtime patch completed with tests.",
		LastCompletedAt:       now,
		LastExecutorUpdatedAt: now,
	}
}

func TestUpdateOperationToolPersistsTypedPhaseGovernanceMetadata(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		json.RawMessage(`{
			"id":"op-consent-plan",
			"status":"blocked",
			"phase_plan":{
				"id":"consent-plan",
				"goal":"Prepare consent-first external channel work.",
				"phases":[
					{
						"id":"phase-consent",
						"summary":"Wait for explicit opt-in before external-channel intake",
						"status":"pending",
						"authority_class":"read_only_review",
						"gate_level":"escalated-operator-approval",
						"gate_reason_code":"external-account-auth-status",
						"approval_subject":"operator",
						"autoapprove_eligible":false,
						"blocked_reason_code":"waiting-for-opt-in",
						"requires_opt_in":true,
						"requires_consent":true,
						"supersedes_phase_ids":["phase-old"],
						"stale_authority":true
					}
				]
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(update_operation phase metadata) err = %v", err)
	}
	if !strings.Contains(out, "[OPERATION_UPDATED]") || !strings.Contains(out, "current_phase: phase-consent") {
		t.Fatalf("update output = %q, want compact ack with current phase", out)
	}
	showOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "update_operation", nil)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(show update_operation) err = %v", err)
	}
	for _, want := range []string{"gate_level: escalated_operator_approval", "gate_reason_code: external_account_auth_status", "approval_subject: operator", "autoapprove_eligible: false", "blocked_reason_code: waiting_for_opt_in", "requires_opt_in: true", "requires_consent: true", "supersedes_phase_ids: phase-old", "stale_authority: true"} {
		if !strings.Contains(showOut, want) {
			t.Fatalf("show output = %q, want %q", showOut, want)
		}
	}

	state, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if len(state.PhasePlan.Phases) != 1 {
		t.Fatalf("phase count = %d, want 1", len(state.PhasePlan.Phases))
	}
	phase := state.PhasePlan.Phases[0]
	if phase.GateLevel != "escalated_operator_approval" ||
		phase.GateReasonCode != "external_account_auth_status" ||
		phase.ApprovalSubject != "operator" ||
		phase.AutoApproveEligible == nil ||
		*phase.AutoApproveEligible ||
		phase.BlockedReasonCode != "waiting_for_opt_in" ||
		!phase.RequiresOptIn ||
		!phase.RequiresConsent ||
		!phase.StaleAuthority {
		t.Fatalf("phase metadata = %#v, want typed blocker flags", phase)
	}
	if len(phase.SupersedesPhaseIDs) != 1 || phase.SupersedesPhaseIDs[0] != "phase-old" {
		t.Fatalf("phase SupersedesPhaseIDs = %#v, want phase-old", phase.SupersedesPhaseIDs)
	}
}

func TestUpdateOperationToolRejectsInvalidProposalStatus(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"update_operation",
		json.RawMessage(`{
			"proposal":{"status":"maybe"}
		}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(update_operation) err = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "proposal status") {
		t.Fatalf("err = %v, want proposal status validation", err)
	}
}

func TestUpdateOperationToolPersistsAndRendersPlanLease(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()

	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		json.RawMessage(`{
			"id":"op-plan-lease",
			"objective":"Reduce approval pings without widening authority.",
			"status":"blocked",
			"stage":"plan_lease_proposal",
			"summary":"A plan lease is pending explicit approval.",
			"plan_lease":{
				"id":"plan-lease-20260503",
				"summary":"Low-risk coordination lease",
				"status":"proposed",
				"turn_budget":5,
				"covered_phase_ids":["phase-1","phase-2"],
				"lanes":[
					{"id":"readonly","summary":"Read-only review","authority_class":"read_only_review","expected_turns":3,"allowed_actions":["inspect_status","draft_proposal"]},
					{"id":"child-checkins","summary":"Child status check-ins","authority_class":"read_only_review","expected_turns":2,"allowed_actions":["request_child_status"],"forbidden_actions":["grant_or_revoke_capability"]}
				],
				"evidence_digest":{
					"turns_spent":1,
					"lanes_used":["readonly"],
					"completed":["drafted lease protocol"],
					"interrupts_raised":["policy_or_grant_change"],
					"evidence_refs":["runtime/continuation_materialize.go"],
					"residual_risk":"Not deployed or activated.",
					"suggested_next_lease":"Focused tests only."
				}
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(update_operation plan_lease) err = %v", err)
	}
	if !strings.Contains(out, "[OPERATION_UPDATED]") || !strings.Contains(out, "received_fields:") {
		t.Fatalf("update output = %q, want compact ack", out)
	}
	showOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "update_operation", nil)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(show update_operation) err = %v", err)
	}
	for _, want := range []string{"plan_lease:", "Low-risk coordination lease", "expected_turns: 3", "hard_interrupts:", "policy_or_grant_change", "child_initiation_lanes:", "capability_request", "authority_note: plan lease is a bounded plan envelope, not a capability grant", "evidence_digest:"} {
		if !strings.Contains(showOut, want) {
			t.Fatalf("show output = %q, want %q", showOut, want)
		}
	}

	state, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	lease := state.PlanLease
	if lease.ID != "plan-lease-20260503" || lease.Status != session.PlanLeaseStatusProposed {
		t.Fatalf("PlanLease = %#v, want proposed lease", lease)
	}
	if lease.TurnBudget != 5 || lease.RemainingTurns != 5 || len(lease.Lanes) != 2 {
		t.Fatalf("PlanLease turns/lanes = %#v", lease)
	}
	if lease.Lanes[0].AuthorityClass != "read_only_review" || lease.Lanes[0].ExpectedTurns != 3 {
		t.Fatalf("PlanLease first lane = %#v", lease.Lanes[0])
	}
	if !containsString(lease.HardInterrupts, "policy_or_grant_change") || !containsString(lease.ChildInitiationLanes, "capability_request") {
		t.Fatalf("PlanLease guardrails = hard=%#v child=%#v", lease.HardInterrupts, lease.ChildInitiationLanes)
	}
	if lease.EvidenceDigest.TurnsSpent != 1 || lease.EvidenceDigest.ResidualRisk != "Not deployed or activated." {
		t.Fatalf("PlanLease evidence = %#v", lease.EvidenceDigest)
	}
}

func TestUpdateOperationToolRequiresPlanLeaseLaneAuthorityAndTurns(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_operation",
		json.RawMessage(`{
			"id":"op-plan-lease",
			"plan_lease":{
				"id":"bad-plan-lease",
				"summary":"Invalid lease",
				"status":"proposed",
				"lanes":[{"id":"vague","summary":"Too vague"}]
			}
		}`),
	)
	if err == nil || !strings.Contains(err.Error(), "requires authority_class") {
		t.Fatalf("err = %v, want lane authority validation", err)
	}
}

func TestUpdateOperationAckUsesLedgerSnapshotPointer(t *testing.T) {
	t.Parallel()

	store := newToolTestStore(t)
	registry := NewRegistry(t.TempDir(), time.Second).WithSessionStore(store)
	out, err := registry.updateOperation(
		context.Background(),
		json.RawMessage(`{"id":"op-ledger","status":"active","stage":"slice-1","summary":"short"}`),
		adminSessionKey(),
	)
	if err != nil {
		t.Fatalf("updateOperation() err = %v", err)
	}
	if !strings.Contains(out, "snapshot: ledger:operations/op-ledger@") {
		t.Fatalf("update_operation ack = %q, want ledger snapshot pointer", out)
	}
	if strings.Contains(out, "summary: short") {
		t.Fatalf("update_operation ack echoed full summary: %q", out)
	}
}

func TestRequestApprovalToolDefinitionExposesRequiredCapabilityGrants(t *testing.T) {
	t.Parallel()

	var schema map[string]any
	if err := json.Unmarshal(requestApprovalToolDefinition().Parameters, &schema); err != nil {
		t.Fatalf("decode request_approval schema: %v", err)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("request_approval schema properties = %#v, want object", schema["properties"])
	}
	phase, ok := properties["phase"].(map[string]any)
	if !ok {
		t.Fatalf("request_approval phase schema = %#v, want object", properties["phase"])
	}
	phaseProperties, ok := phase["properties"].(map[string]any)
	if !ok {
		t.Fatalf("request_approval phase properties = %#v, want object", phase["properties"])
	}
	grantSchema, ok := phaseProperties["required_capability_grants"].(map[string]any)
	if !ok {
		t.Fatalf("request_approval phase properties = %#v, want required_capability_grants", phaseProperties)
	}
	if grantSchema["type"] != "array" {
		t.Fatalf("required_capability_grants type = %#v, want array", grantSchema["type"])
	}
	items, ok := grantSchema["items"].(map[string]any)
	if !ok {
		t.Fatalf("required_capability_grants items = %#v, want object schema", grantSchema["items"])
	}
	itemProperties, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatalf("required_capability_grants item properties = %#v, want object", items["properties"])
	}
	for _, want := range []string{"request_id", "grant_id", "kind", "target_resource", "granted_to", "allowed_actions", "contract", "constraints", "expires_at"} {
		if _, ok := itemProperties[want]; !ok {
			t.Fatalf("required_capability_grants item properties = %#v, want %q", itemProperties, want)
		}
	}
}

func TestDefinitionsIncludeRequestApprovalToolWhenStoreConfigured(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), time.Second)
	names := make([]string, 0, len(registry.Definitions()))
	for _, def := range registry.Definitions() {
		names = append(names, def.Name)
	}
	if containsString(names, "request_approval") {
		t.Fatalf("definitions without store = %#v, do not want request_approval", names)
	}

	store := newToolTestStore(t)
	registry = NewRegistry(t.TempDir(), time.Second).WithSessionStore(store)
	names = names[:0]
	for _, def := range registry.Definitions() {
		names = append(names, def.Name)
	}
	if !containsString(names, "request_approval") {
		t.Fatalf("definitions with store = %#v, want request_approval", names)
	}
}

func TestRequestApprovalToolPersistsPendingManualApprovalPhase(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"request_approval",
		json.RawMessage(`{
			"objective":"Make approval buttons first-class.",
			"phase":{
				"id":"phase-request-approval",
				"summary":"Implement request approval native tool",
				"authority_class":"workspace_write",
				"why_now":"Text-only approval prompts are brittle.",
				"bounded_effect":"Edit local files and run targeted tests; stop before deploy.",
				"allowed_actions":["edit_files","run_tests"],
				"forbidden_actions":["commit","deploy","restart_service"],
				"validation_plan":["targeted tests pass"],
				"required_capability_grants":[{
					"request_id":"cap-imexx-github",
					"kind":"external_account",
					"target_resource":"github:imexx/processes",
					"granted_to":"telegram:1001",
					"allowed_actions":["contents:write","pull_requests:write"]
				}]
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(request_approval) err = %v", err)
	}
	if !strings.Contains(out, "[APPROVAL_REQUESTED]") || !strings.Contains(out, "Implement request approval native tool") {
		t.Fatalf("output = %q, want approval request render", out)
	}

	state, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if state.Status != session.OperationStatusBlocked || state.Stage != "approval_request" {
		t.Fatalf("operation status/stage = %q/%q, want blocked approval_request", state.Status, state.Stage)
	}
	if state.PhasePlan.CurrentPhaseID != "phase-request-approval" || len(state.PhasePlan.Phases) != 1 {
		t.Fatalf("phase plan = %#v, want single current approval phase", state.PhasePlan)
	}
	phase := state.PhasePlan.Phases[0]
	if !phase.RequiresApproval || phase.AutoApproveEligible == nil || *phase.AutoApproveEligible {
		t.Fatalf("phase auto approval = requires=%v auto=%#v, want manual approval", phase.RequiresApproval, phase.AutoApproveEligible)
	}
	if phase.Status != session.PlanStatusPending || phase.AuthorityClass != "workspace_write" {
		t.Fatalf("phase = %#v, want pending workspace_write", phase)
	}
	if len(phase.RequiredCapabilityGrants) != 1 {
		t.Fatalf("required capability grants = %#v, want one bundled grant", phase.RequiredCapabilityGrants)
	}
	grant := phase.RequiredCapabilityGrants[0]
	if grant.RequestID != "cap-imexx-github" || grant.Kind != session.CapabilityKindExternalAccount || grant.TargetResource != "github:imexx/processes" || grant.GrantedTo != "telegram:1001" || !containsString(grant.AllowedActions, "contents:write") {
		t.Fatalf("required capability grant = %#v, want parsed Imexx GitHub grant dependency", grant)
	}
}

func TestRequestApprovalToolRejectsInvalidAuthorityContract(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"request_approval",
		json.RawMessage(`{
			"phase":{
				"summary":"Contradictory deploy request",
				"authority_class":"workspace_write",
				"allowed_actions":["edit_files"],
				"forbidden_actions":["workspace_write"]
			}
		}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(request_approval) err = nil, want authority contradiction")
	}
	if !strings.Contains(err.Error(), "request_approval authority contract invalid") || !strings.Contains(err.Error(), "allowed_action_implies_forbidden_authority") {
		t.Fatalf("err = %v, want authority contract diagnostic", err)
	}
}

func TestRequestApprovalToolRejectsPushProseWhenGitPushForbidden(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"request_approval",
		json.RawMessage(`{
			"objective":"Bundle and publish the XPVENTA book artifacts.",
			"phase":{
				"id":"book-bundle-commit-push-v2",
				"summary":"Bundle XPVENTA book artifacts, commit them, and push to the imex repository.",
				"authority_class":"commit",
				"why_now":"The operator approved the release packaging path.",
				"bounded_effect":"Bundle the approved artifacts, create one local commit, and push the current main branch to origin.",
				"allowed_actions":["inspect_git_status","git_commit_book_artifacts","push_main_to_origin"],
				"forbidden_actions":["git_push","deploy","restart_service"],
				"validation_plan":["report commit hash and remote head"]
			}
		}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(request_approval) err = nil, want push/prose contradiction")
	}
	if !strings.Contains(err.Error(), "request_approval authority contract invalid") || !strings.Contains(err.Error(), session.AuthorityContradictionReasonProposalRequiresForbiddenGitPush) {
		t.Fatalf("err = %v, want proposal_requires_forbidden_git_push diagnostic", err)
	}
}

func TestOperationCompletionEvidenceStatusExplainsMismatch(t *testing.T) {
	t.Parallel()

	state := updateOperationExecutableEvidenceGateState()
	state.Work = session.WorkOperationMetadata{
		LastOperationID:       state.ID,
		LastActionOperationID: "different-proposal",
		LastActionProposalID:  "aprop-different-proposal",
		LastLeaseID:           state.PhasePlan.Phases[0].LeaseID,
		LastWorkMode:          session.OperationPhaseWorkAction(state.PhasePlan.Phases[0]),
		LastCompletedAt:       time.Now().UTC(),
	}

	statuses := OperationCompletionEvidenceStatus(state)
	if len(statuses) != 1 {
		t.Fatalf("statuses len = %d, want 1", len(statuses))
	}
	status := statuses[0]
	if status.Satisfied {
		t.Fatalf("status.Satisfied = true, want false")
	}
	if status.Reason != "last work does not match the current phase proposal" {
		t.Fatalf("status.Reason = %q", status.Reason)
	}
	if status.ReasonCode != "proposal_mismatch" {
		t.Fatalf("status.ReasonCode = %q", status.ReasonCode)
	}
	if status.PhaseID != "implementation" || status.EvidenceKind != "work_metadata" {
		t.Fatalf("status = %#v", status)
	}
}

func TestOperationCompletionEvidenceStatusReportsSatisfied(t *testing.T) {
	t.Parallel()

	state := updateOperationExecutableEvidenceGateState()
	phase := state.PhasePlan.Phases[0]
	proposalID := session.OperationPhaseProposalID(state, phase)
	now := time.Now().UTC()
	state.Work = session.WorkOperationMetadata{
		LastOperationID:       state.ID,
		LastActionOperationID: proposalID,
		LastActionProposalID:  "aprop-" + proposalID,
		LastLeaseID:           phase.LeaseID,
		LastWorkMode:          session.OperationPhaseWorkAction(phase),
		LastCompletedAt:       now,
	}

	statuses := OperationCompletionEvidenceStatus(state)
	if len(statuses) != 1 {
		t.Fatalf("statuses len = %d, want 1", len(statuses))
	}
	status := statuses[0]
	if !status.Satisfied || status.Reason != "" || status.ReasonCode != "" {
		t.Fatalf("status = %#v, want satisfied with no reason", status)
	}
	if status.CompletedAt == nil || !status.CompletedAt.Equal(now) {
		t.Fatalf("CompletedAt = %#v, want %s", status.CompletedAt, now)
	}
}
