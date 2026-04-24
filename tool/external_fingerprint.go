//go:build linux

package tool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type externalToolFingerprintInput struct {
	Name        string                          `json:"name"`
	Owner       string                          `json:"owner"`
	Version     string                          `json:"version,omitempty"`
	Execution   ExternalToolManifestExecution   `json:"execution"`
	IO          externalToolFingerprintIO       `json:"io"`
	Constraints ExternalToolManifestConstraints `json:"constraints,omitempty"`
	Install     ExternalToolManifestInstall     `json:"install,omitempty"`
	Probe       ExternalToolManifestProbe       `json:"probe,omitempty"`
	EntryFile   *externalToolFileFingerprint    `json:"entry_file,omitempty"`
}

type externalToolFingerprintIO struct {
	InputSchema  string `json:"input_schema,omitempty"`
	OutputSchema string `json:"output_schema,omitempty"`
}

type externalToolFileFingerprint struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   string `json:"mode"`
}

func externalToolFingerprint(manifest ExternalToolManifest, workingRoot string) (string, error) {
	manifest = NormalizeExternalToolManifest(manifest)
	if err := validateExternalToolManifest(manifest); err != nil {
		return "", err
	}
	workdir, err := resolveWorkdir(workingRoot, manifest.Execution.Workdir)
	if err != nil {
		return "", err
	}
	entryFile, err := externalToolEntryFileFingerprint(manifest, workdir)
	if err != nil {
		return "", err
	}
	payload := externalToolFingerprintInput{
		Name:    manifest.Name,
		Owner:   manifest.Owner,
		Version: manifest.Version,
		Execution: ExternalToolManifestExecution{
			Mode:           manifest.Execution.Mode,
			Entry:          manifest.Execution.Entry,
			Workdir:        manifest.Execution.Workdir,
			TimeoutSeconds: manifest.Execution.TimeoutSeconds,
		},
		IO: externalToolFingerprintIO{
			InputSchema:  canonicalRawJSON(manifest.IO.InputSchema),
			OutputSchema: canonicalRawJSON(manifest.IO.OutputSchema),
		},
		Constraints: manifest.Constraints,
		Install:     manifest.Install,
		Probe:       manifest.Probe,
		EntryFile:   entryFile,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("external tool %q fingerprint encode failed: %w", manifest.Name, err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func externalToolEntryFileFingerprint(manifest ExternalToolManifest, workdir string) (*externalToolFileFingerprint, error) {
	if manifest.Execution.Mode != "process" && manifest.Execution.Mode != "subprocess" {
		return nil, nil
	}
	fields := strings.Fields(manifest.Execution.Entry)
	if len(fields) == 0 {
		return nil, fmt.Errorf("external tool %q execution entry is empty", manifest.Name)
	}
	target := fields[0]
	var resolved string
	switch {
	case strings.HasPrefix(target, "./") || strings.HasPrefix(target, "../"):
		path, err := resolveWorkdir(workdir, target)
		if err != nil {
			return nil, err
		}
		resolved = path
	case strings.HasPrefix(target, "/"):
		resolved = target
	default:
		return nil, nil
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("external tool %q fingerprint stat entry %q: %w", manifest.Name, resolved, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("external tool %q fingerprint entry %q is a directory", manifest.Name, resolved)
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("external tool %q fingerprint read entry %q: %w", manifest.Name, resolved, err)
	}
	sum := sha256.Sum256(raw)
	return &externalToolFileFingerprint{
		Path:   filepath.Clean(resolved),
		SHA256: "sha256:" + hex.EncodeToString(sum[:]),
		Size:   info.Size(),
		Mode:   info.Mode().Perm().String(),
	}, nil
}

func canonicalRawJSON(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return ""
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	return string(encoded)
}
