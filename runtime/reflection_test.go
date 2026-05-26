//go:build linux

package runtime

import (
	"strings"
	"testing"

	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/session"
)

func TestRenderReflectionRequestUsesOutcomeContractAndPreservesTags(t *testing.T) {
	t.Parallel()

	request := renderReflectionRequest(&reflectionInput{
		Notes: []string{"### daily/2026-05-26.md\nKeep Telegram progress detail toggles stable."},
		Events: []session.ReviewEvent{
			{Summary: "Operator approved making secondary prompts outcome-shaped."},
		},
		Semantic: []memstore.SemanticHit{
			{Source: "memory/decisions.md", Scope: "shared", Kind: "decision", Provenance: "reflection", Score: 0.91, Excerpt: "- Use compact outcome contracts for model prompts."},
		},
	})

	for _, want := range []string{
		heartbeatReflectionMarker,
		"## Role",
		"## Goal",
		"## Success Criteria",
		"## Output",
		"## Stop Rules",
		"Output only the tagged sections below.",
		"Do not invent facts",
		reflectionMemoryTag,
		reflectionMemoryEndTag,
		reflectionKnowledgeTag,
		reflectionKnowledgeEndTag,
		reflectionDecisionsTag,
		reflectionDecisionsEndTag,
		reflectionQuestionsTag,
		reflectionQuestionsEndTag,
		reflectionRhizomeTag,
		reflectionRhizomeEndTag,
		"## Daily Notes",
		"## Review Events",
		"## Semantic Context",
	} {
		if !strings.Contains(request, want) {
			t.Fatalf("reflection request missing %q:\n%s", want, request)
		}
	}
}

func TestParseReflectionSectionsAcceptsTaggedOutputOnly(t *testing.T) {
	t.Parallel()

	raw := strings.Join([]string{
		reflectionMemoryTag,
		"Operator prefers concise status cards.",
		reflectionMemoryEndTag,
		reflectionKnowledgeTag,
		"- Status summaries should be evidence-bounded.",
		reflectionKnowledgeEndTag,
		reflectionDecisionsTag,
		"- Keep prompt changes behind deterministic tests.",
		reflectionDecisionsEndTag,
		reflectionQuestionsTag,
		"- Should reflection cadence change?",
		reflectionQuestionsEndTag,
		reflectionRhizomeTag,
		"- prompt contracts <-> eval evidence",
		reflectionRhizomeEndTag,
	}, "\n")

	sections := parseReflectionSections(raw)
	if got := sections[memstore.StoreMemory]; !strings.Contains(got, "concise status cards") {
		t.Fatalf("memory section = %q", got)
	}
	if got := sections[memstore.StoreKnowledge]; !strings.Contains(got, "evidence-bounded") {
		t.Fatalf("knowledge section = %q", got)
	}
	if got := sections[memstore.StoreDecisions]; !strings.Contains(got, "deterministic tests") {
		t.Fatalf("decisions section = %q", got)
	}
	if got := sections[memstore.StoreQuestions]; !strings.Contains(got, "reflection cadence") {
		t.Fatalf("questions section = %q", got)
	}
	if got := sections[memstore.StoreRhizome]; !strings.Contains(got, "prompt contracts") {
		t.Fatalf("rhizome section = %q", got)
	}
}
