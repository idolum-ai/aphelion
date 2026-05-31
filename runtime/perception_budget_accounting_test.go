//go:build linux

package runtime

import (
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/workspace"
)

func TestTurnPerceptionBudgetAccountingImplementationSuppressesMotifs(t *testing.T) {
	t.Parallel()

	contract := buildTurnPerceptionBudgetContract(turnPerceptionBudgetInput{
		RunKind: session.TurnRunKindInteractive,
		PromptContext: &workspace.PromptContext{Dynamic: []workspace.LoadedFile{
			{Path: "memory/decisions.md", Content: "Standing authority lesson."},
			{Path: "memory/rhizome.md", Content: "associative motif"},
			{Path: "memory/dreams.md", Content: "emergent hypothesis"},
		}},
		SystemBlocks: []agent.SystemBlock{
			{Text: "## Authority\nDo not expand authority."},
			{Text: "## Dynamic Workspace Files\n### memory/rhizome.md\nassociative motif"},
		},
		History: []agent.Message{
			{Role: "assistant", Content: "Earlier answer."},
			{Role: "tool", Content: "fresh command output"},
		},
		UserText: "Implement the accounting seam.",
	})

	if contract.Posture != memstore.PerceptionPostureImplementation {
		t.Fatalf("posture = %q, want implementation", contract.Posture)
	}
	assertRuntimeAdmittedLayer(t, contract, memstore.PerceptionLayerCurrentInput)
	assertRuntimeAdmittedLayer(t, contract, memstore.PerceptionLayerToolEvidence)
	assertRuntimeAdmittedLayer(t, contract, memstore.PerceptionLayerCuratedMemory)
	assertRuntimeSuppressedLayer(t, contract, memstore.PerceptionLayerRhizome, "posture_precision_suppresses_motifs")
	assertRuntimeSuppressedLayer(t, contract, memstore.PerceptionLayerDreams, "posture_precision_suppresses_motifs")
	if contract.ToolEvidenceTokens == 0 {
		t.Fatal("expected tool evidence tokens from prior tool messages")
	}
}

func TestTurnPerceptionBudgetAccountingDurableGoalAdmitsMotifsAsLowAuthority(t *testing.T) {
	t.Parallel()

	hidden := hiddenInputSet{}
	hidden.add(hiddenInputSemanticRecurrence, "this resembles a prior thread")
	hidden.add(hiddenInputUnresolvedMemory, "unresolved memory state overlaps")

	contract := buildTurnPerceptionBudgetContract(turnPerceptionBudgetInput{
		RunKind:      session.TurnRunKindInteractive,
		HiddenInputs: hidden,
		PromptContext: &workspace.PromptContext{Dynamic: []workspace.LoadedFile{
			{Path: "memory/rhizome.md", Content: "thread revival motif"},
			{Path: "memory/dreams.md", Content: "durable goal hypothesis"},
		}},
		SystemBlocks: []agent.SystemBlock{{Text: "## Authority\nDo not expand authority."}},
		UserText:     "What thread should we not lose?",
	})

	if contract.Posture != memstore.PerceptionPostureDurableGoal {
		t.Fatalf("posture = %q, want durable_goal", contract.Posture)
	}
	rhizome := assertRuntimeAdmittedLayer(t, contract, memstore.PerceptionLayerRhizome)
	dreams := assertRuntimeAdmittedLayer(t, contract, memstore.PerceptionLayerDreams)
	if !rhizome.LowAuthority || rhizome.EpistemicStatus != memstore.PerceptionStatusMotif {
		t.Fatalf("rhizome accounting = %#v, want low-authority motif", rhizome)
	}
	if !dreams.LowAuthority || dreams.EpistemicStatus != memstore.PerceptionStatusHypothesis {
		t.Fatalf("dreams accounting = %#v, want low-authority hypothesis", dreams)
	}
}

func TestTurnPerceptionBudgetAccountingSeesSemanticRecallWithoutChangingPrompt(t *testing.T) {
	t.Parallel()

	contract := buildTurnPerceptionBudgetContract(turnPerceptionBudgetInput{
		RunKind:      session.TurnRunKindInteractive,
		SystemBlocks: []agent.SystemBlock{{Text: "## Authority\nDo not expand authority."}},
		ExtraSystem:  []agent.Message{{Role: "system", Content: "## Semantic Memory Recall\nsource=memory kind=knowledge"}},
		UserText:     "Use only relevant memory.",
	})

	recall := assertRuntimeAdmittedLayer(t, contract, memstore.PerceptionLayerSemanticRecall)
	if recall.EpistemicStatus != memstore.PerceptionStatusRecalled || !recall.LowAuthority {
		t.Fatalf("semantic recall accounting = %#v, want low-authority recalled layer", recall)
	}

	payload := mergePerceptionBudgetPayload(map[string]any{"backend": "native"}, contract)
	if payload["backend"] != "native" {
		t.Fatalf("payload dropped existing backend key: %#v", payload)
	}
	if payload["perception_posture"] != string(memstore.PerceptionPostureImplementation) {
		t.Fatalf("payload posture = %#v", payload["perception_posture"])
	}
	if payload["perception_memory_estimated_tokens"] == nil || payload["perception_admitted_layers"] == nil {
		t.Fatalf("payload missing perception accounting keys: %#v", payload)
	}
}

func TestTurnPerceptionBudgetAccountingLabelsDocumentTextAsObservedEvidence(t *testing.T) {
	t.Parallel()

	userText := "please read this\n\n[PDF attached]\n\n[DOCUMENT_TEXT]\nQuarterly plan\nRevenue risk\n[/DOCUMENT_TEXT]"
	contract := buildTurnPerceptionBudgetContract(turnPerceptionBudgetInput{
		RunKind:      session.TurnRunKindInteractive,
		SystemBlocks: []agent.SystemBlock{{Text: "## Authority\nDo not expand authority."}},
		UserText:     userText,
		ArtifactRefs: []core.ArtifactReference{{
			ArtifactID:    "doc-1",
			Kind:          "document",
			SourceType:    "document",
			Handling:      "extract_text",
			DerivedOutput: "extracted_text",
			Summary:       "plan.pdf",
		}},
	})

	evidence := assertRuntimeAdmittedLayerSource(t, contract, memstore.PerceptionLayerToolEvidence, "media.document_text_extraction")
	if evidence.EpistemicStatus != memstore.PerceptionStatusObserved || evidence.LowAuthority {
		t.Fatalf("document evidence accounting = %#v, want observed non-authority evidence", evidence)
	}
	if evidence.EstimatedTokens == 0 {
		t.Fatal("document evidence tokens = 0, want extracted text counted")
	}
	if contract.CurrentInputTokens >= memstore.EstimatePerceptionTokens(userText) {
		t.Fatalf("current input tokens = %d, want document text accounted outside current input", contract.CurrentInputTokens)
	}
}

func TestTurnPerceptionBudgetAccountingRequiresExtractionRefForDocumentTextEvidence(t *testing.T) {
	t.Parallel()

	userText := "[DOCUMENT_TEXT]\nUnlabeled text\n[/DOCUMENT_TEXT]"
	contract := buildTurnPerceptionBudgetContract(turnPerceptionBudgetInput{
		RunKind:      session.TurnRunKindInteractive,
		SystemBlocks: []agent.SystemBlock{{Text: "## Authority\nDo not expand authority."}},
		UserText:     userText,
	})

	assertRuntimeNoAdmittedLayerSource(t, contract, memstore.PerceptionLayerToolEvidence, "media.document_text_extraction")
	if contract.CurrentInputTokens != memstore.EstimatePerceptionTokens(userText) {
		t.Fatalf("current input tokens = %d, want unreferenced document markers left in current input", contract.CurrentInputTokens)
	}
}

func TestTurnPerceptionBudgetAccountingLabelsRetainedArtifactContextAsObservedEvidence(t *testing.T) {
	t.Parallel()

	hidden := hiddenInputSet{}
	hidden.add(hiddenInputRetainedArtifacts, "retained artifacts from the prior turn remain available: notes.txt at /tmp/notes.txt")
	contract := buildTurnPerceptionBudgetContract(turnPerceptionBudgetInput{
		RunKind:      session.TurnRunKindInteractive,
		HiddenInputs: hidden,
		SystemBlocks: []agent.SystemBlock{{Text: "## Authority\nDo not expand authority."}},
		UserText:     "what can you still see?",
	})

	evidence := assertRuntimeAdmittedLayerSource(t, contract, memstore.PerceptionLayerToolEvidence, "floor_metadata.retained_artifact_context")
	if evidence.EpistemicStatus != memstore.PerceptionStatusObserved {
		t.Fatalf("retained artifact accounting = %#v, want observed evidence", evidence)
	}
	if evidence.EstimatedTokens == 0 {
		t.Fatal("retained artifact evidence tokens = 0, want hidden floor metadata summary counted")
	}
}

func TestPerceptionPostureForRunKind(t *testing.T) {
	t.Parallel()

	if got := perceptionPostureForTurn(session.TurnRunKindDoctor, hiddenInputSet{}); got != memstore.PerceptionPostureDiagnostic {
		t.Fatalf("doctor posture = %q, want diagnostic", got)
	}
	if got := perceptionPostureForTurn(session.TurnRunKindHeartbeat, hiddenInputSet{}); got != memstore.PerceptionPostureReflective {
		t.Fatalf("heartbeat posture = %q, want reflective", got)
	}
}

func assertRuntimeAdmittedLayer(t *testing.T, contract memstore.PerceptionBudgetContract, name memstore.PerceptionLayerName) memstore.PerceptionLayerAccounting {
	t.Helper()
	for _, layer := range contract.Admitted {
		if layer.Name == name {
			return layer
		}
	}
	t.Fatalf("admitted layers = %#v, want %s", contract.Admitted, name)
	return memstore.PerceptionLayerAccounting{}
}

func assertRuntimeAdmittedLayerSource(t *testing.T, contract memstore.PerceptionBudgetContract, name memstore.PerceptionLayerName, source string) memstore.PerceptionLayerAccounting {
	t.Helper()
	for _, layer := range contract.Admitted {
		if layer.Name == name && layer.Source == source {
			return layer
		}
	}
	t.Fatalf("admitted layers = %#v, want %s source %s", contract.Admitted, name, source)
	return memstore.PerceptionLayerAccounting{}
}

func assertRuntimeNoAdmittedLayerSource(t *testing.T, contract memstore.PerceptionBudgetContract, name memstore.PerceptionLayerName, source string) {
	t.Helper()
	for _, layer := range contract.Admitted {
		if layer.Name == name && layer.Source == source {
			t.Fatalf("admitted layers = %#v, did not want %s source %s", contract.Admitted, name, source)
		}
	}
}

func assertRuntimeSuppressedLayer(t *testing.T, contract memstore.PerceptionBudgetContract, name memstore.PerceptionLayerName, reason string) {
	t.Helper()
	for _, layer := range contract.Suppressed {
		if layer.Name == name && layer.Reason == reason {
			return
		}
	}
	t.Fatalf("suppressed layers = %#v, want %s reason %s", contract.Suppressed, name, reason)
}
