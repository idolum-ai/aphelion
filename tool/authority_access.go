//go:build linux

package tool

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Registry) authorityManagedTool(name string) bool {
	_, ok := r.externalManifestByName(strings.TrimSpace(name))
	return ok
}

func (r *Registry) toolAuthorityAccessAllowed(toolName string, p principal.Principal) (bool, error) {
	toolName = strings.TrimSpace(toolName)
	if !r.authorityManagedTool(toolName) {
		return true, nil
	}
	if r.store == nil {
		return false, fmt.Errorf("%s requires transcript store", toolName)
	}
	registered, ok, err := r.store.RegisteredTool(toolName)
	if err != nil {
		return false, err
	}
	if !ok || !registered.Registered {
		return false, nil
	}
	_, allowedByGrant, err := r.capabilityGrantAllowsAuthorityToolAccess(toolName, p)
	if err != nil {
		return false, err
	}
	return allowedByGrant, nil
}

func (r *Registry) requireAuthorityToolAccess(name string, p principal.Principal) error {
	name = strings.TrimSpace(name)
	if !r.authorityManagedTool(name) {
		return nil
	}
	if r.store == nil {
		return fmt.Errorf("%s requires transcript store", name)
	}
	registered, ok, err := r.store.RegisteredTool(name)
	if err != nil {
		return err
	}
	if !ok || !registered.Registered {
		return fmt.Errorf("tool %q is not registered", name)
	}
	if len(toolAuthorityPrincipalKeys(p)) == 0 {
		return fmt.Errorf("tool %q is not granted to principal %q", name, toolAuthorityPrincipalDisplay(p))
	}
	grant, allowedByGrant, err := r.capabilityGrantAllowsAuthorityToolAccess(name, p)
	if err != nil {
		return err
	}
	if allowedByGrant {
		if _, err := r.store.RecordCapabilityInvocation(session.CapabilityInvocation{
			GrantID:   grant.GrantID,
			Principal: toolAuthorityPrincipalDisplay(p),
			Action:    "invoke",
			Status:    "allowed",
		}); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("tool %q is not granted to principal %q", name, toolAuthorityPrincipalDisplay(p))
}

func (r *Registry) capabilityGrantAllowsAuthorityToolAccess(toolName string, p principal.Principal) (session.CapabilityGrant, bool, error) {
	if r == nil || r.store == nil {
		return session.CapabilityGrant{}, false, nil
	}
	candidates := append([]string{}, toolAuthorityPrincipalKeys(p)...)
	candidates = append(candidates, toolAuthorityPrincipalDisplay(p))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		grant, ok, err := r.store.ActiveCapabilityGrant(session.CapabilityKindTool, toolName, candidate, "invoke")
		if err != nil {
			return session.CapabilityGrant{}, false, err
		}
		if ok {
			return grant, true, nil
		}
	}
	return session.CapabilityGrant{}, false, nil
}

func toolAuthorityPrincipalKeys(p principal.Principal) []string {
	keys := make([]string, 0, 6)

	switch p.Role {
	case principal.RoleDurableAgent:
		id := strings.TrimSpace(p.DurableAgentID)
		if id != "" {
			keys = append(keys, id, "durable_agent:"+id)
		}
	case principal.RoleApprovedUser, principal.RoleAdmin:
		if p.TelegramUserID > 0 {
			id := strconv.FormatInt(p.TelegramUserID, 10)
			keys = append(keys, "telegram:"+id, "principal:"+id, id)
		} else if p.Role == principal.RoleAdmin {
			keys = append(keys, "admin")
		}
	}

	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

func toolAuthorityPrincipalDisplay(p principal.Principal) string {
	switch p.Role {
	case principal.RoleDurableAgent:
		if id := strings.TrimSpace(p.DurableAgentID); id != "" {
			return id
		}
	case principal.RoleApprovedUser, principal.RoleAdmin:
		if p.TelegramUserID > 0 {
			return "telegram:" + strconv.FormatInt(p.TelegramUserID, 10)
		}
	}
	role := strings.TrimSpace(string(p.Role))
	if role == "" {
		return "unknown"
	}
	return role
}
