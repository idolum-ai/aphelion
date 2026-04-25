//go:build linux

package tool

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadExternalToolManifestRoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "browse_page.json")
	raw := `{
		"name": "browse_page",
		"owner": "child-alpha",
		"version": "0.1.0",
		"execution": {
			"mode": "container",
			"entry": "ghcr.io/idolum/child-browser-tool:pilot",
			"workdir": "/tool",
			"timeout_seconds": 30
		},
		"io": {
			"input_schema": {"type":"object","properties":{"url":{"type":"string"}}},
			"output_schema": {"type":"object","properties":{"summary":{"type":"string"}}}
		},
		"constraints": {
			"network": "allowlist",
			"network_targets": ["example.com", "example.com"],
			"filesystem": "scratch",
			"max_memory_mb": 256,
			"max_runtime_seconds": 30
		},
		"probe": {
			"command": ["/tool/probe", "--self-check"],
			"expected_output_contains": "ok"
		},
		"provenance": {
			"request_id": "cap-tool-browse-page",
			"registered_at": "2026-04-23T00:00:00Z",
			"registered_by": "admin"
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile() err = %v", err)
	}

	manifest, err := LoadExternalToolManifest(path)
	if err != nil {
		t.Fatalf("LoadExternalToolManifest() err = %v", err)
	}
	if manifest.Name != "browse_page" || manifest.Owner != "child-alpha" {
		t.Fatalf("manifest = %#v, want normalized name/owner", manifest)
	}
	if manifest.Execution.Mode != "container" || manifest.Execution.Entry == "" {
		t.Fatalf("execution = %#v, want container entry", manifest.Execution)
	}
	if len(manifest.Constraints.NetworkTargets) != 1 || manifest.Constraints.NetworkTargets[0] != "example.com" {
		t.Fatalf("network targets = %#v, want deduped targets", manifest.Constraints.NetworkTargets)
	}
}

func TestBundledBrowsePagePilotManifestLoads(t *testing.T) {
	t.Parallel()

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() ok = false")
	}
	repoRoot := filepath.Dir(filepath.Dir(source))
	manifest, err := LoadExternalToolManifest(filepath.Join(repoRoot, "external-tools", "browse_page", "manifest.json"))
	if err != nil {
		t.Fatalf("LoadExternalToolManifest(bundled browse_page) err = %v", err)
	}
	if manifest.Name != "browse_page" || manifest.Owner != "child-alpha" || manifest.Execution.Mode != "process" {
		t.Fatalf("bundled manifest = %#v, want child-owned process browse_page pilot", manifest)
	}
	if manifest.Constraints.Network != "none" || len(manifest.Install.Command) == 0 || len(manifest.Probe.Command) == 0 {
		t.Fatalf("bundled manifest constraints/install/probe = %#v/%#v/%#v, want deterministic governed fixture", manifest.Constraints, manifest.Install, manifest.Probe)
	}
}

func TestLoadExternalToolManifestRejectsMissingRequiredFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.json")
	if err := os.WriteFile(path, []byte(`{"owner":"child-alpha","execution":{"mode":"container","entry":"tool"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() err = %v", err)
	}

	_, err := LoadExternalToolManifest(path)
	if err == nil {
		t.Fatal("LoadExternalToolManifest() err = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("err = %v, want missing-name validation", err)
	}
}

func TestLoadExternalToolManifestDirLoadsSortedAndRejectsDuplicates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write := func(name, toolName string) {
		t.Helper()
		raw := `{"name":"` + toolName + `","owner":"child-alpha","execution":{"mode":"process","entry":"./run"}}`
		if err := os.WriteFile(filepath.Join(dir, name), []byte(raw), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) err = %v", name, err)
		}
	}
	write("b.json", "beta")
	write("a.json", "alpha")
	manifests, err := LoadExternalToolManifestDir(dir)
	if err != nil {
		t.Fatalf("LoadExternalToolManifestDir() err = %v", err)
	}
	if len(manifests) != 2 || manifests[0].Name != "alpha" || manifests[1].Name != "beta" {
		t.Fatalf("manifests = %#v, want alpha,beta order", manifests)
	}

	write("dup.json", "alpha")
	_, err = LoadExternalToolManifestDir(dir)
	if err == nil {
		t.Fatal("LoadExternalToolManifestDir() err = nil, want duplicate-name rejection")
	}
	if !strings.Contains(err.Error(), "duplicate external tool manifest name") {
		t.Fatalf("err = %v, want duplicate-name rejection", err)
	}
}
