//go:build linux

package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type ArtifactManifest struct {
	AgentID   string                  `json:"agent_id"`
	UpdatedAt time.Time               `json:"updated_at"`
	Artifacts []ArtifactManifestEntry `json:"artifacts"`
}

type ArtifactManifestEntry struct {
	Path      string    `json:"path"`
	Kind      string    `json:"kind,omitempty"`
	Source    string    `json:"source,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	SHA256    string    `json:"sha256"`
	UpdatedAt time.Time `json:"updated_at"`
}

func LoadArtifactManifest(artifactRoot string, agentID string) (ArtifactManifest, error) {
	manifest := ArtifactManifest{AgentID: strings.TrimSpace(agentID), Artifacts: []ArtifactManifestEntry{}}
	raw, err := os.ReadFile(filepath.Join(artifactRoot, "ARTIFACTS.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return manifest, nil
		}
		return ArtifactManifest{}, fmt.Errorf("read durable agent artifact manifest: %w", err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return ArtifactManifest{}, fmt.Errorf("decode durable agent artifact manifest: %w", err)
	}
	manifest.AgentID = strings.TrimSpace(agentID)
	return manifest, nil
}

func UpsertArtifactManifestEntry(manifest ArtifactManifest, entry ArtifactManifestEntry, updatedAt time.Time) ArtifactManifest {
	for i := range manifest.Artifacts {
		if manifest.Artifacts[i].Path == entry.Path {
			manifest.Artifacts[i] = entry
			manifest.UpdatedAt = updatedAt
			return manifest
		}
	}
	manifest.Artifacts = append(manifest.Artifacts, entry)
	manifest.UpdatedAt = updatedAt
	return manifest
}

func WriteArtifactManifest(artifactRoot string, manifest ArtifactManifest) error {
	if err := os.MkdirAll(artifactRoot, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(artifactRoot, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	path := filepath.Join(artifactRoot, "ARTIFACTS.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func WakeSummary(agent core.DurableAgent, result Result, artifactRel string) string {
	return strings.TrimSpace(fmt.Sprintf("codex_app_server heartbeat received for %s. thread_id=%s turn_id=%s payload_hash=%s artifact=%s", strings.TrimSpace(agent.AgentID), strings.TrimSpace(result.ThreadID), strings.TrimSpace(result.TurnID), firstNonEmpty(strings.TrimSpace(result.Envelope.PayloadHash), strings.TrimSpace(result.PayloadHash)), artifactRel))
}

func SummarizeApprovalDecisions(values []ApprovalDecision) string {
	if len(values) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strings.TrimSpace(value.Method)+":"+strings.TrimSpace(value.Decision))
	}
	return strings.Join(parts, ",")
}
