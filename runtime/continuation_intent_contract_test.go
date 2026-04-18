//go:build linux

package runtime

import (
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/session"
)

func TestParseContinuationIntentContractLines(t *testing.T) {
	t.Parallel()

	parsed, ok := parseContinuationIntentContract(strings.Join([]string{
		"INSPECT: no",
		"CONTINUATION_SCHEMA_VERSION: 1",
		"CONTINUATION_INTENT: continue",
		"CONTINUATION_RATIONALE: The next step is bounded and ready.",
		"CONTINUATION_NEXT_STEP: Run the next bounded command.",
		"CONTINUATION_CONSTRAINTS: Stay within workspace edits.",
		"CONTINUATION_CONFIDENCE: high",
		"CONTINUATION_RATIFIED: yes",
	}, "\n"))
	if !ok {
		t.Fatal("parseContinuationIntentContract(lines) ok = false, want true")
	}
	if parsed.Decision != session.ContinuationIntentDecisionContinue {
		t.Fatalf("decision = %q, want continue", parsed.Decision)
	}
	if parsed.Rationale == "" {
		t.Fatal("rationale empty, want parsed rationale")
	}
	if !parsed.Ratified {
		t.Fatal("ratified = false, want true")
	}
}

func TestParseContinuationIntentContractJSON(t *testing.T) {
	t.Parallel()

	parsed, ok := parseContinuationIntentContract(`{
		"continuation": {
			"schema_version": 1,
			"intent": "hold",
			"rationale": "Waiting for user direction.",
			"next_step": "Ask one clarifying question.",
			"confidence": "medium",
			"ratified": false
		}
	}`)
	if !ok {
		t.Fatal("parseContinuationIntentContract(json) ok = false, want true")
	}
	if parsed.Decision != session.ContinuationIntentDecisionHold {
		t.Fatalf("decision = %q, want hold", parsed.Decision)
	}
	if parsed.Rationale != "Waiting for user direction." {
		t.Fatalf("rationale = %q, want parsed rationale", parsed.Rationale)
	}
}

func TestParseContinuationIntentContractRejectsMissingSchemaVersion(t *testing.T) {
	t.Parallel()

	if _, ok := parseContinuationIntentContract(strings.Join([]string{
		"CONTINUATION_INTENT: continue",
		"CONTINUATION_RATIONALE: continue because it is safe and bounded.",
	}, "\n")); ok {
		t.Fatal("parseContinuationIntentContract() ok = true, want false without schema version")
	}
}
