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

func nativeFileAccessToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case "read_file", "write_file", "list_dir", "search":
		return true
	default:
		return false
	}
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

type authorityInvocationPermit struct {
	InvocationID int64
	Grant        session.CapabilityGrant
	Principal    string
	Action       string
	UseRef       session.AuthorityUseRef
}

func (r *Registry) requireAuthorityToolAccess(ctx context.Context, name string, p principal.Principal, key session.SessionKey, input json.RawMessage) (session.CapabilityGrant, *authorityInvocationPermit, bool, error) {
	name = strings.TrimSpace(name)
	if !r.authorityManagedTool(name) {
		return session.CapabilityGrant{}, nil, false, nil
	}
	if r.store == nil {
		return session.CapabilityGrant{}, nil, false, fmt.Errorf("%s requires transcript store", name)
	}
	registered, ok, err := r.store.RegisteredTool(name)
	if err != nil {
		return session.CapabilityGrant{}, nil, false, err
	}
	if !ok || !registered.Registered {
		return session.CapabilityGrant{}, nil, false, fmt.Errorf("tool %q is not registered", name)
	}
	if len(toolAuthorityPrincipalKeys(p)) == 0 {
		return session.CapabilityGrant{}, nil, false, fmt.Errorf("tool %q is not granted to principal %q", name, toolAuthorityPrincipalDisplay(p))
	}
	grant, allowedByGrant, err := r.capabilityGrantAllowsAuthorityToolAccess(name, p)
	if err != nil {
		return session.CapabilityGrant{}, nil, false, err
	}
	if allowedByGrant {
		principalID := toolAuthorityPrincipalDisplay(p)
		useRef, useRefErr := r.authorityUseRefForGrant(ctx, name, key, p)
		if useRefErr != nil {
			if _, recordErr := r.store.RecordCapabilityInvocation(capabilityInvocationWithAuthorityUseRef(session.CapabilityInvocation{
				GrantID:   grant.GrantID,
				Principal: principalID,
				Action:    "invoke",
				Status:    "blocked",
				ErrorText: useRefErr.Error(),
			}, useRef)); recordErr != nil {
				return session.CapabilityGrant{}, nil, false, recordErr
			}
			return session.CapabilityGrant{}, nil, false, useRefErr
		}
		if err := validateCapabilityToolInvocationInput(grant, input); err != nil {
			if _, recordErr := r.store.RecordCapabilityInvocation(capabilityInvocationWithAuthorityUseRef(session.CapabilityInvocation{
				GrantID:   grant.GrantID,
				Principal: principalID,
				Action:    "invoke",
				Status:    "blocked",
				ErrorText: err.Error(),
			}, useRef)); recordErr != nil {
				return session.CapabilityGrant{}, nil, false, recordErr
			}
			return session.CapabilityGrant{}, nil, false, err
		}
		invocation, err := r.store.RecordCapabilityInvocation(capabilityInvocationWithAuthorityUseRef(session.CapabilityInvocation{
			GrantID:   grant.GrantID,
			Principal: principalID,
			Action:    "invoke",
			Status:    "allowed",
		}, useRef))
		if err != nil {
			return session.CapabilityGrant{}, nil, false, err
		}
		return grant, &authorityInvocationPermit{
			InvocationID: invocation.InvocationID,
			Grant:        grant,
			Principal:    principalID,
			Action:       "invoke",
			UseRef:       useRef,
		}, true, nil
	}
	cause := fmt.Errorf("tool %q is not granted to principal %q", name, toolAuthorityPrincipalDisplay(p))
	return session.CapabilityGrant{}, nil, false, missingGrantError{
		contract: genericMissingGrantContractForTool(name, p),
		cause:    cause,
	}
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

func (r *Registry) recordAuthorityManagedToolOutcome(permit *authorityInvocationPermit, status string, errText string) error {
	if r == nil || r.store == nil || permit == nil || permit.InvocationID <= 0 {
		return nil
	}
	_, err := r.store.CompleteCapabilityInvocation(permit.InvocationID, strings.TrimSpace(status), strings.TrimSpace(errText), time.Now().UTC())
	return err
}

func (r *Registry) authorityUseRefForGrant(ctx context.Context, toolName string, key session.SessionKey, p principal.Principal) (session.AuthorityUseRef, error) {
	ref := session.AuthorityUseRef{}
	if !toolSessionKeyHasIdentity(key) {
		return ref, fmt.Errorf("tool %q requires active turn lease evidence", strings.TrimSpace(toolName))
	}
	sessionID := session.SessionIDForKey(key)
	if contextRef, ok := AuthorityUseRefFromContext(ctx); ok {
		contextRef, err := r.validateContextAuthorityUseRef(toolName, key, sessionID, contextRef, p, time.Now().UTC())
		if err != nil {
			return ref, err
		}
		if authorityUseRefHasLeaseEvidence(contextRef) {
			return contextRef, nil
		}
	}
	return ref, fmt.Errorf("tool %q requires durable run authority evidence", strings.TrimSpace(toolName))
}

func (r *Registry) validateContextAuthorityUseRef(toolName string, key session.SessionKey, sessionID string, ref session.AuthorityUseRef, p principal.Principal, now time.Time) (session.AuthorityUseRef, error) {
	ref = session.NormalizeAuthorityUseRef(ref)
	sessionID = strings.TrimSpace(sessionID)
	if strings.TrimSpace(ref.SessionID) == "" {
		return session.AuthorityUseRef{}, fmt.Errorf("tool %q authority evidence must include a session_id", strings.TrimSpace(toolName))
	}
	if ref.SessionID != sessionID {
		return session.AuthorityUseRef{}, fmt.Errorf("tool %q authority evidence belongs to session %q, not %q", strings.TrimSpace(toolName), strings.TrimSpace(ref.SessionID), sessionID)
	}
	if ref.TurnRunID <= 0 {
		return session.AuthorityUseRef{}, fmt.Errorf("tool %q authority evidence must include turn_run_id", strings.TrimSpace(toolName))
	}
	record, ok, err := r.store.ExecutionRunAuthority(ref.TurnRunID)
	if err != nil {
		return session.AuthorityUseRef{}, fmt.Errorf("load execution run authority: %w", err)
	}
	if !ok {
		return session.AuthorityUseRef{}, fmt.Errorf("tool %q execution run authority %d is not durable", strings.TrimSpace(toolName), ref.TurnRunID)
	}
	if record.SessionID != sessionID {
		return session.AuthorityUseRef{}, fmt.Errorf("tool %q execution run authority belongs to session %q, not %q", strings.TrimSpace(toolName), record.SessionID, sessionID)
	}
	run, err := r.store.TurnRun(record.TurnRunID)
	if err != nil {
		return session.AuthorityUseRef{}, fmt.Errorf("load execution authority turn run: %w", err)
	}
	if run.SessionID != sessionID {
		return session.AuthorityUseRef{}, fmt.Errorf("tool %q execution authority turn run belongs to session %q, not %q", strings.TrimSpace(toolName), run.SessionID, sessionID)
	}
	if run.Status != session.TurnRunStatusRunning {
		return session.AuthorityUseRef{}, fmt.Errorf("tool %q execution authority turn run %d is %s", strings.TrimSpace(toolName), run.ID, run.Status)
	}
	if !executionRunAuthorityPrincipalMatches(record, p) {
		return session.AuthorityUseRef{}, fmt.Errorf("tool %q execution run authority principal %q does not match %q", strings.TrimSpace(toolName), record.Principal, toolAuthorityPrincipalDisplay(p))
	}
	validated := session.AuthorityUseRef{SessionID: sessionID, TurnRunID: ref.TurnRunID}
	switch record.LeaseKind {
	case session.ExecutionAuthorityLeaseKindContinuation:
		if strings.TrimSpace(ref.OperationPlanLeaseID) != "" {
			return session.AuthorityUseRef{}, fmt.Errorf("tool %q authority evidence includes an operation plan lease outside the run authority", strings.TrimSpace(toolName))
		}
		if ref.ContinuationLeaseID != "" && ref.ContinuationLeaseID != record.ContinuationLeaseID {
			return session.AuthorityUseRef{}, fmt.Errorf("tool %q authority evidence continuation lease does not match run authority", strings.TrimSpace(toolName))
		}
		if ref.AuthoritySource != "" && ref.AuthoritySource != session.ExecutionAuthorityLeaseKindContinuation {
			return session.AuthorityUseRef{}, fmt.Errorf("tool %q authority evidence source does not match run authority", strings.TrimSpace(toolName))
		}
		if err := r.validateContinuationAuthorityUseRefForRun(key, record, now); err != nil {
			return session.AuthorityUseRef{}, err
		}
		validated.ContinuationLeaseID = record.ContinuationLeaseID
		validated.AuthoritySource = session.ExecutionAuthorityLeaseKindContinuation
	case session.ExecutionAuthorityLeaseKindOperationPlan:
		if strings.TrimSpace(ref.ContinuationLeaseID) != "" {
			return session.AuthorityUseRef{}, fmt.Errorf("tool %q authority evidence includes a continuation lease outside the run authority", strings.TrimSpace(toolName))
		}
		if ref.OperationPlanLeaseID != "" && ref.OperationPlanLeaseID != record.OperationPlanLeaseID {
			return session.AuthorityUseRef{}, fmt.Errorf("tool %q authority evidence operation plan lease does not match run authority", strings.TrimSpace(toolName))
		}
		if ref.AuthoritySource != "" && ref.AuthoritySource != session.ExecutionAuthorityLeaseKindOperationPlan {
			return session.AuthorityUseRef{}, fmt.Errorf("tool %q authority evidence source does not match run authority", strings.TrimSpace(toolName))
		}
		if err := r.validateOperationPlanAuthorityUseRef(key, record.OperationPlanLeaseID, now); err != nil {
			return session.AuthorityUseRef{}, err
		}
		validated.OperationPlanLeaseID = record.OperationPlanLeaseID
		validated.AuthoritySource = session.ExecutionAuthorityLeaseKindOperationPlan
	default:
		return session.AuthorityUseRef{}, fmt.Errorf("tool %q execution run authority has unsupported lease kind %q", strings.TrimSpace(toolName), record.LeaseKind)
	}
	return session.NormalizeAuthorityUseRef(validated), nil
}

func executionRunAuthorityPrincipalMatches(record session.ExecutionRunAuthority, p principal.Principal) bool {
	want := strings.TrimSpace(record.Principal)
	if want == "" {
		return false
	}
	for _, candidate := range append(toolAuthorityPrincipalKeys(p), toolAuthorityPrincipalDisplay(p)) {
		if strings.TrimSpace(candidate) == want {
			return true
		}
	}
	return false
}

func toolAuthorityCanonicalPrincipal(p principal.Principal) string {
	keys := toolAuthorityPrincipalKeys(p)
	if len(keys) > 0 {
		return strings.TrimSpace(keys[0])
	}
	return toolAuthorityPrincipalDisplay(p)
}

func (r *Registry) validateContinuationAuthorityUseRef(key session.SessionKey, leaseID string, now time.Time) error {
	return r.validateContinuationAuthorityUseRefForRun(key, session.ExecutionRunAuthority{ContinuationLeaseID: leaseID}, now)
}

func (r *Registry) validateContinuationAuthorityUseRefForRun(key session.SessionKey, record session.ExecutionRunAuthority, now time.Time) error {
	leaseID := strings.TrimSpace(record.ContinuationLeaseID)
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
	if continuationLeaseValidForExecutionRun(lease, record, now) {
		return nil
	}
	return fmt.Errorf("continuation lease evidence %q is not active", strings.TrimSpace(leaseID))
}

func continuationLeaseValidForExecutionRun(lease session.ContinuationLease, record session.ExecutionRunAuthority, now time.Time) bool {
	lease = session.NormalizeContinuationLease(lease)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !lease.ExpiresAt.IsZero() && !lease.ExpiresAt.After(now.UTC()) {
		return false
	}
	if lease.ActiveAt(now) {
		return true
	}
	if lease.Status != session.ContinuationLeaseStatusConsumed {
		return false
	}
	if session.ContinuationLeaseStatus(record.LeaseStatus) != session.ContinuationLeaseStatusActive {
		return false
	}
	if record.LeaseRemainingTurns <= 0 {
		return false
	}
	return true
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
	return r.activeGrantForMissingGrantContract(genericMissingGrantContractForTool(toolName, p), nil)
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

func toolAuthorityPrincipalIDs(p principal.Principal) []string {
	return normalizeUniqueStrings(append(toolAuthorityPrincipalKeys(p), toolAuthorityPrincipalDisplay(p)))
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
