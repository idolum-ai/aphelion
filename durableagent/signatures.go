//go:build linux

package durableagent

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

const durableAgentHMACPrefix = "hmac-sha256:"

type signedEnvelopePayload struct {
	ProtocolVersion string `json:"protocol_version,omitempty"`
	AgentID         string `json:"agent_id,omitempty"`
	ParentAgentID   string `json:"parent_agent_id,omitempty"`
	MessageKind     string `json:"message_kind,omitempty"`
	MessageID       string `json:"message_id,omitempty"`
	Sequence        int64  `json:"sequence,omitempty"`
	Timestamp       string `json:"timestamp,omitempty"`
	Payload         any    `json:"payload,omitempty"`
}

func SignEnvelopeHMAC(secret string, envelope core.DurableAgentControlEnvelope, payload any) (string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", fmt.Errorf("durable agent control-plane secret is required")
	}
	raw, err := marshalSignedEnvelopePayload(envelope, payload)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write(raw); err != nil {
		return "", fmt.Errorf("sign durable agent envelope: %w", err)
	}
	return durableAgentHMACPrefix + hex.EncodeToString(mac.Sum(nil)), nil
}

func VerifyEnvelopeHMAC(secret string, envelope core.DurableAgentControlEnvelope, payload any) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return fmt.Errorf("durable agent control-plane secret is required")
	}
	expected, err := SignEnvelopeHMAC(secret, envelope, payload)
	if err != nil {
		return err
	}
	actual := strings.TrimSpace(envelope.Signature)
	if actual == "" {
		return fmt.Errorf("invalid signature")
	}
	if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
		return fmt.Errorf("invalid signature")
	}
	return nil
}

func marshalSignedEnvelopePayload(envelope core.DurableAgentControlEnvelope, payload any) ([]byte, error) {
	envelope = core.NormalizeDurableAgentControlEnvelope(envelope)
	envelope.Signature = ""
	body := signedEnvelopePayload{
		ProtocolVersion: envelope.ProtocolVersion,
		AgentID:         envelope.AgentID,
		ParentAgentID:   envelope.ParentAgentID,
		MessageKind:     envelope.MessageKind,
		MessageID:       envelope.MessageID,
		Sequence:        envelope.Sequence,
		Timestamp:       envelope.Timestamp.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		Payload:         payload,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal durable agent signed envelope payload: %w", err)
	}
	return raw, nil
}
