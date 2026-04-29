//go:build linux

package durableagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadArchetypeRequiresTemplateFiles(t *testing.T) {
	root := t.TempDir()
	writeTestArchetypeFile(t, root, "maintainer", "AGENT.md", "# Maintainer\n")
	writeTestArchetypeFile(t, root, "maintainer", "profile/charter.md", "Review the system.\n")
	writeTestArchetypeFile(t, root, "maintainer", "profile/policy.md", "- outbound_mode: read_only\n")
	writeTestArchetypeFile(t, root, "maintainer", "profile/capabilities.md", "- session_log_read\n")

	_, err := LoadArchetype(root, "maintainer")
	if err == nil {
		t.Fatal("LoadArchetype() err = nil, want missing runtime error")
	}
	if !strings.Contains(err.Error(), "profile/runtime.md") {
		t.Fatalf("err = %v, want missing runtime file", err)
	}
}

func TestLoadArchetypeRejectsLiveState(t *testing.T) {
	root := t.TempDir()
	writeValidTestArchetype(t, root, "maintainer")
	writeTestArchetypeFile(t, root, "maintainer", "artifacts/report.md", "live report\n")

	_, err := LoadArchetype(root, "maintainer")
	if err == nil {
		t.Fatal("LoadArchetype() err = nil, want live-state rejection")
	}
	if !strings.Contains(err.Error(), "live-state") {
		t.Fatalf("err = %v, want live-state context", err)
	}
}

func TestLoadArchetypeReadsProfileAndExamples(t *testing.T) {
	root := t.TempDir()
	writeValidTestArchetype(t, root, "maintainer")
	writeTestArchetypeFile(t, root, "maintainer", "examples/doctor-report.md", "## Report\n")

	archetype, err := LoadArchetype(root, "maintainer")
	if err != nil {
		t.Fatalf("LoadArchetype() err = %v", err)
	}
	if archetype.Name != "maintainer" {
		t.Fatalf("Name = %q, want maintainer", archetype.Name)
	}
	if !strings.Contains(archetype.Files["AGENT.md"], "Maintainer") {
		t.Fatalf("AGENT.md = %q, want loaded content", archetype.Files["AGENT.md"])
	}
	if archetype.Profile["charter.md"] != "Review the system.\n" {
		t.Fatalf("profile charter = %q, want loaded content", archetype.Profile["charter.md"])
	}
	if len(archetype.Examples) != 1 || archetype.Examples[0] != "examples/doctor-report.md" {
		t.Fatalf("examples = %#v, want doctor report example", archetype.Examples)
	}
}

func TestListArchetypesSkipsInvalidEntries(t *testing.T) {
	root := t.TempDir()
	writeValidTestArchetype(t, root, "maintainer")
	writeTestArchetypeFile(t, root, "broken", "AGENT.md", "# Broken\n")

	list, err := ListArchetypes(root)
	if err != nil {
		t.Fatalf("ListArchetypes() err = %v", err)
	}
	if len(list) != 1 || list[0].Name != "maintainer" {
		t.Fatalf("list = %#v, want only valid maintainer archetype", list)
	}
}

func writeValidTestArchetype(t *testing.T, root, name string) {
	t.Helper()
	writeTestArchetypeFile(t, root, name, "AGENT.md", "# Maintainer\n")
	writeTestArchetypeFile(t, root, name, "profile/charter.md", "Review the system.\n")
	writeTestArchetypeFile(t, root, name, "profile/policy.md", "- outbound_mode: read_only\n")
	writeTestArchetypeFile(t, root, name, "profile/capabilities.md", "- session_log_read\n")
	writeTestArchetypeFile(t, root, name, "profile/runtime.md", "Store proposals under child artifacts.\n")
}

func writeTestArchetypeFile(t *testing.T, root, name, rel, content string) {
	t.Helper()
	target := filepath.Join(root, name, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) err = %v", filepath.Dir(target), err)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) err = %v", target, err)
	}
}
