//go:build linux

package maintenancecli

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func appendTailnetMaintenanceEvent(store *session.SQLiteStore, surface session.TailnetSurfaceRecord, reason string) error {
	if store == nil {
		return nil
	}
	payload := map[string]any{
		"action":       "cli_revoke",
		"surface_id":   strings.TrimSpace(surface.SurfaceID),
		"owner_kind":   strings.TrimSpace(surface.OwnerKind),
		"owner_id":     strings.TrimSpace(surface.OwnerID),
		"surface_kind": strings.TrimSpace(surface.SurfaceKind),
		"name":         strings.TrimSpace(surface.Name),
		"status":       strings.TrimSpace(surface.Status),
		"url":          strings.TrimSpace(surface.URL),
		"reason":       strings.TrimSpace(reason),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = store.AppendExecutionEvent(tailnetMaintenanceSessionKey(), session.ExecutionEventInput{
		EventType:   core.ExecutionEventTailnetSurfaceChanged,
		Stage:       "tailnet",
		Status:      strings.TrimSpace(surface.Status),
		PayloadJSON: string(raw),
		CreatedAt:   time.Now().UTC(),
	})
	return err
}

func appendTailnetGrantMaintenanceEvent(store *session.SQLiteStore, binding session.TailnetGrantBinding, status string, reason string) error {
	if store == nil {
		return nil
	}
	payload := map[string]any{
		"binding_id":           strings.TrimSpace(binding.BindingID),
		"grant_id":             strings.TrimSpace(binding.GrantID),
		"surface_id":           strings.TrimSpace(binding.SurfaceID),
		"status":               strings.TrimSpace(binding.Status),
		"reason":               strings.TrimSpace(reason),
		"applied_policy_hash":  strings.TrimSpace(binding.AppliedPolicyHash),
		"observed_policy_hash": strings.TrimSpace(binding.ObservedPolicyHash),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = store.AppendExecutionEvent(tailnetMaintenanceSessionKey(), session.ExecutionEventInput{
		EventType:   core.ExecutionEventTailnetGrantChanged,
		Stage:       "tailnet",
		Status:      strings.TrimSpace(status),
		PayloadJSON: string(raw),
		CreatedAt:   time.Now().UTC(),
	})
	return err
}

func tailnetMaintenanceSessionKey() session.SessionKey {
	return session.SessionKey{
		ChatID: tailnetMaintenanceChatID,
		UserID: 0,
		Scope: session.ScopeRef{
			Kind: session.ScopeKindHeartbeat,
			ID:   "admin-house",
		},
	}
}

func firstArg(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
