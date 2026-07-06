//go:build linux

package runtime

import (
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func TestContinuationReadyNextActionClosesWhenLeaseConsumed(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9441, UserID: 0, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "9441"}}
	now := time.Now().UTC()
	state := session.NormalizeContinuationState(session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:        "proposal-ready-close",
			RiskClass: "workspace",
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-ready-close",
			Status:         session.ContinuationLeaseStatusActive,
			LeaseClass:     session.ContinuationLeaseClassLocalWorkspace,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      now.Add(time.Hour),
		},
	})

	if err := rt.recordContinuationReadyNextAction(key, state, now); err != nil {
		t.Fatalf("recordContinuationReadyNextAction() err = %v", err)
	}
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(open) err = %v", err)
	}
	if len(open) != 1 || open[0].State != session.NextActionReadyToExecute || open[0].SubjectRef != "lease-ready-close" {
		t.Fatalf("open next actions = %#v, want ready action for lease", open)
	}
	if err := rt.resolveContinuationReadyNextAction(key, state, "continuation_lease_consumed", now.Add(time.Second)); err != nil {
		t.Fatalf("resolveContinuationReadyNextAction() err = %v", err)
	}
	open, err = store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(resolved) err = %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open next actions after lease consumed = %#v, want none", open)
	}
}
