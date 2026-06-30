//go:build linux

package runtime

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestExternalToolInvocationReadinessShowsReadyOnlyWhenAllProofsPass(t *testing.T) {
	tools := []core.ToolLifecycleStatusSnapshot{{
		ToolName:      "public-feed-readonly",
		InstallStatus: string(session.ToolInstallStatusVerified),
		ProbeStatus:   string(session.ToolProbeStatusPassed),
		AuditStatus:   string(session.ToolAuditStatusPassed),
	}}
	grants := []core.CapabilityGrantStatusSnapshot{{
		GrantID:             "capg-x-ready",
		Kind:                string(session.CapabilityKindTool),
		TargetResource:      "public-feed-readonly",
		Status:              string(session.CapabilityGrantStatusActive),
		GrantedTo:           "durable_agent:child-public-feed",
		AllowedActions:      []string{"invoke"},
		ToolInvocationScope: "public_profile_metadata_read[username]",
		ChildRuntimePresent: true,
	}}

	row := externalToolInvocationReadinessFromSnapshots("public-feed-readonly", "durable_agent:child-public-feed", "public_profile_metadata_read", "username", "example", tools, grants)
	if !row.Ready || row.Status != "ready" || row.NextRepairAction != "none" {
		t.Fatalf("readiness = %#v, want ready with no repair", row)
	}
	if !strings.Contains(row.Why, "tool exists") || !strings.Contains(row.Why, "runtime material is present") {
		t.Fatalf("why = %q, want compact four-proof success", row.Why)
	}
}

func TestExternalToolInvocationReadinessNamesExactMissingMaterial(t *testing.T) {
	tools := []core.ToolLifecycleStatusSnapshot{{
		ToolName:      "public-feed-readonly",
		InstallStatus: string(session.ToolInstallStatusVerified),
		ProbeStatus:   string(session.ToolProbeStatusPassed),
		AuditStatus:   string(session.ToolAuditStatusPassed),
	}}
	grants := []core.CapabilityGrantStatusSnapshot{{
		GrantID:                "capg-x-blocked",
		Kind:                   string(session.CapabilityKindTool),
		TargetResource:         "public-feed-readonly",
		Status:                 string(session.CapabilityGrantStatusActive),
		GrantedTo:              "durable_agent:child-public-feed",
		AllowedActions:         []string{"invoke"},
		ToolInvocationScope:    "public_profile_metadata_read[username]",
		ChildRuntimePresent:    true,
		RuntimeMaterialMissing: `env_from_parent "APHELION_E2_MISSING_ENV"`,
	}}

	row := externalToolInvocationReadinessFromSnapshots("public-feed-readonly", "durable_agent:child-public-feed", "public_profile_metadata_read", "username", "example", tools, grants)
	if row.Ready || row.Status != "blocked" {
		t.Fatalf("readiness = %#v, want blocked", row)
	}
	if !strings.Contains(row.Why, `env_from_parent "APHELION_E2_MISSING_ENV"`) {
		t.Fatalf("why = %q, want exact missing env material", row.Why)
	}
	if row.NextRepairAction != "provide or correct the named child_runtime material" {
		t.Fatalf("next repair = %q, want compact material repair", row.NextRepairAction)
	}
}

func TestFirstMissingChildRuntimeMaterialDistinguishesSecretBindAndEnv(t *testing.T) {
	missingSecret := filepath.Join(t.TempDir(), "missing.env")
	secretMissing := firstMissingChildRuntimeMaterial(core.ChildRuntimeContract{SecretBinds: []core.ChildRuntimeBind{{Source: missingSecret, Target: "/run/secrets/x.env"}}})
	if !strings.Contains(secretMissing, "secret_bind source") || !strings.Contains(secretMissing, missingSecret) {
		t.Fatalf("secret missing = %q, want exact secret_bind source", secretMissing)
	}

	envMissing := firstMissingChildRuntimeMaterial(core.ChildRuntimeContract{EnvFromParent: []string{"APHELION_E3_MISSING_ENV"}})
	if envMissing != `env_from_parent "APHELION_E3_MISSING_ENV"` {
		t.Fatalf("env missing = %q, want exact env_from_parent", envMissing)
	}
}

func TestExternalToolInvocationReadinessBlocksWrongActionSelector(t *testing.T) {
	tools := []core.ToolLifecycleStatusSnapshot{{ToolName: "public-feed-readonly", InstallStatus: "verified", ProbeStatus: "passed", AuditStatus: "passed"}}
	grants := []core.CapabilityGrantStatusSnapshot{{
		GrantID:             "capg-x-scope",
		Kind:                "tool",
		TargetResource:      "public-feed-readonly",
		Status:              "active",
		GrantedTo:           "durable_agent:child-public-feed",
		AllowedActions:      []string{"invoke"},
		ToolInvocationScope: "public_profile_metadata_read[username]",
		ChildRuntimePresent: true,
	}}
	row := externalToolInvocationReadinessFromSnapshots("public-feed-readonly", "durable_agent:child-public-feed", "read_timeline", "username", "example", tools, grants)
	if row.Ready || !strings.Contains(row.Why, "does not allow action/selector") {
		t.Fatalf("readiness = %#v, want wrong action/selector blocked", row)
	}
}

func TestExternalToolInvocationReadinessRequiresAccountGrantForAccountSelector(t *testing.T) {
	tools := []core.ToolLifecycleStatusSnapshot{{
		ToolName:      "gog_cli",
		InstallStatus: string(session.ToolInstallStatusVerified),
		ProbeStatus:   string(session.ToolProbeStatusPassed),
		AuditStatus:   string(session.ToolAuditStatusPassed),
	}}
	grants := []core.CapabilityGrantStatusSnapshot{{
		GrantID:             "capg-gog-cli-tool",
		Kind:                string(session.CapabilityKindTool),
		TargetResource:      "gog_cli",
		Status:              string(session.CapabilityGrantStatusActive),
		GrantedTo:           "durable_agent:idolum-email",
		AllowedActions:      []string{"invoke"},
		ToolInvocationScope: "search_unread_jobs[account]",
		ChildRuntimePresent: true,
	}}

	row := externalToolInvocationReadinessFromSnapshots("gog_cli", "durable_agent:idolum-email", "search_unread_jobs", "account", "host@idolum.ai", tools, grants)
	if row.Ready || row.Status != "blocked" {
		t.Fatalf("readiness = %#v, want account-scoped tool call blocked without external_account grant", row)
	}
	if !strings.Contains(row.Why, "external_account") && !strings.Contains(row.Why, "host@idolum.ai") {
		t.Fatalf("why = %q, want missing account grant surfaced", row.Why)
	}
}

func TestExternalToolInvocationReadinessComposesToolAndAccountGrants(t *testing.T) {
	tools := []core.ToolLifecycleStatusSnapshot{{
		ToolName:      "gog_cli",
		InstallStatus: string(session.ToolInstallStatusVerified),
		ProbeStatus:   string(session.ToolProbeStatusPassed),
		AuditStatus:   string(session.ToolAuditStatusPassed),
	}}
	grants := []core.CapabilityGrantStatusSnapshot{
		{
			GrantID:             "capg-gog-cli-tool",
			Kind:                string(session.CapabilityKindTool),
			TargetResource:      "gog_cli",
			Status:              string(session.CapabilityGrantStatusActive),
			GrantedTo:           "durable_agent:idolum-email",
			AllowedActions:      []string{"invoke"},
			ToolInvocationScope: "search_unread_jobs[account]",
			ChildRuntimePresent: true,
		},
		{
			GrantID:        "capg-host-read",
			Kind:           string(session.CapabilityKindExternalAccount),
			TargetResource: "host@idolum.ai",
			Status:         string(session.CapabilityGrantStatusActive),
			GrantedTo:      "durable_agent:idolum-email",
			AllowedActions: []string{"read", "search"},
		},
	}

	row := externalToolInvocationReadinessFromSnapshots("gog_cli", "durable_agent:idolum-email", "search_unread_jobs", "account", "host@idolum.ai", tools, grants)
	if !row.Ready || row.Status != "ready" || row.NextRepairAction != "none" {
		t.Fatalf("readiness = %#v, want ready only after tool and account grants compose", row)
	}
	for _, want := range []string{"tool exists", "runtime material is present", "external account"} {
		if !strings.Contains(row.Why, want) {
			t.Fatalf("why = %q, want %q", row.Why, want)
		}
	}
}

func TestExternalToolInvocationReadinessBlocksAccountActionScopeMismatch(t *testing.T) {
	tools := []core.ToolLifecycleStatusSnapshot{{
		ToolName:      "gog_cli",
		InstallStatus: string(session.ToolInstallStatusVerified),
		ProbeStatus:   string(session.ToolProbeStatusPassed),
		AuditStatus:   string(session.ToolAuditStatusPassed),
	}}
	grants := []core.CapabilityGrantStatusSnapshot{
		{
			GrantID:             "capg-gog-cli-tool",
			Kind:                string(session.CapabilityKindTool),
			TargetResource:      "gog_cli",
			Status:              string(session.CapabilityGrantStatusActive),
			GrantedTo:           "durable_agent:idolum-email",
			AllowedActions:      []string{"invoke"},
			ToolInvocationScope: "search_unread_jobs[account]",
			ChildRuntimePresent: true,
		},
		{
			GrantID:        "capg-host-connection-only",
			Kind:           string(session.CapabilityKindExternalAccount),
			TargetResource: "host@idolum.ai",
			Status:         string(session.CapabilityGrantStatusActive),
			GrantedTo:      "durable_agent:idolum-email",
			AllowedActions: []string{"connection_test"},
		},
	}

	row := externalToolInvocationReadinessFromSnapshots("gog_cli", "durable_agent:idolum-email", "search_unread_jobs", "account", "host@idolum.ai", tools, grants)
	if row.Ready || row.Status != "blocked" {
		t.Fatalf("readiness = %#v, want account action-scope mismatch blocked", row)
	}
	if !strings.Contains(row.Why, "host@idolum.ai") || !strings.Contains(row.Why, "read") {
		t.Fatalf("why = %q, want account grant action mismatch surfaced", row.Why)
	}
}
