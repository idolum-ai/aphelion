//go:build linux

package durableagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

const forensicRefPrefix = "forensic://durable-agent/"

type ForensicRecord struct {
	AgentID        string            `json:"agent_id"`
	Reason         string            `json:"reason"`
	CreatedAt      time.Time         `json:"created_at"`
	RedactedFields []string          `json:"redacted_fields,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Payload        map[string]string `json:"payload,omitempty"`
}

var secretLikePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bsk-[A-Za-z0-9_-]{12,}\b`),
	regexp.MustCompile(`(?i)\bxi-[A-Za-z0-9_-]{12,}\b`),
	regexp.MustCompile(`(?i)\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)\bgh[pousr]_[A-Za-z0-9_]{16,}\b`),
	regexp.MustCompile(`(?i)\b(?:password|token|secret|api key|credential|ssh key|private key)\b`),
}

func PrepareReviewArtifact(agent core.DurableAgent, artifact core.DurableReviewArtifact) (core.DurableReviewArtifact, error) {
	artifact.AgentID = firstNonEmpty(strings.TrimSpace(artifact.AgentID), strings.TrimSpace(agent.AgentID))
	artifact.LocalActions = cloneStrings(artifact.LocalActions)
	artifact.Questions = cloneStrings(artifact.Questions)
	artifact.RiskFlags = cloneStrings(artifact.RiskFlags)
	artifact.ArtifactRefs = cloneStrings(artifact.ArtifactRefs)
	artifact.Metadata = cloneStringMap(artifact.Metadata)

	payload := make(map[string]string)
	redactedFields := make([]string, 0, 4)
	secretPressure := artifactHasSecretPressure(artifact)

	if shouldQuarantineArtifactText(secretPressure, artifact.Summary) {
		payload["summary"] = strings.TrimSpace(artifact.Summary)
		artifact.Summary = redactedSecretValue("summary")
		redactedFields = append(redactedFields, "summary")
	}
	artifact.LocalActions = redactSecretSlice("local_action", artifact.LocalActions, secretPressure, payload, &redactedFields)
	artifact.Questions = redactSecretSlice("question", artifact.Questions, secretPressure, payload, &redactedFields)

	for key, value := range artifact.Metadata {
		if shouldQuarantineArtifactMetadata(key, value, secretPressure) {
			payload[key] = strings.TrimSpace(value)
			artifact.Metadata[key] = redactedSecretValue(key)
			redactedFields = append(redactedFields, key)
		}
	}

	if len(payload) == 0 {
		return artifact, nil
	}

	sort.Strings(redactedFields)
	record := ForensicRecord{
		AgentID:        strings.TrimSpace(agent.AgentID),
		Reason:         "secret_like_material",
		CreatedAt:      time.Now().UTC(),
		RedactedFields: uniqueStrings(redactedFields),
		Metadata: map[string]string{
			"channel_kind": strings.TrimSpace(agent.ChannelKind),
		},
		Payload: payload,
	}
	ref, err := WriteForensicRecord(agent, record)
	if err != nil {
		artifact.Metadata["forensic_ref_status"] = "write_failed"
		return artifact, nil
	}
	artifact.Metadata["forensic_ref"] = ref
	artifact.Metadata["redacted_fields"] = strings.Join(record.RedactedFields, ",")
	artifact.ArtifactRefs = append(artifact.ArtifactRefs, ref)
	return artifact, nil
}

func WriteForensicRecord(agent core.DurableAgent, record ForensicRecord) (string, error) {
	_, memoryRoot := LocalRoots(agent.AgentID, agent.LocalStorageRoots)
	if strings.TrimSpace(memoryRoot) == "" {
		return "", fmt.Errorf("durable agent %q has no memory root for forensic storage", strings.TrimSpace(agent.AgentID))
	}
	if err := os.MkdirAll(filepath.Join(memoryRoot, "forensics"), 0o700); err != nil {
		return "", fmt.Errorf("create forensic root: %w", err)
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal forensic record: %w", err)
	}
	sum := sha256.Sum256(raw)
	name := fmt.Sprintf("%s-%s.json", record.CreatedAt.UTC().Format("20060102T150405.000000000Z"), hex.EncodeToString(sum[:6]))
	path := filepath.Join(memoryRoot, "forensics", name)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", fmt.Errorf("write forensic record: %w", err)
	}
	return forensicRef(agent.AgentID, name), nil
}

func ReadForensicRecord(agent core.DurableAgent, ref string) (*ForensicRecord, error) {
	refAgentID, name, err := parseForensicRef(ref)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(refAgentID) != strings.TrimSpace(agent.AgentID) {
		return nil, fmt.Errorf("forensic ref %q does not belong to agent %q", ref, strings.TrimSpace(agent.AgentID))
	}
	_, memoryRoot := LocalRoots(agent.AgentID, agent.LocalStorageRoots)
	if strings.TrimSpace(memoryRoot) == "" {
		return nil, fmt.Errorf("durable agent %q has no memory root for forensic storage", strings.TrimSpace(agent.AgentID))
	}
	raw, err := os.ReadFile(filepath.Join(memoryRoot, "forensics", name))
	if err != nil {
		return nil, fmt.Errorf("read forensic record: %w", err)
	}
	var record ForensicRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("decode forensic record: %w", err)
	}
	return &record, nil
}

func forensicRef(agentID string, name string) string {
	return forensicRefPrefix + strings.TrimSpace(agentID) + "/" + strings.TrimSpace(name)
}

func parseForensicRef(ref string) (agentID string, name string, err error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, forensicRefPrefix) {
		return "", "", fmt.Errorf("invalid forensic ref %q", ref)
	}
	trimmed := strings.TrimPrefix(ref, forensicRefPrefix)
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("invalid forensic ref %q", ref)
	}
	name = filepath.Base(parts[1])
	if name == "." || name == "" {
		return "", "", fmt.Errorf("invalid forensic ref %q", ref)
	}
	return parts[0], name, nil
}

func redactSecretSlice(prefix string, values []string, secretPressure bool, payload map[string]string, redactedFields *[]string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for i, value := range values {
		if shouldQuarantineArtifactText(secretPressure, value) {
			key := fmt.Sprintf("%s_%d", prefix, i+1)
			payload[key] = strings.TrimSpace(value)
			out = append(out, redactedSecretValue(key))
			*redactedFields = append(*redactedFields, key)
			continue
		}
		out = append(out, strings.TrimSpace(value))
	}
	return out
}

func shouldQuarantineArtifactMetadata(key string, value string, secretPressure bool) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if shouldQuarantineArtifactText(secretPressure, value) {
		return true
	}
	return secretPressure && (strings.Contains(key, "excerpt") || strings.Contains(key, "response") || strings.Contains(key, "payload") || strings.Contains(key, "body"))
}

func shouldQuarantineArtifactText(secretPressure bool, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if secretPressure {
		return true
	}
	return containsSecretLikeMaterial(value)
}

func artifactHasSecretPressure(artifact core.DurableReviewArtifact) bool {
	for _, flag := range artifact.RiskFlags {
		if strings.EqualFold(strings.TrimSpace(flag), "secret_request_pressure") {
			return true
		}
	}
	return false
}

func containsSecretLikeMaterial(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, pattern := range secretLikePatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

func redactedSecretValue(label string) string {
	if strings.TrimSpace(label) == "" {
		return "[REDACTED: secret-like content]"
	}
	return fmt.Sprintf("[REDACTED: %s]", strings.TrimSpace(label))
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
