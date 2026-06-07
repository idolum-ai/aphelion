//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"errors"
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
