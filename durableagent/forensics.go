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
	regexp.MustCompile(`(?i)\b(?:password|passphrase|token|secret|api key|credential|ssh key|private key)\s*[:=]\s*["']?[^\s"']{4,}`),
}

var secretConceptPattern = regexp.MustCompile(`(?i)\b(?:password|passphrase|token|secret|api key|credential|credentials|ssh key|private key)\b`)

type reviewTextSensitivity string

const (
	reviewTextSensitivityConceptOnly   reviewTextSensitivity = "secret_concept_without_value"
	reviewTextSensitivityConcreteValue reviewTextSensitivity = "concrete_secret_value"
)

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
	redactionReasons := make(map[string]string)
	conceptMentions := make([]string, 0, 4)

	if shouldQuarantineArtifactText(artifact.Summary) {
		payload["summary"] = strings.TrimSpace(artifact.Summary)
		artifact.Summary = redactedSecretValue("summary")
		redactedFields = append(redactedFields, "summary")
		redactionReasons["summary"] = string(reviewTextSensitivityConcreteValue)
	} else if mentionsSecretConcept(artifact.Summary) {
		conceptMentions = append(conceptMentions, "summary")
	}
	artifact.LocalActions = redactSecretSlice("local_action", artifact.LocalActions, payload, &redactedFields, redactionReasons, &conceptMentions)
	artifact.Questions = redactSecretSlice("question", artifact.Questions, payload, &redactedFields, redactionReasons, &conceptMentions)

	for key, value := range artifact.Metadata {
		if shouldQuarantineArtifactMetadata(key, value, secretPressure) {
			payload[key] = strings.TrimSpace(value)
			artifact.Metadata[key] = redactedSecretValue(key)
			redactedFields = append(redactedFields, key)
			redactionReasons[key] = metadataRedactionReason(key, value, secretPressure)
		} else if mentionsSecretConcept(value) {
			conceptMentions = append(conceptMentions, "metadata."+strings.TrimSpace(key))
		}
	}

	if len(payload) == 0 {
		if len(conceptMentions) > 0 {
			artifact.Metadata["redaction_action"] = "none"
			artifact.Metadata["redaction_source"] = "deterministic"
			artifact.Metadata["redaction_reason"] = string(reviewTextSensitivityConceptOnly)
			artifact.Metadata["redaction_concept_fields"] = strings.Join(uniqueStrings(conceptMentions), ",")
		}
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
	if len(redactionReasons) > 0 {
		record.Metadata["redaction_reasons"] = encodeStringMapForMetadata(redactionReasons)
	}
	ref, err := WriteForensicRecord(agent, record)
	if err != nil {
		artifact.Metadata["forensic_ref_status"] = "write_failed"
		return artifact, nil
	}
	artifact.Metadata["forensic_ref"] = ref
	artifact.Metadata["redacted_fields"] = strings.Join(record.RedactedFields, ",")
	artifact.Metadata["redaction_action"] = "quarantined_fields"
	artifact.Metadata["redaction_source"] = "deterministic"
	artifact.Metadata["redaction_reason"] = primaryRedactionReason(redactionReasons)
	if summaryIsRedacted(record.RedactedFields) && strings.TrimSpace(artifact.Metadata["operator_summary"]) == "" {
		safeSummary := safeOperatorSummary(agent, artifact)
		artifact.Metadata["operator_summary"] = safeSummary
		artifact.Metadata["safe_operator_summary"] = safeSummary
	}
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

func redactSecretSlice(prefix string, values []string, payload map[string]string, redactedFields *[]string, redactionReasons map[string]string, conceptMentions *[]string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for i, value := range values {
		if shouldQuarantineArtifactText(value) {
			key := fmt.Sprintf("%s_%d", prefix, i+1)
			payload[key] = strings.TrimSpace(value)
			out = append(out, redactedSecretValue(key))
			*redactedFields = append(*redactedFields, key)
			redactionReasons[key] = string(reviewTextSensitivityConcreteValue)
			continue
		}
		if mentionsSecretConcept(value) {
			*conceptMentions = append(*conceptMentions, fmt.Sprintf("%s_%d", prefix, i+1))
		}
		out = append(out, strings.TrimSpace(value))
	}
	return out
}

func shouldQuarantineArtifactMetadata(key string, value string, secretPressure bool) bool {
	if shouldQuarantineArtifactText(value) {
		return true
	}
	return secretPressure && isRawSourceMetadataKey(key) && mentionsSecretConcept(value)
}

func metadataRedactionReason(key string, value string, secretPressure bool) string {
	if shouldQuarantineArtifactText(value) {
		return string(reviewTextSensitivityConcreteValue)
	}
	if secretPressure && isRawSourceMetadataKey(key) && mentionsSecretConcept(value) {
		return "secret_pressure_source_excerpt"
	}
	return string(reviewTextSensitivityConcreteValue)
}

func isRawSourceMetadataKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(key, "excerpt") || strings.Contains(key, "payload") || strings.Contains(key, "body")
}

func shouldQuarantineArtifactText(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
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

func ContainsConcreteSecretValue(text string) bool {
	return containsSecretLikeMaterial(text)
}

func mentionsSecretConcept(text string) bool {
	return secretConceptPattern.MatchString(strings.TrimSpace(text))
}

func summaryIsRedacted(fields []string) bool {
	for _, field := range fields {
		if strings.TrimSpace(field) == "summary" {
			return true
		}
	}
	return false
}

func primaryRedactionReason(reasons map[string]string) string {
	if len(reasons) == 0 {
		return string(reviewTextSensitivityConcreteValue)
	}
	keys := make([]string, 0, len(reasons))
	for key := range reasons {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if reason := strings.TrimSpace(reasons[key]); reason != "" {
			return reason
		}
	}
	return string(reviewTextSensitivityConcreteValue)
}

func safeOperatorSummary(agent core.DurableAgent, artifact core.DurableReviewArtifact) string {
	label := durableReviewAgentLabel(agent)
	status := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		artifact.Metadata["external_channel_status"],
		artifact.Metadata["status"],
		artifact.Metadata["review_status"],
	)))
	errorText := strings.TrimSpace(firstNonEmpty(
		artifact.Metadata["external_channel_error"],
		artifact.Metadata["blocker"],
		artifact.Metadata["error"],
	))
	if containsSecretLikeMaterial(errorText) {
		errorText = ""
	}
	if strings.Contains(status, "blocked") || strings.Contains(status, "failed") {
		if errorText != "" {
			return fmt.Sprintf("%s blocked: %s", label, errorText)
		}
		return label + " blocked; raw child summary is stored locally because it may contain sensitive material."
	}
	if errorText != "" {
		return fmt.Sprintf("%s reported: %s", label, errorText)
	}
	return label + " review contains sensitive material; raw child summary is stored locally."
}

func durableReviewAgentLabel(agent core.DurableAgent) string {
	if strings.TrimSpace(agent.AgentID) == "" {
		return "Child review"
	}
	return strings.TrimSpace(agent.AgentID)
}

func encodeStringMapForMetadata(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if value := strings.TrimSpace(values[key]); value != "" {
			parts = append(parts, strings.TrimSpace(key)+"="+value)
		}
	}
	return strings.Join(parts, ",")
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
