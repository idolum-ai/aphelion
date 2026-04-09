//go:build linux

package tool

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
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

	manifest := RenderManifest(defs)
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
