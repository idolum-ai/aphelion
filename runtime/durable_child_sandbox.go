//go:build linux

package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type durableChildSandboxAccess struct {
	readonlyPaths []string
	readonlyBinds []sandbox.BindPath
	env           map[string]string
}

type durableChildGrantMaterialization struct {
	Executable     string             `json:"executable,omitempty"`
	ReadonlyPaths  []string           `json:"readonly_paths,omitempty"`
	ReadonlyBinds  []sandbox.BindPath `json:"readonly_binds,omitempty"`
	EnvFromParent  []string           `json:"env_from_parent,omitempty"`
	Environment    []string           `json:"environment,omitempty"`
	CapabilityNote string             `json:"capability_note,omitempty"`
}

var durableChildEnvNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func durableChildSandboxAccessFor(binaryPath string, agent core.DurableAgent, store *session.SQLiteStore) (durableChildSandboxAccess, error) {
	access := durableChildSandboxAccess{readonlyPaths: []string{strings.TrimSpace(binaryPath)}}
	access.readonlyPaths = compactNonEmptyStrings(access.readonlyPaths)

	bootstrap := core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM)
	if bootstrap.Backend == "codex" && strings.TrimSpace(bootstrap.CodexHome) != "" {
		access.readonlyPaths = append(access.readonlyPaths, strings.TrimSpace(bootstrap.CodexHome))
	}
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
	principalIDs := []string{agentID, "durable_agent:" + agentID}
	seen := map[string]struct{}{}
	out := make([]session.CapabilityGrant, 0)
	for _, principalID := range principalIDs {
		grants, err := store.CapabilityGrants(100, session.CapabilityGrantStatusActive, "", principalID)
		if err != nil {
			return nil, fmt.Errorf("load durable child capability grants principal=%s: %w", principalID, err)
		}
		for _, grant := range grants {
			grant = session.NormalizeCapabilityGrant(grant)
			if strings.TrimSpace(grant.GrantID) == "" {
				continue
			}
			if _, ok := seen[grant.GrantID]; ok {
				continue
			}
			seen[grant.GrantID] = struct{}{}
			out = append(out, grant)
		}
	}
	return out, nil
}

func durableChildGrantMaterializationFrom(grant session.CapabilityGrant) (durableChildGrantMaterialization, bool, error) {
	var material durableChildGrantMaterialization
	found := false
	for _, raw := range []string{grant.Contract, grant.Constraints} {
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == "{}" {
			continue
		}
		var wrapper struct {
			ChildRuntime           *durableChildGrantMaterialization `json:"child_runtime,omitempty"`
			RuntimeMaterialization *durableChildGrantMaterialization `json:"runtime_materialization,omitempty"`
		}
		if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
			return durableChildGrantMaterialization{}, false, fmt.Errorf("parse capability grant %s materialization: %w", strings.TrimSpace(grant.GrantID), err)
		}
		for _, candidate := range []*durableChildGrantMaterialization{wrapper.ChildRuntime, wrapper.RuntimeMaterialization} {
			if candidate == nil || !candidate.active() {
				continue
			}
			material.merge(*candidate)
			found = true
		}
	}
	return material, found, nil
}

func (m durableChildGrantMaterialization) active() bool {
	return strings.TrimSpace(m.Executable) != "" || len(m.ReadonlyPaths) > 0 || len(m.ReadonlyBinds) > 0 || len(m.EnvFromParent) > 0 || len(m.Environment) > 0
}

func (m *durableChildGrantMaterialization) merge(other durableChildGrantMaterialization) {
	if m == nil {
		return
	}
	if strings.TrimSpace(other.Executable) != "" {
		m.Executable = strings.TrimSpace(other.Executable)
	}
	m.ReadonlyPaths = append(m.ReadonlyPaths, other.ReadonlyPaths...)
	m.ReadonlyBinds = append(m.ReadonlyBinds, other.ReadonlyBinds...)
	m.EnvFromParent = append(m.EnvFromParent, other.EnvFromParent...)
	m.Environment = append(m.Environment, other.Environment...)
}

func (a *durableChildSandboxAccess) applyGrantMaterialization(grant session.CapabilityGrant, material durableChildGrantMaterialization) error {
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
		bind.Source = strings.TrimSpace(bind.Source)
		bind.Target = strings.TrimSpace(bind.Target)
		if bind.Source == "" || bind.Target == "" {
			continue
		}
		if !filepath.IsAbs(bind.Source) || !filepath.IsAbs(bind.Target) {
			return fmt.Errorf("materialize capability grant %s readonly bind source and target must be absolute", strings.TrimSpace(grant.GrantID))
		}
		a.readonlyBinds = append(a.readonlyBinds, bind)
	}
	for _, name := range append(material.EnvFromParent, material.Environment...) {
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
	if !durableChildEnvNamePattern.MatchString(name) {
		return fmt.Errorf("invalid env var name %q", name)
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
