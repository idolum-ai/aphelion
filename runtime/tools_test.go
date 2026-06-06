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
