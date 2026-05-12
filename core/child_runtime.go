//go:build linux

package core

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ChildRuntimeContract is the reviewable capability materialization contract
// for durable children. It describes the runtime material a parent may expose
// after an active capability grant; it is not inferred from channel identity.
type ChildRuntimeContract struct {
	Executable     string             `json:"executable,omitempty"`
	ReadonlyPaths  []string           `json:"readonly_paths,omitempty"`
	ReadonlyBinds  []ChildRuntimeBind `json:"readonly_binds,omitempty"`
	SecretBinds    []ChildRuntimeBind `json:"secret_binds,omitempty"`
	EnvFromParent  []string           `json:"env_from_parent,omitempty"`
	CapabilityNote string             `json:"capability_note,omitempty"`
}

type ChildRuntimeBind struct {
	Source string `json:"source,omitempty"`
	Target string `json:"target,omitempty"`
}

var childRuntimeEnvNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func NormalizeChildRuntimeContract(contract ChildRuntimeContract) ChildRuntimeContract {
	contract.Executable = strings.TrimSpace(contract.Executable)
	contract.ReadonlyPaths = normalizeUniqueStrings(contract.ReadonlyPaths)
	contract.ReadonlyBinds = normalizeChildRuntimeBinds(contract.ReadonlyBinds)
	contract.SecretBinds = normalizeChildRuntimeBinds(contract.SecretBinds)
	contract.EnvFromParent = normalizeUniqueStrings(contract.EnvFromParent)
	contract.CapabilityNote = strings.TrimSpace(contract.CapabilityNote)
	return contract
}

func normalizeChildRuntimeBinds(values []ChildRuntimeBind) []ChildRuntimeBind {
	binds := make([]ChildRuntimeBind, 0, len(values))
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
		binds = append(binds, bind)
	}
	return binds
}

func (c ChildRuntimeContract) Active() bool {
	c = NormalizeChildRuntimeContract(c)
	return c.Executable != "" || len(c.ReadonlyPaths) > 0 || len(c.ReadonlyBinds) > 0 || len(c.SecretBinds) > 0 || len(c.EnvFromParent) > 0 || c.CapabilityNote != ""
}

func ValidateChildRuntimeContract(contract ChildRuntimeContract) error {
	contract = NormalizeChildRuntimeContract(contract)
	if strings.Contains(contract.Executable, "/") && !filepath.IsAbs(contract.Executable) {
		return fmt.Errorf("child_runtime executable path must be absolute")
	}
	for _, path := range contract.ReadonlyPaths {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("child_runtime readonly path must be absolute: %s", path)
		}
	}
	for _, bind := range contract.ReadonlyBinds {
		if !filepath.IsAbs(bind.Source) || !filepath.IsAbs(bind.Target) {
			return fmt.Errorf("child_runtime readonly bind source and target must be absolute")
		}
	}
	for _, bind := range contract.SecretBinds {
		if !filepath.IsAbs(bind.Source) || !filepath.IsAbs(bind.Target) {
			return fmt.Errorf("child_runtime secret bind source and target must be absolute")
		}
	}
	for _, name := range contract.EnvFromParent {
		if !childRuntimeEnvNamePattern.MatchString(name) {
			return fmt.Errorf("child_runtime env_from_parent contains invalid env var name %q", name)
		}
	}
	return nil
}

func MergeChildRuntimeContract(existing ChildRuntimeContract, next ChildRuntimeContract) ChildRuntimeContract {
	existing = NormalizeChildRuntimeContract(existing)
	next = NormalizeChildRuntimeContract(next)
	if strings.TrimSpace(next.Executable) != "" {
		existing.Executable = strings.TrimSpace(next.Executable)
	}
	existing.ReadonlyPaths = append(existing.ReadonlyPaths, next.ReadonlyPaths...)
	existing.ReadonlyBinds = append(existing.ReadonlyBinds, next.ReadonlyBinds...)
	existing.SecretBinds = append(existing.SecretBinds, next.SecretBinds...)
	existing.EnvFromParent = append(existing.EnvFromParent, next.EnvFromParent...)
	if strings.TrimSpace(next.CapabilityNote) != "" {
		existing.CapabilityNote = strings.TrimSpace(next.CapabilityNote)
	}
	return NormalizeChildRuntimeContract(existing)
}

func ExtractChildRuntimeContract(contractJSON string, constraintsJSON string) (ChildRuntimeContract, bool, error) {
	var out ChildRuntimeContract
	found := false
	for _, raw := range []string{contractJSON, constraintsJSON} {
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == "{}" {
			continue
		}
		var removed map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &removed); err != nil {
			return ChildRuntimeContract{}, false, fmt.Errorf("decode child_runtime contract: %w", err)
		}
		if _, ok := removed["runtime_materialization"]; ok {
			return ChildRuntimeContract{}, false, fmt.Errorf("runtime_materialization has been removed; use child_runtime")
		}
		var wrapper struct {
			ChildRuntime *ChildRuntimeContract `json:"child_runtime,omitempty"`
		}
		if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
			return ChildRuntimeContract{}, false, fmt.Errorf("decode child_runtime contract: %w", err)
		}
		if wrapper.ChildRuntime == nil || !wrapper.ChildRuntime.Active() {
			continue
		}
		merged := MergeChildRuntimeContract(out, *wrapper.ChildRuntime)
		if err := ValidateChildRuntimeContract(merged); err != nil {
			return ChildRuntimeContract{}, false, err
		}
		out = merged
		found = true
	}
	return NormalizeChildRuntimeContract(out), found, nil
}

func normalizeUniqueStrings(values []string) []string {
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
