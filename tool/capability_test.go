//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	out, err = registry.ExecuteForSessionPrincipal(context.Background(), admin, key, "capability_authority", json.RawMessage(`{
		"action":"request_review",
		"request_id":"cap-family-amazon",
		"review_status":"approved",
		"rationale":"admin tried to skip parent"
	}`))
	if err == nil || !strings.Contains(err.Error(), "requires parent_approved first") {
		t.Fatalf("admin approve before parent err = %v, want parent_approved-first denial", err)
	}
	if !strings.Contains(out, "[CAPABILITY_BLOCKED]") || !strings.Contains(out, "next_action") {
		t.Fatalf("admin approve before parent output = %q, want actionable block", out)
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

func TestCapabilityGrantSetRejectsMissingDurableAgentTarget(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	admin := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-missing-child",
		RequestedBy:    "telegram:1001",
		RequestedFor:   "durable_agent:missing-child",
		Kind:           session.CapabilityKindTool,
		TargetResource: "public-feed-readonly",
		Purpose:        "grant a child tool that cannot be woken",
		ReviewStatus:   session.CapabilityReviewStatusApproved,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(context.Background(), admin, key, "capability_authority", json.RawMessage(`{
		"action":"grant_set",
		"request_id":"cap-missing-child",
		"grant_id":"capg-missing-child",
		"principal":"durable_agent:missing-child",
		"allowed_actions":["invoke"]
	}`))
	if err == nil || !strings.Contains(err.Error(), `target durable agent "missing-child" does not exist`) {
		t.Fatalf("grant_set err = %v, want missing durable agent preflight", err)
	}
	if _, ok, err := store.CapabilityGrant("capg-missing-child"); err != nil {
		t.Fatalf("CapabilityGrant() err = %v", err)
	} else if ok {
		t.Fatal("capg-missing-child was stored despite missing durable target")
	}
}

func TestCapabilityGrantSetNotifiesActiveGrantObserver(t *testing.T) {
	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	admin := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:     "child-alpha",
		ChannelKind: "manual_channel",
		WakeupMode:  "manual",
		Status:      "active",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:         "codex",
			CodexAuthSource: "codex_cli",
			CodexHome:       "/tmp/codex-home",
		},
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-observed",
		RequestedBy:    "durable_agent:child-alpha",
		RequestedFor:   "durable_agent:child-alpha",
		Kind:           session.CapabilityKindTool,
		TargetResource: "codex",
		Purpose:        "allow child agent to use codex",
		ReviewStatus:   session.CapabilityReviewStatusApproved,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	observed := make(chan session.CapabilityGrant, 1)
	registry.WithCapabilityGrantObserver(func(_ context.Context, observedKey session.SessionKey, grant session.CapabilityGrant) {
		if observedKey.ChatID != key.ChatID {
			t.Fatalf("observer key chat_id = %d, want %d", observedKey.ChatID, key.ChatID)
		}
		observed <- grant
	})

	out, err := registry.ExecuteForSessionPrincipal(context.Background(), admin, key, "capability_authority", json.RawMessage(`{
		"action":"grant_set",
		"request_id":"cap-observed",
		"grant_id":"capg-observed",
		"principal":"durable_agent:child-alpha",
		"allowed_actions":["invoke"]
	}`))
	if err != nil {
		t.Fatalf("grant_set err = %v", err)
	}
	if !strings.Contains(out, "status: active") {
		t.Fatalf("grant_set output = %q, want active", out)
	}
	var grant session.CapabilityGrant
	select {
	case grant = <-observed:
	case <-time.After(time.Second):
		t.Fatal("observer was not called")
	}
	if grant.GrantID != "capg-observed" || grant.GrantedTo != "durable_agent:child-alpha" || grant.Status != session.CapabilityGrantStatusActive {
		t.Fatalf("observed grant = %#v, want active capg-observed", grant)
	}
}

func TestCapabilityGrantSetDoesNotBlockOnObserver(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	admin := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-blocking-observer",
		RequestedBy:    "telegram:1001",
		RequestedFor:   "telegram:1001",
		Kind:           session.CapabilityKindTool,
		TargetResource: "codex",
		Purpose:        "prove observer side effects do not hold the tool open",
		ReviewStatus:   session.CapabilityReviewStatusApproved,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	block := make(chan struct{})
	registry.WithCapabilityGrantObserver(func(context.Context, session.SessionKey, session.CapabilityGrant) {
		<-block
	})
	defer close(block)

	done := make(chan error, 1)
	go func() {
		_, err := registry.ExecuteForSessionPrincipal(context.Background(), admin, key, "capability_authority", json.RawMessage(`{
			"action":"grant_set",
			"request_id":"cap-blocking-observer",
			"grant_id":"capg-blocking-observer",
			"principal":"telegram:1001",
			"allowed_actions":["invoke"]
		}`))
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("grant_set err = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("grant_set blocked on capability grant observer")
	}
}

func TestCapabilityGrantFailureNextActionsDescribeBootstrapCeiling(t *testing.T) {
	t.Parallel()

	out := renderCapabilityGrantFailure(session.CapabilityGrant{
		GrantID:        "capg-test",
		Kind:           session.CapabilityKindPublicWeb,
		TargetResource: "public-web",
		Status:         session.CapabilityGrantStatusFailed,
		GrantedTo:      "durable_agent:child",
	}, &core.DurableAgentPolicyCeilingError{
		Field:     "capability_envelope",
		Requested: []string{"public_web"},
		Allowed:   []string{"conversation"},
	})

	if !strings.Contains(out, "next_action") {
		t.Fatalf("grant failure output = %q, want next_action", out)
	}
	if !strings.Contains(out, "capability_envelope") || !strings.Contains(out, "public_web") || !strings.Contains(out, "conversation") {
		t.Fatalf("grant failure output = %q, want ceiling details", out)
	}
}

func TestCapabilityRequestCanQueueReviewEvent(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	child := principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: 300}
	childKey := session.SessionKey{
		ChatID: 300,
		UserID: 300,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "300"},
	}
	out, err := registry.ExecuteForSessionPrincipal(context.Background(), child, childKey, "capability_request", json.RawMessage(`{
		"action":"request_submit",
		"request_id":"cap-public-web",
		"kind":"public_web",
		"target_resource":"public-chat",
		"requested_for":"public-web-agent",
		"admin_principal":"telegram:1001",
		"purpose":"answer public visitors without affecting the core system",
		"risk_class":"public_surface",
		"contract":{"mode":"read incoming messages and draft bounded replies"},
		"constraints":{"max_messages":20},
		"review_target_chat_id":1001,
		"review_summary":"Public web agent requests bounded public interaction"
	}`))
	if err != nil {
		t.Fatalf("capability_request request_submit with review target err = %v", err)
	}
	if !strings.Contains(out, "request_id: cap-public-web") || !strings.Contains(out, "review_event_id:") {
		t.Fatalf("request_submit output = %q, want request and review event id", out)
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("pending review events len = %d, want 1", len(events))
	}
	event := events[0]
	if event.SourceRole != "capability_request" {
		t.Fatalf("SourceRole = %q, want capability_request", event.SourceRole)
	}
	if event.SourceScope.Kind != session.ScopeKindTelegramDM || event.SourceScope.ID != "300" {
		t.Fatalf("SourceScope = %#v, want telegram_dm 300", event.SourceScope)
	}
	if !strings.Contains(event.Summary, "Public web agent requests bounded public interaction") {
		t.Fatalf("Summary = %q, want explicit review summary", event.Summary)
	}
	if !strings.Contains(event.MetadataJSON, `"request_id":"cap-public-web"`) ||
		!strings.Contains(event.MetadataJSON, `"kind":"public_web"`) ||
		!strings.Contains(event.MetadataJSON, `"requested_for":"public-web-agent"`) {
		t.Fatalf("MetadataJSON = %q, want capability request metadata", event.MetadataJSON)
	}
}

func TestCapabilityGrantEnablesRegisteredToolWithoutRemovedExposureTable(t *testing.T) {
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
		Owner:     "child-alpha",
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
	grantAuthorityUseLease(t, store, adminSessionKey())

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

func TestCapabilityGrantRendersFirstClassChildRuntimeContract(t *testing.T) {
	registry, store := newDurableAgentToolRegistry(t)
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	request, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-child-runtime",
		RequestedBy:    "parent",
		RequestedFor:   "profile-child",
		Kind:           session.CapabilityKindTool,
		TargetResource: "mail-reader",
		Purpose:        "read mailbox through negotiated child runtime",
		ReviewStatus:   session.CapabilityReviewStatusApproved,
		Contract:       `{"child_runtime":{"executable":"mail-reader","readonly_paths":["/srv/mail/config"],"secret_binds":[{"source":"/srv/mail/.secret.env","target":"/run/secrets/mail.env"}],"env_from_parent":["MAIL_TOKEN"]}}`,
	})
	if err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	out, err := registry.capabilityAuthorityGrantSet(context.Background(), capabilityInput{RequestID: request.RequestID, Principal: "profile-child"}, actor, key)
	if err != nil {
		t.Fatalf("capabilityAuthorityGrantSet() err = %v", err)
	}
	if !strings.Contains(out, "child_runtime: present") || !strings.Contains(out, "child_runtime_executable: mail-reader") || !strings.Contains(out, "child_runtime_secret_binds: 1") || !strings.Contains(out, "child_runtime_env_from_parent: MAIL_TOKEN") {
		t.Fatalf("grant output = %q, want child_runtime contract rendered", out)
	}
}

func TestCapabilityGrantRejectsInvalidChildRuntimeContract(t *testing.T) {
	registry, store := newDurableAgentToolRegistry(t)
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	request, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-child-runtime-bad",
		RequestedBy:    "parent",
		RequestedFor:   "profile-child",
		Kind:           session.CapabilityKindTool,
		TargetResource: "mail-reader",
		Purpose:        "read mailbox through negotiated child runtime",
		ReviewStatus:   session.CapabilityReviewStatusApproved,
		Contract:       `{"child_runtime":{"readonly_paths":["relative"]}}`,
	})
	if err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	_, err = registry.capabilityAuthorityGrantSet(context.Background(), capabilityInput{RequestID: request.RequestID, Principal: "profile-child"}, actor, key)
	if err == nil {
		t.Fatal("capabilityAuthorityGrantSet() err = nil, want child_runtime validation error")
	}
}

func TestCapabilityRejectsRemovedRuntimeMaterializationInput(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	admin := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	_, err := registry.ExecuteForSessionPrincipal(context.Background(), admin, adminSessionKey(), "capability_request", json.RawMessage(`{
		"action":"request_submit",
		"request_id":"cap-removed-runtime",
		"kind":"tool",
		"target_resource":"mail-reader",
		"purpose":"removed runtime key should be rejected",
		"contract":{"runtime_materialization":{"readonly_paths":["/srv/mail"]}}
	}`))
	if err == nil || !strings.Contains(err.Error(), "child_runtime") || !strings.Contains(err.Error(), "removed") {
		t.Fatalf("request_submit err = %v, want child_runtime-only rejection", err)
	}
}

func TestCapabilityGrantSetRendersToolInvocationScope(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	scopeJSON := json.RawMessage(`{"tool_invocation":{"actions":{"public_profile_metadata_read":{"selectors":{"username":["example_handle"]}}}}}`)
	requestOut, err := registry.capabilityRequestSubmit(capabilityInput{
		RequestID:      "cap-x-profile-scope",
		Kind:           "tool",
		TargetResource: "public-feed-readonly",
		RequestedFor:   "telegram:1001",
		Purpose:        "bounded exact X profile metadata read",
		Constraints:    scopeJSON,
	}, actor, key)
	if err != nil {
		t.Fatalf("capabilityRequestSubmit() err = %v", err)
	}
	if !strings.Contains(requestOut, "tool_invocation_scope: public_profile_metadata_read[username]") {
		t.Fatalf("request_submit output = %q, want rendered tool invocation scope", requestOut)
	}
	request, ok, err := store.CapabilityRequest("cap-x-profile-scope")
	if err != nil {
		t.Fatalf("CapabilityRequest() err = %v", err)
	}
	if !ok {
		t.Fatal("CapabilityRequest() ok = false, want stored request")
	}
	request.ReviewStatus = session.CapabilityReviewStatusApproved
	request, err = store.UpsertCapabilityRequest(request)
	if err != nil {
		t.Fatalf("UpsertCapabilityRequest(approved) err = %v", err)
	}
	out, err := registry.capabilityAuthorityGrantSet(context.Background(), capabilityInput{RequestID: request.RequestID, GrantID: "capg-x-profile-scope-render", Principal: "telegram:1001"}, actor, key)
	if err != nil {
		t.Fatalf("capabilityAuthorityGrantSet() err = %v", err)
	}
	if !strings.Contains(out, "tool_invocation_scope: public_profile_metadata_read[username]") {
		t.Fatalf("grant_set output = %q, want rendered tool invocation scope", out)
	}
	grant, ok, err := store.CapabilityGrant("capg-x-profile-scope-render")
	if err != nil {
		t.Fatalf("CapabilityGrant(%q) err = %v", "capg-x-profile-scope-render", err)
	}
	if !ok || !strings.Contains(grant.Constraints, "tool_invocation") {
		t.Fatalf("stored grant = %#v ok=%t, want tool_invocation constraints", grant, ok)
	}
}
