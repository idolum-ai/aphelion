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
