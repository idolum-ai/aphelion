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
	if !strings.Contains(out, "[OPERATION_UPDATED]") || !strings.Contains(out, "Investigate my internet footprint.") {
		t.Fatalf("update output = %q, want updated operation summary", out)
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
	if !strings.Contains(out, "tmp/reddit.png") {
		t.Fatalf("merge output = %q, want appended artifact", out)
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
	if !strings.Contains(out, "phase_plan:") || !strings.Contains(out, "phase-2-implementation") {
		t.Fatalf("update output = %q, want rendered phase plan", out)
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
