//go:build linux

package turn

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

// BrokerageConvergeInput defines callback-based brokerage convergence flow.
// Runtime adapters provide concrete ratify/revise/fallback behavior.
type BrokerageConvergeInput[T any] struct {
	Initial      T
	MaxRounds    int
	Note         func(state T) string
	Phase        func(state T) string
	Ratification func(state T) string
	Ratify       func(ctx context.Context, round int, state T) (T, core.TokenUsage, error)
	Revise       func(ctx context.Context, round int, state T) (T, core.TokenUsage, error)
	Fallback     func(ctx context.Context, state T) (T, core.TokenUsage)
	OnRound      func(round int, before T, after T, err error)
	OnConverged  func(converged bool)
}

// ConvergeBrokerage runs bounded brokerage convergence while keeping concrete
// provider/prompt behavior outside turn orchestration.
func ConvergeBrokerage[T any](ctx context.Context, input BrokerageConvergeInput[T]) (T, core.TokenUsage) {
	current := input.Initial
	if input.Note == nil || input.Phase == nil || strings.TrimSpace(input.Note(current)) == "" || input.Phase(current) != "brokerage" {
		return current, core.TokenUsage{}
	}
	rounds := input.MaxRounds
	if rounds <= 0 {
		rounds = 1
	}
	total := core.TokenUsage{}
	for round := 1; round <= rounds; round++ {
		before := current
		updated, usage, err := input.Ratify(ctx, round, current)
		total = addTokenUsage(total, usage)
		if err != nil {
			if input.OnRound != nil {
				input.OnRound(round, before, before, err)
			}
			if input.OnConverged != nil {
				input.OnConverged(false)
			}
			if input.Fallback != nil {
				fallbackState, fallbackUsage := input.Fallback(ctx, before)
				total = addTokenUsage(total, fallbackUsage)
				return fallbackState, total
			}
			return before, total
		}

		current = updated
		if input.OnRound != nil {
			input.OnRound(round, before, current, nil)
		}
		if input.Ratification != nil && strings.TrimSpace(input.Ratification(current)) == "accept" {
			if input.OnConverged != nil {
				input.OnConverged(true)
			}
			return current, total
		}
		if round == rounds {
			if input.OnConverged != nil {
				input.OnConverged(false)
			}
			if input.Fallback != nil {
				fallbackState, fallbackUsage := input.Fallback(ctx, current)
				total = addTokenUsage(total, fallbackUsage)
				return fallbackState, total
			}
			return current, total
		}
		if input.Revise != nil {
			revised, reviseUsage, reviseErr := input.Revise(ctx, round, current)
			total = addTokenUsage(total, reviseUsage)
			if reviseErr != nil {
				if input.OnConverged != nil {
					input.OnConverged(false)
				}
				if input.Fallback != nil {
					fallbackState, fallbackUsage := input.Fallback(ctx, current)
					total = addTokenUsage(total, fallbackUsage)
					return fallbackState, total
				}
				return current, total
			}
			current = revised
		}
	}
	if input.OnConverged != nil {
		input.OnConverged(false)
	}
	return current, total
}
