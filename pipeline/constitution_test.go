//go:build linux

package pipeline

import (
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/prompt"
)

func TestValidateProgressTextFlagsGovernorRelationshipLeakage(t *testing.T) {
	t.Parallel()

	violations := ValidateProgressText("I deferred this to Aphelion while I worked.")
	if len(violations) != 1 {
		t.Fatalf("violations len = %d, want 1", len(violations))
	}
	if violations[0].Rule != RuleProgressGovernorLeakage {
		t.Fatalf("rule = %q, want %q", violations[0].Rule, RuleProgressGovernorLeakage)
	}
	if violations[0].Surface != "progress" {
		t.Fatalf("surface = %q, want progress", violations[0].Surface)
	}
}

func TestValidateFinalReplyFlagsMediaContradiction(t *testing.T) {
	t.Parallel()

	violations := ValidateFinalReply("I can't produce that right now.", []core.Media{{Type: "image"}})
	if !containsViolationRule(violations, RuleMediaReplyContradiction) {
		t.Fatalf("violations = %#v, want %q", violations, RuleMediaReplyContradiction)
	}
}

func TestValidateFinalReplyFlagsMissingNarrationForMedia(t *testing.T) {
	t.Parallel()

	violations := ValidateFinalReply("   ", []core.Media{{Type: "image"}})
	if !containsViolationRule(violations, RuleMediaNeedsNarration) {
		t.Fatalf("violations = %#v, want %q", violations, RuleMediaNeedsNarration)
	}
}

func TestBuildRepairNotesUsesDetailThenRuleFallback(t *testing.T) {
	t.Parallel()

	got := BuildRepairNotes([]ConstitutionViolation{
		{Rule: RuleFinalGovernorLeakage, Detail: "user-visible boundary leakage"},
		{Rule: RuleMediaNeedsNarration, Detail: "   "},
	})
	if len(got) != 2 {
		t.Fatalf("notes len = %d, want 2", len(got))
	}
	if got[0] != "user-visible boundary leakage" {
		t.Fatalf("notes[0] = %q, want detail", got[0])
	}
	if got[1] != RuleMediaNeedsNarration {
		t.Fatalf("notes[1] = %q, want %q", got[1], RuleMediaNeedsNarration)
	}
}

func TestBuildRepairContractShapesRepairModeAndNotes(t *testing.T) {
	t.Parallel()

	contract, ok := BuildRepairContract(RepairContract{
		Channel:       " telegram ",
		PrincipalRole: " admin ",
		UserText:      " user ask ",
		Candidate:     " draft reply ",
		FloorText:     " floor text ",
		Material: FloorMaterial{
			Packet: core.MaterialPacket{
				Facts: []string{"fact"},
			},
		},
		Runtime: prompt.RuntimeAwareness{
			DeliveryMode: "idolum_render",
			StreamReply:  true,
		},
		Violations: []string{"should_be_replaced"},
		MediaCount: 1,
	}, []ConstitutionViolation{
		{Rule: RuleFinalGovernorLeakage, Detail: "keep one voice"},
	})
	if !ok {
		t.Fatal("BuildRepairContract() ok = false, want true")
	}
	if contract.Channel != "telegram" {
		t.Fatalf("Channel = %q, want telegram", contract.Channel)
	}
	if contract.PrincipalRole != "admin" {
		t.Fatalf("PrincipalRole = %q, want admin", contract.PrincipalRole)
	}
	if contract.UserText != "user ask" {
		t.Fatalf("UserText = %q, want trimmed value", contract.UserText)
	}
	if contract.Candidate != "draft reply" {
		t.Fatalf("Candidate = %q, want trimmed value", contract.Candidate)
	}
	if contract.FloorText != "floor text" {
		t.Fatalf("FloorText = %q, want trimmed value", contract.FloorText)
	}
	if contract.Runtime.DeliveryMode != "constitution_repair" {
		t.Fatalf("Runtime.DeliveryMode = %q, want constitution_repair", contract.Runtime.DeliveryMode)
	}
	if contract.Runtime.StreamReply {
		t.Fatal("Runtime.StreamReply = true, want false")
	}
	if !contract.Runtime.MediaAttached {
		t.Fatal("Runtime.MediaAttached = false, want true")
	}
	if contract.Runtime.MediaMode != "attachments" {
		t.Fatalf("Runtime.MediaMode = %q, want attachments", contract.Runtime.MediaMode)
	}
	if len(contract.Violations) != 1 || contract.Violations[0] != "keep one voice" {
		t.Fatalf("Violations = %#v, want repair note detail", contract.Violations)
	}
}

func TestBuildRepairContractReturnsFalseWhenNoNotes(t *testing.T) {
	t.Parallel()

	_, ok := BuildRepairContract(RepairContract{}, nil)
	if ok {
		t.Fatal("BuildRepairContract() ok = true, want false")
	}
}

func containsViolationRule(violations []ConstitutionViolation, want string) bool {
	for _, violation := range violations {
		if strings.TrimSpace(violation.Rule) == strings.TrimSpace(want) {
			return true
		}
	}
	return false
}
