//go:build linux

package tool

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func parseOperationTime(value string, field string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("update_operation %s must be RFC3339: %w", field, err)
	}
	return parsed.UTC(), nil
}

func parseOperationFindingInputs(inputs []updateOperationFindingInput) ([]session.OperationFinding, error) {
	findings := make([]session.OperationFinding, 0, len(inputs))
	for _, item := range inputs {
		claim := strings.TrimSpace(item.Claim)
		if claim == "" {
			return nil, fmt.Errorf("update_operation finding claim is required")
		}
		confidence := session.NormalizeFindingConfidence(session.FindingConfidence(item.Confidence))
		if strings.TrimSpace(item.Confidence) != "" && confidence == "" {
			return nil, fmt.Errorf("update_operation finding confidence must be low, medium, or high")
		}
		findings = append(findings, session.OperationFinding{
			Claim:      claim,
			Confidence: confidence,
			Basis:      strings.TrimSpace(item.Basis),
		})
	}
	return findings, nil
}

func parseOperationArtifactInputs(inputs []updateOperationArtifactInput) ([]session.OperationArtifact, error) {
	artifacts := make([]session.OperationArtifact, 0, len(inputs))
	for _, item := range inputs {
		ref := strings.TrimSpace(item.Ref)
		if ref == "" {
			return nil, fmt.Errorf("update_operation artifact ref is required")
		}
		artifacts = append(artifacts, session.OperationArtifact{
			Label: strings.TrimSpace(item.Label),
			Ref:   ref,
		})
	}
	return artifacts, nil
}

func appendDedupedFindings(existing []session.OperationFinding, added []session.OperationFinding) []session.OperationFinding {
	out := append([]session.OperationFinding(nil), existing...)
	seen := make(map[string]struct{}, len(out))
	for _, item := range out {
		seen[item.Claim+"\x00"+string(item.Confidence)+"\x00"+item.Basis] = struct{}{}
	}
	for _, item := range added {
		key := item.Claim + "\x00" + string(item.Confidence) + "\x00" + item.Basis
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func appendDedupedArtifacts(existing []session.OperationArtifact, added []session.OperationArtifact) []session.OperationArtifact {
	out := append([]session.OperationArtifact(nil), existing...)
	seen := make(map[string]struct{}, len(out))
	for _, item := range out {
		seen[item.Label+"\x00"+item.Ref] = struct{}{}
	}
	for _, item := range added {
		key := item.Label + "\x00" + item.Ref
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func generatedOperationID(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "op"
	}
	return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano())
}
