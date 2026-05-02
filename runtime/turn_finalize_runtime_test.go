//go:build linux

package runtime

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/turn"
)

func TestTurnDeliveryPortPostCommitHooksReceiveTurnResult(t *testing.T) {
	t.Parallel()

	var got []*turn.Result
	port := &turnDeliveryPort{
		runtime: &Runtime{},
		hooks: turnCommitHooks{
			QueueReviewEvents: func(result *turn.Result) error {
				got = append(got, result)
				return nil
			},
			DeliverReviewEvents: func(result *turn.Result) error {
				got = append(got, result)
				return nil
			},
			QueueDurableArtifact: func(result *turn.Result) error {
				got = append(got, result)
				return nil
			},
			PostReplyContinuationUI: func(_ context.Context, result *turn.Result) error {
				got = append(got, result)
				return nil
			},
		},
	}

	result := &turn.Result{VisibleReply: "rendered scene"}
	_, err := port.Deliver(context.Background(), turn.DeliveryRequest{
		Message: core.OutboundMessage{ChatID: 1, Text: "rendered scene"},
		Result:  result,
	})
	if err != nil {
		t.Fatalf("Deliver() err = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("hook calls = %d, want 4", len(got))
	}
	if !reflect.DeepEqual(got, []*turn.Result{result, result, result, result}) {
		t.Fatalf("hook results = %#v, want all hooks to receive same turn result", got)
	}
}

func TestEnforceVisibleRecurrenceContractAppendsSpecificNote(t *testing.T) {
	t.Parallel()

	got := enforceVisibleRecurrenceContract("Here is the plan.", prompt.RuntimeAwareness{
		HiddenInputsActive:    true,
		HiddenInputCategories: []string{hiddenInputSemanticRecurrence},
		ProvenanceSummary:     "Prior Lighthouse thread about Proton Bridge inbox testing.",
	})

	if !strings.Contains(got, "Here is the plan.") || !strings.Contains(got, "Continuity note: This resembles Prior Lighthouse thread") {
		t.Fatalf("reply = %q, want appended continuity note", got)
	}
}

func TestEnforceVisibleRecurrenceContractSanitizesInternalProvenance(t *testing.T) {
	t.Parallel()

	got := enforceVisibleRecurrenceContract("Here is the plan.", prompt.RuntimeAwareness{
		HiddenInputsActive:    true,
		HiddenInputCategories: []string{hiddenInputSemanticRecurrence},
		ProvenanceSummary:     "related prior material in memory/decisions.md is surfacing again; an open question overlaps with this turn",
	})

	if strings.Contains(got, "memory/decisions.md") || strings.Contains(got, "related prior material") {
		t.Fatalf("reply = %q, want sanitized recurrence note without raw provenance", got)
	}
	if !strings.Contains(got, "can't identify it cleanly") {
		t.Fatalf("reply = %q, want generic unclear-prior note", got)
	}
}

func TestEnforceVisibleRecurrenceContractDoesNotDuplicatePriorThreadMention(t *testing.T) {
	t.Parallel()

	reply := "This matches the prior thread about Lighthouse."
	got := enforceVisibleRecurrenceContract(reply, prompt.RuntimeAwareness{
		HiddenInputsActive:    true,
		HiddenInputCategories: []string{hiddenInputUnresolvedMemory},
		ProvenanceSummary:     "Prior Lighthouse thread.",
	})

	if got != reply {
		t.Fatalf("reply = %q, want unchanged %q", got, reply)
	}
}
