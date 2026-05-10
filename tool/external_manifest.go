//go:build linux

package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ExternalToolManifest is the minimal core-readable record for an agent-owned
// external tool. This slice only loads and validates manifests; execution
// wiring comes later.
type ExternalToolManifest struct {
	Name        string                          `json:"name"`
	Owner       string                          `json:"owner"`
	Version     string                          `json:"version,omitempty"`
	Execution   ExternalToolManifestExecution   `json:"execution"`
	IO          ExternalToolManifestIO          `json:"io"`
	Constraints ExternalToolManifestConstraints `json:"constraints,omitempty"`
	Container   ExternalToolManifestContainer   `json:"container,omitempty"`
	Install     ExternalToolManifestInstall     `json:"install,omitempty"`
	Audit       ExternalToolManifestAudit       `json:"audit,omitempty"`
	Probe       ExternalToolManifestProbe       `json:"probe,omitempty"`
	Rollback    ExternalToolManifestRollback    `json:"rollback,omitempty"`
	Uninstall   ExternalToolManifestUninstall   `json:"uninstall,omitempty"`
	Provenance  ExternalToolManifestProvenance  `json:"provenance,omitempty"`
}

type ExternalToolManifestExecution struct {
	Mode           string `json:"mode"`
	Entry          string `json:"entry"`
	Workdir        string `json:"workdir,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type ExternalToolManifestIO struct {
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
}

type ExternalToolManifestConstraints struct {
	Network           string   `json:"network,omitempty"`
	NetworkTargets    []string `json:"network_targets,omitempty"`
	Filesystem        string   `json:"filesystem,omitempty"`
	MaxMemoryMB       int      `json:"max_memory_mb,omitempty"`
	MaxRuntimeSeconds int      `json:"max_runtime_seconds,omitempty"`
}

type ExternalToolManifestInstall struct {
	Command []string `json:"command,omitempty"`
}

type ExternalToolManifestRollback struct {
	Command []string `json:"command,omitempty"`
}

type ExternalToolManifestUninstall struct {
	Command []string `json:"command,omitempty"`
}

type ExternalToolManifestAudit struct {
	Command                []string `json:"command,omitempty"`
	ExpectedOutputContains string   `json:"expected_output_contains,omitempty"`
}

type ExternalToolManifestProbe struct {
	Command                []string `json:"command,omitempty"`
	ExpectedOutputContains string   `json:"expected_output_contains,omitempty"`
}

type ExternalToolManifestContainer struct {
	Image       string                              `json:"image,omitempty"`
	Digest      string                              `json:"digest,omitempty"`
	BuildRef    string                              `json:"build_ref,omitempty"`
	Healthcheck ExternalToolManifestContainerHealth `json:"healthcheck,omitempty"`
}

type ExternalToolManifestContainerHealth struct {
	Command                []string `json:"command,omitempty"`
	ExpectedOutputContains string   `json:"expected_output_contains,omitempty"`
}

type ExternalToolManifestProvenance struct {
	RequestID    string `json:"request_id,omitempty"`
	RegisteredAt string `json:"registered_at,omitempty"`
	RegisteredBy string `json:"registered_by,omitempty"`
}

func NormalizeExternalToolManifest(m ExternalToolManifest) ExternalToolManifest {
	m.Name = strings.TrimSpace(m.Name)
	m.Owner = strings.TrimSpace(m.Owner)
	m.Version = strings.TrimSpace(m.Version)
	m.Execution.Mode = strings.TrimSpace(strings.ToLower(m.Execution.Mode))
	m.Execution.Entry = strings.TrimSpace(m.Execution.Entry)
	m.Execution.Workdir = strings.TrimSpace(m.Execution.Workdir)
	m.Constraints.Network = strings.TrimSpace(strings.ToLower(m.Constraints.Network))
	m.Constraints.Filesystem = strings.TrimSpace(strings.ToLower(m.Constraints.Filesystem))
	m.Container.Image = strings.TrimSpace(m.Container.Image)
	m.Container.Digest = strings.TrimSpace(m.Container.Digest)
	m.Container.BuildRef = strings.TrimSpace(m.Container.BuildRef)
	if m.Execution.Mode == "container" && m.Container.Image == "" {
		m.Container.Image = m.Execution.Entry
	}
	m.Provenance.RequestID = strings.TrimSpace(m.Provenance.RequestID)
	m.Provenance.RegisteredAt = strings.TrimSpace(m.Provenance.RegisteredAt)
	m.Provenance.RegisteredBy = strings.TrimSpace(m.Provenance.RegisteredBy)
	m.Constraints.NetworkTargets = normalizeStringList(m.Constraints.NetworkTargets)
	if len(m.Install.Command) > 0 {
		m.Install.Command = normalizeStringList(m.Install.Command)
	}
	if len(m.Audit.Command) > 0 {
		m.Audit.Command = normalizeStringList(m.Audit.Command)
	}
	if len(m.Probe.Command) > 0 {
		m.Probe.Command = normalizeStringList(m.Probe.Command)
	}
	if len(m.Rollback.Command) > 0 {
		m.Rollback.Command = normalizeStringList(m.Rollback.Command)
	}
	if len(m.Uninstall.Command) > 0 {
		m.Uninstall.Command = normalizeStringList(m.Uninstall.Command)
	}
	if len(m.Container.Healthcheck.Command) > 0 {
		m.Container.Healthcheck.Command = normalizeStringList(m.Container.Healthcheck.Command)
	}
	return m
}

func validateExternalToolManifest(m ExternalToolManifest) error {
	if m.Name == "" {
		return fmt.Errorf("external tool manifest name is required")
	}
	if m.Owner == "" {
		return fmt.Errorf("external tool manifest owner is required")
	}
	switch m.Execution.Mode {
	case "process", "subprocess", "container", "workspace_runner":
		// ok
	case "":
		return fmt.Errorf("external tool manifest execution.mode is required")
	default:
		return fmt.Errorf("external tool manifest execution.mode %q is not supported", m.Execution.Mode)
	}
	if m.Execution.Entry == "" && strings.TrimSpace(m.Container.Image) == "" {
		return fmt.Errorf("external tool manifest execution.entry is required")
	}
	if m.Execution.Mode == "container" {
		if strings.TrimSpace(m.Container.Image) == "" {
			m.Container.Image = strings.TrimSpace(m.Execution.Entry)
		}
		if strings.TrimSpace(m.Container.Image) == "" {
			return fmt.Errorf("external tool manifest container.image is required for container execution")
		}
	}
	if len(m.IO.InputSchema) > 0 && !json.Valid(m.IO.InputSchema) {
		return fmt.Errorf("external tool manifest io.input_schema must be valid json")
	}
	if len(m.IO.OutputSchema) > 0 && !json.Valid(m.IO.OutputSchema) {
		return fmt.Errorf("external tool manifest io.output_schema must be valid json")
	}
	switch m.Constraints.Network {
	case "", "allowlist", "denylist", "none":
	default:
		return fmt.Errorf("external tool manifest constraints.network %q is not supported", m.Constraints.Network)
	}
	switch m.Constraints.Filesystem {
	case "", "none", "scratch":
	default:
		return fmt.Errorf("external tool manifest constraints.filesystem %q is not supported", m.Constraints.Filesystem)
	}
	return nil
}

func LoadExternalToolManifest(path string) (ExternalToolManifest, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return ExternalToolManifest{}, fmt.Errorf("external tool manifest path is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ExternalToolManifest{}, fmt.Errorf("read external tool manifest: %w", err)
	}
	var manifest ExternalToolManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return ExternalToolManifest{}, fmt.Errorf("decode external tool manifest: %w", err)
	}
	manifest = NormalizeExternalToolManifest(manifest)
	if err := validateExternalToolManifest(manifest); err != nil {
		return ExternalToolManifest{}, err
	}
	return manifest, nil
}

func LoadExternalToolManifestDir(dir string) ([]ExternalToolManifest, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read external tool manifest dir: %w", err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		paths = append(paths, filepath.Join(dir, name))
	}
	sort.Strings(paths)
	out := make([]ExternalToolManifest, 0, len(paths))
	seen := make(map[string]string, len(paths))
	for _, path := range paths {
		manifest, err := LoadExternalToolManifest(path)
		if err != nil {
			return nil, err
		}
		if prior, exists := seen[manifest.Name]; exists {
			return nil, fmt.Errorf("duplicate external tool manifest name %q in %s and %s", manifest.Name, prior, path)
		}
		seen[manifest.Name] = path
		out = append(out, manifest)
	}
	return out, nil
}

func normalizeStringList(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func (r *Registry) externalManifestByName(name string) (ExternalToolManifest, bool) {
	if r == nil {
		return ExternalToolManifest{}, false
	}
	target := strings.TrimSpace(name)
	if target == "" {
		return ExternalToolManifest{}, false
	}
	for _, manifest := range r.externalManifests {
		if strings.EqualFold(strings.TrimSpace(manifest.Name), target) {
			return manifest, true
		}
	}
	return ExternalToolManifest{}, false
}
