//go:build linux

package tool

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvariantToolReadmeDefinesAuthorityLifecycleBoundary(t *testing.T) {
	t.Parallel()

	readme := readToolInvariantFile(t, "README.md")
	for _, want := range []string{
		"# `tool/` boundary",
		"`tool/` is Aphelion's governed tool-runtime facade",
		"`tool/` owns governed tool behavior and lifecycle evidence, not ambient",
		"## Owned responsibilities",
		"## Non-owned responsibilities",
		"## Authority and lifecycle contract",
		"request != review/approval != grant",
		"registration != exposure != invocation",
		"install != audit != probe != verified",
		"manifest present != tool available",
		"repo artifact != active capability",
		"## Current subsystem map",
		"## Growth rules",
		"## Import direction",
		"## Cleanup posture",
		"tool/          -X->  runtime/",
		"tool/          -X->  turn/",
		"tool/          -X->  pipeline/",
		"tool/          -X->  telegram/",
		"tool/sandbox/  -X->  tool/",
		"tool/sandbox/  -X->  runtime/turn/pipeline/telegram/session/",
	} {
		assertToolInvariantContains(t, readme, want)
	}
}

func TestInvariantToolReadmePinsVerifiedAndDriftSemantics(t *testing.T) {
	t.Parallel()

	readme := readToolInvariantFile(t, "README.md")
	for _, want := range []string{
		"Verified",
		"current evidence is green for the active baseline",
		"install, audit, and probe evidence must match the current manifest/install",
		"Verification must not inherit stale confidence",
		"Drift",
		"manifest hash drift",
		"install-ref drift",
		"workspace fingerprint drift",
		"container/runtime identity",
		"failed reprobe",
		"explicit stale reason",
	} {
		assertToolInvariantContains(t, readme, want)
	}
}

func TestInvariantToolPackageDocsDeclareBoundary(t *testing.T) {
	t.Parallel()

	doc := readToolInvariantFile(t, "doc.go")
	for _, want := range []string{
		"Package tool owns Aphelion's governed tool runtime.",
		"capability authority",
		"install/audit/probe lifecycle",
		"grant-gated invocation",
		"not installed, verified",
		"registered, granted, callable, or safe",
		"because it exists in the repo",
		"should not import runtime, turn, or pipeline orchestration",
	} {
		assertToolInvariantContains(t, doc, want)
	}

	sandboxDoc := readToolInvariantFile(t, filepath.Join("sandbox", "doc.go"))
	for _, want := range []string{
		"Package sandbox owns process execution profiles.",
		"below tool policy",
		"runtime orchestration",
	} {
		assertToolInvariantContains(t, sandboxDoc, want)
	}
}

func TestInvariantToolDoesNotImportOrchestrationOrTransport(t *testing.T) {
	t.Parallel()

	forbidden := map[string]bool{
		"github.com/idolum-ai/aphelion/runtime":  true,
		"github.com/idolum-ai/aphelion/turn":     true,
		"github.com/idolum-ai/aphelion/pipeline": true,
		"github.com/idolum-ai/aphelion/telegram": true,
	}
	assertGoFilesDoNotImport(t, "*.go", forbidden)
}

func TestInvariantToolSandboxStaysBelowPolicyAndRuntime(t *testing.T) {
	t.Parallel()

	forbidden := map[string]bool{
		"github.com/idolum-ai/aphelion/tool":     true,
		"github.com/idolum-ai/aphelion/session":  true,
		"github.com/idolum-ai/aphelion/runtime":  true,
		"github.com/idolum-ai/aphelion/turn":     true,
		"github.com/idolum-ai/aphelion/pipeline": true,
		"github.com/idolum-ai/aphelion/telegram": true,
	}
	assertGoFilesDoNotImport(t, filepath.Join("sandbox", "*.go"), forbidden)
}

func readToolInvariantFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) err = %v", path, err)
	}
	return string(content)
}

func assertToolInvariantContains(t *testing.T, content string, want string) {
	t.Helper()
	if strings.Contains(content, want) {
		return
	}
	if strings.Contains(strings.Join(strings.Fields(content), " "), strings.Join(strings.Fields(want), " ")) {
		return
	}
	t.Fatalf("content missing %q", want)
}

func assertGoFilesDoNotImport(t *testing.T, pattern string, forbidden map[string]bool) {
	t.Helper()
	goFiles, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("Glob(%q) err = %v", pattern, err)
	}
	if len(goFiles) == 0 {
		t.Fatalf("no Go files matched %q", pattern)
	}
	fset := token.NewFileSet()
	for _, path := range goFiles {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("ParseFile(%q) err = %v", path, err)
			}
			for _, spec := range file.Imports {
				imp := strings.Trim(spec.Path.Value, "\"")
				if forbidden[imp] {
					t.Fatalf("%s imports forbidden boundary package %q", path, imp)
				}
			}
		})
	}
}
