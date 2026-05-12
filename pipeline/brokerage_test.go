//go:build linux

package pipeline

import (
	"strings"
	"testing"
)

func TestParseExecutionContractExtractsFlags(t *testing.T) {
	t.Parallel()

	contract := ParseExecutionContract(strings.Join([]string{
		"INSPECT: yes",
		"QUESTION: no",
		"ANSWER: yes",
	}, "\n"))
	if contract == nil {
		t.Fatal("contract = nil, want parsed execution contract")
	}
	if !contract.NeedsInspection || contract.NeedsQuestion || !contract.MayAnswerNow {
		t.Fatalf("contract = %#v, want inspect=yes question=no answer=yes", contract)
	}
}

func TestParseExecutionContractRejectsRemovedModeDirectives(t *testing.T) {
	t.Parallel()

	if got := ParseExecutionContract("MODE: ask_then_wait"); got != nil {
		t.Fatalf("contract = %#v, want nil for removed mode directive", got)
	}
}

func TestParseExecutionContractParsesJSON(t *testing.T) {
	t.Parallel()

	got := ParseExecutionContract(`{"inspect": true, "question": false, "answer": true}`)
	if got == nil {
		t.Fatal("contract = nil, want parsed execution contract")
	}
	if !got.NeedsInspection || got.NeedsQuestion || !got.MayAnswerNow {
		t.Fatalf("contract = %#v, want inspect=yes question=no answer=yes", got)
	}
}

func TestParseExecutionContractReturnsNilForIncompleteContract(t *testing.T) {
	t.Parallel()

	if got := ParseExecutionContract("INSPECT: yes\nQUESTION: no"); got != nil {
		t.Fatal("contract != nil, want nil for incomplete directives")
	}
}

func TestParseBrokerageRatificationParsesStructuredFields(t *testing.T) {
	t.Parallel()

	parsed, err := ParseBrokerageRatification(strings.Join([]string{
		"INSPECT: yes",
		"QUESTION: no",
		"ANSWER: yes",
		"RATIFICATION: adapt",
		"SIGNAL_JUDGMENT: confirmed",
		"PLAN:",
		"1. Inspect the repo first.",
		"2. Reply with prioritized ideas.",
	}, "\n"))
	if err != nil {
		t.Fatalf("ParseBrokerageRatification() err = %v", err)
	}
	if !parsed.RatifiedContract.NeedsInspection || parsed.RatifiedContract.NeedsQuestion || !parsed.RatifiedContract.MayAnswerNow {
		t.Fatalf("RatifiedExecutionContract = %#v, want inspect=yes question=no answer=yes", parsed.RatifiedContract)
	}
	if parsed.Disposition != RatificationAdapt {
		t.Fatalf("Disposition = %q, want %q", parsed.Disposition, RatificationAdapt)
	}
	if parsed.SignalJudgment != SignalJudgmentConfirmed {
		t.Fatalf("SignalJudgment = %q, want %q", parsed.SignalJudgment, SignalJudgmentConfirmed)
	}
	if len(parsed.RatifiedSteps) != 2 {
		t.Fatalf("len(RatifiedSteps) = %d, want 2", len(parsed.RatifiedSteps))
	}
	if parsed.RatifiedSteps[0] != "Inspect the repo first." {
		t.Fatalf("first step = %q, want parsed first step", parsed.RatifiedSteps[0])
	}
}

func TestParseBrokerageRatificationRejectsMissingFields(t *testing.T) {
	t.Parallel()

	if _, err := ParseBrokerageRatification("INSPECT: yes\nQUESTION: no\nANSWER: yes\nPLAN:\n- Inspect the repo first."); err == nil {
		t.Fatal("ParseBrokerageRatification() err = nil, want missing disposition error")
	}
	if _, err := ParseBrokerageRatification("RATIFICATION: adapt\nPLAN:\n- Inspect the repo first."); err == nil {
		t.Fatal("ParseBrokerageRatification() err = nil, want missing execution contract error")
	}
	if _, err := ParseBrokerageRatification("INSPECT: yes\nQUESTION: no\nANSWER: yes\nRATIFICATION: adapt"); err == nil {
		t.Fatal("ParseBrokerageRatification() err = nil, want missing plan steps error")
	}
}

func TestParseBrokerageRatificationParsesJSONPayload(t *testing.T) {
	t.Parallel()

	parsed, err := ParseBrokerageRatification(`{
		"contract": {"inspect": true, "question": false, "answer": true},
		"ratification": "accept",
		"signal_judgment": "confirmed",
		"plan": ["Inspect the repo first.", "Reply with prioritized ideas."]
	}`)
	if err != nil {
		t.Fatalf("ParseBrokerageRatification() err = %v", err)
	}
	if !parsed.RatifiedContract.NeedsInspection || parsed.RatifiedContract.NeedsQuestion || !parsed.RatifiedContract.MayAnswerNow {
		t.Fatalf("RatifiedContract = %#v, want inspect=yes question=no answer=yes", parsed.RatifiedContract)
	}
	if parsed.Disposition != RatificationAccept {
		t.Fatalf("Disposition = %q, want %q", parsed.Disposition, RatificationAccept)
	}
	if parsed.SignalJudgment != SignalJudgmentConfirmed {
		t.Fatalf("SignalJudgment = %q, want %q", parsed.SignalJudgment, SignalJudgmentConfirmed)
	}
	if len(parsed.RatifiedSteps) != 2 {
		t.Fatalf("len(RatifiedSteps) = %d, want 2", len(parsed.RatifiedSteps))
	}
}
