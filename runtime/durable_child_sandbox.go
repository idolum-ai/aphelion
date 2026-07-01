//go:build linux

package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type durableChildSandboxAccess struct {
	readonlyPaths []string
	readonlyBinds []sandbox.BindPath
	env           map[string]string
	workingRoot   string
}

func durableChildSandboxAccessFor(binaryPath string, agent core.DurableAgent, store *session.SQLiteStore) (durableChildSandboxAccess, error) {
	return durableChildSandboxAccessForScope(binaryPath, agent, store, sandbox.Scope{})
}

func durableChildSandboxAccessForScope(binaryPath string, agent core.DurableAgent, store *session.SQLiteStore, scope sandbox.Scope) (durableChildSandboxAccess, error) {
	substrate := durableChildSubstrateFor(binaryPath, agent)
	access := durableChildSandboxAccess{readonlyPaths: append([]string(nil), substrate.ReadonlyPaths...), workingRoot: strings.TrimSpace(scope.WorkingRoot)}
	access.readonlyPaths = compactNonEmptyStrings(access.readonlyPaths)

	if err := access.addGrantedCapabilities(agent, store); err != nil {
		return durableChildSandboxAccess{}, err
	}
	access.readonlyPaths = compactNonEmptyStrings(access.readonlyPaths)
	access.readonlyBinds = compactBindPaths(access.readonlyBinds)
	return access, nil
}

func (a *durableChildSandboxAccess) addGrantedCapabilities(agent core.DurableAgent, store *session.SQLiteStore) error {
	if a == nil || store == nil {
		return nil
	}
	grants, err := durableChildRuntimeCapabilityGrants(store, strings.TrimSpace(agent.AgentID))
	if err != nil {
		return err
	}
	for _, grant := range grants {
		material, ok, err := durableChildGrantMaterializationFrom(grant)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := a.applyGrantMaterialization(grant, material); err != nil {
			return err
		}
	}
	return nil
}

func durableChildRuntimeCapabilityGrants(store *session.SQLiteStore, agentID string) ([]session.CapabilityGrant, error) {
	agentID = strings.TrimSpace(agentID)
	if store == nil || agentID == "" {
		return nil, nil
	}
	principalID := core.DurableAgentPrincipal(agentID)
	grants, err := store.CapabilityGrants(100, "", "", principalID)
	if err != nil {
		return nil, fmt.Errorf("load durable child capability grants principal=%s: %w", principalID, err)
	}
	activeRuntime := make([]session.CapabilityGrant, 0, len(grants))
	inactiveRuntime := make([]session.CapabilityGrant, 0)
	for _, grant := range grants {
		grant = session.NormalizeCapabilityGrant(grant)
		if strings.TrimSpace(grant.GrantID) == "" {
			continue
		}
		if _, ok, err := durableChildGrantMaterializationFrom(grant); err != nil {
			return nil, err
		} else if !ok {
			continue
		}
		if grant.Status == session.CapabilityGrantStatusActive {
			activeRuntime = append(activeRuntime, grant)
			continue
		}
		inactiveRuntime = append(inactiveRuntime, grant)
	}
	if len(activeRuntime) == 0 && len(inactiveRuntime) > 0 {
		// Inactive child_runtime grants are not materialized. Keep them reachable
		// only as the operator-facing reason a required runtime grant cannot be
		// used, instead of letting the child wake with silently missing material.
		if err := durableChildGrantFreshnessError(inactiveRuntime[0]); err != nil {
			return nil, err
		}
	}
	out := make([]session.CapabilityGrant, 0, len(activeRuntime))
	for _, grant := range activeRuntime {
		if err := durableChildGrantFreshnessError(grant); err != nil {
			return nil, err
		}
		out = append(out, grant)
	}
	return out, nil
}

func durableChildGrantFreshnessError(grant session.CapabilityGrant) error {
	grant = session.NormalizeCapabilityGrant(grant)
	switch grant.Status {
	case session.CapabilityGrantStatusActive:
	default:
		return fmt.Errorf("child_runtime_blocked: grant_%s grant_id=%s", strings.TrimSpace(string(grant.Status)), strings.TrimSpace(grant.GrantID))
	}
	if !grant.RevokedAt.IsZero() {
		return fmt.Errorf("child_runtime_blocked: grant_revoked grant_id=%s", strings.TrimSpace(grant.GrantID))
	}
	if !grant.ExpiresAt.IsZero() && !grant.ExpiresAt.After(time.Now().UTC()) {
		return fmt.Errorf("child_runtime_blocked: grant_expired grant_id=%s", strings.TrimSpace(grant.GrantID))
	}
	if strings.TrimSpace(grant.StaleReason) != "" {
		return fmt.Errorf("child_runtime_blocked: grant_stale_%s grant_id=%s", normalizeBlockReason(grant.StaleReason), strings.TrimSpace(grant.GrantID))
	}
	if grant.BaselinePolicyHash != "" && grant.CurrentPolicyHash != "" && grant.BaselinePolicyHash != grant.CurrentPolicyHash {
		return fmt.Errorf("child_runtime_blocked: grant_policy_hash_mismatch grant_id=%s", strings.TrimSpace(grant.GrantID))
	}
	return nil
}

func normalizeBlockReason(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func durableChildGrantMaterializationFrom(grant session.CapabilityGrant) (core.ChildRuntimeContract, bool, error) {
	material, ok, err := core.ExtractChildRuntimeContract(grant.Contract, grant.Constraints)
	if err != nil {
		return core.ChildRuntimeContract{}, false, fmt.Errorf("parse capability grant %s child_runtime: %w", strings.TrimSpace(grant.GrantID), err)
	}
	return material, ok, nil
}

func (a *durableChildSandboxAccess) applyGrantMaterialization(grant session.CapabilityGrant, material core.ChildRuntimeContract) error {
	if a == nil {
		return nil
	}
	grantID := strings.TrimSpace(grant.GrantID)
	if executable := strings.TrimSpace(material.Executable); executable != "" {
		path, err := durableChildResolveExecutable(executable)
		if err != nil {
			return fmt.Errorf("materialize capability grant %s executable %q: %w", grantID, executable, err)
		}
		a.readonlyBinds = append(a.readonlyBinds, sandbox.BindPath{Source: path, Target: filepath.ToSlash(filepath.Join("/usr/local/bin", filepath.Base(path)))})
		if source := durableChildRuntimeBinCompatibilityRoot(filepath.Dir(path), "/usr/local/bin"); source != "" {
			a.readonlyPaths = append(a.readonlyPaths, source)
			if workspaceTarget := a.workspaceRuntimeBinCompatibilityTarget(); workspaceTarget != "" {
				a.readonlyBinds = append(a.readonlyBinds, sandbox.BindPath{Source: source, Target: workspaceTarget})
			}
		}
	}
	for _, path := range material.ReadonlyPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		canonical, err := durableChildCanonicalRuntimeSourcePath(grantID, "readonly path", path)
		if err != nil {
			return err
		}
		a.readonlyPaths = append(a.readonlyPaths, canonical)
	}
	for _, bind := range material.ReadonlyBinds {
		canonical, err := durableChildCanonicalRuntimeBind(grantID, "readonly bind", bind)
		if err != nil {
			return err
		}
		a.readonlyBinds = append(a.readonlyBinds, canonical)
		if source := durableChildRuntimeBinCompatibilityRoot(canonical.Source, canonical.Target); source != "" {
			a.readonlyPaths = append(a.readonlyPaths, source)
			if workspaceTarget := a.workspaceRuntimeBinCompatibilityTarget(); workspaceTarget != "" {
				a.readonlyBinds = append(a.readonlyBinds, sandbox.BindPath{Source: source, Target: workspaceTarget})
			}
		}
	}
	for _, bind := range material.SecretBinds {
		canonical, err := durableChildCanonicalRuntimeBind(grantID, "secret bind", bind)
		if err != nil {
			return err
		}
		a.readonlyBinds = append(a.readonlyBinds, canonical)
	}
	for _, name := range material.EnvFromParent {
		if err := a.inheritEnv(strings.TrimSpace(name)); err != nil {
			return fmt.Errorf("materialize capability grant %s environment: %w", grantID, err)
		}
	}
	return nil
}

func durableChildCanonicalRuntimeBind(grantID string, kind string, bind core.ChildRuntimeBind) (sandbox.BindPath, error) {
	source, err := durableChildCanonicalRuntimeSourcePath(grantID, kind+" source", bind.Source)
	if err != nil {
		return sandbox.BindPath{}, err
	}
	target := strings.TrimSpace(bind.Target)
	if target == "" {
		return sandbox.BindPath{}, fmt.Errorf("materialize capability grant %s %s target is required", strings.TrimSpace(grantID), kind)
	}
	target = filepath.Clean(target)
	if !filepath.IsAbs(target) {
		return sandbox.BindPath{}, fmt.Errorf("materialize capability grant %s %s target must be absolute: %s", strings.TrimSpace(grantID), kind, bind.Target)
	}
	return sandbox.BindPath{Source: source, Target: target}, nil
}

func durableChildCanonicalRuntimeSourcePath(grantID string, kind string, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("materialize capability grant %s %s is required", strings.TrimSpace(grantID), kind)
	}
	cleaned := filepath.Clean(value)
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("materialize capability grant %s %s must be absolute: %s", strings.TrimSpace(grantID), kind, value)
	}
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return "", fmt.Errorf("materialize capability grant %s %s %q: resolve symlinks: %w", strings.TrimSpace(grantID), kind, value, err)
	}
	if !filepath.IsAbs(resolved) {
		return "", fmt.Errorf("materialize capability grant %s %s resolved to non-absolute path: %s", strings.TrimSpace(grantID), kind, resolved)
	}
	return filepath.Clean(resolved), nil
}

func (a durableChildSandboxAccess) workspaceRuntimeBinCompatibilityTarget() string {
	workingRoot := strings.TrimSpace(a.workingRoot)
	if workingRoot == "" || !filepath.IsAbs(workingRoot) {
		return ""
	}
	return filepath.Join(workingRoot, "runtime-bin")
}

func durableChildRuntimeBinCompatibilityRoot(source string, target string) string {
	source = strings.TrimSpace(source)
	target = filepath.Clean(strings.TrimSpace(target))
	if source == "" || target != "/usr/local/bin" {
		return ""
	}
	cleaned := filepath.Clean(source)
	if !filepath.IsAbs(cleaned) || filepath.Base(cleaned) != "runtime-bin" {
		return ""
	}
	return cleaned
}

func durableChildResolveExecutable(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("empty executable")
	}
	if strings.Contains(value, "/") {
		resolved, err := durableChildCanonicalRuntimeSourcePath("", "executable", value)
		if err != nil {
			return "", err
		}
		if info, err := os.Stat(resolved); err != nil {
			return "", err
		} else if info.IsDir() {
			return "", fmt.Errorf("executable path is a directory")
		}
		return resolved, nil
	}
	path, err := exec.LookPath(value)
	if err != nil {
		return "", err
	}
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("executable path must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return "", err
	}
	cleaned = filepath.Clean(resolved)
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

func (a *durableChildSandboxAccess) inheritEnv(name string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	if err := core.ValidateChildRuntimeContract(core.ChildRuntimeContract{EnvFromParent: []string{name}}); err != nil {
		return err
	}
	if value, ok := os.LookupEnv(name); ok {
		a.ensureEnv()[name] = value
	}
	return nil
}

func (a *durableChildSandboxAccess) ensureEnv() map[string]string {
	if a.env == nil {
		a.env = make(map[string]string)
	}
	return a.env
}

func compactNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
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

func compactBindPaths(values []sandbox.BindPath) []sandbox.BindPath {
	out := make([]sandbox.BindPath, 0, len(values))
	seen := make(map[string]struct{}, len(values))
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
