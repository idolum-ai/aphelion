//go:build linux

package session

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvariantSessionReadmeDefinesDurableStoreBoundary(t *testing.T) {
	t.Parallel()

	readme := readSessionInvariantFile(t, "README.md")
	for _, want := range []string{
		"# `session/` boundary",
		"`session/` is Aphelion's durable record/store shell",
		"`session/` owns durable facts and atomic persistence transitions, not live",
		"## Owned responsibilities",
		"## Non-owned responsibilities",
		"## Current subsystem map",
		"## Top-level growth rules",
		"## Policy-like storage predicates",
		"## Extraction criteria",
		"## Relationship to `runtime/`",
		"runtime/  --->  session/",
		"session/  -X->  runtime/",
		"session/  -X->  turn/",
		"session/  -X->  pipeline/",
		"session/  -X->  telegram/",
	} {
		assertSessionInvariantContains(t, readme, want)
	}
}

func TestInvariantSessionPackageDocDefinesDurableStoreBoundary(t *testing.T) {
	t.Parallel()

	doc := readSessionInvariantFile(t, "doc.go")
	for _, want := range []string{
		"Package session owns Aphelion's durable record/store shell.",
		"Session owns durable facts, not live decisions.",
		"This package should not import orchestration or transport packages such as",
		"runtime, turn, pipeline, or telegram.",
		"See README.md for the full boundary",
	} {
		assertSessionInvariantContains(t, doc, want)
	}
}

func TestInvariantSessionDoesNotImportOrchestrationOrTransportPackages(t *testing.T) {
	t.Parallel()

	forbidden := map[string]bool{
		"github.com/idolum-ai/aphelion/runtime":  true,
		"github.com/idolum-ai/aphelion/turn":     true,
		"github.com/idolum-ai/aphelion/pipeline": true,
		"github.com/idolum-ai/aphelion/telegram": true,
	}

	goFiles, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("Glob(*.go) err = %v", err)
	}
	if len(goFiles) == 0 {
		t.Fatal("no Go files found in session package")
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

func TestInvariantFutureSessionSubpackagesHavePackageDocs(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.) err = %v", err)
	}
	for _, entry := range entries {
		entry := entry
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			t.Parallel()
			docPath := filepath.Join(entry.Name(), "doc.go")
			if _, err := os.Stat(docPath); err != nil {
				if os.IsNotExist(err) {
					t.Fatalf("session subpackage %q must declare its ownership boundary in doc.go", entry.Name())
				}
				t.Fatalf("Stat(%q) err = %v", docPath, err)
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, docPath, nil, parser.ParseComments)
			if err != nil {
				t.Fatalf("ParseFile(%q) err = %v", docPath, err)
			}
			if file.Doc == nil || strings.TrimSpace(file.Doc.Text()) == "" {
				t.Fatalf("session subpackage %q doc.go must include a package doc comment", entry.Name())
			}
			if !docMentionsOwnershipBoundary(file.Doc) {
				t.Fatalf("session subpackage %q doc.go must mention ownership or boundary", entry.Name())
			}
		})
	}
}

func readSessionInvariantFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) err = %v", path, err)
	}
	return string(content)
}

func assertSessionInvariantContains(t *testing.T, content string, want string) {
	t.Helper()
	if strings.Contains(content, want) {
		return
	}
	if strings.Contains(strings.Join(strings.Fields(content), " "), strings.Join(strings.Fields(want), " ")) {
		return
	}
	t.Fatalf("content missing %q", want)
}

func docMentionsOwnershipBoundary(doc *ast.CommentGroup) bool {
	text := strings.ToLower(doc.Text())
	return strings.Contains(text, "own") || strings.Contains(text, "boundary")
}
