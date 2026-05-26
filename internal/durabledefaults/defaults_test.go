//go:build linux

package durabledefaults

import (
	"os"
	"strings"
	"testing"
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
