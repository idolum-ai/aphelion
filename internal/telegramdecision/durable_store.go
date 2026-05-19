//go:build linux

package telegramdecision

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
)

type telegramDecisionDurableStore struct {
	store *session.SQLiteStore
}

func newTelegramDecisionDurableStore(store *session.SQLiteStore) decision.DurableStore {
	if store == nil {
		return nil
	}
	return &telegramDecisionDurableStore{store: store}
}

func (s *telegramDecisionDurableStore) LoadPending(_ context.Context) ([]decision.DurableDecision, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}

	records, err := s.store.PendingDecisions()
	if err != nil {
		return nil, err
	}
	out := make([]decision.DurableDecision, 0, len(records))
	for _, record := range records {
		choicesJSON := strings.TrimSpace(record.ChoicesJSON)
		if choicesJSON == "" {
			choicesJSON = "[]"
		}
		var choices []decision.Choice
		if err := json.Unmarshal([]byte(choicesJSON), &choices); err != nil {
			return nil, fmt.Errorf("decode pending decision choices id=%s: %w", record.ID, err)
		}
		out = append(out, decision.DurableDecision{
			Pending: decision.PendingDecision{
				ID: record.ID,
				Request: decision.Request{
					Kind:           decision.Kind(strings.TrimSpace(record.Kind)),
					ChatID:         record.ChatID,
					SenderID:       record.SenderID,
					MessageID:      record.MessageID,
					OwnerKey:       record.OwnerKey,
					SessionID:      record.SessionID,
					ScopeKind:      record.ScopeKind,
					ScopeID:        record.ScopeID,
					DurableAgentID: record.DurableAgentID,
					Prompt:         record.Prompt,
					Details:        record.Details,
					Choices:        choices,
					DefaultChoice:  record.DefaultChoice,
					Timeout:        time.Duration(record.TimeoutNanos),
				},
			},
			Seq:      record.Sequence,
			OwnerKey: record.OwnerKey,
			Delivery: decision.Delivery{MessageID: record.DeliveryMessageID},
		})
	}
	return out, nil
}

func (s *telegramDecisionDurableStore) UpsertPending(_ context.Context, pending decision.DurableDecision) error {
	if s == nil || s.store == nil {
		return nil
	}

	choices := pending.Pending.Choices
	if len(choices) == 0 {
		choices = []decision.Choice{}
	}
	choicesJSON, err := json.Marshal(choices)
	if err != nil {
		return fmt.Errorf("encode pending decision choices id=%s: %w", pending.Pending.ID, err)
	}

	return s.store.UpsertPendingDecision(session.PendingDecisionRecord{
		ID:                pending.Pending.ID,
		Sequence:          pending.Seq,
		OwnerKey:          pending.OwnerKey,
		SessionID:         pending.Pending.SessionID,
		ScopeKind:         pending.Pending.ScopeKind,
		ScopeID:           pending.Pending.ScopeID,
		DurableAgentID:    pending.Pending.DurableAgentID,
		Kind:              string(pending.Pending.Kind),
		ChatID:            pending.Pending.ChatID,
		SenderID:          pending.Pending.SenderID,
		MessageID:         pending.Pending.MessageID,
		Prompt:            pending.Pending.Prompt,
		Details:           pending.Pending.Details,
		ChoicesJSON:       string(choicesJSON),
		DefaultChoice:     pending.Pending.DefaultChoice,
		TimeoutNanos:      int64(pending.Pending.Timeout),
		DeliveryMessageID: pending.Delivery.MessageID,
	})
}

func (s *telegramDecisionDurableStore) DeletePending(_ context.Context, id string) error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.DeletePendingDecision(id)
}

func (s *telegramDecisionDurableStore) DetachByOwner(_ context.Context, ownerKey string) (int, error) {
	if s == nil || s.store == nil {
		return 0, nil
	}
	return s.store.DeletePendingDecisionsByOwner(ownerKey)
}

func (s *telegramDecisionDurableStore) DetachByChatSender(_ context.Context, chatID int64, senderID int64) (int, error) {
	if s == nil || s.store == nil {
		return 0, nil
	}
	return s.store.DeletePendingDecisionsByChatSender(chatID, senderID)
}

func (s *telegramDecisionDurableStore) DetachAll(_ context.Context) (int, error) {
	if s == nil || s.store == nil {
		return 0, nil
	}
	return s.store.DeleteAllPendingDecisions()
}
