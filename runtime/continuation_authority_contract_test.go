//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestInvalidAuthorityContractDoesNotRenderApprovalButtons(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9044, UserID: 0, Scope: telegramDMScopeRef(9044)}
	now := time.Now().UTC()
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     "invalid-authority-contract",
		Objective:      "Commit contradictory local work.",
		StageSummary:   "Commit validated local slices.",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:               "aprop-invalid-authority-contract",
			Summary:          "Commit validated local slices",
			RiskClass:        "workspace_commit_then_repo_write_bounded",
			AllowedActions:   []string{"git_commit_validated_slices", "edit_repo_code"},
			ForbiddenActions: []string{"commit"},
			Status:           session.ProposalStatusPending,
			ExpiresAt:        now.Add(time.Hour),
		},
	}
	state.ContinuationLease = buildContinuationLease(state.ActionProposal, 1, now)
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := rt.sendContinuationApprovalPrompt(context.Background(), key, core.InboundMessage{ChatID: 9044, SenderID: 1001, Text: "continue", MessageID: 1}, state, "approve?"); err != nil {
		t.Fatalf("sendContinuationApprovalPrompt() err = %v", err)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sent := append([]core.OutboundMessage(nil), sender.sent...)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no approval buttons", inlineCount)
	}
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "internally contradictory") {
		t.Fatalf("sent = %#v, want contradiction status", sent)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusRevoked || got.ActionProposal.Status != session.ProposalStatusSuperseded {
		t.Fatalf("state = %#v, want revoked/superseded invalid authority", got)
	}
}
