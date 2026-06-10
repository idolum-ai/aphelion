//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

type stubToolRegistry struct{ defs []agent.ToolDef }

func (s *stubToolRegistry) Definitions() []agent.ToolDef {
	return append([]agent.ToolDef(nil), s.defs...)
}
func (s *stubToolRegistry) Execute(_ context.Context, _ string, _ json.RawMessage) (string, error) {
	return "", nil
}

type stubPrincipalManifestRegistry struct{}

func (stubPrincipalManifestRegistry) Definitions() []agent.ToolDef { return nil }
func (stubPrincipalManifestRegistry) Execute(_ context.Context, _ string, _ json.RawMessage) (string, error) {
	return "", nil
}
func (stubPrincipalManifestRegistry) ManifestForPrincipal(p principal.Principal) string {
	return "principal=" + p.DurableAgentID
}

type lyingManifestToolRegistry struct {
	stubToolRegistry
	manifest string
}

func (s *lyingManifestToolRegistry) Manifest() string { return s.manifest }

func TestPrincipalScopedToolsManifestUsesPrincipalAwareManifest(t *testing.T) {
	registry := &principalScopedTools{base: stubPrincipalManifestRegistry{}, principal: principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"}}
	if got := registry.Manifest(); got != "principal=child-alpha" {
		t.Fatalf("Manifest() = %q, want principal-aware manifest", got)
	}
}

func TestToolManifestForRunKindFiltersConservativeLanes(t *testing.T) {
	registry := &stubToolRegistry{defs: []agent.ToolDef{
		{Name: "exec"},
		{Name: "fetch_url"},
		{Name: "request_approval"},
		{Name: "read_file"},
		{Name: "operation_artifact"},
		{Name: "update_operation"},
	}}

	if got := toolManifestForRunKind(registry, ""); got != "exec, fetch_url, operation_artifact, read_file, request_approval, update_operation" {
		t.Fatalf("interactive manifest = %q", got)
	}
	heartbeat := toolManifestForRunKind(registry, session.TurnRunKindHeartbeat)
	for _, forbidden := range []string{"exec", "fetch_url", "request_approval", "read_file"} {
		if strings.Contains(heartbeat, forbidden) {
			t.Fatalf("heartbeat manifest = %q leaked %q", heartbeat, forbidden)
		}
	}
	if !strings.Contains(heartbeat, "operation_artifact") || !strings.Contains(heartbeat, "update_operation") {
		t.Fatalf("heartbeat manifest = %q, want state/digest tools", heartbeat)
	}
	doctor := toolManifestForRunKind(registry, session.TurnRunKindDoctor)
	if strings.Contains(doctor, "exec") || strings.Contains(doctor, "fetch_url") || strings.Contains(doctor, "request_approval") {
		t.Fatalf("doctor manifest = %q leaked write/external/proposal tools", doctor)
	}
	if !strings.Contains(doctor, "read_file") || !strings.Contains(doctor, "operation_artifact") {
		t.Fatalf("doctor manifest = %q, want read-only diagnostic subset", doctor)
	}
}

func TestToolLaneAllowlistsByRunKind(t *testing.T) {
	t.Parallel()

	cases := []struct {
		runKind   session.TurnRunKind
		allowed   []string
		forbidden []string
	}{
		{runKind: session.TurnRunKindHeartbeat, allowed: []string{"update_plan", "update_operation", "operation_artifact", "memory", "session_search", "semantic_search"}, forbidden: []string{"exec", "fetch_url", "read_file", "request_approval"}},
		{runKind: session.TurnRunKindCron, allowed: []string{"update_plan", "update_operation", "operation_artifact", "memory", "session_search", "semantic_search"}, forbidden: []string{"exec", "fetch_url", "read_file", "request_approval"}},
		{runKind: session.TurnRunKindDoctor, allowed: []string{"read_file", "list_dir", "search", "session_search", "semantic_search", "operation_artifact"}, forbidden: []string{"exec", "fetch_url", "request_approval", "write_file"}},
		{runKind: session.TurnRunKindRecovery, allowed: []string{"read_file", "operation_artifact"}, forbidden: []string{"exec", "fetch_url", "request_approval", "write_file", "update_operation"}},
	}

	for _, tc := range cases {
		allowed := toolLaneAllowlist(tc.runKind)
		for _, name := range tc.allowed {
			if !toolAllowedByName(name, allowed) {
				t.Fatalf("%s allowlist missing %s", tc.runKind, name)
			}
		}
		for _, name := range tc.forbidden {
			if toolAllowedByName(name, allowed) {
				t.Fatalf("%s allowlist unexpectedly permits %s", tc.runKind, name)
			}
		}
	}
}

func TestToolRegistryForRunKindEnforcesConservativeLane(t *testing.T) {
	registry := &stubToolRegistry{defs: []agent.ToolDef{
		{Name: "exec"},
		{Name: "fetch_url"},
		{Name: "read_file"},
		{Name: "operation_artifact"},
		{Name: "update_operation"},
	}}

	heartbeat := toolRegistryForRunKind(registry, session.TurnRunKindHeartbeat)
	if got := renderToolManifest(heartbeat.Definitions()); strings.Contains(got, "exec") || strings.Contains(got, "read_file") {
		t.Fatalf("heartbeat definitions = %q leaked disallowed tools", got)
	}
	if _, err := heartbeat.Execute(context.Background(), "exec", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("heartbeat exec err = %v, want lane rejection", err)
	}
	if _, err := heartbeat.Execute(context.Background(), "update_operation", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("heartbeat update_operation err = %v, want allowed execution", err)
	}

	interactive := toolRegistryForRunKind(registry, session.TurnRunKindInteractive)
	if interactive != registry {
		t.Fatalf("interactive registry was wrapped; want original registry")
	}
}

func TestToolRegistryForRunKindIgnoresManifestTextForAuthority(t *testing.T) {
	registry := &lyingManifestToolRegistry{
		stubToolRegistry: stubToolRegistry{defs: []agent.ToolDef{
			{Name: "exec"},
			{Name: "update_operation"},
		}},
		manifest: "exec, update_operation",
	}

	heartbeat := toolRegistryForRunKind(registry, session.TurnRunKindHeartbeat)
	if got := toolManifestForRunKind(heartbeat, session.TurnRunKindHeartbeat); strings.Contains(got, "exec") {
		t.Fatalf("heartbeat manifest = %q, want filtered definitions instead of registry manifest text", got)
	}
	if _, err := heartbeat.Execute(context.Background(), "exec", json.RawMessage(`{}`)); err == nil {
		t.Fatal("heartbeat exec err = nil, want lane rejection despite manifest text")
	}
}
