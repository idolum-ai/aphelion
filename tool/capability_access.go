//go:build linux

package tool

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Registry) validateCapabilityGrantTarget(grantedTo string, status session.CapabilityGrantStatus) error {
	if status != session.CapabilityGrantStatusActive {
		return nil
	}
	agentID, ok := core.DurableAgentIDFromPrincipal(grantedTo)
	if !ok {
		return nil
	}
	if r == nil || r.store == nil {
		return fmt.Errorf("capability_authority grant_set cannot validate durable agent %q without transcript store", agentID)
	}
	if _, err := r.store.DurableAgent(agentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("capability_authority grant_set target durable agent %q does not exist; create or repair the durable agent before granting active capability", agentID)
		}
		return fmt.Errorf("load durable agent %q for grant_set: %w", agentID, err)
	}
	return nil
}

func (r *Registry) capabilityGrantVisibleTo(actor principal.Principal, grant session.CapabilityGrant) bool {
	if actor.Role == principal.RoleAdmin {
		return true
	}
	if capabilityPrincipalMatches(actor, grant.GrantedTo) || capabilityPrincipalMatches(actor, grant.GrantedBy) {
		return true
	}
	if strings.TrimSpace(grant.RequestID) == "" || r == nil || r.store == nil {
		return false
	}
	request, ok, err := r.store.CapabilityRequest(grant.RequestID)
	if err != nil || !ok {
		return false
	}
	return capabilityRequestVisibleTo(actor, request)
}

func capabilityRequestVisibleTo(actor principal.Principal, request session.CapabilityRequest) bool {
	if actor.Role == principal.RoleAdmin {
		return true
	}
	return capabilityPrincipalMatches(actor, request.RequestedBy) ||
		capabilityPrincipalMatches(actor, request.RequestedFor) ||
		capabilityPrincipalMatches(actor, request.ParentPrincipal) ||
		capabilityPrincipalMatches(actor, request.AdminPrincipal)
}

func capabilityPrincipalMatches(actor principal.Principal, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if target == toolAuthorityPrincipalDisplay(actor) {
		return true
	}
	for _, key := range toolAuthorityPrincipalKeys(actor) {
		if target == key {
			return true
		}
	}
	return false
}

func normalizeCapabilityJSONBlob(raw json.RawMessage, field string) (string, error) {
	return normalizeCapabilityJSONBlobWithDefault(raw, field, "{}")
}

func normalizeCapabilityJSONBlobWithDefault(raw json.RawMessage, field string, fallback string) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		if strings.TrimSpace(fallback) == "" {
			return "{}", nil
		}
		return strings.TrimSpace(fallback), nil
	}
	if !json.Valid([]byte(trimmed)) {
		return "", fmt.Errorf("capability %s must be valid json", strings.TrimSpace(field))
	}
	return trimmed, nil
}

func capabilityGrantPolicyHash(kind session.CapabilityKind, target string, principalID string, actions []string, contract string, constraints string) string {
	payload := map[string]any{
		"kind":            string(kind),
		"target_resource": strings.TrimSpace(target),
		"principal":       strings.TrimSpace(principalID),
		"allowed_actions": session.NormalizeCapabilityActions(actions),
		"contract":        strings.TrimSpace(contract),
		"constraints":     strings.TrimSpace(constraints),
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (r *Registry) appendCapabilityEvent(key session.SessionKey, eventType string, status string, payload map[string]any) error {
	return r.appendToolLifecycleEvent(key, "capability_delegation", eventType, status, payload)
}

func boundedLimit(raw int, max int) int {
	if max <= 0 {
		max = 50
	}
	if raw <= 0 || raw > max {
		return max
	}
	return raw
}

func validateCapabilityChildRuntimeContract(contract string, constraints string) error {
	for _, raw := range []string{contract, constraints} {
		if capabilityJSONBlobHasKey(raw, "runtime_materialization") {
			return fmt.Errorf("capability contract must use child_runtime; runtime_materialization has been removed")
		}
	}
	_, _, err := core.ExtractChildRuntimeContract(contract, constraints)
	if err != nil {
		return err
	}
	return nil
}

func capabilityJSONBlobHasKey(raw string, key string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return false
	}
	_, ok := obj[key]
	return ok
}
