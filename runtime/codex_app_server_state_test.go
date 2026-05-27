//go:build linux

package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	runtimecodex "github.com/idolum-ai/aphelion/runtime/codex"
)

func TestWriteCodexAppServerHeartbeatArtifactUsesRestrictiveModes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	agent := core.DurableAgent{AgentID: "agent-y"}
	result := runtimecodex.Result{
		EnvelopeRaw: []byte(`{"kind":"durable_child_status","agent_id":"agent-y","schema_version":"status.v1","generated_at":"2026-05-27T00:00:00Z","payload":{}}`),
	}

	rel, _, err := writeCodexAppServerHeartbeatArtifact(root, agent, result, time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("writeCodexAppServerHeartbeatArtifact err = %v", err)
	}
	relPath := strings.TrimPrefix(rel, "artifacts/")
	target := filepath.Join(root, "artifacts", filepath.FromSlash(relPath))

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat artifact err = %v", err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("heartbeat artifact mode = %#o, want no group/other bits set", mode)
	}

	dirInfo, err := os.Stat(filepath.Dir(target))
	if err != nil {
		t.Fatalf("stat artifact dir err = %v", err)
	}
	if mode := dirInfo.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("heartbeat dir mode = %#o, want no group/other bits set", mode)
	}
}

func TestWriteCodexAppServerHeartbeatArtifactTightensExistingPermissiveModes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	heartbeatDir := filepath.Join(root, "artifacts", "heartbeats")
	if err := os.MkdirAll(heartbeatDir, 0o755); err != nil {
		t.Fatalf("MkdirAll err = %v", err)
	}
	target := filepath.Join(heartbeatDir, "codex-app-server-20260527T000000Z.json")
	if err := os.WriteFile(target, []byte(`{"old":true}`), 0o644); err != nil {
		t.Fatalf("WriteFile err = %v", err)
	}
	if err := os.Chmod(heartbeatDir, 0o755); err != nil {
		t.Fatalf("chmod dir err = %v", err)
	}
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatalf("chmod artifact err = %v", err)
	}

	agent := core.DurableAgent{AgentID: "agent-y"}
	result := runtimecodex.Result{
		EnvelopeRaw: []byte(`{"kind":"durable_child_status","agent_id":"agent-y","schema_version":"status.v1","generated_at":"2026-05-27T00:00:00Z","payload":{}}`),
	}
	if _, _, err := writeCodexAppServerHeartbeatArtifact(root, agent, result, time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("writeCodexAppServerHeartbeatArtifact err = %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat artifact err = %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("heartbeat artifact mode = %#o, want 0600", mode)
	}
	dirInfo, err := os.Stat(heartbeatDir)
	if err != nil {
		t.Fatalf("stat artifact dir err = %v", err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Fatalf("heartbeat dir mode = %#o, want 0700", mode)
	}
}
