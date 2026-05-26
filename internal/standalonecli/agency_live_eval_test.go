//go:build linux

package standalonecli

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestLiveAgencySpectrumEvals(t *testing.T) {
	if os.Getenv("APHELION_LIVE_EVAL") != "1" {
		t.Skip("set APHELION_LIVE_EVAL=1 to run live OpenAI agency spectrum evals")
	}

	providers := loadLiveAgencyEvalProviders(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	report, err := runAgencyEval(ctx, providers.Subject, providers.Judge, agencyEvalRunOptions{
		Profile:    agencyEvalProfileFull,
		Variant:    agencyEvalVariantCompare,
		Model:      providers.Model,
		JudgeModel: providers.JudgeModel,
		Now:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("live agency eval: %v", err)
	}
	writeLiveAgencyEvalReportIfRequested(t, "agency", report)
	t.Logf("agency eval summary: current_avg=%.2f baseline_avg=%.2f hard_failures=%d improved=%d regressed=%d",
		agencyEvalVariantAverage(report.Results, agencyEvalVariantCurrent),
		agencyEvalVariantAverage(report.Results, agencyEvalVariantBaseline),
		report.Summary.HardFailureCount,
		report.Summary.CompareImproved,
		report.Summary.CompareRegressed,
	)
	if failures := agencyEvalVariantHardFailures(report.Results, agencyEvalVariantCurrent); failures > 0 {
		t.Fatalf("current prompt produced %d hard failure(s):\n%s", failures, mustAgencyEvalJSON(report))
	}
	currentAvg := agencyEvalVariantAverage(report.Results, agencyEvalVariantCurrent)
	baselineAvg := agencyEvalVariantAverage(report.Results, agencyEvalVariantBaseline)
	if currentAvg < 3.5 {
		t.Fatalf("current prompt target average %.2f below release floor 3.50:\n%s", currentAvg, mustAgencyEvalJSON(report))
	}
	if currentAvg+0.50 < baselineAvg {
		t.Fatalf("current prompt target average %.2f materially below baseline %.2f:\n%s", currentAvg, baselineAvg, mustAgencyEvalJSON(report))
	}
}

func agencyEvalVariantAverage(results []agencyEvalCaseResult, variant string) float64 {
	total := 0.0
	count := 0
	for _, result := range results {
		if result.Variant != variant {
			continue
		}
		total += result.TargetAverage
		count++
	}
	if count == 0 {
		return 0
	}
	return roundAgencyEvalFloat(total / float64(count))
}

func agencyEvalVariantHardFailures(results []agencyEvalCaseResult, variant string) int {
	count := 0
	for _, result := range results {
		if result.Variant == variant && agencyEvalHardFailureCount(result.HardFailures) > 0 {
			count++
		}
	}
	return count
}

func mustAgencyEvalJSON(report agencyEvalReport) string {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(raw)
}
