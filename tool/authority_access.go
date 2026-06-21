//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
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

func (r *Registry) requireAuthorityToolAccess(ctx context.Context, name string, p principal.Principal, key session.SessionKey, input json.RawMessage) (session.CapabilityGrant, bool, error) {
	name = strings.TrimSpace(name)
	if !r.authorityManagedTool(name) {
		return session.CapabilityGrant{}, false, nil
	}
	if r.store == nil {
		return session.CapabilityGrant{}, false, fmt.Errorf("%s requires transcript store", name)
	}
	registered, ok, err := r.store.RegisteredTool(name)
	if err != nil {
		return session.CapabilityGrant{}, false, err
	}
	if !ok || !registered.Registered {
		return session.CapabilityGrant{}, false, fmt.Errorf("tool %q is not registered", name)
	}
	if len(toolAuthorityPrincipalKeys(p)) == 0 {
		return session.CapabilityGrant{}, false, fmt.Errorf("tool %q is not granted to principal %q", name, toolAuthorityPrincipalDisplay(p))
	}
	grant, allowedByGrant, err := r.capabilityGrantAllowsAuthorityToolAccess(name, p)
	if err != nil {
		return session.CapabilityGrant{}, false, err
	}
	if allowedByGrant {
		principalID := toolAuthorityPrincipalDisplay(p)
		useRef, useRefErr := r.authorityUseRefForGrant(ctx, name, key)
		if useRefErr != nil {
			if _, recordErr := r.store.RecordCapabilityInvocation(capabilityInvocationWithAuthorityUseRef(session.CapabilityInvocation{
				GrantID:   grant.GrantID,
				Principal: principalID,
				Action:    "invoke",
				Status:    "blocked",
				ErrorText: useRefErr.Error(),
			}, useRef)); recordErr != nil {
				return session.CapabilityGrant{}, false, recordErr
			}
			return session.CapabilityGrant{}, false, useRefErr
		}
		if err := validateCapabilityToolInvocationInput(grant, input); err != nil {
			if _, recordErr := r.store.RecordCapabilityInvocation(capabilityInvocationWithAuthorityUseRef(session.CapabilityInvocation{
				GrantID:   grant.GrantID,
				Principal: principalID,
				Action:    "invoke",
				Status:    "blocked",
				ErrorText: err.Error(),
			}, useRef)); recordErr != nil {
				return session.CapabilityGrant{}, false, recordErr
			}
			return session.CapabilityGrant{}, false, err
		}
		if _, err := r.store.RecordCapabilityInvocation(capabilityInvocationWithAuthorityUseRef(session.CapabilityInvocation{
			GrantID:   grant.GrantID,
			Principal: principalID,
			Action:    "invoke",
			Status:    "allowed",
		}, useRef)); err != nil {
			return session.CapabilityGrant{}, false, err
		}
		return grant, true, nil
	}
	return session.CapabilityGrant{}, false, fmt.Errorf("tool %q is not granted to principal %q", name, toolAuthorityPrincipalDisplay(p))
}

func capabilityInvocationWithAuthorityUseRef(invocation session.CapabilityInvocation, ref session.AuthorityUseRef) session.CapabilityInvocation {
	ref = session.NormalizeAuthorityUseRef(ref)
	invocation.SessionID = ref.SessionID
	invocation.TurnRunID = ref.TurnRunID
	invocation.ContinuationLeaseID = ref.ContinuationLeaseID
	invocation.OperationPlanLeaseID = ref.OperationPlanLeaseID
	invocation.AuthoritySource = ref.AuthoritySource
	return invocation
}

func (r *Registry) authorityUseRefForGrant(ctx context.Context, toolName string, key session.SessionKey) (session.AuthorityUseRef, error) {
	ref := session.AuthorityUseRef{}
	if !toolSessionKeyHasIdentity(key) {
		return ref, fmt.Errorf("tool %q requires active turn lease evidence", strings.TrimSpace(toolName))
	}
	sessionID := session.SessionIDForKey(key)
	if contextRef, ok := AuthorityUseRefFromContext(ctx); ok {
		contextRef, err := r.validateContextAuthorityUseRef(toolName, key, sessionID, contextRef, time.Now().UTC())
		if err != nil {
			return ref, err
		}
		if authorityUseRefHasLeaseEvidence(contextRef) {
			return contextRef, nil
		}
	}

	ref.SessionID = sessionID
	now := time.Now().UTC()
	sources := []string{}

	if state, exists, err := r.store.ContinuationStateIfExists(key); err != nil {
		return ref, fmt.Errorf("load continuation lease evidence: %w", err)
	} else if exists {
		lease := session.NormalizeContinuationLease(state.ContinuationLease)
		if lease.ActiveAt(now) && strings.TrimSpace(lease.ID) != "" {
			ref.ContinuationLeaseID = lease.ID
			sources = append(sources, "continuation_lease")
		}
	}

	if _, operation, exists, err := r.store.PlanAndOperationStateIfExists(key); err != nil {
		return ref, fmt.Errorf("load operation plan lease evidence: %w", err)
	} else if exists {
		lease := session.NormalizeOperationPlanLease(operation.PlanLease)
		if operationPlanLeaseUsableForGrantUse(lease, now) {
			ref.OperationPlanLeaseID = lease.ID
			sources = append(sources, "operation_plan_lease")
		}
	}

	if len(sources) == 0 {
		return ref, fmt.Errorf("tool %q requires active continuation or operation plan lease evidence", strings.TrimSpace(toolName))
	}
	ref.AuthoritySource = strings.Join(sources, "+")
	return session.NormalizeAuthorityUseRef(ref), nil
}

func (r *Registry) validateContextAuthorityUseRef(toolName string, key session.SessionKey, sessionID string, ref session.AuthorityUseRef, now time.Time) (session.AuthorityUseRef, error) {
	ref = session.NormalizeAuthorityUseRef(ref)
	sessionID = strings.TrimSpace(sessionID)
	if strings.TrimSpace(ref.SessionID) == "" {
		return session.AuthorityUseRef{}, fmt.Errorf("tool %q authority evidence must include a session_id", strings.TrimSpace(toolName))
	}
	if ref.SessionID != sessionID {
		return session.AuthorityUseRef{}, fmt.Errorf("tool %q authority evidence belongs to session %q, not %q", strings.TrimSpace(toolName), strings.TrimSpace(ref.SessionID), sessionID)
	}
	if strings.TrimSpace(ref.AuthoritySource) == "" {
		return session.AuthorityUseRef{}, fmt.Errorf("tool %q authority evidence must include an authority_source", strings.TrimSpace(toolName))
	}
	sources := strings.Split(ref.AuthoritySource, "+")
	validated := session.AuthorityUseRef{SessionID: sessionID, TurnRunID: ref.TurnRunID}
	validatedSources := make([]string, 0, len(sources))
	seen := map[string]struct{}{}
	for _, source := range sources {
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		if _, ok := seen[source]; ok {
			continue
		}
		seen[source] = struct{}{}
		switch source {
		case "continuation_lease":
			if strings.TrimSpace(ref.ContinuationLeaseID) == "" {
				return session.AuthorityUseRef{}, fmt.Errorf("tool %q continuation authority evidence is missing continuation_lease_id", strings.TrimSpace(toolName))
			}
			if err := r.validateContinuationAuthorityUseRef(key, ref.ContinuationLeaseID, now); err != nil {
				return session.AuthorityUseRef{}, err
			}
			validated.ContinuationLeaseID = ref.ContinuationLeaseID
			validatedSources = append(validatedSources, source)
		case "operation_plan_lease":
			if strings.TrimSpace(ref.OperationPlanLeaseID) == "" {
				return session.AuthorityUseRef{}, fmt.Errorf("tool %q operation-plan authority evidence is missing operation_plan_lease_id", strings.TrimSpace(toolName))
			}
			if err := r.validateOperationPlanAuthorityUseRef(key, ref.OperationPlanLeaseID, now); err != nil {
				return session.AuthorityUseRef{}, err
			}
			validated.OperationPlanLeaseID = ref.OperationPlanLeaseID
			validatedSources = append(validatedSources, source)
		default:
			return session.AuthorityUseRef{}, fmt.Errorf("tool %q authority evidence has unsupported authority_source %q", strings.TrimSpace(toolName), source)
		}
	}
	if len(validatedSources) == 0 {
		return session.AuthorityUseRef{}, fmt.Errorf("tool %q authority evidence must include active continuation or operation plan lease evidence", strings.TrimSpace(toolName))
	}
	validated.AuthoritySource = strings.Join(validatedSources, "+")
	return session.NormalizeAuthorityUseRef(validated), nil
}

func (r *Registry) validateContinuationAuthorityUseRef(key session.SessionKey, leaseID string, now time.Time) error {
	state, exists, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return fmt.Errorf("load continuation lease evidence: %w", err)
	}
	if !exists {
		return fmt.Errorf("continuation lease evidence %q is not durable for this session", strings.TrimSpace(leaseID))
	}
	lease := session.NormalizeContinuationLease(state.ContinuationLease)
	if strings.TrimSpace(lease.ID) != strings.TrimSpace(leaseID) {
		return fmt.Errorf("continuation lease evidence %q does not match current session lease", strings.TrimSpace(leaseID))
	}
	if !lease.ActiveAt(now) {
		return fmt.Errorf("continuation lease evidence %q is not active", strings.TrimSpace(leaseID))
	}
	return nil
}

func (r *Registry) validateOperationPlanAuthorityUseRef(key session.SessionKey, leaseID string, now time.Time) error {
	_, operation, exists, err := r.store.PlanAndOperationStateIfExists(key)
	if err != nil {
		return fmt.Errorf("load operation plan lease evidence: %w", err)
	}
	if !exists {
		return fmt.Errorf("operation plan lease evidence %q is not durable for this session", strings.TrimSpace(leaseID))
	}
	lease := session.NormalizeOperationPlanLease(operation.PlanLease)
	if strings.TrimSpace(lease.ID) != strings.TrimSpace(leaseID) {
		return fmt.Errorf("operation plan lease evidence %q does not match current session lease", strings.TrimSpace(leaseID))
	}
	if !operationPlanLeaseUsableForGrantUse(lease, now) {
		return fmt.Errorf("operation plan lease evidence %q is not active", strings.TrimSpace(leaseID))
	}
	return nil
}

func authorityUseRefHasLeaseEvidence(ref session.AuthorityUseRef) bool {
	ref = session.NormalizeAuthorityUseRef(ref)
	if strings.TrimSpace(ref.AuthoritySource) == "" {
		return false
	}
	return strings.TrimSpace(ref.ContinuationLeaseID) != "" || strings.TrimSpace(ref.OperationPlanLeaseID) != ""
}

func operationPlanLeaseUsableForGrantUse(lease session.OperationPlanLease, now time.Time) bool {
	lease = session.NormalizeOperationPlanLease(lease)
	if strings.TrimSpace(lease.ID) == "" || lease.RemainingTurns <= 0 {
		return false
	}
	if !lease.ExpiresAt.IsZero() && !lease.ExpiresAt.After(now.UTC()) {
		return false
	}
	switch lease.Status {
	case session.PlanLeaseStatusApproved, session.PlanLeaseStatusActive:
		return true
	default:
		return false
	}
}

func toolSessionKeyHasIdentity(key session.SessionKey) bool {
	return key.ChatID != 0 || key.UserID != 0 || !key.Scope.IsZero()
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

func externalToolExecutionAccessFromGrant(p principal.Principal, grant session.CapabilityGrant) (ExternalToolExecutionAccess, error) {
	if p.Role != principal.RoleDurableAgent {
		return ExternalToolExecutionAccess{}, nil
	}
	material, ok, err := core.ExtractChildRuntimeContract(grant.Contract, grant.Constraints)
	if err != nil {
		return ExternalToolExecutionAccess{}, fmt.Errorf("external tool child_runtime contract: %w", err)
	}
	if !ok {
		return ExternalToolExecutionAccess{}, nil
	}
	access := ExternalToolExecutionAccess{ExtraReadonlyPaths: append([]string(nil), material.ReadonlyPaths...)}
	for _, path := range material.ReadonlyPaths {
		if err := ensureChildRuntimePathExists("readonly_path", path); err != nil {
			return ExternalToolExecutionAccess{}, fmt.Errorf("materialize capability grant %s child_runtime: %w", strings.TrimSpace(grant.GrantID), err)
		}
	}
	if executable := strings.TrimSpace(material.Executable); executable != "" {
		path, err := resolveChildRuntimeExecutableForTool(executable)
		if err != nil {
			return ExternalToolExecutionAccess{}, fmt.Errorf("materialize capability grant %s executable %q: %w", strings.TrimSpace(grant.GrantID), executable, err)
		}
		access.ExtraReadonlyBinds = append(access.ExtraReadonlyBinds, sandbox.BindPath{Source: path, Target: filepath.ToSlash(filepath.Join("/usr/local/bin", filepath.Base(path)))})
	}
	for _, bind := range material.ReadonlyBinds {
		if err := ensureChildRuntimeBindSourceExists("readonly_bind", bind.Source); err != nil {
			return ExternalToolExecutionAccess{}, fmt.Errorf("materialize capability grant %s child_runtime: %w", strings.TrimSpace(grant.GrantID), err)
		}
		access.ExtraReadonlyBinds = append(access.ExtraReadonlyBinds, sandbox.BindPath{Source: bind.Source, Target: bind.Target})
	}
	for _, bind := range material.SecretBinds {
		if err := ensureChildRuntimeBindSourceExists("secret_bind", bind.Source); err != nil {
			return ExternalToolExecutionAccess{}, fmt.Errorf("materialize capability grant %s child_runtime: %w", strings.TrimSpace(grant.GrantID), err)
		}
		access.ExtraReadonlyBinds = append(access.ExtraReadonlyBinds, sandbox.BindPath{Source: bind.Source, Target: bind.Target})
	}
	for _, name := range material.EnvFromParent {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if err := core.ValidateChildRuntimeContract(core.ChildRuntimeContract{EnvFromParent: []string{name}}); err != nil {
			return ExternalToolExecutionAccess{}, err
		}
		value, ok := os.LookupEnv(name)
		if !ok {
			return ExternalToolExecutionAccess{}, fmt.Errorf("materialize capability grant %s child_runtime: env_from_parent %q is not set", strings.TrimSpace(grant.GrantID), name)
		}
		if access.ExtraEnv == nil {
			access.ExtraEnv = map[string]string{}
		}
		access.ExtraEnv[name] = value
	}
	access.ExtraReadonlyPaths = compactStringSetForTool(access.ExtraReadonlyPaths)
	access.ExtraReadonlyBinds = compactBindSetForTool(access.ExtraReadonlyBinds)
	return access, nil
}

func ensureChildRuntimePathExists(kind string, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("%s %q: %w", kind, path, err)
	}
	return nil
}

func ensureChildRuntimeBindSourceExists(kind string, source string) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil
	}
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("%s source %q: %w", kind, source, err)
	}
	return nil
}

func resolveChildRuntimeExecutableForTool(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("empty executable")
	}
	if strings.Contains(value, "/") {
		cleaned := filepath.Clean(value)
		if !filepath.IsAbs(cleaned) {
			return "", fmt.Errorf("executable path must be absolute")
		}
		if info, err := os.Stat(cleaned); err != nil {
			return "", err
		} else if info.IsDir() {
			return "", fmt.Errorf("executable path is a directory")
		}
		return cleaned, nil
	}
	path, err := exec.LookPath(value)
	if err != nil {
		return "", err
	}
	return path, nil
}

func compactStringSetForTool(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func compactBindSetForTool(values []sandbox.BindPath) []sandbox.BindPath {
	out := make([]sandbox.BindPath, 0, len(values))
	seen := map[string]struct{}{}
	for _, bind := range values {
		bind.Source = strings.TrimSpace(bind.Source)
		bind.Target = strings.TrimSpace(bind.Target)
		if bind.Source == "" || bind.Target == "" {
			continue
		}
		key := bind.Source + "\x00" + bind.Target
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, bind)
	}
	return out
}
