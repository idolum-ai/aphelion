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
	Audit       ExternalToolManifestAudit       `json:"audit,omitempty"`
	Probe       ExternalToolManifestProbe       `json:"probe,omitempty"`
}

type externalToolFingerprintIO struct {
	InputSchema  string `json:"input_schema,omitempty"`
	OutputSchema string `json:"output_schema,omitempty"`
}

type externalToolFingerprintSet struct {
	Aggregate            string
	InstallRef           string
	ManifestHash         string
	WorkspaceFingerprint string
}

type externalToolWorkspaceFingerprintInput struct {
	Mode    string                         `json:"mode"`
	Workdir string                         `json:"workdir,omitempty"`
	Files   []externalToolFileFingerprint  `json:"files,omitempty"`
	Image   *externalToolContainerIdentity `json:"image,omitempty"`
}

type externalToolFileFingerprint struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   string `json:"mode"`
}

type externalToolContainerIdentity struct {
	Image       string                              `json:"image,omitempty"`
	Digest      string                              `json:"digest,omitempty"`
	BuildRef    string                              `json:"build_ref,omitempty"`
	Healthcheck ExternalToolManifestContainerHealth `json:"healthcheck,omitempty"`
}

func externalToolFingerprints(manifest ExternalToolManifest, workingRoot string, installRef string) (externalToolFingerprintSet, error) {
	manifest = NormalizeExternalToolManifest(manifest)
	if err := validateExternalToolManifest(manifest); err != nil {
		return externalToolFingerprintSet{}, err
	}
	manifestHash, err := externalToolManifestHash(manifest)
	if err != nil {
		return externalToolFingerprintSet{}, err
	}
	workspaceFingerprint, err := externalToolWorkspaceFingerprint(manifest, workingRoot)
	if err != nil {
		return externalToolFingerprintSet{}, err
	}
	installRef = strings.TrimSpace(installRef)
	aggregate, err := hashCanonicalJSON(struct {
		InstallRef           string `json:"install_ref,omitempty"`
		ManifestHash         string `json:"manifest_hash"`
		WorkspaceFingerprint string `json:"workspace_fingerprint,omitempty"`
	}{
		InstallRef:           installRef,
		ManifestHash:         manifestHash,
		WorkspaceFingerprint: workspaceFingerprint,
	})
	if err != nil {
		return externalToolFingerprintSet{}, fmt.Errorf("external tool %q fingerprint encode failed: %w", manifest.Name, err)
	}
	return externalToolFingerprintSet{
		Aggregate:            aggregate,
		InstallRef:           installRef,
		ManifestHash:         manifestHash,
		WorkspaceFingerprint: workspaceFingerprint,
	}, nil
}

func externalToolManifestHash(manifest ExternalToolManifest) (string, error) {
	manifest = NormalizeExternalToolManifest(manifest)
	payload := externalToolFingerprintInput{
		Name:      manifest.Name,
		Owner:     manifest.Owner,
		Version:   manifest.Version,
		Execution: manifest.Execution,
		IO: externalToolFingerprintIO{
			InputSchema:  canonicalRawJSON(manifest.IO.InputSchema),
			OutputSchema: canonicalRawJSON(manifest.IO.OutputSchema),
		},
		Constraints: manifest.Constraints,
		Install:     manifest.Install,
		Audit:       manifest.Audit,
		Probe:       manifest.Probe,
	}
	return hashCanonicalJSON(payload)
}

func externalToolWorkspaceFingerprint(manifest ExternalToolManifest, workingRoot string) (string, error) {
	manifest = NormalizeExternalToolManifest(manifest)
	workdir, err := resolveWorkdir(workingRoot, manifest.Execution.Workdir)
	if err != nil {
		return "", err
	}
	payload := externalToolWorkspaceFingerprintInput{
		Mode:    manifest.Execution.Mode,
		Workdir: strings.TrimSpace(manifest.Execution.Workdir),
	}
	switch manifest.Execution.Mode {
	case "process", "subprocess":
		files, err := externalToolLocalFileFingerprints(manifest, workdir)
		if err != nil {
			return "", err
		}
		payload.Files = files
	case "container":
		payload.Image = &externalToolContainerIdentity{
			Image:       strings.TrimSpace(manifest.Container.Image),
			Digest:      strings.TrimSpace(manifest.Container.Digest),
			BuildRef:    strings.TrimSpace(manifest.Container.BuildRef),
			Healthcheck: manifest.Container.Healthcheck,
		}
	default:
		return "", nil
	}
	return hashCanonicalJSON(payload)
}

func hashCanonicalJSON(payload any) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func externalToolLocalFileFingerprints(manifest ExternalToolManifest, workdir string) ([]externalToolFileFingerprint, error) {
	candidates := make([]string, 0, 4)
	if first := firstCommandToken(manifest.Execution.Entry); first != "" {
		candidates = append(candidates, first)
	}
	for _, command := range [][]string{manifest.Install.Command, manifest.Audit.Command, manifest.Probe.Command} {
		if len(command) > 0 {
			candidates = append(candidates, command[0])
		}
	}
	seen := make(map[string]struct{}, len(candidates))
	files := make([]externalToolFileFingerprint, 0, len(candidates))
	for _, candidate := range candidates {
		fp, ok, err := externalToolLocalFileFingerprint(manifest.Name, candidate, workdir)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if _, exists := seen[fp.Path]; exists {
			continue
		}
		seen[fp.Path] = struct{}{}
		files = append(files, *fp)
	}
	return files, nil
}

func firstCommandToken(command string) string {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func externalToolLocalFileFingerprint(toolName string, target string, workdir string) (*externalToolFileFingerprint, bool, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, false, nil
	}
	var resolved string
	logicalPath := target
	switch {
	case strings.HasPrefix(target, "./") || strings.HasPrefix(target, "../"):
		path, err := resolveWorkdir(workdir, target)
		if err != nil {
			return nil, true, err
		}
		resolved = path
		logicalPath = filepath.Clean(target)
	case strings.HasPrefix(target, "/"):
		resolved = target
		logicalPath = filepath.Clean(target)
	default:
		return nil, false, nil
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, true, fmt.Errorf("external tool %q fingerprint stat entry %q: %w", toolName, resolved, err)
	}
	if info.IsDir() {
		return nil, true, fmt.Errorf("external tool %q fingerprint entry %q is a directory", toolName, resolved)
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return nil, true, fmt.Errorf("external tool %q fingerprint read entry %q: %w", toolName, resolved, err)
	}
	sum := sha256.Sum256(raw)
	return &externalToolFileFingerprint{
		Path:   logicalPath,
		SHA256: "sha256:" + hex.EncodeToString(sum[:]),
		Size:   info.Size(),
		Mode:   info.Mode().Perm().String(),
	}, true, nil
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
