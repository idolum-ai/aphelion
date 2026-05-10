//go:build linux

package tool

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func TestAuthorityManagedToolRequiresTurnLeaseEvidence(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	manifest := ExternalToolManifest{
		Name:      "leased_tool",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
	}
	if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest}); err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "leased_tool", ImplementationRef: "external:leased_tool", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-leased-tool",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindTool,
		TargetResource: "leased_tool",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	_, _, err := registry.requireAuthorityToolAccess("leased_tool", actor, key, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "requires active continuation or operation plan lease evidence") {
		t.Fatalf("requireAuthorityToolAccess() err = %v, want missing lease evidence", err)
	}
	invocations, err := store.CapabilityInvocationsByGrant("capg-leased-tool", 10)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant(blocked) err = %v", err)
	}
	if len(invocations) != 1 || invocations[0].Status != "blocked" || invocations[0].ContinuationLeaseID != "" {
		t.Fatalf("blocked invocations = %#v, want blocked without lease evidence", invocations)
	}

	grantAuthorityUseLease(t, store, key)
	grant, managed, err := registry.requireAuthorityToolAccess("leased_tool", actor, key, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("requireAuthorityToolAccess(with lease) err = %v", err)
	}
	if !managed || grant.GrantID != "capg-leased-tool" {
		t.Fatalf("grant=%#v managed=%t, want capg-leased-tool managed", grant, managed)
	}
	invocations, err = store.CapabilityInvocationsByGrant("capg-leased-tool", 10)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant(allowed) err = %v", err)
	}
	if len(invocations) < 2 || invocations[0].Status != "allowed" || invocations[0].ContinuationLeaseID == "" || invocations[0].SessionID == "" {
		t.Fatalf("allowed invocations = %#v, want lease-backed allowed invocation", invocations)
	}
}
