//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestRequestApprovalToolMaterializesVisibleContinuationWithCapabilityDependency(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutonomy(context.Background(), 9044, 1001, "leased 15m all"); err != nil {
		t.Fatalf("ConfigureAutonomy() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 9044, 1001, "15m all"); err != nil {
		t.Fatalf("ConfigureAutoApproval() err = %v", err)
	}
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-imexx-github-runtime",
		RequestedBy:    "telegram:1001",
		RequestedFor:   "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github:imexx/processes",
		Purpose:        "Push Imexx process scaffold after approval.",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}

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

	key := session.SessionKey{ChatID: 9044, UserID: 0, Scope: telegramDMScopeRef(9044)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-imexx-request-approval",
		Status:    session.OperationStatusActive,
		PhasePlan: session.OperationPhasePlan{ID: "plan-imexx-request-approval", Goal: "Ship Imexx scaffold safely"},
	}); err != nil {
		t.Fatalf("UpdateOperationState(seed operation) err = %v", err)
	}
	out, err := tools.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		key,
		"request_approval",
		json.RawMessage(`{
			"objective":"Make approval cards first-class.",
			"phase":{
				"id":"phase-request-approval-runtime",
				"summary":"Prepare Imexx process scaffold",
				"authority_class":"workspace_write",
				"why_now":"The operator approved a narrow scaffold-preparation phase that depends on GitHub access metadata.",
				"bounded_effect":"Prepare only the non-secret Imexx process scaffold and stop before commit, deploy, restart, or unrelated external effects.",
				"allowed_actions":["edit_files","run_tests"],
				"forbidden_actions":["commit","deploy","restart_service","external_send_or_contact"],
				"validation_plan":["request_approval materializes visible buttons"],
				"required_capability_grants":[{
					"request_id":"cap-imexx-github-runtime",
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
	if !strings.Contains(out, "[APPROVAL_REQUESTED]") {
		t.Fatalf("request_approval output = %q, want approval requested marker", out)
	}
	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9044, SenderID: 1001, Text: "show approval buttons", MessageID: 77},
		"show approval buttons",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want visible approval prompt")
	}

	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	var labels []string
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
		labels = continuationButtonLabels(sender.inline[0].rows)
	}
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one approval card", inlineCount)
	}
	for _, want := range []string{"Prepare Imexx process scaffold", "external_account", "github:imexx/processes", "cap-imexx-github-runtime", "contents:write"} {
		if !strings.Contains(inlineText, want) {
			t.Fatalf("inline text = %q, want %q", inlineText, want)
		}
	}
	if got, want := labels, []string{"Start", "Details", "Change", "Pause", "Stop"}; !equalStringSlices(got, want) {
		t.Fatalf("inline labels = %#v, want %#v", got, want)
	}

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("continuation = %#v, want pending manual lease", cont)
	}
	if cont.ActionProposal.AutoApproveEligible == nil || *cont.ActionProposal.AutoApproveEligible {
		t.Fatalf("autoapprove_eligible = %#v, want manual button-backed request", cont.ActionProposal.AutoApproveEligible)
	}
	if cont.ActionProposal.RiskClass != "workspace_write" || !actionListContains(cont.ActionProposal.AllowedActions, "edit_files") {
		t.Fatalf("action proposal = %#v, want workspace_write edit_files", cont.ActionProposal)
	}
	if len(cont.ContinuationLease.RequiredCapabilityGrants) != 1 {
		t.Fatalf("continuation lease grants = %#v, want required capability grant preserved", cont.ContinuationLease.RequiredCapabilityGrants)
	}
	grantSpec := cont.ContinuationLease.RequiredCapabilityGrants[0]
	if grantSpec.RequestID != "cap-imexx-github-runtime" || grantSpec.TargetResource != "github:imexx/processes" || !actionListContains(grantSpec.AllowedActions, "contents:write") {
		t.Fatalf("grant spec = %#v, want Imexx GitHub dependency", grantSpec)
	}
	leases, err := store.ActiveOperatorAutoApprovalLeases(9044, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 1 || leases[0].UsedCount != 0 {
		t.Fatalf("autoapproval leases = %#v, want visible request to bypass autoapproval", leases)
	}
}
