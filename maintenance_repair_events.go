//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func maintenanceRepairKey() session.SessionKey {
	return session.SessionKey{
		ChatID: -1,
		UserID: 0,
		Scope:  session.ScopeRef{Kind: session.ScopeKindHeartbeat, ID: "admin-house"},
	}
}

func appendMaintenanceExecutionEvent(store *session.SQLiteStore, key session.SessionKey, eventType string, stage string, status string, payload map[string]any, createdAt time.Time) error {
	if store == nil {
		return nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal maintenance execution payload: %w", err)
	}
	_, err = store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   strings.TrimSpace(eventType),
		Stage:       strings.TrimSpace(stage),
		Status:      strings.TrimSpace(status),
		PayloadJSON: string(raw),
		CreatedAt:   createdAt.UTC(),
	})
	return err
}
