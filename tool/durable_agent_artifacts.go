//go:build linux

package tool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

const durableAgentArtifactManifestFile = "ARTIFACTS.json"
const durableAgentArtifactSource = "parent_governed_artifact"

type durableAgentArtifactManifest struct {
	AgentID   string                              `json:"agent_id"`
	UpdatedAt time.Time                           `json:"updated_at"`
	Artifacts []durableAgentArtifactManifestEntry `json:"artifacts"`
}

type durableAgentArtifactManifestEntry struct {
	Path      string    `json:"path"`
	Kind      string    `json:"kind,omitempty"`
	Source    string    `json:"source,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	SHA256    string    `json:"sha256"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (r *Registry) putDurableAgentArtifact(in durableAgentInput) (string, error) {
	agent, memoryRoot, err := r.resolveDurableAgentArtifactRoot(in.AgentID)
	if err != nil {
		return "", err
	}
	if in.Artifact == nil {
		return "", fmt.Errorf("durable_agent artifact_put requires artifact")
	}
	rel, err := cleanDurableAgentArtifactPath(in.Artifact.Path)
	if err != nil {
		return "", err
	}
	artifactRoot := filepath.Join(memoryRoot, "artifacts")
	artifactRoot, err = safeDirectoryUnderRootNoSymlink(memoryRoot, "artifacts")
	if err != nil {
		return "", err
	}
	content := []byte(in.Artifact.Content)
	if _, err := safeWriteFileUnderRootNoSymlink(memoryRoot, filepath.ToSlash(filepath.Join("artifacts", rel)), content, 0o644); err != nil {
		return "", fmt.Errorf("write durable agent artifact: %w", err)
	}

	now := time.Now().UTC()
	sum := sha256.Sum256(content)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	reason := firstNonEmpty(strings.TrimSpace(in.Artifact.Reason), strings.TrimSpace(in.Reason))
	manifest, err := loadDurableAgentArtifactManifest(artifactRoot, agent.AgentID)
	if err != nil {
		return "", err
	}
	manifest = upsertDurableAgentArtifactManifestEntry(manifest, durableAgentArtifactManifestEntry{
		Path:      rel,
		Kind:      strings.TrimSpace(in.Artifact.Kind),
		Source:    durableAgentArtifactSource,
		Reason:    reason,
		SHA256:    hash,
		UpdatedAt: now,
	}, now)
	if err := writeDurableAgentArtifactManifest(memoryRoot, manifest); err != nil {
		return "", err
	}

	return renderDurableAgentArtifactPut(*agent, artifactRoot, rel, hash), nil
}

func (r *Registry) listDurableAgentArtifacts(in durableAgentInput) (string, error) {
	agent, memoryRoot, err := r.resolveDurableAgentArtifactRoot(in.AgentID)
	if err != nil {
		return "", err
	}
	artifactRoot := filepath.Join(memoryRoot, "artifacts")
	artifactRoot, err = safeDirectoryUnderRootNoSymlink(memoryRoot, "artifacts")
	if err != nil {
		return "", err
	}
	manifest, err := loadDurableAgentArtifactManifest(artifactRoot, agent.AgentID)
	if err != nil {
		return "", err
	}
	return renderDurableAgentArtifactList(*agent, artifactRoot, manifest), nil
}

func (r *Registry) showDurableAgentArtifact(in durableAgentInput) (string, error) {
	agent, memoryRoot, err := r.resolveDurableAgentArtifactRoot(in.AgentID)
	if err != nil {
		return "", err
	}
	if in.Artifact == nil {
		return "", fmt.Errorf("durable_agent artifact_show requires artifact")
	}
	rel, err := cleanDurableAgentArtifactPath(in.Artifact.Path)
	if err != nil {
		return "", err
	}
	artifactRoot := filepath.Join(memoryRoot, "artifacts")
	artifactRoot, err = safeDirectoryUnderRootNoSymlink(memoryRoot, "artifacts")
	if err != nil {
		return "", err
	}
	targetPath, err := durableAgentArtifactTargetPath(artifactRoot, rel)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(targetPath)
	if err != nil {
		return "", fmt.Errorf("read durable agent artifact: %w", err)
	}
	return renderDurableAgentArtifactShow(*agent, artifactRoot, rel, raw), nil
}

func (r *Registry) resolveDurableAgentArtifactRoot(agentID string) (*core.DurableAgent, string, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, "", fmt.Errorf("durable_agent agent_id is required for artifact actions")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return nil, "", err
	}
	memoryRoot, err := durableAgentMemoryRoot(*agent, r.store)
	if err != nil {
		return nil, "", err
	}
	return agent, memoryRoot, nil
}

func cleanDurableAgentArtifactPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("durable agent artifact path is required")
	}
	if strings.Contains(raw, "\x00") || strings.Contains(raw, "\\") {
		return "", fmt.Errorf("durable agent artifact path %q is not allowed", raw)
	}
	raw = strings.TrimPrefix(filepath.ToSlash(raw), "artifacts/")
	if strings.HasPrefix(raw, "/") || filepath.IsAbs(raw) {
		return "", fmt.Errorf("durable agent artifact path %q must be relative", raw)
	}
	clean := path.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("durable agent artifact path %q must stay under artifacts/", raw)
	}
	if strings.EqualFold(clean, durableAgentArtifactManifestFile) {
		return "", fmt.Errorf("durable agent artifact path %q is reserved", clean)
	}
	return clean, nil
}

func durableAgentArtifactTargetPath(artifactRoot string, rel string) (string, error) {
	root := filepath.Clean(artifactRoot)
	target := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	targetRel, err := filepath.Rel(root, target)
	if err != nil {
		return "", fmt.Errorf("resolve durable agent artifact path: %w", err)
	}
	if targetRel == ".." || strings.HasPrefix(targetRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("durable agent artifact path %q escapes artifact root", rel)
	}
	return target, nil
}

func loadDurableAgentArtifactManifest(artifactRoot string, agentID string) (durableAgentArtifactManifest, error) {
	manifest := durableAgentArtifactManifest{
		AgentID:   strings.TrimSpace(agentID),
		Artifacts: []durableAgentArtifactManifestEntry{},
	}
	raw, err := os.ReadFile(filepath.Join(artifactRoot, durableAgentArtifactManifestFile))
	if err != nil {
		if os.IsNotExist(err) {
			return manifest, nil
		}
		return durableAgentArtifactManifest{}, fmt.Errorf("read durable agent artifact manifest: %w", err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return durableAgentArtifactManifest{}, fmt.Errorf("decode durable agent artifact manifest: %w", err)
	}
	manifest.AgentID = strings.TrimSpace(agentID)
	if manifest.Artifacts == nil {
		manifest.Artifacts = []durableAgentArtifactManifestEntry{}
	}
	sort.SliceStable(manifest.Artifacts, func(i, j int) bool {
		return manifest.Artifacts[i].Path < manifest.Artifacts[j].Path
	})
	return manifest, nil
}

func upsertDurableAgentArtifactManifestEntry(manifest durableAgentArtifactManifest, entry durableAgentArtifactManifestEntry, updatedAt time.Time) durableAgentArtifactManifest {
	entry.Path = strings.TrimSpace(entry.Path)
	entry.Kind = strings.TrimSpace(entry.Kind)
	entry.Source = firstNonEmpty(strings.TrimSpace(entry.Source), durableAgentArtifactSource)
	entry.Reason = strings.TrimSpace(entry.Reason)
	entry.SHA256 = strings.TrimSpace(entry.SHA256)
	for i := range manifest.Artifacts {
		if manifest.Artifacts[i].Path == entry.Path {
			manifest.Artifacts[i] = entry
			manifest.UpdatedAt = updatedAt
			sort.SliceStable(manifest.Artifacts, func(i, j int) bool {
				return manifest.Artifacts[i].Path < manifest.Artifacts[j].Path
			})
			return manifest
		}
	}
	manifest.Artifacts = append(manifest.Artifacts, entry)
	manifest.UpdatedAt = updatedAt
	sort.SliceStable(manifest.Artifacts, func(i, j int) bool {
		return manifest.Artifacts[i].Path < manifest.Artifacts[j].Path
	})
	return manifest
}

func writeDurableAgentArtifactManifest(memoryRoot string, manifest durableAgentArtifactManifest) error {
	if _, err := safeDirectoryUnderRootNoSymlink(memoryRoot, "artifacts"); err != nil {
		return fmt.Errorf("create durable agent artifact root: %w", err)
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode durable agent artifact manifest: %w", err)
	}
	raw = append(raw, '\n')
	if _, err := safeWriteFileUnderRootNoSymlink(memoryRoot, filepath.ToSlash(filepath.Join("artifacts", durableAgentArtifactManifestFile)), raw, 0o644); err != nil {
		return fmt.Errorf("write durable agent artifact manifest: %w", err)
	}
	return nil
}

func renderDurableAgentArtifactPut(agent core.DurableAgent, artifactRoot string, rel string, hash string) string {
	var b strings.Builder
	b.WriteString("action: durable-agent artifact put\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "artifact_root: %s\n", artifactRoot)
	fmt.Fprintf(&b, "written: artifacts/%s\n", rel)
	fmt.Fprintf(&b, "manifest: artifacts/%s\n", durableAgentArtifactManifestFile)
	fmt.Fprintf(&b, "sha256: %s\n", hash)
	return b.String()
}

func renderDurableAgentArtifactList(agent core.DurableAgent, artifactRoot string, manifest durableAgentArtifactManifest) string {
	var b strings.Builder
	b.WriteString("action: durable-agent artifact list\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "artifact_root: %s\n", artifactRoot)
	fmt.Fprintf(&b, "count: %d\n", len(manifest.Artifacts))
	b.WriteString("artifacts:\n")
	if len(manifest.Artifacts) == 0 {
		b.WriteString("- none\n")
		return b.String()
	}
	for _, entry := range manifest.Artifacts {
		fmt.Fprintf(
			&b,
			"- path=artifacts/%s kind=%s sha256=%s updated_at=%s reason=%s\n",
			entry.Path,
			firstNonEmpty(entry.Kind, "-"),
			firstNonEmpty(entry.SHA256, "-"),
			entry.UpdatedAt.UTC().Format(time.RFC3339),
			firstNonEmpty(compactWhitespace(entry.Reason), "-"),
		)
	}
	return b.String()
}

func renderDurableAgentArtifactShow(agent core.DurableAgent, artifactRoot string, rel string, raw []byte) string {
	sum := sha256.Sum256(raw)
	content := string(raw)
	truncated := false
	if len(content) > 12000 {
		content = content[:12000]
		truncated = true
	}
	var b strings.Builder
	b.WriteString("action: durable-agent artifact show\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "artifact_root: %s\n", artifactRoot)
	fmt.Fprintf(&b, "path: artifacts/%s\n", rel)
	fmt.Fprintf(&b, "bytes: %d\n", len(raw))
	fmt.Fprintf(&b, "sha256: sha256:%s\n", hex.EncodeToString(sum[:]))
	if truncated {
		b.WriteString("truncated: true\n")
	}
	b.WriteString("content:\n")
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}
