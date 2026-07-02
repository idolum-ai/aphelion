//go:build linux

package runtime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestCompileAuthorityDiscoveryMenuScoresFrontierAndLoadout(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	entry := session.IdentificationLedgerEntry{
		EntryID:     "ident-frontier",
		PlanID:      "plan-long-run",
		PlanVersion: "v1",
		SessionID:   "telegram_dm:1001",
		StepRef:     "step:mail-triage",
		ShapeHash:   "sha256:mail-triage",
		LabelRef:    "crc-frontier",
		Status:      session.IdentificationLedgerStatusProposed,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	menu := CompileAuthorityDiscoveryMenu(AuthorityDiscoveryMenuInput{
		PlanID:      "plan-long-run",
		PlanVersion: "v1",
		SessionID:   "telegram_dm:1001",
		Now:         now,
		Entries: []session.IdentificationLedgerProjection{{
			Entry: entry,
			Observations: []session.IdentificationLedgerObservation{
				{ObservationID: "obs-static", EntryID: entry.EntryID, Method: session.IdentificationObservationStatic, Property: session.IdentificationPropertyApprovalClass, Value: "data_access", ObservedAt: now},
				{ObservationID: "obs-collision", EntryID: entry.EntryID, Method: session.IdentificationObservationCollision, Property: session.IdentificationPropertyRetryability, Value: "bounded_backoff", ObservedAt: now},
			},
		}},
		Loadout: []AuthorityDiscoveryLoadoutSlot{{
			TokenID:   "loadout-github",
			LabelRef:  "authbundle-standing-github",
			ExpiresAt: now.Add(time.Hour),
		}},
	})
	if len(menu.Tokens) != 2 {
		t.Fatalf("tokens = %#v, want frontier plus loadout", menu.Tokens)
	}
	var frontier AuthorityDiscoveryMenuToken
	for _, token := range menu.Tokens {
		if token.TokenID == "ident-frontier" {
			frontier = token
		}
	}
	if frontier.State != AuthorityDiscoveryTokenOneApprovalAway {
		t.Fatalf("frontier state = %q, want one_approval_away", frontier.State)
	}
	if len(frontier.ResolutionCandidates) != 1 || frontier.ResolutionCandidates[0].Kind != "continuation_recovery_contract" {
		t.Fatalf("frontier candidates = %#v, want continuation recovery contract", frontier.ResolutionCandidates)
	}
	metrics := ScoreAuthorityDiscoveryMenu(menu)
	if metrics.Interruptions != 1 {
		t.Fatalf("interruptions = %d, want 1", metrics.Interruptions)
	}
	if metrics.OverGrantMass != 1 {
		t.Fatalf("over grant mass = %d, want one unbound standing loadout", metrics.OverGrantMass)
	}
	if metrics.IdentifiedBreadth != 3 {
		t.Fatalf("identified breadth = %d, want approval/retry/loadout properties", metrics.IdentifiedBreadth)
	}
}

func TestReviewEventLookaheadRecordsNonExecutingLedgerObservation(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	rt := &Runtime{store: store}
	event := session.ReviewEvent{
		ID:                77,
		SourceSessionID:   "telegram_dm:1001",
		TargetSessionID:   "telegram_dm:1001",
		TargetAdminChatID: 1001,
		Summary:           "Approved current grant.",
		MetadataJSON:      `{"request_id":"cap-current"}`,
		Status:            "delivered",
		DeliveryMessageID: 44,
	}
	text, err := rt.HandleReviewEventAction(context.Background(), telegram.CallbackQuery{
		ID:      "cb-lookahead-runtime",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 44, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionLookaheadNext),
	}, event, core.ReviewEventActionLookaheadNext)
	if err != nil {
		t.Fatalf("HandleReviewEventAction(lookahead) err = %v", err)
	}
	if !strings.Contains(text, "No authority was approved or executed") {
		t.Fatalf("lookahead text = %q, want non-executing acknowledgement", text)
	}
	projections, err := store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		PlanID:      session.IdentificationPlanIDForSession("telegram_dm:1001"),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   "telegram_dm:1001",
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries() err = %v", err)
	}
	if len(projections) != 1 {
		t.Fatalf("ledger entries = %#v, want one lookahead entry", projections)
	}
	if got := projections[0].Properties[session.IdentificationPropertyApprovalClass][0].Method; got != session.IdentificationObservationLookahead {
		t.Fatalf("observation method = %q, want lookahead", got)
	}
}

func TestReviewEventLookaheadRejectsMismatchedAdminWithoutLedgerMutation(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	rt := &Runtime{store: store}
	event := session.ReviewEvent{
		ID:                78,
		SourceSessionID:   "telegram_dm:1001",
		TargetSessionID:   "telegram_dm:1001",
		TargetAdminChatID: 1001,
		Status:            "delivered",
		DeliveryMessageID: 45,
	}
	text, err := rt.HandleReviewEventAction(context.Background(), telegram.CallbackQuery{
		ID:      "cb-lookahead-denied",
		From:    &telegram.User{ID: 2002},
		Message: &telegram.Message{MessageID: 45, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionLookaheadNext),
	}, event, core.ReviewEventActionLookaheadNext)
	if err != nil {
		t.Fatalf("HandleReviewEventAction(lookahead denied) err = %v", err)
	}
	if !strings.Contains(text, "Only the target admin") {
		t.Fatalf("denied text = %q, want target-admin response", text)
	}
	projections, err := store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		SessionID: "telegram_dm:1001",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries() err = %v", err)
	}
	if len(projections) != 0 {
		t.Fatalf("ledger entries = %#v, want no mutation for unauthorized callback", projections)
	}
}
