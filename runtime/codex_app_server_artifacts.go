//go:build linux

package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type codexAppServerArtifactManifest struct {
	AgentID   string                                `json:"agent_id"`
	UpdatedAt time.Time                             `json:"updated_at"`
	Artifacts []codexAppServerArtifactManifestEntry `json:"artifacts"`
}

type codexAppServerArtifactManifestEntry struct {
	Path      string    `json:"path"`
	Kind      string    `json:"kind,omitempty"`
	Source    string    `json:"source,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	SHA256    string    `json:"sha256"`
	UpdatedAt time.Time `json:"updated_at"`
}

func loadCodexAppServerArtifactManifest(artifactRoot string, agentID string) (codexAppServerArtifactManifest, error) {
	manifest := codexAppServerArtifactManifest{AgentID: strings.TrimSpace(agentID), Artifacts: []codexAppServerArtifactManifestEntry{}}
	raw, err := os.ReadFile(filepath.Join(artifactRoot, "ARTIFACTS.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return manifest, nil
		}
		return codexAppServerArtifactManifest{}, fmt.Errorf("read durable agent artifact manifest: %w", err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return codexAppServerArtifactManifest{}, fmt.Errorf("decode durable agent artifact manifest: %w", err)
	}
	manifest.AgentID = strings.TrimSpace(agentID)
	return manifest, nil
}

func upsertCodexAppServerArtifactManifestEntry(manifest codexAppServerArtifactManifest, entry codexAppServerArtifactManifestEntry, updatedAt time.Time) codexAppServerArtifactManifest {
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

func writeCodexAppServerArtifactManifest(artifactRoot string, manifest codexAppServerArtifactManifest) error {
	if err := os.MkdirAll(artifactRoot, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(filepath.Join(artifactRoot, "ARTIFACTS.json"), raw, 0o644)
}

func codexAppServerWakeSummary(agent core.DurableAgent, result codexAppServerResult, artifactRel string) string {
	return strings.TrimSpace(fmt.Sprintf("codex_app_server heartbeat received for %s. thread_id=%s turn_id=%s payload_hash=%s artifact=%s", strings.TrimSpace(agent.AgentID), strings.TrimSpace(result.ThreadID), strings.TrimSpace(result.TurnID), firstNonEmpty(strings.TrimSpace(result.Envelope.PayloadHash), strings.TrimSpace(result.PayloadHash)), artifactRel))
}

func summarizeCodexApprovalDecisions(values []codexAppServerApprovalDecision) string {
	if len(values) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strings.TrimSpace(value.Method)+":"+strings.TrimSpace(value.Decision))
	}
	return strings.Join(parts, ",")
}
