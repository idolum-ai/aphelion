//go:build linux

package tool

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func TestRenderManifestIncludesDefinitionsAndParameters(t *testing.T) {
	t.Parallel()

	defs := []agent.ToolDef{
		{
			Name:        "alpha",
			Description: "first tool",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"z":{"type":"string"},
					"a":{"type":"integer"}
				},
				"required":["a"]
			}`),
		},
		{
			Name:        "beta",
			Description: "",
		},
	}

	manifest := RenderManifest(defs, nil, nil)
	if !strings.Contains(manifest, "- alpha: first tool") {
		t.Fatalf("manifest missing alpha definition:\n%s", manifest)
	}
	if !strings.Contains(manifest, "a(integer,required)") {
		t.Fatalf("manifest missing required parameter:\n%s", manifest)
	}
	if !strings.Contains(manifest, "z(string,optional)") {
		t.Fatalf("manifest missing optional parameter:\n%s", manifest)
	}
	if !strings.Contains(manifest, "- beta: (no description)") {
		t.Fatalf("manifest missing beta fallback description:\n%s", manifest)
	}
	if !strings.Contains(manifest, "params: (none)") {
		t.Fatalf("manifest missing empty params marker:\n%s", manifest)
	}
}

func TestRegistryManifestReflectsExecConstraints(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	registry := NewRegistry(workspace, 7*time.Second)
	registry.maxOutputBytes = 1234

	manifest := registry.Manifest()
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		t.Fatalf("Abs() err = %v", err)
	}

	if !strings.Contains(manifest, "- exec:") {
		t.Fatalf("manifest missing exec definition:\n%s", manifest)
	}
	if !strings.Contains(manifest, "exec constraints:") {
		t.Fatalf("manifest missing constraints section:\n%s", manifest)
	}
	if !strings.Contains(manifest, "- exec_root: "+absWorkspace) {
		t.Fatalf("manifest missing exec_root:\n%s", manifest)
	}
	if !strings.Contains(manifest, "- default_timeout_sec: 7") {
		t.Fatalf("manifest missing timeout:\n%s", manifest)
	}
	if !strings.Contains(manifest, "- max_output_bytes: 1234") {
		t.Fatalf("manifest missing max output bytes:\n%s", manifest)
	}
}

func TestRegistryManifestTracksCurrentState(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), 1*time.Second)
	before := registry.Manifest()

	registry.timeout = 9 * time.Second
	registry.maxOutputBytes = 2048
	after := registry.Manifest()

	if before == after {
		t.Fatalf("manifest did not change after state update:\n%s", before)
	}
	if !strings.Contains(after, "- default_timeout_sec: 9") {
		t.Fatalf("updated manifest missing timeout:\n%s", after)
	}
	if !strings.Contains(after, "- max_output_bytes: 2048") {
		t.Fatalf("updated manifest missing max output bytes:\n%s", after)
	}
}

func TestRegistryManifestIncludesExternalManifestAsNonExecutable(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), time.Second)
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "container", Entry: "ghcr.io/idolum/child-browser-tool:pilot"},
		IO:        ExternalToolManifestIO{InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`)},
	}})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}

	manifest := registry.Manifest()
	for _, needle := range []string{
		"- browse_page: external tool owned by child-alpha",
		"url(string,required)",
		"executable: false",
		"reason: external manifest is visible but executor support is not wired yet",
	} {
		if !strings.Contains(manifest, needle) {
			t.Fatalf("manifest = %q, want substring %q", manifest, needle)
		}
	}
}

func TestRegistryExecuteExternalManifestReturnsCleanNonExecutableError(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "container", Entry: "ghcr.io/idolum/child-browser-tool:pilot"},
	}})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "browse_page", ImplementationRef: "external:browse_page", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	grantToolInvoke(t, store, "browse_page", "telegram:1001")

	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	ctx := authorityRunContextForPrincipal(t, store, key, actor)
	_, err = registry.ExecuteForSessionPrincipal(ctx, actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal() err = nil, want non-executable error")
	}
	if !strings.Contains(err.Error(), "present in the manifest but not yet executable") {
		t.Fatalf("err = %v, want clean non-executable error", err)
	}
}

func TestRegistryRejectsExternalManifestNameCollisionWithNativeTool(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), time.Second)
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "exec",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run"},
	}})
	if err == nil {
		t.Fatal("WithExternalToolManifests() err = nil, want native collision rejection")
	}
	if !strings.Contains(err.Error(), "collides with native tool definition") {
		t.Fatalf("err = %v, want collision rejection", err)
	}
}
