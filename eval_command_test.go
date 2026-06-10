//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aphruntime "github.com/idolum-ai/aphelion/runtime"
)

func TestEvalListCommandRendersJSON(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := runEvalCommandWithDeps([]string{"list", "--suite", "canonical", "--format", "json"}, &out); err != nil {
		t.Fatalf("eval list err = %v", err)
	}
	var decoded struct {
		Suite     string                        `json:"suite"`
		Scenarios []aphruntime.EvalScenarioInfo `json:"scenarios"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("decode eval list JSON: %v\n%s", err, out.String())
	}
	if decoded.Suite != "canonical" || len(decoded.Scenarios) != 12 {
		t.Fatalf("decoded list = %#v", decoded)
	}
	if decoded.Scenarios[0].ID == "" || decoded.Scenarios[0].Domain == "" {
		t.Fatalf("scenario missing stable fields: %#v", decoded.Scenarios[0])
	}
}

func TestEvalListCommandSupportsTrajectorySuite(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := runEvalCommandWithDeps([]string{"list", "--suite", "trajectory", "--format", "json"}, &out); err != nil {
		t.Fatalf("eval list trajectory err = %v", err)
	}
	var decoded struct {
		Suite     string                        `json:"suite"`
		Scenarios []aphruntime.EvalScenarioInfo `json:"scenarios"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("decode trajectory list JSON: %v\n%s", err, out.String())
	}
	if decoded.Suite != "trajectory" || len(decoded.Scenarios) != 13 {
		t.Fatalf("decoded trajectory list = %#v", decoded)
	}
}

func TestEvalRunCommandLocalRendersJSON(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := runEvalCommandWithDeps([]string{"run", "--suite", "canonical", "--mode", "local", "--rollouts", "1", "--format", "json"}, &out)
	if err != nil {
		t.Fatalf("eval run err = %v\n%s", err, out.String())
	}
	var report aphruntime.EvalReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode eval report JSON: %v\n%s", err, out.String())
	}
	if report.Failed || report.HardFailureCount != 0 || report.ResultCount != 12 {
		t.Fatalf("report = %#v", report)
	}
}

func TestEvalRunCommandSupportsJobsFlag(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := runEvalCommandWithDeps([]string{"run", "--suite", "canonical", "--mode", "local", "--scenario", "token_budget_recovery_no_dead_end", "--rollouts", "2", "--jobs", "2", "--format", "json"}, &out)
	if err != nil {
		t.Fatalf("eval run err = %v\n%s", err, out.String())
	}
	var report aphruntime.EvalReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode eval report JSON: %v\n%s", err, out.String())
	}
	if report.Jobs != 2 || report.ResultCount != 2 {
		t.Fatalf("report jobs/results = %d/%d, want 2/2", report.Jobs, report.ResultCount)
	}
	for i, result := range report.Results {
		if result.SampleIndex != i {
			t.Fatalf("result[%d] sample = %d, want stable sample order", i, result.SampleIndex)
		}
	}
}

func TestEvalRunCommandRejectsInvalidJobsFlag(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := runEvalCommandWithDeps([]string{"run", "--suite", "canonical", "--mode", "local", "--jobs", "0", "--format", "json"}, &out)
	if err == nil || !strings.Contains(err.Error(), "--jobs >= 1") {
		t.Fatalf("eval run err = %v, want invalid jobs error", err)
	}
}

func TestEvalRunCommandSupportsGovernorSubjectAndScenarioFilter(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := runEvalCommandWithDeps([]string{"run", "--suite", "canonical", "--mode", "local", "--subject", "governor", "--scenario", "token_budget_recovery_no_dead_end", "--rollouts", "1", "--format", "json"}, &out)
	if err != nil {
		t.Fatalf("eval run err = %v\n%s", err, out.String())
	}
	var report aphruntime.EvalReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode eval report JSON: %v\n%s", err, out.String())
	}
	if report.SubjectMode != aphruntime.EvalSubjectGovernor || report.ResultCount != 1 {
		t.Fatalf("report subject/results = %s/%d", report.SubjectMode, report.ResultCount)
	}
	if got := report.Results[0].ScenarioID; got != "token_budget_recovery_no_dead_end" {
		t.Fatalf("scenario = %s", got)
	}
	if !strings.HasPrefix(report.Results[0].PromptHash, "sha256:") {
		t.Fatalf("prompt hash = %q", report.Results[0].PromptHash)
	}
}

func TestEvalRunCommandSupportsLocalJudgeScoring(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := runEvalCommandWithDeps([]string{"run", "--suite", "canonical", "--mode", "local", "--subject", "governor", "--scenario", "token_budget_recovery_no_dead_end", "--rollouts", "1", "--scoring", "judge", "--trace", "redacted", "--format", "json"}, &out)
	if err != nil {
		t.Fatalf("eval run err = %v\n%s", err, out.String())
	}
	var report aphruntime.EvalReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode eval report JSON: %v\n%s", err, out.String())
	}
	if report.ScoringMode != aphruntime.EvalScoringJudge || report.JudgeRouteCount != 2 || report.AmbiguousCount != 0 {
		t.Fatalf("judge report = %#v", report)
	}
	if len(report.Results) != 1 || len(report.Results[0].JudgeResults) != 2 || report.Results[0].CandidateTrace == "" {
		t.Fatalf("judge result = %#v", report.Results)
	}
}

func TestEvalLocalModeDoesNotRequireConfigOrRoutes(t *testing.T) {
	t.Parallel()

	routes, err := evalRoutesForCommand(aphruntime.EvalModeLocal, "configured", "/path/that/does/not/exist.toml")
	if err != nil {
		t.Fatalf("local eval routes err = %v", err)
	}
	if len(routes) != 0 {
		t.Fatalf("local eval routes = %#v, want command to defer to runtime default", routes)
	}
}

func TestEvalGateCommandRendersMarkdown(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	beforePath := filepath.Join(dir, "before.json")
	afterPath := filepath.Join(dir, "after.json")
	before := evalCommandGateReportFixture(1, 0, "baseline failure")
	after := evalCommandGateReportFixture(0, 0, "")
	writeEvalReportFixture(t, beforePath, before)
	writeEvalReportFixture(t, afterPath, after)

	var out bytes.Buffer
	if err := runEvalCommandWithDeps([]string{"gate", "--before", beforePath, "--after", afterPath, "--format", "markdown"}, &out); err != nil {
		t.Fatalf("eval gate err = %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Eval Stability Gate: pass") || !strings.Contains(out.String(), "Scenario Deltas") {
		t.Fatalf("gate output missing expected content:\n%s", out.String())
	}
}

func TestEvalReportFailureReturnsCommandError(t *testing.T) {
	t.Parallel()

	err := evalReportFailureError(aphruntime.EvalReport{Failed: true, HardFailureCount: 2})
	var failure evalCommandFailure
	if !errors.As(err, &failure) || !strings.Contains(err.Error(), "2 hard failure") {
		t.Fatalf("failure err = %v", err)
	}
	if err := evalReportFailureError(aphruntime.EvalReport{}); err != nil {
		t.Fatalf("passing report err = %v", err)
	}
}

func TestEvalCompareCommandRendersMarkdown(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	beforePath := filepath.Join(dir, "before.json")
	afterPath := filepath.Join(dir, "after.json")
	before := aphruntime.EvalReport{
		Suite:            aphruntime.EvalSuiteCanonical,
		Mode:             aphruntime.EvalModeLive,
		SubjectMode:      aphruntime.EvalSubjectGovernor,
		ScenarioRevision: aphruntime.EvalScenarioRevision,
		ResultCount:      1,
		HardFailureCount: 1,
		HardFailureRate:  1,
		Results: []aphruntime.EvalScenarioResult{{
			ScenarioID:       "token_budget_recovery_no_dead_end",
			HardFailures:     []aphruntime.EvalFinding{{Class: "completed_after_budget_recovery"}},
			CandidatePreview: "completed",
		}},
	}
	after := before
	after.HardFailureCount = 0
	after.HardFailureRate = 0
	after.Results = []aphruntime.EvalScenarioResult{{ScenarioID: "token_budget_recovery_no_dead_end", Pass: true}}
	writeEvalReportFixture(t, beforePath, before)
	writeEvalReportFixture(t, afterPath, after)

	var out bytes.Buffer
	if err := runEvalCommandWithDeps([]string{"compare", "--before", beforePath, "--after", afterPath, "--format", "markdown"}, &out); err != nil {
		t.Fatalf("eval compare err = %v", err)
	}
	if !strings.Contains(out.String(), "Measured Impact") || !strings.Contains(out.String(), "token_budget_recovery_no_dead_end") {
		t.Fatalf("compare output missing expected content:\n%s", out.String())
	}
}

func writeEvalReportFixture(t *testing.T, path string, report aphruntime.EvalReport) {
	t.Helper()
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write report: %v", err)
	}
}

func evalCommandGateReportFixture(hardFailures int, providerFailures int, trace string) aphruntime.EvalReport {
	result := aphruntime.EvalScenarioResult{
		ScenarioID:       "token_budget_recovery_no_dead_end",
		ScenarioName:     "Token budget recovery keeps work incomplete",
		ScenarioRevision: aphruntime.EvalScenarioRevision,
		Domain:           "budget_recovery",
		AuthorityClass:   "commit",
		TransportSurface: "telegram_dm",
		Route:            "openai:gpt-5.5",
		Provider:         "openai",
		Model:            "gpt-5.5",
		SubjectMode:      aphruntime.EvalSubjectGovernor,
		Pass:             hardFailures == 0 && providerFailures == 0,
		CandidateTrace:   trace,
		CandidatePreview: trace,
	}
	for i := 0; i < hardFailures; i++ {
		result.HardFailures = append(result.HardFailures, aphruntime.EvalFinding{Class: "forbidden_claim", Reason: "fixture"})
	}
	if providerFailures > 0 {
		result.ProviderFailure = true
	}
	report := aphruntime.EvalReport{
		Suite:                aphruntime.EvalSuiteCanonical,
		Mode:                 aphruntime.EvalModeLive,
		SubjectMode:          aphruntime.EvalSubjectGovernor,
		ScenarioRevision:     aphruntime.EvalScenarioRevision,
		ScoringMode:          aphruntime.EvalScoringJudge,
		JudgeQuorum:          aphruntime.EvalJudgeQuorumPair,
		TraceMode:            aphruntime.EvalTraceRedacted,
		Rollouts:             1,
		RouteCount:           1,
		JudgeRouteCount:      2,
		ScenarioCount:        1,
		ResultCount:          1,
		HardFailureCount:     hardFailures,
		ProviderFailureCount: providerFailures,
		HardFailureRate:      float64(hardFailures),
		Failed:               hardFailures > 0,
		Results:              []aphruntime.EvalScenarioResult{result},
	}
	return report
}
