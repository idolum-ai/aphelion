//go:build linux

package turn

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/idolum-ai/aphelion/core"
)

type brokerageTestState struct {
	note         string
	phase        string
	ratification string
}

func TestConvergeBrokerageReturnsEarlyWhenNotBrokerage(t *testing.T) {
	t.Parallel()

	state, usage := ConvergeBrokerage(context.Background(), BrokerageConvergeInput[brokerageTestState]{
		Initial: brokerageTestState{
			note:  "note",
			phase: "proposal",
		},
		Note:  func(s brokerageTestState) string { return s.note },
		Phase: func(s brokerageTestState) string { return s.phase },
		Ratify: func(context.Context, int, brokerageTestState) (brokerageTestState, core.TokenUsage, error) {
			t.Fatal("ratify should not run")
			return brokerageTestState{}, core.TokenUsage{}, nil
		},
	})
	if state.phase != "proposal" {
		t.Fatalf("phase = %q, want proposal", state.phase)
	}
	if usage != (core.TokenUsage{}) {
		t.Fatalf("usage = %#v, want zero", usage)
	}
}

func TestConvergeBrokerageAcceptsAfterRevision(t *testing.T) {
	t.Parallel()

	var rounds []int
	accepted := false
	fallbackCalled := false
	state, usage := ConvergeBrokerage(context.Background(), BrokerageConvergeInput[brokerageTestState]{
		Initial: brokerageTestState{
			note:  "first",
			phase: "brokerage",
		},
		MaxRounds:    3,
		Note:         func(s brokerageTestState) string { return s.note },
		Phase:        func(s brokerageTestState) string { return s.phase },
		Ratification: func(s brokerageTestState) string { return s.ratification },
		Ratify: func(_ context.Context, round int, s brokerageTestState) (brokerageTestState, core.TokenUsage, error) {
			rounds = append(rounds, round)
			if round == 1 {
				s.ratification = "adapt"
				return s, core.TokenUsage{OutputTokens: 2}, nil
			}
			s.ratification = "accept"
			return s, core.TokenUsage{OutputTokens: 3}, nil
		},
		Revise: func(_ context.Context, round int, s brokerageTestState) (brokerageTestState, core.TokenUsage, error) {
			if round != 1 {
				t.Fatalf("revise round = %d, want 1", round)
			}
			s.note = "revised"
			s.ratification = ""
			return s, core.TokenUsage{OutputTokens: 5}, nil
		},
		Fallback: func(context.Context, brokerageTestState) (brokerageTestState, core.TokenUsage) {
			fallbackCalled = true
			return brokerageTestState{}, core.TokenUsage{}
		},
		OnConverged: func(converged bool) {
			accepted = converged
		},
	})
	if fallbackCalled {
		t.Fatal("fallback called on accepted convergence")
	}
	if !accepted {
		t.Fatal("converged = false, want true")
	}
	if state.note != "revised" {
		t.Fatalf("note = %q, want revised", state.note)
	}
	if state.ratification != "accept" {
		t.Fatalf("ratification = %q, want accept", state.ratification)
	}
	if !reflect.DeepEqual(rounds, []int{1, 2}) {
		t.Fatalf("ratify rounds = %#v, want [1 2]", rounds)
	}
	if usage.OutputTokens != 10 {
		t.Fatalf("usage.OutputTokens = %d, want 10", usage.OutputTokens)
	}
}

func TestConvergeBrokerageRatifyErrorFallsBack(t *testing.T) {
	t.Parallel()

	var gotRoundErr error
	converged := true
	state, usage := ConvergeBrokerage(context.Background(), BrokerageConvergeInput[brokerageTestState]{
		Initial: brokerageTestState{
			note:  "first",
			phase: "brokerage",
		},
		MaxRounds:    3,
		Note:         func(s brokerageTestState) string { return s.note },
		Phase:        func(s brokerageTestState) string { return s.phase },
		Ratification: func(s brokerageTestState) string { return s.ratification },
		Ratify: func(context.Context, int, brokerageTestState) (brokerageTestState, core.TokenUsage, error) {
			return brokerageTestState{}, core.TokenUsage{OutputTokens: 4}, errors.New("ratify failed")
		},
		Fallback: func(_ context.Context, s brokerageTestState) (brokerageTestState, core.TokenUsage) {
			s.phase = "proposal"
			return s, core.TokenUsage{OutputTokens: 6}
		},
		OnRound: func(_ int, _ brokerageTestState, _ brokerageTestState, err error) {
			gotRoundErr = err
		},
		OnConverged: func(v bool) { converged = v },
	})
	if gotRoundErr == nil {
		t.Fatal("round error not captured")
	}
	if converged {
		t.Fatal("converged = true, want false")
	}
	if state.phase != "proposal" {
		t.Fatalf("phase = %q, want proposal fallback state", state.phase)
	}
	if usage.OutputTokens != 10 {
		t.Fatalf("usage.OutputTokens = %d, want 10", usage.OutputTokens)
	}
}

func TestConvergeBrokerageMaxRoundsFallsBackWithoutExtraRevise(t *testing.T) {
	t.Parallel()

	ratifyCalls := 0
	reviseCalls := 0
	converged := true
	_, usage := ConvergeBrokerage(context.Background(), BrokerageConvergeInput[brokerageTestState]{
		Initial: brokerageTestState{
			note:  "first",
			phase: "brokerage",
		},
		MaxRounds:    2,
		Note:         func(s brokerageTestState) string { return s.note },
		Phase:        func(s brokerageTestState) string { return s.phase },
		Ratification: func(s brokerageTestState) string { return s.ratification },
		Ratify: func(context.Context, int, brokerageTestState) (brokerageTestState, core.TokenUsage, error) {
			ratifyCalls++
			return brokerageTestState{
				note:         "note",
				phase:        "brokerage",
				ratification: "adapt",
			}, core.TokenUsage{OutputTokens: 1}, nil
		},
		Revise: func(context.Context, int, brokerageTestState) (brokerageTestState, core.TokenUsage, error) {
			reviseCalls++
			return brokerageTestState{
				note:  "revised",
				phase: "brokerage",
			}, core.TokenUsage{OutputTokens: 2}, nil
		},
		Fallback: func(context.Context, brokerageTestState) (brokerageTestState, core.TokenUsage) {
			return brokerageTestState{}, core.TokenUsage{OutputTokens: 3}
		},
		OnConverged: func(v bool) { converged = v },
	})
	if converged {
		t.Fatal("converged = true, want false")
	}
	if ratifyCalls != 2 {
		t.Fatalf("ratifyCalls = %d, want 2", ratifyCalls)
	}
	if reviseCalls != 1 {
		t.Fatalf("reviseCalls = %d, want 1", reviseCalls)
	}
	if usage.OutputTokens != 7 {
		t.Fatalf("usage.OutputTokens = %d, want 7", usage.OutputTokens)
	}
}
