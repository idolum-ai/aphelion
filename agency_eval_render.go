//go:build linux

package main

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/face"
)

func renderAgencyEvalHuman(report agencyEvalReport) string {
	state := "passed"
	hardFailuresForState := report.Summary.HardFailureCount
	if report.Variant == agencyEvalVariantCompare {
		hardFailuresForState = report.Summary.CurrentHardFailures
	}
	if hardFailuresForState > 0 || report.Summary.CompareRegressed > 0 {
		state = "needs review"
	}
	details := []string{
		fmt.Sprintf("Profile: %s", report.Profile),
		fmt.Sprintf("Variant: %s", report.Variant),
		fmt.Sprintf("Results: %d result(s) across %d case(s)", report.Summary.ResultCount, report.Summary.CaseCount),
		fmt.Sprintf("Target average: %.2f", report.Summary.TargetAverageScore),
		fmt.Sprintf("Hard failures: %d", report.Summary.HardFailureCount),
	}
	if report.Variant == agencyEvalVariantCompare {
		details = append(details,
			fmt.Sprintf("Compare: improved=%d regressed=%d", report.Summary.CompareImproved, report.Summary.CompareRegressed),
			fmt.Sprintf("Current hard failures: %d", report.Summary.CurrentHardFailures),
			fmt.Sprintf("Baseline hard failures: %d", report.Summary.BaselineHardFailures),
		)
	}
	evidence := []string{
		"Model: " + firstAgencyEvalNonEmpty(report.Model, "unknown"),
		"Judge model: " + firstAgencyEvalNonEmpty(report.JudgeModel, "unknown"),
		"Generated: " + firstAgencyEvalNonEmpty(report.GeneratedAt, "unknown"),
	}
	if len(report.Comparisons) > 0 {
		for _, comparison := range report.Comparisons {
			evidence = append(evidence, fmt.Sprintf("%s delta=%.2f hard_failure_delta=%d", comparison.CaseID, comparison.TargetDelta, comparison.HardFailureDelta))
		}
	}
	return face.RenderOperatorPanel(face.OperatorPanel{
		Title:    "Agency Eval",
		State:    state,
		Why:      "This is local behavioral evidence for agency prompt quality; it is not runtime authority.",
		Next:     "Review JSON output for line scores and hard failures before prompt releases.",
		Details:  details,
		Evidence: evidence,
	})
}

func renderAgencyEvalKV(report agencyEvalReport) string {
	lines := []string{
		"generated_at: " + report.GeneratedAt,
		"profile: " + report.Profile,
		"variant: " + report.Variant,
		"model: " + firstAgencyEvalNonEmpty(report.Model, "unknown"),
		"judge_model: " + firstAgencyEvalNonEmpty(report.JudgeModel, "unknown"),
		fmt.Sprintf("case_count: %d", report.Summary.CaseCount),
		fmt.Sprintf("result_count: %d", report.Summary.ResultCount),
		fmt.Sprintf("hard_failure_count: %d", report.Summary.HardFailureCount),
		fmt.Sprintf("target_average_score: %.2f", report.Summary.TargetAverageScore),
	}
	if report.Variant == agencyEvalVariantCompare {
		lines = append(lines,
			fmt.Sprintf("compare_improved: %d", report.Summary.CompareImproved),
			fmt.Sprintf("compare_regressed: %d", report.Summary.CompareRegressed),
			fmt.Sprintf("current_hard_failures: %d", report.Summary.CurrentHardFailures),
			fmt.Sprintf("baseline_hard_failures: %d", report.Summary.BaselineHardFailures),
		)
	}
	for _, line := range agencyEvalLines {
		lines = append(lines, fmt.Sprintf("line_%s_average: %.2f", line, report.Summary.LineAverages[line]))
	}
	for _, comparison := range report.Comparisons {
		lines = append(lines, fmt.Sprintf("comparison_%s_delta: %.2f", comparison.CaseID, comparison.TargetDelta))
		lines = append(lines, fmt.Sprintf("comparison_%s_hard_failure_delta: %d", comparison.CaseID, comparison.HardFailureDelta))
	}
	return strings.Join(lines, "\n") + "\n"
}
