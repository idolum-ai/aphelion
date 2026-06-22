//go:build linux

package tool

import (
	"context"
	"time"

	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/effectauth"
	"github.com/idolum-ai/aphelion/session"
)

type continuationExecAuthorityContextKey struct{}

// ContinuationExecAuthorityDecision records whether an active continuation
// envelope permits a boundary-crossing command.
type ContinuationExecAuthorityDecision = effectauth.Decision

func WithContinuationExecAuthority(ctx context.Context, state session.ContinuationState) context.Context {
	return context.WithValue(ctx, continuationExecAuthorityContextKey{}, session.NormalizeContinuationState(state))
}

func ContinuationExecAuthorityFromContext(ctx context.Context) (session.ContinuationState, bool) {
	if ctx == nil {
		return session.ContinuationState{}, false
	}
	state, ok := ctx.Value(continuationExecAuthorityContextKey{}).(session.ContinuationState)
	if !ok {
		return session.ContinuationState{}, false
	}
	return session.NormalizeContinuationState(state), true
}

func ContinuationExecAuthorityDecisionForCommand(state session.ContinuationState, command string, now time.Time) ContinuationExecAuthorityDecision {
	return effectauth.AuthorizeCommand(effectauth.CommandRequest{
		State:   state,
		Command: command,
		Now:     now,
	})
}

func ContinuationExecAuthorityDecisionForPlan(state session.ContinuationState, command string, plan commandeffect.EffectPlan, now time.Time) ContinuationExecAuthorityDecision {
	return effectauth.AuthorizePlan(effectauth.PlanRequest{
		State:   state,
		Command: command,
		Plan:    plan,
		Now:     now,
	})
}

func ContinuationExecAuthorityError(decision ContinuationExecAuthorityDecision) error {
	return effectauth.DecisionError(decision)
}
