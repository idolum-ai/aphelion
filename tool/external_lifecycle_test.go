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
	registry.WithToolProposalRatificationApprover(&stubToolProposalRatificationApprover{approved: true})
	installExternalLifecycleFixture(t, registry, "browse_page")
	key := adminSessionKey()
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}

	for _, step := range []struct {
		name    string
		input   string
		wantOut string
	}{
		{
			name: "submit proposal",
			input: `{
				"action":"proposal_submit",
				"proposal_id":"tp-external-browse",
				"proposed_by":"idolum-email",
				"tool_name":"browse_page",
				"why_now":"Email agent needs a bounded read-only page fetcher.",
				"contract":{"constraints":["read_only","external_manifest_owned_by_email_agent"]}
			}`,
			wantOut: "review_status: proposed",
		},
		{
			name:    "ratify proposal",
			input:   `{"action":"proposal_ratify","proposal_id":"tp-external-browse"}`,
			wantOut: "review_status: approved",
		},
		{
			name:    "create pending install",
			input:   `{"action":"install_set","tool_name":"browse_page","status":"pending","installer":"aphelion","install_ref":"workspace:browse-page-fixture"}`,
			wantOut: "status: pending",
		},
		{
			name:    "run install",
			input:   `{"action":"install_execute","tool_name":"browse_page"}`,
			wantOut: "status: installed",
		},
		{
			name:    "run audit",
			input:   `{"action":"audit_run","tool_name":"browse_page"}`,
			wantOut: "status: passed",
		},
		{
			name:    "run probe",
			input:   `{"action":"probe_run","tool_name":"browse_page"}`,
			wantOut: "probe_status: passed",
		},
		{
			name:    "verify install",
			input:   `{"action":"install_set","tool_name":"browse_page","status":"verified","installer":"aphelion","install_ref":"workspace:browse-page-fixture"}`,
			wantOut: "status: verified",
		},
		{
			name:    "register approved proposal",
			input:   `{"action":"register","proposal_id":"tp-external-browse","implementation_ref":"external:browse_page"}`,
			wantOut: "registered: true",
		},
		{
			name:    "expose to admin principal",
			input:   `{"action":"exposure_set","tool_name":"browse_page","principal":"telegram:1001","active":true}`,
			wantOut: "active: true",
		},
	} {
		out, err := executeToolAuthorityJSON(t, registry, actor, key, step.input)
		if err != nil {
			t.Fatalf("%s err = %v", step.name, err)
		}
		if !strings.Contains(out, step.wantOut) {
			t.Fatalf("%s output = %q, want %q", step.name, out, step.wantOut)
		}
	}

	out, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
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
		core.ExecutionEventToolProposalCreated,
		core.ExecutionEventToolProposalReviewed,
		core.ExecutionEventToolInstallUpdated,
		core.ExecutionEventToolAuditUpdated,
		core.ExecutionEventToolRegistered,
		core.ExecutionEventToolExposureChanged,
	} {
		if !executionEventTypeExists(events, eventType) {
			t.Fatalf("missing %s event in lifecycle flow", eventType)
		}
	}
}

func TestExternalToolAuthorityLifecycleNegativeGates(t *testing.T) {
	t.Parallel()

	t.Run("rejected proposal blocks registration", func(t *testing.T) {
		t.Parallel()
		registry, _ := newDurableAgentToolRegistry(t)
		installExternalLifecycleFixture(t, registry, "browse_page")
		key := adminSessionKey()
		actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}

		if _, err := executeToolAuthorityJSON(t, registry, actor, key, `{
			"action":"proposal_submit",
			"proposal_id":"tp-rejected-browse",
			"proposed_by":"idolum-email",
			"tool_name":"browse_page",
			"why_now":"Need bounded page reads.",
			"contract":{"constraints":["read_only"]}
		}`); err != nil {
			t.Fatalf("proposal_submit err = %v", err)
		}
		if _, err := executeToolAuthorityJSON(t, registry, actor, key, `{
			"action":"proposal_review",
			"proposal_id":"tp-rejected-browse",
			"review_status":"rejected",
			"review_notes":"not bounded enough"
		}`); err != nil {
			t.Fatalf("proposal_review err = %v", err)
		}
		_, err := executeToolAuthorityJSON(t, registry, actor, key, `{"action":"register","proposal_id":"tp-rejected-browse","implementation_ref":"external:browse_page"}`)
		if err == nil || !strings.Contains(err.Error(), "must be approved before registration") {
			t.Fatalf("register rejected proposal err = %v, want proposal approval gate", err)
		}
	})

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

	t.Run("unregistered unexposed and revoked exposure block invocation", func(t *testing.T) {
		t.Parallel()
		registry, _ := newDurableAgentToolRegistry(t)
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
		if err == nil || !strings.Contains(err.Error(), "not exposed") {
			t.Fatalf("unexposed invoke err = %v, want exposure gate", err)
		}
		if _, err := executeToolAuthorityJSON(t, registry, actor, key, `{"action":"exposure_set","tool_name":"browse_page","principal":"telegram:1001","active":true}`); err != nil {
			t.Fatalf("exposure_set active err = %v", err)
		}
		if _, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`)); err != nil {
			t.Fatalf("exposed invoke err = %v", err)
		}
		if _, err := executeToolAuthorityJSON(t, registry, actor, key, `{"action":"exposure_set","tool_name":"browse_page","principal":"telegram:1001","active":false}`); err != nil {
			t.Fatalf("exposure_set inactive err = %v", err)
		}
		_, err = registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
		if err == nil || !strings.Contains(err.Error(), "not exposed") {
			t.Fatalf("revoked exposure invoke err = %v, want exposure gate", err)
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
	if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      toolName,
		Owner:     "idolum-email",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		IO: ExternalToolManifestIO{
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"},"installed":{"type":"boolean"}},"required":["summary","installed"]}`),
		},
		Install: ExternalToolManifestInstall{Command: []string{"./install.sh"}},
		Probe:   ExternalToolManifestProbe{Command: []string{"./probe.sh"}, ExpectedOutputContains: "probe ok"},
		Constraints: ExternalToolManifestConstraints{
			Network: "none",
		},
	}}); err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
}
