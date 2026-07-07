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

type durableStore struct {
	store *session.SQLiteStore
}

func newTelegramDecisionDurableStore(store *session.SQLiteStore) decision.DurableStore {
	if store == nil {
		return nil
	}
	return &durableStore{store: store}
}

func (s *durableStore) LoadPending(_ context.Context) ([]decision.DurableDecision, error) {
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
		metadata, err := decodePendingDecisionMetadata(record.MetadataJSON)
		if err != nil {
			return nil, fmt.Errorf("decode pending decision metadata id=%s: %w", record.ID, err)
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
					Metadata:       metadata,
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

func (s *durableStore) UpsertPending(_ context.Context, pending decision.DurableDecision) error {
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
	metadataJSON, err := encodePendingDecisionMetadata(pending.Pending.Metadata)
	if err != nil {
		return fmt.Errorf("encode pending decision metadata id=%s: %w", pending.Pending.ID, err)
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
		MetadataJSON:      metadataJSON,
		ChoicesJSON:       string(choicesJSON),
		DefaultChoice:     pending.Pending.DefaultChoice,
		TimeoutNanos:      int64(pending.Pending.Timeout),
		DeliveryMessageID: pending.Delivery.MessageID,
	})
}

func (s *durableStore) DeletePending(_ context.Context, id string) error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.DeletePendingDecision(id)
}

func (s *durableStore) DetachByOwner(_ context.Context, ownerKey string) (int, error) {
	if s == nil || s.store == nil {
		return 0, nil
	}
	return s.store.DeletePendingDecisionsByOwner(ownerKey)
}

func (s *durableStore) DetachByChatSender(_ context.Context, chatID int64, senderID int64) (int, error) {
	if s == nil || s.store == nil {
		return 0, nil
	}
	return s.store.DeletePendingDecisionsByChatSender(chatID, senderID)
}

func (s *durableStore) DetachAll(_ context.Context) (int, error) {
	if s == nil || s.store == nil {
		return 0, nil
	}
	return s.store.DeleteAllPendingDecisions()
}

func decodePendingDecisionMetadata(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var metadata map[string]string
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func encodePendingDecisionMetadata(metadata map[string]string) (string, error) {
	if len(metadata) == 0 {
		return "{}", nil
	}
	normalized := make(map[string]string, len(metadata))
	for key, value := range metadata {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		normalized[key] = value
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
