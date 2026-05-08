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
}

func durableChildSandboxAccessFor(binaryPath string, agent core.DurableAgent, store *session.SQLiteStore) (durableChildSandboxAccess, error) {
	substrate := durableChildSubstrateFor(binaryPath, agent)
	access := durableChildSandboxAccess{readonlyPaths: append([]string(nil), substrate.ReadonlyPaths...)}
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
	grants, err := durableChildActiveCapabilityGrants(store, strings.TrimSpace(agent.AgentID))
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

func durableChildActiveCapabilityGrants(store *session.SQLiteStore, agentID string) ([]session.CapabilityGrant, error) {
	agentID = strings.TrimSpace(agentID)
	if store == nil || agentID == "" {
		return nil, nil
	}
	principalID := core.DurableAgentPrincipal(agentID)
	grants, err := store.CapabilityGrants(100, session.CapabilityGrantStatusActive, "", principalID)
	if err != nil {
		return nil, fmt.Errorf("load durable child capability grants principal=%s: %w", principalID, err)
	}
	out := make([]session.CapabilityGrant, 0, len(grants))
	for _, grant := range grants {
		grant = session.NormalizeCapabilityGrant(grant)
		if strings.TrimSpace(grant.GrantID) == "" {
			continue
		}
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
	if executable := strings.TrimSpace(material.Executable); executable != "" {
		path, err := durableChildResolveExecutable(executable)
		if err != nil {
			return fmt.Errorf("materialize capability grant %s executable %q: %w", strings.TrimSpace(grant.GrantID), executable, err)
		}
		a.readonlyBinds = append(a.readonlyBinds, sandbox.BindPath{Source: path, Target: filepath.ToSlash(filepath.Join("/usr/local/bin", filepath.Base(path)))})
	}
	for _, path := range material.ReadonlyPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			return fmt.Errorf("materialize capability grant %s readonly path must be absolute: %s", strings.TrimSpace(grant.GrantID), path)
		}
		a.readonlyPaths = append(a.readonlyPaths, path)
	}
	for _, bind := range material.ReadonlyBinds {
		a.readonlyBinds = append(a.readonlyBinds, sandbox.BindPath{Source: bind.Source, Target: bind.Target})
	}
	for _, bind := range material.SecretBinds {
		a.readonlyBinds = append(a.readonlyBinds, sandbox.BindPath{Source: bind.Source, Target: bind.Target})
	}
	for _, name := range material.EnvFromParent {
		if err := a.inheritEnv(strings.TrimSpace(name)); err != nil {
			return fmt.Errorf("materialize capability grant %s environment: %w", strings.TrimSpace(grant.GrantID), err)
		}
	}
	return nil
}

func durableChildResolveExecutable(value string) (string, error) {
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
