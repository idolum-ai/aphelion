//go:build linux

package turn

import (
	"context"
	"reflect"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
)

func TestRunConstitutionStageReturnsTrimmedReplyWithoutValidator(t *testing.T) {
	t.Parallel()

	got := RunConstitutionStage(context.Background(), ConstitutionStageInput{
		ReplyText: "  hello  ",
	}, ConstitutionStageCallbacks{})
	if got != "hello" {
		t.Fatalf("RunConstitutionStage() = %q, want hello", got)
	}
}

func TestRunConstitutionStageNoViolationsNoRepair(t *testing.T) {
	t.Parallel()

	repairCalled := false
	got := RunConstitutionStage(context.Background(), ConstitutionStageInput{
		ReplyText: "reply",
	}, ConstitutionStageCallbacks{
		Validate: func(string, []core.Media) []pipeline.ConstitutionViolation {
			return nil
		},
		Repair: func(context.Context, string, []core.Media, []pipeline.ConstitutionViolation) (string, bool) {
			repairCalled = true
			return "unused", true
		},
	})
	if got != "reply" {
		t.Fatalf("RunConstitutionStage() = %q, want reply", got)
	}
	if repairCalled {
		t.Fatal("repair callback called for non-violating reply")
	}
}

func TestRunConstitutionStageViolationWithoutRepairReturnsOriginal(t *testing.T) {
	t.Parallel()

	records := 0
	got := RunConstitutionStage(context.Background(), ConstitutionStageInput{
		ReplyText: "reply",
		Media:     []core.Media{{Type: "image"}},
	}, ConstitutionStageCallbacks{
		Validate: func(string, []core.Media) []pipeline.ConstitutionViolation {
			return []pipeline.ConstitutionViolation{{Rule: pipeline.RuleMediaNeedsNarration}}
		},
		RecordViolations: func([]pipeline.ConstitutionViolation) {
			records++
		},
	})
	if got != "reply" {
		t.Fatalf("RunConstitutionStage() = %q, want reply", got)
	}
	if records != 1 {
		t.Fatalf("RecordViolations calls = %d, want 1", records)
	}
}

func TestRunConstitutionStageRepairSuccessReturnsRepaired(t *testing.T) {
	t.Parallel()

	calls := 0
	got := RunConstitutionStage(context.Background(), ConstitutionStageInput{
		ReplyText: "reply",
		Media:     []core.Media{{Type: "image"}},
	}, ConstitutionStageCallbacks{
		Validate: func(replyText string, media []core.Media) []pipeline.ConstitutionViolation {
			calls++
			if calls == 1 {
				return []pipeline.ConstitutionViolation{{Rule: pipeline.RuleMediaNeedsNarration}}
			}
			if replyText == "repaired reply" {
				return nil
			}
			t.Fatalf("unexpected reply text passed to second validation: %q", replyText)
			return nil
		},
		Repair: func(context.Context, string, []core.Media, []pipeline.ConstitutionViolation) (string, bool) {
			return "repaired reply", true
		},
	})
	if got != "repaired reply" {
		t.Fatalf("RunConstitutionStage() = %q, want repaired reply", got)
	}
	if calls != 2 {
		t.Fatalf("Validate calls = %d, want 2", calls)
	}
}

func TestRunConstitutionStageRepairStillViolatingFallsBackToOriginal(t *testing.T) {
	t.Parallel()

	var recorded [][]pipeline.ConstitutionViolation
	got := RunConstitutionStage(context.Background(), ConstitutionStageInput{
		ReplyText: "reply",
		Media:     []core.Media{{Type: "image"}},
	}, ConstitutionStageCallbacks{
		Validate: func(replyText string, media []core.Media) []pipeline.ConstitutionViolation {
			if replyText == "reply" {
				return []pipeline.ConstitutionViolation{{Rule: pipeline.RuleMediaNeedsNarration}}
			}
			return []pipeline.ConstitutionViolation{{Rule: pipeline.RuleMediaReplyContradiction}}
		},
		Repair: func(context.Context, string, []core.Media, []pipeline.ConstitutionViolation) (string, bool) {
			return "still bad", true
		},
		RecordViolations: func(v []pipeline.ConstitutionViolation) {
			recorded = append(recorded, append([]pipeline.ConstitutionViolation(nil), v...))
		},
	})
	if got != "reply" {
		t.Fatalf("RunConstitutionStage() = %q, want original reply", got)
	}
	if len(recorded) != 2 {
		t.Fatalf("RecordViolations calls = %d, want 2", len(recorded))
	}
	if !reflect.DeepEqual(recorded[0], []pipeline.ConstitutionViolation{{Rule: pipeline.RuleMediaNeedsNarration}}) {
		t.Fatalf("first recorded violations = %#v", recorded[0])
	}
	if !reflect.DeepEqual(recorded[1], []pipeline.ConstitutionViolation{{Rule: pipeline.RuleMediaReplyContradiction}}) {
		t.Fatalf("second recorded violations = %#v", recorded[1])
	}
}

func TestRunConstitutionStageRepairFailureReturnsOriginal(t *testing.T) {
	t.Parallel()

	got := RunConstitutionStage(context.Background(), ConstitutionStageInput{
		ReplyText: "reply",
	}, ConstitutionStageCallbacks{
		Validate: func(string, []core.Media) []pipeline.ConstitutionViolation {
			return []pipeline.ConstitutionViolation{{Rule: pipeline.RuleFinalGovernorLeakage}}
		},
		Repair: func(context.Context, string, []core.Media, []pipeline.ConstitutionViolation) (string, bool) {
			return "", false
		},
	})
	if got != "reply" {
		t.Fatalf("RunConstitutionStage() = %q, want original reply", got)
	}
}
