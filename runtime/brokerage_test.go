//go:build linux

package runtime

import "testing"

func TestParseBrokerageRatificationParsesStructuredFields(t *testing.T) {
	t.Parallel()

	parsed, err := parseBrokerageRatification("INSPECT: yes\nQUESTION: no\nANSWER: yes\nRATIFICATION: adapt\nSIGNAL_JUDGMENT: confirmed\nPLAN:\n1. Inspect the repo first.\n2. Reply with prioritized ideas.")
	if err != nil {
		t.Fatalf("parseBrokerageRatification() err = %v", err)
	}
	if parsed.RatifiedExecutionContract == nil {
		t.Fatal("RatifiedExecutionContract = nil, want parsed execution contract")
	}
	if !parsed.RatifiedExecutionContract.NeedsInspection || parsed.RatifiedExecutionContract.NeedsQuestion || !parsed.RatifiedExecutionContract.MayAnswerNow {
		t.Fatalf("RatifiedExecutionContract = %#v, want inspect=yes question=no answer=yes", parsed.RatifiedExecutionContract)
	}
	if parsed.Ratification != "adapt" {
		t.Fatalf("Ratification = %q, want adapt", parsed.Ratification)
	}
	if parsed.SignalJudgment != "confirmed" {
		t.Fatalf("SignalJudgment = %q, want confirmed", parsed.SignalJudgment)
	}
	if len(parsed.RatifiedSteps) != 2 {
		t.Fatalf("RatifiedSteps len = %d, want 2", len(parsed.RatifiedSteps))
	}
	if parsed.RatifiedSteps[0] != "Inspect the repo first." {
		t.Fatalf("first step = %q, want parsed first step", parsed.RatifiedSteps[0])
	}
}

func TestParseBrokerageRatificationRejectsMissingFields(t *testing.T) {
	t.Parallel()

	if _, err := parseBrokerageRatification("INSPECT: yes\nQUESTION: no\nANSWER: yes\nPLAN:\n- Inspect the repo first."); err == nil {
		t.Fatal("parseBrokerageRatification() err = nil, want missing disposition error")
	}
	if _, err := parseBrokerageRatification("RATIFICATION: adapt\nPLAN:\n- Inspect the repo first."); err == nil {
		t.Fatal("parseBrokerageRatification() err = nil, want missing execution contract error")
	}
	if _, err := parseBrokerageRatification("INSPECT: yes\nQUESTION: no\nANSWER: yes\nRATIFICATION: adapt"); err == nil {
		t.Fatal("parseBrokerageRatification() err = nil, want missing plan steps error")
	}
}
