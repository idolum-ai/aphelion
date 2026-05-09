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
