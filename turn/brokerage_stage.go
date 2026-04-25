//go:build linux

package turn

import (
	"context"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

const (
	BrokerageStopAccepted         = "accepted"
	BrokerageStopRejected         = "rejected"
	BrokerageStopStableContract   = "stable_contract"
	BrokerageStopRepeatedProposal = "repeated_proposal"
	BrokerageStopMaxRounds        = "max_rounds"
	BrokerageStopMaxElapsed       = "max_elapsed"
	BrokerageStopRatifyError      = "ratify_error"
	BrokerageStopReviseError      = "revise_error"
)

// BrokerageConvergencePolicy keeps deliberation bounded without making the
// round count the only stopping criterion.
type BrokerageConvergencePolicy struct {
	MinRounds              int
	MaxRounds              int
	AbsoluteMaxRounds      int
	MaxElapsed             time.Duration
	StableContractRounds   int
	StopOnStableContract   bool
	StopOnRepeatedProposal bool
	StopOnReject           bool
}

type BrokerageStop struct {
	Reason    string
	Round     int
	Converged bool
	Fallback  bool
}

func DefaultBrokerageConvergencePolicy() BrokerageConvergencePolicy {
	return BrokerageConvergencePolicy{
		MinRounds:              1,
		MaxRounds:              4,
		AbsoluteMaxRounds:      6,
		MaxElapsed:             20 * time.Second,
		StableContractRounds:   2,
		StopOnStableContract:   true,
		StopOnRepeatedProposal: true,
		StopOnReject:           true,
	}
}

func NormalizeBrokerageConvergencePolicy(policy BrokerageConvergencePolicy) BrokerageConvergencePolicy {
	defaults := DefaultBrokerageConvergencePolicy()
	if policy.MinRounds <= 0 {
		policy.MinRounds = defaults.MinRounds
	}
	if policy.MaxRounds <= 0 {
		policy.MaxRounds = defaults.MaxRounds
	}
	if policy.AbsoluteMaxRounds <= 0 {
		policy.AbsoluteMaxRounds = defaults.AbsoluteMaxRounds
	}
	if policy.MaxRounds > policy.AbsoluteMaxRounds {
		policy.MaxRounds = policy.AbsoluteMaxRounds
	}
	if policy.MinRounds > policy.MaxRounds {
		policy.MinRounds = policy.MaxRounds
	}
	if policy.MaxElapsed <= 0 {
		policy.MaxElapsed = defaults.MaxElapsed
	}
	if policy.StableContractRounds <= 0 {
		policy.StableContractRounds = defaults.StableContractRounds
	}
	if policy.StableContractRounds < 2 {
		policy.StableContractRounds = 2
	}
	return policy
}

// BrokerageConvergeInput defines callback-based brokerage convergence flow.
// Runtime adapters provide concrete ratify/revise/fallback behavior.
type BrokerageConvergeInput[T any] struct {
	Initial             T
	MaxRounds           int
	Policy              BrokerageConvergencePolicy
	Note                func(state T) string
	Phase               func(state T) string
	Ratification        func(state T) string
	ContractFingerprint func(state T) string
	ProposalFingerprint func(state T) string
	Ratify              func(ctx context.Context, round int, state T) (T, core.TokenUsage, error)
	Revise              func(ctx context.Context, round int, state T) (T, core.TokenUsage, error)
	Fallback            func(ctx context.Context, state T) (T, core.TokenUsage)
	OnRound             func(round int, before T, after T, err error)
	OnConverged         func(converged bool)
	OnStop              func(stop BrokerageStop)
	Now                 func() time.Time
}

// ConvergeBrokerage runs bounded brokerage convergence while keeping concrete
// provider/prompt behavior outside turn orchestration.
func ConvergeBrokerage[T any](ctx context.Context, input BrokerageConvergeInput[T]) (T, core.TokenUsage) {
	current := input.Initial
	if input.Note == nil || input.Phase == nil || strings.TrimSpace(input.Note(current)) == "" || input.Phase(current) != "brokerage" {
		return current, core.TokenUsage{}
	}
	policy := input.Policy
	if policy == (BrokerageConvergencePolicy{}) {
		policy = DefaultBrokerageConvergencePolicy()
	}
	if input.MaxRounds > 0 {
		policy.MaxRounds = input.MaxRounds
	}
	policy = NormalizeBrokerageConvergencePolicy(policy)
	now := input.Now
	if now == nil {
		now = time.Now
	}
	startedAt := now()
	total := core.TokenUsage{}
	lastContractFingerprint := ""
	stableContractRounds := 0
	seenProposalFingerprints := map[string]struct{}{}
	if proposalFingerprint := brokerageProposalFingerprint(input, current); proposalFingerprint != "" {
		seenProposalFingerprints[proposalFingerprint] = struct{}{}
	}
	stop := func(reason string, round int, state T, converged bool, fallback bool) (T, core.TokenUsage) {
		if input.OnConverged != nil {
			input.OnConverged(converged)
		}
		if input.OnStop != nil {
			input.OnStop(BrokerageStop{
				Reason:    reason,
				Round:     round,
				Converged: converged,
				Fallback:  fallback,
			})
		}
		if fallback && input.Fallback != nil {
			fallbackState, fallbackUsage := input.Fallback(ctx, state)
			total = addTokenUsage(total, fallbackUsage)
			return fallbackState, total
		}
		return state, total
	}
	for round := 1; round <= policy.MaxRounds; round++ {
		if round > policy.MinRounds && policy.MaxElapsed > 0 && now().Sub(startedAt) >= policy.MaxElapsed {
			return stop(BrokerageStopMaxElapsed, round-1, current, false, true)
		}
		before := current
		updated, usage, err := input.Ratify(ctx, round, current)
		total = addTokenUsage(total, usage)
		if err != nil {
			if input.OnRound != nil {
				input.OnRound(round, before, before, err)
			}
			return stop(BrokerageStopRatifyError, round, before, false, true)
		}

		current = updated
		if input.OnRound != nil {
			input.OnRound(round, before, current, nil)
		}
		if input.Ratification != nil && strings.TrimSpace(input.Ratification(current)) == "accept" {
			return stop(BrokerageStopAccepted, round, current, true, false)
		}
		if policy.StopOnReject && input.Ratification != nil && strings.TrimSpace(input.Ratification(current)) == "reject" && round >= policy.MinRounds {
			return stop(BrokerageStopRejected, round, current, false, true)
		}
		if policy.StopOnStableContract && input.ContractFingerprint != nil {
			fingerprint := strings.TrimSpace(input.ContractFingerprint(current))
			if fingerprint != "" {
				if fingerprint == lastContractFingerprint {
					stableContractRounds++
				} else {
					lastContractFingerprint = fingerprint
					stableContractRounds = 1
				}
				if stableContractRounds >= policy.StableContractRounds && round >= policy.MinRounds {
					return stop(BrokerageStopStableContract, round, current, false, true)
				}
			}
		}
		if round == policy.MaxRounds {
			return stop(BrokerageStopMaxRounds, round, current, false, true)
		}
		if input.Revise != nil {
			revised, reviseUsage, reviseErr := input.Revise(ctx, round, current)
			total = addTokenUsage(total, reviseUsage)
			if reviseErr != nil {
				return stop(BrokerageStopReviseError, round, current, false, true)
			}
			current = revised
			if policy.StopOnRepeatedProposal {
				fingerprint := brokerageProposalFingerprint(input, current)
				if fingerprint != "" {
					if _, ok := seenProposalFingerprints[fingerprint]; ok && round >= policy.MinRounds {
						return stop(BrokerageStopRepeatedProposal, round, current, false, true)
					}
					seenProposalFingerprints[fingerprint] = struct{}{}
				}
			}
		}
	}
	return current, total
}

func brokerageProposalFingerprint[T any](input BrokerageConvergeInput[T], state T) string {
	if input.ProposalFingerprint != nil {
		return strings.TrimSpace(input.ProposalFingerprint(state))
	}
	if input.Note == nil {
		return ""
	}
	return normalizeBrokerageFingerprint(input.Note(state))
}

func normalizeBrokerageFingerprint(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}
