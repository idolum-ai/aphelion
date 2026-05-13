//go:build linux

package tool

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func (r *Registry) appendToolAuthorityEvent(key session.SessionKey, eventType string, status string, payload map[string]any) error {
	return r.appendToolLifecycleEvent(key, "tool_authority", eventType, status, payload)
}

func (r *Registry) appendToolLifecycleEvent(key session.SessionKey, stage string, eventType string, status string, payload map[string]any) error {
	if r == nil || r.store == nil {
		return nil
	}
	payloadJSON := "{}"
	if len(payload) > 0 {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal tool authority event payload: %w", err)
		}
		payloadJSON = string(encoded)
	}
	_, err := r.store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   strings.TrimSpace(eventType),
		Stage:       strings.TrimSpace(stage),
		Status:      strings.TrimSpace(status),
		PayloadJSON: payloadJSON,
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("append tool authority event %q: %w", strings.TrimSpace(eventType), err)
	}
	return nil
}

func boolToStatus(v bool) string {
	if v {
		return "enabled"
	}
	return "disabled"
}

func (r *Registry) canonicalTrustedToolName(raw string) (string, bool) {
	target := strings.TrimSpace(raw)
	if target == "" {
		return "", false
	}
	for _, def := range r.Definitions() {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}
		if strings.EqualFold(name, target) {
			return name, true
		}
	}
	if manifest, ok := r.externalManifestByName(target); ok {
		return strings.TrimSpace(manifest.Name), true
	}
	return "", false
}
