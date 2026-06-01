//go:build linux

package telegram

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvariantTelegramReadmeDefinesTransportBoundary(t *testing.T) {
	t.Parallel()

	readme := readTelegramInvariantFile(t, "README.md")
	for _, want := range []string{
		"# `telegram/` boundary",
		"`telegram/` is Aphelion's Telegram transport and Bot API adapter",
		"`telegram/` owns Telegram transport mechanics, not command policy or runtime",
		"## Owned responsibilities",
		"## Non-owned responsibilities",
		"## Current subsystem map",
		"## Import direction",
		"## Growth rules",
		"## Cleanup posture",
		"slash-command behavior",
		"approval decisions",
		"durable session schema",
		"transport observation and authority to act",
		"internal/telegramcommands",
		"internal/telegramdecision",
	} {
		assertTelegramInvariantContains(t, readme, want)
	}
}

func TestInvariantTelegramPackageDocDefinesTransportBoundary(t *testing.T) {
	t.Parallel()

	doc := readTelegramInvariantFile(t, "doc.go")
	for _, want := range []string{
		"Package telegram owns Telegram wire types and Bot API client behavior.",
		"normalizes Telegram updates into core transport records",
		"Telegram API requests",
		"stay transport-level",
		"avoid importing",
		"runtime, turn, pipeline",
		"storage orchestration",
	} {
		assertTelegramInvariantContains(t, doc, want)
	}
}

func TestInvariantTelegramDoesNotImportCommandRuntimeSessionOrPolicyPackages(t *testing.T) {
	t.Parallel()

	forbidden := map[string]bool{
		"github.com/idolum-ai/aphelion/runtime":                   true,
		"github.com/idolum-ai/aphelion/turn":                      true,
		"github.com/idolum-ai/aphelion/pipeline":                  true,
		"github.com/idolum-ai/aphelion/session":                   true,
		"github.com/idolum-ai/aphelion/tool":                      true,
		"github.com/idolum-ai/aphelion/internal/telegramcommands": true,
		"github.com/idolum-ai/aphelion/internal/telegramdecision": true,
	}
	assertTelegramGoFilesDoNotImport(t, "*.go", forbidden)
}

func readTelegramInvariantFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) err = %v", path, err)
	}
	return string(content)
}

func assertTelegramInvariantContains(t *testing.T, content string, want string) {
	t.Helper()
	if strings.Contains(content, want) {
		return
	}
	if strings.Contains(strings.Join(strings.Fields(content), " "), strings.Join(strings.Fields(want), " ")) {
		return
	}
	t.Fatalf("content missing %q", want)
}

func assertTelegramGoFilesDoNotImport(t *testing.T, pattern string, forbidden map[string]bool) {
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
