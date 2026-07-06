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
)

func TestExternalToolAuthorityEndToEndLifecycleFlow(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	installExternalLifecycleFixture(t, registry, "browse_page")
	key := adminSessionKey()
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}

	for _, step := range []struct {
		name    string
		input   string
		wantOut string
	}{
		{name: "create pending install", input: `{"action":"install_set","tool_name":"browse_page","status":"pending","installer":"aphelion","install_ref":"workspace:browse-page-fixture"}`, wantOut: "status: pending"},
		{name: "run install", input: `{"action":"install_execute","tool_name":"browse_page"}`, wantOut: "status: installed"},
		{name: "run audit", input: `{"action":"audit_run","tool_name":"browse_page"}`, wantOut: "status: passed"},
		{name: "run probe", input: `{"action":"probe_run","tool_name":"browse_page"}`, wantOut: "probe_status: passed"},
		{name: "verify install", input: `{"action":"install_set","tool_name":"browse_page","status":"verified","installer":"aphelion","install_ref":"workspace:browse-page-fixture"}`, wantOut: "status: verified"},
		{name: "register tool", input: `{"action":"register","tool_name":"browse_page","implementation_ref":"external:browse_page"}`, wantOut: "registered: true"},
	} {
		out, err := executeToolAuthorityJSON(t, registry, actor, key, step.input)
		if err != nil {
			t.Fatalf("%s err = %v", step.name, err)
		}
		if !strings.Contains(out, step.wantOut) {
			t.Fatalf("%s output = %q, want %q", step.name, out, step.wantOut)
		}
	}
	grantToolInvoke(t, store, "browse_page", "telegram:1001")

	ctx := authorityRunContextForPrincipal(t, store, key, actor)
	out, err := registry.ExecuteForSessionPrincipal(ctx, actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
	if err != nil {
		t.Fatalf("browse_page invoke err = %v", err)
	}
	if out != `{"summary":"ok","installed":true}` {
		t.Fatalf("browse_page output = %q, want fixture output", out)
	}

	events, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	for _, eventType := range []string{
		core.ExecutionEventToolInstallUpdated,
		core.ExecutionEventToolAuditUpdated,
		core.ExecutionEventToolRegistered,
	} {
		if !executionEventTypeExists(events, eventType) {
			t.Fatalf("missing %s event in lifecycle flow", eventType)
		}
	}
}

func TestExternalToolAuthorityRollbackAndUninstallRetireExecutionSurface(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	installExternalLifecycleFixture(t, registry, "browse_page")
	key := adminSessionKey()
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}

	verifyExternalLifecycleFixture(t, registry, actor, key)
	if _, err := executeToolAuthorityJSON(t, registry, actor, key, `{"action":"register","tool_name":"browse_page","implementation_ref":"external:browse_page"}`); err != nil {
		t.Fatalf("register err = %v", err)
	}
	grantToolInvoke(t, store, "browse_page", "telegram:1001")
	ctx := authorityRunContextForPrincipal(t, store, key, actor)
	if _, err := registry.ExecuteForSessionPrincipal(ctx, actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`)); err != nil {
		t.Fatalf("browse_page before rollback err = %v", err)
	}

	rollbackOut, err := executeToolAuthorityJSON(t, registry, actor, key, `{"action":"rollback","tool_name":"browse_page","rationale":"retire fixture install before replacement"}`)
	if err != nil {
		t.Fatalf("rollback err = %v", err)
	}
	for _, needle := range []string{"[TOOL_ROLLBACK]", "status: stale", "drift_source: rollback", "registration_disabled: true", "revoked_capability_grants: 1", "command_output: stdout:", "rollback ok"} {
		if !strings.Contains(rollbackOut, needle) {
			t.Fatalf("rollback output = %q, want %q", rollbackOut, needle)
		}
	}
	registered, ok, err := store.RegisteredTool("browse_page")
	if err != nil || !ok || registered.Registered {
		t.Fatalf("RegisteredTool after rollback = %#v ok=%t err=%v, want disabled", registered, ok, err)
	}
	if _, ok, err := store.ActiveCapabilityGrant(session.CapabilityKindTool, "browse_page", "telegram:1001", "invoke"); err != nil || ok {
		t.Fatalf("ActiveCapabilityGrant after rollback ok=%t err=%v, want revoked", ok, err)
	}
	if _, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`)); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("browse_page after rollback err = %v, want registration gate", err)
	}

	verifyExternalLifecycleFixture(t, registry, actor, key)
	if _, err := executeToolAuthorityJSON(t, registry, actor, key, `{"action":"register","tool_name":"browse_page","implementation_ref":"external:browse_page"}`); err != nil {
		t.Fatalf("register after rollback err = %v", err)
	}
	grantToolInvoke(t, store, "browse_page", "telegram:1001")
	uninstallOut, err := executeToolAuthorityJSON(t, registry, actor, key, `{"action":"uninstall","tool_name":"browse_page","rationale":"operator removed external fixture"}`)
	if err != nil {
		t.Fatalf("uninstall err = %v", err)
	}
	for _, needle := range []string{"[TOOL_UNINSTALL]", "status: stale", "drift_source: removal", "registration_disabled: true", "revoked_capability_grants: 1", "command_output: stdout:", "uninstall ok"} {
		if !strings.Contains(uninstallOut, needle) {
			t.Fatalf("uninstall output = %q, want %q", uninstallOut, needle)
		}
	}

	events, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	for _, eventType := range []string{
		core.ExecutionEventToolRollbackApplied,
		core.ExecutionEventToolRemovalApplied,
		core.ExecutionEventToolRegistered,
		core.ExecutionEventCapabilityGrantChanged,
	} {
		if !executionEventTypeExists(events, eventType) {
			t.Fatalf("missing %s event after rollback/uninstall", eventType)
		}
	}
}

func TestExternalToolTenantCapabilityRequestCarriesThroughToInvocation(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	installExternalLifecycleFixture(t, registry, "browse_page")
	adminKey := adminSessionKey()
	admin := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	tenant := principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: 2002}
	tenantKey := session.SessionKey{ChatID: 2002, UserID: 2002, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "2002"}}

	requestOut, err := registry.ExecuteForSessionPrincipal(context.Background(), tenant, tenantKey, "capability_request", json.RawMessage(`{
		"action":"request_submit",
		"request_id":"cap-tenant-browse",
		"kind":"tool",
		"target_resource":"browse_page",
		"purpose":"Tenant needs a bounded page fetcher.",
		"contract":{"constraints":["read_only","operator_governed_install"]}
	}`))
	if err != nil {
		t.Fatalf("capability_request request_submit err = %v", err)
	}
	if !strings.Contains(requestOut, "request_id: cap-tenant-browse") || !strings.Contains(requestOut, "requested_by: telegram:2002") {
		t.Fatalf("capability_request output = %q, want tenant attribution", requestOut)
	}

	for _, input := range []string{
		`{"action":"install_set","tool_name":"browse_page","status":"pending","installer":"aphelion","install_ref":"workspace:browse-page-fixture"}`,
		`{"action":"install_execute","tool_name":"browse_page"}`,
		`{"action":"audit_run","tool_name":"browse_page"}`,
		`{"action":"probe_run","tool_name":"browse_page"}`,
		`{"action":"install_set","tool_name":"browse_page","status":"verified","installer":"aphelion","install_ref":"workspace:browse-page-fixture"}`,
		`{"action":"register","tool_name":"browse_page","implementation_ref":"external:browse_page"}`,
	} {
		if _, err := executeToolAuthorityJSON(t, registry, admin, adminKey, input); err != nil {
			t.Fatalf("tool_authority input %s err = %v", input, err)
		}
	}
	if _, err := registry.ExecuteForSessionPrincipal(context.Background(), admin, adminKey, "capability_authority", json.RawMessage(`{"action":"request_review","request_id":"cap-tenant-browse","review_status":"approved","rationale":"bounded tool request"}`)); err != nil {
		t.Fatalf("capability_authority request_review err = %v", err)
	}
	if _, err := registry.ExecuteForSessionPrincipal(context.Background(), admin, adminKey, "capability_authority", json.RawMessage(`{"action":"grant_set","request_id":"cap-tenant-browse","grant_id":"capg-tenant-browse","allowed_actions":["invoke"],"grant_status":"active","principal":"telegram:2002"}`)); err != nil {
		t.Fatalf("capability_authority grant_set err = %v", err)
	}
	grantAuthorityUseLease(t, store, tenantKey)

	tenantScope, err := registry.sandbox.Resolve(tenant)
	if err != nil {
		t.Fatalf("Resolve(tenant) err = %v", err)
	}
	if err := os.MkdirAll(tenantScope.WorkingRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(tenant working root) err = %v", err)
	}
	for _, name := range []string{"install.sh", "probe.sh", "run.sh", ".browse_page_installed"} {
		raw, err := os.ReadFile(filepath.Join(registry.workspace, name))
		if err != nil {
			t.Fatalf("ReadFile(%s) err = %v", name, err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(tenantScope.WorkingRoot, name), raw, mode); err != nil {
			t.Fatalf("WriteFile(tenant %s) err = %v", name, err)
		}
	}

	ctx := authorityRunContextForPrincipal(t, store, tenantKey, tenant)
	out, err := registry.ExecuteForSessionPrincipal(ctx, tenant, tenantKey, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
	if err != nil {
		t.Fatalf("tenant browse_page invoke err = %v", err)
	}
	if out != `{"summary":"ok","installed":true}` {
		t.Fatalf("tenant browse_page output = %q, want fixture output", out)
	}
}

func TestExternalToolAuthorityLifecycleNegativeGates(t *testing.T) {
	t.Parallel()

	t.Run("installed but unaudited blocks verification", func(t *testing.T) {
		t.Parallel()
		registry, _ := newDurableAgentToolRegistry(t)
		installExternalLifecycleFixture(t, registry, "browse_page")
		key := adminSessionKey()
		actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
		if _, err := executeToolAuthorityJSON(t, registry, actor, key, `{"action":"install_set","tool_name":"browse_page","status":"pending","installer":"aphelion","install_ref":"workspace:browse-page-fixture"}`); err != nil {
			t.Fatalf("install_set pending err = %v", err)
		}
		if _, err := executeToolAuthorityJSON(t, registry, actor, key, `{"action":"install_execute","tool_name":"browse_page"}`); err != nil {
			t.Fatalf("install_execute err = %v", err)
		}
		if _, err := executeToolAuthorityJSON(t, registry, actor, key, `{"action":"probe_run","tool_name":"browse_page"}`); err != nil {
			t.Fatalf("probe_run err = %v", err)
		}
		_, err := executeToolAuthorityJSON(t, registry, actor, key, `{"action":"install_set","tool_name":"browse_page","status":"verified","installer":"aphelion","install_ref":"workspace:browse-page-fixture"}`)
		if err == nil || !strings.Contains(err.Error(), "requires a passed runtime-authored audit_run record") {
			t.Fatalf("verify unaudited err = %v, want audit gate", err)
		}
	})

	t.Run("audited but unprobed blocks verification", func(t *testing.T) {
		t.Parallel()
		registry, _ := newDurableAgentToolRegistry(t)
		installExternalLifecycleFixture(t, registry, "browse_page")
		key := adminSessionKey()
		actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
		if _, err := executeToolAuthorityJSON(t, registry, actor, key, `{"action":"install_set","tool_name":"browse_page","status":"pending","installer":"aphelion","install_ref":"workspace:browse-page-fixture"}`); err != nil {
			t.Fatalf("install_set pending err = %v", err)
		}
		if _, err := executeToolAuthorityJSON(t, registry, actor, key, `{"action":"install_execute","tool_name":"browse_page"}`); err != nil {
			t.Fatalf("install_execute err = %v", err)
		}
		if _, err := executeToolAuthorityJSON(t, registry, actor, key, `{"action":"audit_run","tool_name":"browse_page"}`); err != nil {
			t.Fatalf("audit_run err = %v", err)
		}
		_, err := executeToolAuthorityJSON(t, registry, actor, key, `{"action":"install_set","tool_name":"browse_page","status":"verified","installer":"aphelion","install_ref":"workspace:browse-page-fixture"}`)
		if err == nil || !strings.Contains(err.Error(), "requires a passed runtime-authored probe_run record") {
			t.Fatalf("verify unprobed err = %v, want probe gate", err)
		}
	})

	t.Run("unregistered and ungranted block invocation", func(t *testing.T) {
		t.Parallel()
		registry, store := newDurableAgentToolRegistry(t)
		installExternalLifecycleFixture(t, registry, "browse_page")
		key := adminSessionKey()
		actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
		verifyExternalLifecycleFixture(t, registry, actor, key)

		_, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
		if err == nil || !strings.Contains(err.Error(), "not registered") {
			t.Fatalf("unregistered invoke err = %v, want registration gate", err)
		}
		if _, err := executeToolAuthorityJSON(t, registry, actor, key, `{"action":"register","tool_name":"browse_page","implementation_ref":"external:browse_page"}`); err != nil {
			t.Fatalf("register err = %v", err)
		}
		_, err = registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
		if err == nil || !strings.Contains(err.Error(), "missing capability grant") || !strings.Contains(err.Error(), "review request queued") {
			t.Fatalf("ungranted invoke err = %v, want queued missing-grant review", err)
		}
		grantToolInvoke(t, store, "browse_page", "telegram:1001")
		ctx := authorityRunContextForPrincipal(t, store, key, actor)
		if _, err := registry.ExecuteForSessionPrincipal(ctx, actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`)); err != nil {
			t.Fatalf("granted invoke err = %v", err)
		}
	})
}

func executeToolAuthorityJSON(t *testing.T, registry *Registry, actor principal.Principal, key session.SessionKey, input string) (string, error) {
	t.Helper()
	return registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "tool_authority", json.RawMessage(input))
}

func verifyExternalLifecycleFixture(t *testing.T, registry *Registry, actor principal.Principal, key session.SessionKey) {
	t.Helper()
	for _, input := range []string{
		`{"action":"install_set","tool_name":"browse_page","status":"pending","installer":"aphelion","install_ref":"workspace:browse-page-fixture"}`,
		`{"action":"install_execute","tool_name":"browse_page"}`,
		`{"action":"audit_run","tool_name":"browse_page"}`,
		`{"action":"probe_run","tool_name":"browse_page"}`,
		`{"action":"install_set","tool_name":"browse_page","status":"verified","installer":"aphelion","install_ref":"workspace:browse-page-fixture"}`,
	} {
		if _, err := executeToolAuthorityJSON(t, registry, actor, key, input); err != nil {
			t.Fatalf("lifecycle fixture input %s err = %v", input, err)
		}
	}
}

func installExternalLifecycleFixture(t *testing.T, registry *Registry, toolName string) {
	t.Helper()

	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(registry.workspace, "install.sh"), []byte(`#!/usr/bin/env bash
set -euo pipefail
printf installed > .browse_page_installed
echo 'install ok'
`), 0o755); err != nil {
		t.Fatalf("WriteFile(install.sh) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(registry.workspace, "probe.sh"), []byte(`#!/usr/bin/env bash
set -euo pipefail
test -f .browse_page_installed
echo 'probe ok'
`), 0o755); err != nil {
		t.Fatalf("WriteFile(probe.sh) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(registry.workspace, "run.sh"), []byte(`#!/usr/bin/env bash
set -euo pipefail
test -f .browse_page_installed
cat >/dev/null
echo '{"summary":"ok","installed":true}'
`), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(registry.workspace, "rollback.sh"), []byte(`#!/usr/bin/env bash
set -euo pipefail
rm -f .browse_page_installed
echo 'rollback ok'
`), 0o755); err != nil {
		t.Fatalf("WriteFile(rollback.sh) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(registry.workspace, "uninstall.sh"), []byte(`#!/usr/bin/env bash
set -euo pipefail
rm -f .browse_page_installed
echo 'uninstall ok'
`), 0o755); err != nil {
		t.Fatalf("WriteFile(uninstall.sh) err = %v", err)
	}
	if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      toolName,
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		IO: ExternalToolManifestIO{
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"},"installed":{"type":"boolean"}},"required":["summary","installed"]}`),
		},
		Install: ExternalToolManifestInstall{Command: []string{"./install.sh"}},
		Probe:   ExternalToolManifestProbe{Command: []string{"./probe.sh"}, ExpectedOutputContains: "probe ok"},
		Rollback: ExternalToolManifestRollback{
			Command: []string{"./rollback.sh"},
		},
		Uninstall: ExternalToolManifestUninstall{
			Command: []string{"./uninstall.sh"},
		},
		Constraints: ExternalToolManifestConstraints{
			Network: "none",
		},
	}}); err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
}
