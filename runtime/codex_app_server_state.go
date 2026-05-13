//go:build linux

package runtime

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func recordCodexAppServerFailure(store *session.SQLiteStore, state *core.DurableAgentState, continuity core.DurableAgentContinuityState, runtimeState core.DurableAgentExternalChannelRuntimeState, memoryRoot string, agent core.DurableAgent, result codexAppServerResult, cause error, now time.Time) error {
	if store == nil || state == nil {
		return fmt.Errorf("record codex app-server failure: store/state unavailable")
	}
	if cause == nil {
		cause = fmt.Errorf("codex app-server failure")
	}
	now = now.UTC()
	codexState := decodeCodexAdapterState(runtimeState.AdapterState)
	if codexAppServerFailureShouldResetThread(cause) {
		runtimeState.SessionRef = ""
		codexState.ThreadID = ""
	}
	artifactRel := ""
	if rel, _, err := writeCodexAppServerFailureArtifact(memoryRoot, agent, result, cause, now); err == nil && strings.TrimSpace(rel) != "" {
		artifactRel = rel
	}
	runtimeState = externalChannelRecordFailure(runtimeState, externalChannelCommandLifecycle{
		Adapter:      codexAppServerAdapterName,
		Command:      codexAppServerStatusCommandName,
		SessionRef:   runtimeState.SessionRef,
		LastArtifact: artifactRel,
		LastStatus:   "blocked",
		LastError:    truncateRunes(cause.Error(), 900),
	}, now)
	continuity.ExternalChannel = encodeCodexExternalChannelState(runtimeState, codexState)
	raw, err := continuity.Marshal()
	if err != nil {
		return err
	}
	state.StateJSON = raw
	return store.SaveDurableAgentState(*state)
}

func codexAppServerFailureShouldResetThread(cause error) bool {
	msg := strings.ToLower(strings.TrimSpace(cause.Error()))
	return strings.Contains(msg, "resume codex app-server thread") ||
		strings.Contains(msg, "read limited at") ||
		strings.Contains(msg, "unexpected rsv bits") ||
		strings.Contains(msg, "payload_hash mismatch")
}

func writeCodexAppServerFailureArtifact(memoryRoot string, agent core.DurableAgent, result codexAppServerResult, cause error, now time.Time) (string, string, error) {
	if strings.TrimSpace(memoryRoot) == "" {
		return "", "", nil
	}
	payload := map[string]any{
		"kind":          "codex_app_server_failure",
		"agent_id":      strings.TrimSpace(agent.AgentID),
		"recorded_at":   now.UTC().Format(time.RFC3339),
		"error":         truncateRunes(cause.Error(), 2000),
		"thread_id":     strings.TrimSpace(result.ThreadID),
		"turn_id":       strings.TrimSpace(result.TurnID),
		"text_excerpt":  truncateRunes(result.Text, 4000),
		"envelope_raw":  string(bytes.TrimSpace(result.EnvelopeRaw)),
		"approval_log":  result.ApprovalLog,
		"notifications": result.Notifications,
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", "", err
	}
	raw = append(raw, '\n')
	date := now.UTC().Format("20060102T150405Z")
	rel := filepath.ToSlash(filepath.Join("heartbeats", fmt.Sprintf("codex-app-server-failure-%s.json", date)))
	artifactRoot := filepath.Join(memoryRoot, "artifacts")
	target := filepath.Join(artifactRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(target, raw, 0o644); err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(raw)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	manifest, err := loadCodexAppServerArtifactManifest(artifactRoot, agent.AgentID)
	if err != nil {
		return "", "", err
	}
	manifest = upsertCodexAppServerArtifactManifestEntry(manifest, codexAppServerArtifactManifestEntry{
		Path:      rel,
		Kind:      "failure_quarantine",
		Source:    codexAppServerAdapterName,
		Reason:    "Codex app-server heartbeat failure quarantined instead of retry-spamming.",
		SHA256:    hash,
		UpdatedAt: now.UTC(),
	}, now.UTC())
	if err := writeCodexAppServerArtifactManifest(artifactRoot, manifest); err != nil {
		return "", "", err
	}
	return "artifacts/" + rel, hash, nil
}

func loadDurableAgentContinuityFromStore(store *session.SQLiteStore, agentID string) (*core.DurableAgentState, core.DurableAgentContinuityState, error) {
	state, err := store.DurableAgentState(strings.TrimSpace(agentID))
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) && !strings.Contains(err.Error(), "no rows") {
			return nil, core.DurableAgentContinuityState{}, err
		}
		state = &core.DurableAgentState{AgentID: strings.TrimSpace(agentID)}
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		return nil, core.DurableAgentContinuityState{}, err
	}
	return state, continuity, nil
}

type codexAppServerAdapterState struct {
	ThreadID        string `json:"thread_id,omitempty"`
	LastTurnID      string `json:"last_turn_id,omitempty"`
	LastPayloadHash string `json:"last_payload_hash,omitempty"`
}

const codexAppServerStatusCommandName = "codex_app_server.status_heartbeat"

func decodeCodexAdapterState(raw json.RawMessage) codexAppServerAdapterState {
	var state codexAppServerAdapterState
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = json.Unmarshal(raw, &state)
	}
	state.ThreadID = strings.TrimSpace(state.ThreadID)
	state.LastTurnID = strings.TrimSpace(state.LastTurnID)
	state.LastPayloadHash = strings.TrimSpace(state.LastPayloadHash)
	return state
}

func encodeCodexExternalChannelState(runtimeState core.DurableAgentExternalChannelRuntimeState, codexState codexAppServerAdapterState) *core.DurableAgentExternalChannelRuntimeState {
	codexState.ThreadID = strings.TrimSpace(codexState.ThreadID)
	codexState.LastTurnID = strings.TrimSpace(codexState.LastTurnID)
	codexState.LastPayloadHash = strings.TrimSpace(codexState.LastPayloadHash)
	if strings.TrimSpace(runtimeState.SessionRef) == "" {
		runtimeState.SessionRef = codexState.ThreadID
	}
	runtimeState.Adapter = codexAppServerAdapterName
	raw, _ := json.Marshal(codexState)
	runtimeState.AdapterState = json.RawMessage(raw)
	return core.NormalizeDurableAgentContinuityState(core.DurableAgentContinuityState{ExternalChannel: &runtimeState}).ExternalChannel
}

func writeCodexAppServerHeartbeatArtifact(memoryRoot string, agent core.DurableAgent, result codexAppServerResult, now time.Time) (string, string, error) {
	envelopeRaw := bytes.TrimSpace(result.EnvelopeRaw)
	if len(envelopeRaw) == 0 {
		envelopeRaw = bytes.TrimSpace([]byte(result.Text))
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, envelopeRaw, "", "  "); err != nil {
		pretty.Write(envelopeRaw)
	}
	date := now.UTC().Format("20060102T150405Z")
	rel := filepath.ToSlash(filepath.Join("heartbeats", fmt.Sprintf("codex-app-server-%s.json", date)))
	artifactRoot := filepath.Join(memoryRoot, "artifacts")
	target := filepath.Join(artifactRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", "", fmt.Errorf("create codex app-server heartbeat artifact dir: %w", err)
	}
	content := append(pretty.Bytes(), '\n')
	if err := os.WriteFile(target, content, 0o644); err != nil {
		return "", "", fmt.Errorf("write codex app-server heartbeat artifact: %w", err)
	}
	sum := sha256.Sum256(content)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	manifest, err := loadCodexAppServerArtifactManifest(artifactRoot, agent.AgentID)
	if err != nil {
		return "", "", err
	}
	manifest = upsertCodexAppServerArtifactManifestEntry(manifest, codexAppServerArtifactManifestEntry{
		Path:      rel,
		Kind:      "heartbeat_envelope",
		Source:    codexAppServerAdapterName,
		Reason:    "Read-only durable_child_status envelope collected through generic codex_app_server adapter.",
		SHA256:    hash,
		UpdatedAt: now.UTC(),
	}, now.UTC())
	if err := writeCodexAppServerArtifactManifest(artifactRoot, manifest); err != nil {
		return "", "", err
	}
	return "artifacts/" + rel, hash, nil
}
