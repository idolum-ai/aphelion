//go:build linux

package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

const DurableChildStatusEnvelopeKind = "durable_child_status"

type DurableChildStatusEnvelope struct {
	Kind              string          `json:"kind"`
	AgentID           string          `json:"agent_id"`
	SchemaVersion     string          `json:"schema_version"`
	GeneratedAt       time.Time       `json:"generated_at"`
	CapabilityPosture string          `json:"capability_posture,omitempty"`
	Payload           json.RawMessage `json:"payload"`
	PayloadHash       string          `json:"payload_hash,omitempty"`
}

func ParseDurableChildStatusEnvelope(raw []byte) (DurableChildStatusEnvelope, error) {
	var env DurableChildStatusEnvelope
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return DurableChildStatusEnvelope{}, fmt.Errorf("decode durable child status envelope: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return DurableChildStatusEnvelope{}, fmt.Errorf("decode durable child status envelope: trailing JSON")
	}
	env = NormalizeDurableChildStatusEnvelope(env)
	if err := ValidateDurableChildStatusEnvelope(env); err != nil {
		return DurableChildStatusEnvelope{}, err
	}
	if env.PayloadHash != "" {
		got, err := DurableChildStatusPayloadHash(env.Payload)
		if err != nil {
			return DurableChildStatusEnvelope{}, err
		}
		if !strings.EqualFold(env.PayloadHash, got) {
			return DurableChildStatusEnvelope{}, fmt.Errorf("durable child status payload_hash mismatch: got %s want %s", env.PayloadHash, got)
		}
	}
	return env, nil
}

func NormalizeDurableChildStatusEnvelope(env DurableChildStatusEnvelope) DurableChildStatusEnvelope {
	env.Kind = strings.TrimSpace(env.Kind)
	env.AgentID = strings.TrimSpace(env.AgentID)
	env.SchemaVersion = strings.TrimSpace(env.SchemaVersion)
	env.CapabilityPosture = strings.TrimSpace(env.CapabilityPosture)
	env.PayloadHash = strings.TrimSpace(env.PayloadHash)
	env.Payload = json.RawMessage(bytes.TrimSpace(env.Payload))
	return env
}

func ValidateDurableChildStatusEnvelope(env DurableChildStatusEnvelope) error {
	env = NormalizeDurableChildStatusEnvelope(env)
	if env.Kind != DurableChildStatusEnvelopeKind {
		return fmt.Errorf("durable child status kind must be %q", DurableChildStatusEnvelopeKind)
	}
	if env.AgentID == "" {
		return fmt.Errorf("durable child status agent_id is required")
	}
	if env.SchemaVersion == "" {
		return fmt.Errorf("durable child status schema_version is required")
	}
	if env.GeneratedAt.IsZero() {
		return fmt.Errorf("durable child status generated_at is required")
	}
	if len(env.Payload) == 0 || bytes.Equal(env.Payload, []byte("null")) {
		return fmt.Errorf("durable child status payload is required")
	}
	if !json.Valid(env.Payload) {
		return fmt.Errorf("durable child status payload must be valid JSON")
	}
	return nil
}

func ValidateDurableChildStatusEnvelopeForAgent(env DurableChildStatusEnvelope, agent DurableAgent) error {
	env = NormalizeDurableChildStatusEnvelope(env)
	if err := ValidateDurableChildStatusEnvelope(env); err != nil {
		return err
	}
	if strings.TrimSpace(agent.AgentID) == "" {
		return fmt.Errorf("durable child status target agent_id is required")
	}
	if env.AgentID != strings.TrimSpace(agent.AgentID) {
		return fmt.Errorf("durable child status agent_id %q does not match durable agent %q", env.AgentID, strings.TrimSpace(agent.AgentID))
	}
	return nil
}

func DurableChildStatusPayloadHash(payload json.RawMessage) (string, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, bytes.TrimSpace(payload)); err != nil {
		return "", fmt.Errorf("compact durable child status payload: %w", err)
	}
	sum := sha256.Sum256(compact.Bytes())
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
