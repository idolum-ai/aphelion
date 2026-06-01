//go:build linux

package memory

import (
	"strings"
	"testing"
)

func TestPerceptionBudgetImplementationSuppressesMotifsAndPreservesEvidence(t *testing.T) {
	t.Parallel()

	contract := BuildPerceptionBudgetContract(PerceptionBudgetRequest{
		Posture:         PerceptionPostureImplementation,
		ContextWindow:   2000,
		MaxContextRatio: 0.50,
		Layers: []PerceptionLayerRequest{
			{Name: PerceptionLayerAuthority, EstimatedTokens: 120, Required: true},
			{Name: PerceptionLayerCurrentInput, Source: "turn", EstimatedTokens: 180, Required: true},
			{Name: PerceptionLayerToolEvidence, Source: "exec", EstimatedTokens: 220, Required: true},
			{Name: PerceptionLayerCuratedMemory, Source: "memory/decisions.md", EstimatedTokens: 80},
			{Name: PerceptionLayerRhizome, Source: "memory/rhizome.md", EstimatedTokens: 60},
			{Name: PerceptionLayerDreams, Source: "memory/dreams.md", EstimatedTokens: 60},
		},
	})

	current := assertAdmittedLayer(t, contract, PerceptionLayerCurrentInput)
	tool := assertAdmittedLayer(t, contract, PerceptionLayerToolEvidence)
	assertSuppressedLayer(t, contract, PerceptionLayerRhizome, "posture_precision_suppresses_motifs")
	assertSuppressedLayer(t, contract, PerceptionLayerDreams, "posture_precision_suppresses_motifs")
	if current.Priority <= 800 || tool.Priority <= 800 {
		t.Fatalf("current/tool priority = %d/%d, want evidence-priority layers", current.Priority, tool.Priority)
	}
	if contract.CurrentInputTokens != 180 || contract.ToolEvidenceTokens != 220 {
		t.Fatalf("current/tool tokens = %d/%d, want 180/220", contract.CurrentInputTokens, contract.ToolEvidenceTokens)
	}
	if contract.MemoryEstimatedTokens > contract.MemoryBudgetTokens {
		t.Fatalf("memory tokens = %d over budget %d", contract.MemoryEstimatedTokens, contract.MemoryBudgetTokens)
	}
	assertAttestation(t, contract, "current_input_admitted")
	assertAttestation(t, contract, "tool_evidence_admitted")
	assertAttestationPrefix(t, contract, "memory_budget_tokens=")
	assertAttestationPrefix(t, contract, "remaining_headroom_tokens=")
}

func TestPerceptionBudgetDurableGoalAdmitsRhizomeAndDreamsAsLowAuthority(t *testing.T) {
	t.Parallel()

	contract := BuildPerceptionBudgetContract(PerceptionBudgetRequest{
		Posture:         PerceptionPostureDurableGoal,
		ContextWindow:   3000,
		MaxContextRatio: 0.50,
		Layers: []PerceptionLayerRequest{
			{Name: PerceptionLayerCurrentInput, EstimatedTokens: 100, Required: true},
			{Name: PerceptionLayerRhizome, Source: "memory/rhizome.md", EstimatedTokens: 90},
			{Name: PerceptionLayerDreams, Source: "memory/dreams.md", EstimatedTokens: 90},
			{Name: PerceptionLayerSemanticRecall, Source: "semantic", EstimatedTokens: 120},
		},
	})

	rhizome := assertAdmittedLayer(t, contract, PerceptionLayerRhizome)
	dreams := assertAdmittedLayer(t, contract, PerceptionLayerDreams)
	if rhizome.EpistemicStatus != PerceptionStatusMotif || !rhizome.LowAuthority {
		t.Fatalf("rhizome accounting = %#v, want motif + low authority", rhizome)
	}
	if dreams.EpistemicStatus != PerceptionStatusHypothesis || !dreams.LowAuthority {
		t.Fatalf("dreams accounting = %#v, want hypothesis + low authority", dreams)
	}
	if !strings.Contains(rhizome.AdmissionReason, "durable_goal") {
		t.Fatalf("rhizome admission reason = %q, want durable goal continuity", rhizome.AdmissionReason)
	}
	assertAttestation(t, contract, "low_authority:rhizome")
	assertAttestation(t, contract, "low_authority:dreams")
	if len(contract.Risks) != 0 {
		t.Fatalf("risks = %#v, want none", contract.Risks)
	}
}

func TestPerceptionBudgetReflectiveAdmitsMotifsButCapsMemory(t *testing.T) {
	t.Parallel()

	contract := BuildPerceptionBudgetContract(PerceptionBudgetRequest{
		Posture:         PerceptionPostureReflective,
		ContextWindow:   1000,
		MaxContextRatio: 0.50,
		Layers: []PerceptionLayerRequest{
			{Name: PerceptionLayerCurrentInput, EstimatedTokens: 40, Required: true},
			{Name: PerceptionLayerRhizome, EstimatedTokens: 70},
			{Name: PerceptionLayerDreams, EstimatedTokens: 70},
			{Name: PerceptionLayerSemanticRecall, EstimatedTokens: 80},
			{Name: PerceptionLayerCuratedMemory, EstimatedTokens: 80},
		},
	})

	assertAdmittedLayer(t, contract, PerceptionLayerRhizome)
	assertAdmittedLayer(t, contract, PerceptionLayerDreams)
	if contract.MemoryEstimatedTokens > contract.MemoryBudgetTokens {
		t.Fatalf("memory tokens = %d over budget %d", contract.MemoryEstimatedTokens, contract.MemoryBudgetTokens)
	}
	if len(contract.Suppressed) == 0 {
		t.Fatal("expected at least one memory layer suppressed by budget")
	}
	foundBudgetSuppression := false
	for _, suppressed := range contract.Suppressed {
		if suppressed.Reason == "memory_budget_cap" {
			foundBudgetSuppression = true
		}
	}
	if !foundBudgetSuppression {
		t.Fatalf("suppressed = %#v, want memory_budget_cap", contract.Suppressed)
	}
}

func TestPerceptionBudgetImportedArchivesRequireExplicitApproval(t *testing.T) {
	t.Parallel()

	contract := BuildPerceptionBudgetContract(PerceptionBudgetRequest{
		Posture:         PerceptionPostureDiagnostic,
		ContextWindow:   4000,
		MaxContextRatio: 0.50,
		Layers: []PerceptionLayerRequest{
			{Name: PerceptionLayerCurrentInput, EstimatedTokens: 80, Required: true},
			{Name: PerceptionLayerImportedArchive, Source: "codex_sessions/empty-state.jsonl", EstimatedTokens: 200},
			{Name: PerceptionLayerImportedArchive, Source: "codex_sessions/quarantined.jsonl", EstimatedTokens: 200, ImportState: SemanticImportStateQuarantine},
			{Name: PerceptionLayerImportedArchive, Source: "openclaw/approved", EstimatedTokens: 200, ImportState: SemanticImportStateApproved},
		},
	})

	approved := assertAdmittedLayer(t, contract, PerceptionLayerImportedArchive)
	if approved.Source != "openclaw/approved" || approved.EpistemicStatus != PerceptionStatusImported {
		t.Fatalf("approved import accounting = %#v", approved)
	}
	count := 0
	for _, suppressed := range contract.Suppressed {
		if suppressed.Name == PerceptionLayerImportedArchive && suppressed.Reason == "import_state_not_approved" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("suppressed imports = %#v, want empty and quarantined states suppressed", contract.Suppressed)
	}
}

func TestPerceptionBudgetUnknownLayersAreSuppressed(t *testing.T) {
	t.Parallel()

	contract := BuildPerceptionBudgetContract(PerceptionBudgetRequest{
		Posture:         PerceptionPostureImplementation,
		ContextWindow:   2000,
		MaxContextRatio: 0.50,
		Layers: []PerceptionLayerRequest{
			{Name: PerceptionLayerName("future_layer"), Source: "malformed", EstimatedTokens: 50},
			{Name: PerceptionLayerCurrentInput, EstimatedTokens: 80, Required: true},
		},
	})

	assertAdmittedLayer(t, contract, PerceptionLayerCurrentInput)
	assertSuppressedLayer(t, contract, PerceptionLayerUnknown, "unknown_layer")
	for _, layer := range contract.Admitted {
		if layer.Source == "malformed" || layer.Name == PerceptionLayerUnknown {
			t.Fatalf("unknown layer admitted: %#v", layer)
		}
	}
}

func TestPerceptionBudgetRequiredLayersCanExceedCapsButAttestRisk(t *testing.T) {
	t.Parallel()

	contract := BuildPerceptionBudgetContract(PerceptionBudgetRequest{
		Posture:         PerceptionPostureImplementation,
		ContextWindow:   500,
		MaxContextRatio: 0.50,
		Layers: []PerceptionLayerRequest{
			{Name: PerceptionLayerCurrentInput, EstimatedTokens: 220, Required: true},
			{Name: PerceptionLayerToolEvidence, EstimatedTokens: 120, Required: true},
		},
	})

	if contract.RemainingHeadroomTokens >= 0 {
		t.Fatalf("remaining headroom = %d, want over-budget required evidence", contract.RemainingHeadroomTokens)
	}
	if !containsString(contract.Risks, "over_budget") {
		t.Fatalf("risks = %#v, want over_budget", contract.Risks)
	}
}

func assertAdmittedLayer(t *testing.T, contract PerceptionBudgetContract, name PerceptionLayerName) PerceptionLayerAccounting {
	t.Helper()
	for _, layer := range contract.Admitted {
		if layer.Name == name {
			return layer
		}
	}
	t.Fatalf("admitted layers = %#v, want %s", contract.Admitted, name)
	return PerceptionLayerAccounting{}
}

func assertSuppressedLayer(t *testing.T, contract PerceptionBudgetContract, name PerceptionLayerName, reason string) {
	t.Helper()
	for _, layer := range contract.Suppressed {
		if layer.Name == name && layer.Reason == reason {
			return
		}
	}
	t.Fatalf("suppressed layers = %#v, want %s reason %s", contract.Suppressed, name, reason)
}

func assertAttestation(t *testing.T, contract PerceptionBudgetContract, want string) {
	t.Helper()
	if !containsString(contract.Attestations, want) {
		t.Fatalf("attestations = %#v, want %q", contract.Attestations, want)
	}
}

func assertAttestationPrefix(t *testing.T, contract PerceptionBudgetContract, prefix string) {
	t.Helper()
	for _, value := range contract.Attestations {
		if strings.HasPrefix(value, prefix) {
			return
		}
	}
	t.Fatalf("attestations = %#v, want prefix %q", contract.Attestations, prefix)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
