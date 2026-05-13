//go:build linux

package memory

import "testing"

func TestPlanAdaptiveRecallStaysLeanForLowSignalInput(t *testing.T) {
	t.Parallel()

	plan := PlanAdaptiveRecall(AdaptiveRecallRequest{
		Query:            "howdy",
		Purpose:          RecallPurposeInteractive,
		ContextWindow:    250000,
		MaxContextRatio:  0.90,
		BaselineTopK:     5,
		BaselineMaxChars: 4000,
	})

	if plan.Mode != RecallModeLean {
		t.Fatalf("mode = %q, want %q", plan.Mode, RecallModeLean)
	}
	if plan.TopK != 1 {
		t.Fatalf("TopK = %d, want 1", plan.TopK)
	}
	if plan.MaxChars <= 0 || plan.MaxChars > 1200 {
		t.Fatalf("MaxChars = %d, want a small non-zero lean budget", plan.MaxChars)
	}
}

func TestPlanAdaptiveRecallExpandsForDenseTechnicalWork(t *testing.T) {
	t.Parallel()

	plan := PlanAdaptiveRecall(AdaptiveRecallRequest{
		Query:            "review the last 24 hours of live session logs, identify retry timeout regressions, memory prompt failures, semantic markdown gaps, and recommend code changes with tests",
		Purpose:          RecallPurposeInteractive,
		ContextWindow:    250000,
		MaxContextRatio:  0.90,
		BaselineTopK:     5,
		BaselineMaxChars: 4000,
	})

	if plan.Mode != RecallModeDeep {
		t.Fatalf("mode = %q, want %q (score %.2f reasons %#v)", plan.Mode, RecallModeDeep, plan.Score, plan.Reasons)
	}
	if plan.TopK <= 5 {
		t.Fatalf("TopK = %d, want expansion beyond baseline", plan.TopK)
	}
	if plan.MaxChars <= 4000 {
		t.Fatalf("MaxChars = %d, want expansion beyond baseline", plan.MaxChars)
	}
}

func TestPlanAdaptiveRecallDoctorUsesDiagnosticBudget(t *testing.T) {
	t.Parallel()

	plan := PlanAdaptiveRecall(AdaptiveRecallRequest{
		Query:            "/health diagnose latest memory footprint, prompt files, session logs, and code recommendations",
		Purpose:          RecallPurposeDoctor,
		ContextWindow:    200000,
		MaxContextRatio:  0.80,
		BaselineTopK:     5,
		BaselineMaxChars: 4000,
	})

	if plan.Mode != RecallModeDoctor {
		t.Fatalf("mode = %q, want %q", plan.Mode, RecallModeDoctor)
	}
	if plan.TopK < 12 {
		t.Fatalf("TopK = %d, want diagnostic breadth", plan.TopK)
	}
	if plan.MaxChars < 16000 {
		t.Fatalf("MaxChars = %d, want diagnostic depth", plan.MaxChars)
	}
}

func TestPlanAdaptiveRecallRespectsSmallContextWindow(t *testing.T) {
	t.Parallel()

	plan := PlanAdaptiveRecall(AdaptiveRecallRequest{
		Query:            "diagnose the deployment failure logs, memory state, semantic index, code changes, and recommendations",
		Purpose:          RecallPurposeInteractive,
		ContextWindow:    8000,
		MaxContextRatio:  0.50,
		BaselineTopK:     5,
		BaselineMaxChars: 4000,
	})

	maxAllowedChars := plan.TokenBudget * adaptiveRecallCharsPerToken
	if plan.MaxChars > maxAllowedChars {
		t.Fatalf("MaxChars = %d, token budget allows at most %d", plan.MaxChars, maxAllowedChars)
	}
	if plan.MaxChars <= 0 {
		t.Fatalf("MaxChars = %d, want a positive budget even with small context", plan.MaxChars)
	}
}
