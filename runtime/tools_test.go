//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/principal"
)

type stubPrincipalManifestRegistry struct{}

func (stubPrincipalManifestRegistry) Definitions() []agent.ToolDef { return nil }
func (stubPrincipalManifestRegistry) Execute(_ context.Context, _ string, _ json.RawMessage) (string, error) {
	return "", nil
}
func (stubPrincipalManifestRegistry) ManifestForPrincipal(p principal.Principal) string {
	return "principal=" + p.DurableAgentID
}

func TestPrincipalScopedToolsManifestUsesPrincipalAwareManifest(t *testing.T) {
	registry := &principalScopedTools{base: stubPrincipalManifestRegistry{}, principal: principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "idolum-email"}}
	if got := registry.Manifest(); got != "principal=idolum-email" {
		t.Fatalf("Manifest() = %q, want principal-aware manifest", got)
	}
}
