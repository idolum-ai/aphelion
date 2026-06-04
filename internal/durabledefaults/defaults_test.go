//go:build linux

package durabledefaults

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/config"
)

func TestBundledDailyReviewRecipePromptUsesOutcomeContract(t *testing.T) {
	t.Parallel()

	recipe, err := loadBundledDailyReviewRecipe(Deps{RecipeFS: os.DirFS("../..")})
	if err != nil {
		t.Fatalf("loadBundledDailyReviewRecipe() err = %v", err)
	}
	prompt := recipe.Review.PromptTemplate
	for _, want := range []string{
		"## Role",
		"## Goal",
		"## Evidence",
		"Use only this staged transcript file",
		"## Output",
		"concrete action items for tomorrow (max 5)",
		"## Stop Rules",
		"Do not invent patterns or outcomes not supported by the transcript.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("daily review prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestDefaultDurableAgentBootstrapUsesConfiguredCodexHome(t *testing.T) {
	t.Parallel()

	codexHome := filepath.Join(t.TempDir(), "codex-home")
	cfg := config.Default()
	cfg.Governor.Backend = "codex"
	cfg.Governor.Codex.AuthSource = "codex_cli"
	cfg.Governor.Codex.CodexHome = codexHome
	cfg.Governor.Codex.BaseURL = "https://chatgpt.example.test/backend-api"

	got := DefaultDurableAgentBootstrapFromConfig(&cfg)
	if got.Backend != "codex" {
		t.Fatalf("Backend = %q, want codex", got.Backend)
	}
	if got.CodexAuthSource != "codex_cli" {
		t.Fatalf("CodexAuthSource = %q, want codex_cli", got.CodexAuthSource)
	}
	if got.CodexHome != codexHome {
		t.Fatalf("CodexHome = %q, want %q", got.CodexHome, codexHome)
	}
	if got.CodexBaseURL != "https://chatgpt.example.test/backend-api" {
		t.Fatalf("CodexBaseURL = %q, want configured base URL", got.CodexBaseURL)
	}
}

func TestDefaultDurableAgentBootstrapUsesCODEXHOMEFallback(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	t.Setenv("CODEX_HOME", codexHome)

	cfg := config.Default()
	cfg.Governor.Backend = "codex"
	cfg.Governor.Codex.AuthSource = "codex_cli"
	cfg.Governor.Codex.CodexHome = ""

	got := DefaultDurableAgentBootstrapFromConfig(&cfg)
	if got.Backend != "codex" {
		t.Fatalf("Backend = %q, want codex", got.Backend)
	}
	if got.CodexHome != codexHome {
		t.Fatalf("CodexHome = %q, want CODEX_HOME %q", got.CodexHome, codexHome)
	}
}
