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
	for _, want := range []string{"gate_level: escalated_operator_approval", "gate_reason_code: external_account_auth_status", "approval_subject: operator", "autoapprove_eligible: false", "blocked_reason_code: waiting_for_opt_in", "requires_opt_in: true", "requires_consent: true", "supersedes_phase_ids: phase-old", "stale_authority: true"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, want %q", out, want)
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
	for _, want := range []string{"plan_lease:", "Low-risk coordination lease", "expected_turns: 3", "hard_interrupts:", "policy_or_grant_change", "child_initiation_lanes:", "capability_request", "authority_note: plan lease is a bounded plan envelope, not a capability grant", "evidence_digest:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, want %q", out, want)
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
