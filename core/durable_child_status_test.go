//go:build linux

package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseDurableChildStatusEnvelopeAllowsSpecificPayloadGenerically(t *testing.T) {
	payload := json.RawMessage(`{"surface":"console","status":"ok","details":{"queue":2}}`)
	hash := testPayloadHash(t, payload)
	raw := []byte(`{
		"kind":"durable_child_status",
		"agent_id":"console-child",
		"schema_version":"console.status.v1",
		"generated_at":"2026-04-29T10:00:00Z",
		"capability_posture":"report_only",
		"payload":` + string(payload) + `,
		"payload_hash":"` + hash + `"
	}`)

	env, err := ParseDurableChildStatusEnvelope(raw)
	if err != nil {
		t.Fatalf("ParseDurableChildStatusEnvelope() err = %v", err)
	}
	if env.Kind != DurableChildStatusEnvelopeKind {
		t.Fatalf("kind = %q, want %q", env.Kind, DurableChildStatusEnvelopeKind)
	}
	if env.AgentID != "console-child" || env.SchemaVersion != "console.status.v1" {
		t.Fatalf("env = %+v, want generic child id and schema version", env)
	}
	if env.GeneratedAt.Format(time.RFC3339) != "2026-04-29T10:00:00Z" {
		t.Fatalf("generated_at = %s, want parsed timestamp", env.GeneratedAt.Format(time.RFC3339))
	}
	if !json.Valid(env.Payload) || !bytes.Contains(env.Payload, []byte(`"surface"`)) {
		t.Fatalf("payload = %s, want preserved specific JSON payload", env.Payload)
	}
}

func TestParseDurableChildStatusEnvelopeRejectsUnknownTopLevelFields(t *testing.T) {
	_, err := ParseDurableChildStatusEnvelope([]byte(`{
		"kind":"durable_child_status",
		"agent_id":"child",
		"schema_version":"status.v1",
		"generated_at":"2026-04-29T10:00:00Z",
		"payload":{},
		"console_specific":true
	}`))
	if err == nil {
		t.Fatal("ParseDurableChildStatusEnvelope() err = nil, want unknown field error")
	}
}

func TestParseDurableChildStatusEnvelopeValidatesPayloadHash(t *testing.T) {
	_, err := ParseDurableChildStatusEnvelope([]byte(`{
		"kind":"durable_child_status",
		"agent_id":"child",
		"schema_version":"status.v1",
		"generated_at":"2026-04-29T10:00:00Z",
		"payload":{"ok":true},
		"payload_hash":"sha256:not-the-payload"
	}`))
	if err == nil {
		t.Fatal("ParseDurableChildStatusEnvelope() err = nil, want hash mismatch")
	}
	if !strings.Contains(err.Error(), "payload_hash") {
		t.Fatalf("err = %v, want payload_hash context", err)
	}
}

func TestValidateDurableChildStatusEnvelopeForAgentRejectsMismatch(t *testing.T) {
	env := DurableChildStatusEnvelope{
		Kind:          DurableChildStatusEnvelopeKind,
		AgentID:       "child-a",
		SchemaVersion: "status.v1",
		GeneratedAt:   time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC),
		Payload:       json.RawMessage(`{}`),
	}

	err := ValidateDurableChildStatusEnvelopeForAgent(env, DurableAgent{AgentID: "child-b"})
	if err == nil {
		t.Fatal("ValidateDurableChildStatusEnvelopeForAgent() err = nil, want agent mismatch")
	}
}

func testPayloadHash(t *testing.T, payload json.RawMessage) string {
	t.Helper()
	var compact bytes.Buffer
	if err := json.Compact(&compact, payload); err != nil {
		t.Fatalf("json.Compact() err = %v", err)
	}
	sum := sha256.Sum256(compact.Bytes())
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestParseDurableAgentContinuityStateParsesExternalChannelState(t *testing.T) {
	raw := `{
		"external_channel":{
			"adapter":"codex_app_server",
			"session_ref":" thread-1 ",
			"last_command":"codex_app_server.status_heartbeat",
			"last_attempt_at":"2026-04-29T11:12:35Z",
			"last_success_at":"2026-04-29T11:12:35Z",
			"last_artifact":"artifacts/heartbeats/codex.json",
			"last_status":"ok",
			"adapter_state":{"thread_id":"thread-1","last_turn_id":"turn-1","last_payload_hash":"sha256:abc"}
		}
	}`
	state, err := ParseDurableAgentContinuityState(raw)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if state.ExternalChannel == nil {
		t.Fatal("ExternalChannel = nil, want parsed state")
	}
	if state.ExternalChannel.Adapter != "codex_app_server" || state.ExternalChannel.SessionRef != "thread-1" {
		t.Fatalf("ExternalChannel = %#v, want codex adapter and thread session", state.ExternalChannel)
	}
	if state.ExternalChannel.LastCommand != "codex_app_server.status_heartbeat" || state.ExternalChannel.LastArtifact != "artifacts/heartbeats/codex.json" {
		t.Fatalf("ExternalChannel = %#v, want migrated command/artifact", state.ExternalChannel)
	}
	if got := string(state.ExternalChannel.AdapterState); got == "" || !strings.Contains(got, `"thread_id":"thread-1"`) || !strings.Contains(got, `"last_turn_id":"turn-1"`) {
		t.Fatalf("AdapterState = %s, want compact adapter residue", got)
	}
	marshaled, err := state.Marshal()
	if err != nil {
		t.Fatalf("Marshal() err = %v", err)
	}
	if !strings.Contains(marshaled, "external_channel") {
		t.Fatalf("Marshal() = %s, want external_channel", marshaled)
	}
}
