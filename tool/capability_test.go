//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestCapabilityRequestParentAdminGrantFlow(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	child := principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: 300}
	parent := principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: 200}
	otherParent := principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: 201}
	admin := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}

	out, err := registry.ExecuteForSessionPrincipal(context.Background(), child, key, "capability_request", json.RawMessage(`{
		"action":"request_submit",
		"request_id":"cap-family-amazon",
		"kind":"purchase",
		"target_resource":"amazon",
		"requested_for":"family-child",
		"parent_principal":"telegram:200",
		"purpose":"order approved school supplies",
		"risk_class":"spend",
		"contract":{"success":"only approved supplies"},
		"constraints":{"max_usd":50}
	}`))
	if err != nil {
		t.Fatalf("capability_request request_submit err = %v", err)
	}
	if !strings.Contains(out, "[CAPABILITY_REQUEST]") || !strings.Contains(out, "review_status: proposed") {
		t.Fatalf("request_submit output = %q, want proposed capability request", out)
	}

	_, err = registry.ExecuteForSessionPrincipal(context.Background(), admin, key, "capability_authority", json.RawMessage(`{
		"action":"request_review",
		"request_id":"cap-family-amazon",
		"review_status":"approved",
		"rationale":"admin tried to skip parent"
	}`))
	if err == nil || !strings.Contains(err.Error(), "requires parent_approved first") {
		t.Fatalf("admin approve before parent err = %v, want parent_approved-first denial", err)
	}

	_, err = registry.ExecuteForSessionPrincipal(context.Background(), otherParent, key, "capability_authority", json.RawMessage(`{
		"action":"request_review",
		"request_id":"cap-family-amazon",
		"review_status":"parent_approved"
	}`))
	if err == nil || !strings.Contains(err.Error(), "requires parent_principal") {
		t.Fatalf("other parent review err = %v, want parent principal denial", err)
	}

	out, err = registry.ExecuteForSessionPrincipal(context.Background(), parent, key, "capability_authority", json.RawMessage(`{
		"action":"request_review",
		"request_id":"cap-family-amazon",
		"review_status":"parent_approved",
		"rationale":"bounded spend is okay"
	}`))
	if err != nil {
		t.Fatalf("parent request_review err = %v", err)
	}
	if !strings.Contains(out, "review_status: parent_approved") {
		t.Fatalf("parent request_review output = %q, want parent_approved", out)
	}

	_, err = registry.ExecuteForSessionPrincipal(context.Background(), parent, key, "capability_authority", json.RawMessage(`{
		"action":"grant_set",
		"request_id":"cap-family-amazon",
		"principal":"family-child"
	}`))
	if err == nil || !strings.Contains(err.Error(), "admin-only") {
		t.Fatalf("parent grant_set err = %v, want admin-only denial", err)
	}

	out, err = registry.ExecuteForSessionPrincipal(context.Background(), admin, key, "capability_authority", json.RawMessage(`{
		"action":"request_review",
		"request_id":"cap-family-amazon",
		"review_status":"approved",
		"rationale":"parent endorsed"
	}`))
	if err != nil {
		t.Fatalf("admin request_review err = %v", err)
	}
	if !strings.Contains(out, "review_status: approved") {
		t.Fatalf("admin request_review output = %q, want approved", out)
	}

	out, err = registry.ExecuteForSessionPrincipal(context.Background(), admin, key, "capability_authority", json.RawMessage(`{
		"action":"grant_set",
		"request_id":"cap-family-amazon",
		"grant_id":"capg-family-amazon",
		"principal":"family-child",
		"allowed_actions":["order"],
		"expires_in_seconds":3600
	}`))
	if err != nil {
		t.Fatalf("grant_set err = %v", err)
	}
	if !strings.Contains(out, "[CAPABILITY_GRANT]") || !strings.Contains(out, "status: active") {
		t.Fatalf("grant_set output = %q, want active grant", out)
	}

	out, err = registry.ExecuteForSessionPrincipal(context.Background(), admin, key, "capability_authority", json.RawMessage(`{
		"action":"access_check",
		"kind":"purchase",
		"target_resource":"amazon",
		"principal":"family-child",
		"capability_action":"order"
	}`))
	if err != nil {
		t.Fatalf("access_check active err = %v", err)
	}
	if !strings.Contains(out, "allowed: true") || !strings.Contains(out, "grant_id: capg-family-amazon") {
		t.Fatalf("access_check active output = %q, want allowed grant", out)
	}

	out, err = registry.ExecuteForSessionPrincipal(context.Background(), admin, key, "capability_authority", json.RawMessage(`{
		"action":"grant_revoke",
		"grant_id":"capg-family-amazon",
		"rationale":"test revoke"
	}`))
	if err != nil {
		t.Fatalf("grant_revoke err = %v", err)
	}
	if !strings.Contains(out, "status: revoked") {
		t.Fatalf("grant_revoke output = %q, want revoked", out)
	}

	out, err = registry.ExecuteForSessionPrincipal(context.Background(), admin, key, "capability_authority", json.RawMessage(`{
		"action":"access_check",
		"kind":"purchase",
		"target_resource":"amazon",
		"principal":"family-child",
		"capability_action":"order"
	}`))
	if err != nil {
		t.Fatalf("access_check revoked err = %v", err)
	}
	if !strings.Contains(out, "allowed: false") {
		t.Fatalf("access_check revoked output = %q, want denied", out)
	}

	events, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	for _, eventType := range []string{core.ExecutionEventCapabilityRequestCreated, core.ExecutionEventCapabilityReviewed, core.ExecutionEventCapabilityGrantChanged} {
		if !executionEventTypeExists(events, eventType) {
			t.Fatalf("missing %s event", eventType)
		}
	}
}

func TestCapabilityGrantEnablesRegisteredToolWithoutLegacyExposure(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	script := filepath.Join(registry.workspace, "run.sh")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\necho '{\"summary\":\"grant-ok\"}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	manifest := ExternalToolManifest{
		Name:      "browse_page",
		Owner:     "idolum-email",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		IO:        ExternalToolManifestIO{OutputSchema: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"]}`)},
	}
	if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest}); err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	seedVerifiedExternalToolLifecycle(t, registry, store, manifest, sandbox.Scope{WorkingRoot: registry.workspace})
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "browse_page", ImplementationRef: "external:browse_page", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-tool-browse",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindTool,
		TargetResource: "browse_page",
		AllowedActions: []string{"invoke"},
		Contract:       `{}`,
		Constraints:    `{}`,
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	defs := registry.DefinitionsForPrincipal(principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001})
	if !toolDefExists(defs, "browse_page") {
		t.Fatalf("DefinitionsForPrincipal() missing grant-authorized browse_page: %#v", defs)
	}
	out, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}, adminSessionKey(), "browse_page", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(browse_page) err = %v", err)
	}
	if out != `{"summary":"grant-ok"}` {
		t.Fatalf("browse_page output = %q, want grant-ok", out)
	}
	grant, ok, err := store.CapabilityGrant("capg-tool-browse")
	if err != nil {
		t.Fatalf("CapabilityGrant() err = %v", err)
	}
	if !ok || grant.InvocationCount != 1 {
		t.Fatalf("CapabilityGrant invocation count = %#v ok=%t, want one runtime invocation", grant, ok)
	}
}

func TestToolRequestCreatesCanonicalCapabilityRequest(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	actor := principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: 300}
	_, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, adminSessionKey(), "tool_request", json.RawMessage(`{
		"action":"proposal_submit",
		"proposal_id":"tp-compat",
		"tool_name":"search_web",
		"why_now":"summarize public job descriptions",
		"contract":{"inputs":{"query":"string"}}
	}`))
	if err != nil {
		t.Fatalf("tool_request proposal_submit err = %v", err)
	}
	request, ok, err := store.CapabilityRequest("cap-tp-compat")
	if err != nil {
		t.Fatalf("CapabilityRequest() err = %v", err)
	}
	if !ok {
		t.Fatal("CapabilityRequest(cap-tp-compat) ok=false, want canonical request")
	}
	if request.Kind != session.CapabilityKindTool || request.TargetResource != "search_web" || request.RequestedBy != "telegram:300" {
		t.Fatalf("CapabilityRequest = %#v, want tool/search_web attributed to telegram:300", request)
	}
}
