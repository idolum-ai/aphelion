//go:build linux

package core

import (
	"strings"
	"time"
)

type RuntimeFinding struct {
	Kind             string `json:"kind,omitempty"`
	ClaimType        string `json:"claim_type,omitempty"`
	EvidenceStatus   string `json:"evidence_status,omitempty"`
	Detail           string `json:"detail,omitempty"`
	LatestTurnStatus string `json:"latest_turn_status,omitempty"`
	LatestTerminalAt string `json:"latest_terminal_at,omitempty"`
	RequiredBehavior string `json:"required_behavior,omitempty"`
}

type RuntimeAdjudication struct {
	Kind          string           `json:"kind,omitempty"`
	Surface       string           `json:"surface,omitempty"`
	SubjectID     string           `json:"subject_id,omitempty"`
	OperatorLabel string           `json:"operator_label,omitempty"`
	Findings      []RuntimeFinding `json:"findings,omitempty"`
	EvidenceRefs  []string         `json:"evidence_refs,omitempty"`
	VisibleAction string           `json:"visible_action,omitempty"`
	CreatedAt     time.Time        `json:"created_at,omitempty"`
}

func NormalizeRuntimeFinding(f RuntimeFinding) RuntimeFinding {
	f.Kind = normalizeRuntimeText(f.Kind)
	f.ClaimType = normalizeRuntimeText(f.ClaimType)
	if f.Kind == "" {
		f.Kind = f.ClaimType
	}
	if f.ClaimType == "" {
		f.ClaimType = f.Kind
	}
	f.EvidenceStatus = normalizeRuntimeText(f.EvidenceStatus)
	f.Detail = normalizeRuntimeText(f.Detail)
	f.LatestTurnStatus = normalizeRuntimeText(f.LatestTurnStatus)
	f.LatestTerminalAt = normalizeRuntimeText(f.LatestTerminalAt)
	f.RequiredBehavior = normalizeRuntimeText(f.RequiredBehavior)
	return f
}

func NormalizeRuntimeAdjudication(a RuntimeAdjudication) RuntimeAdjudication {
	a.Kind = normalizeRuntimeText(a.Kind)
	a.Surface = normalizeRuntimeText(a.Surface)
	a.SubjectID = normalizeRuntimeText(a.SubjectID)
	a.OperatorLabel = normalizeRuntimeText(a.OperatorLabel)
	a.VisibleAction = normalizeRuntimeText(a.VisibleAction)
	a.EvidenceRefs = normalizeRuntimeList(a.EvidenceRefs)
	findings := make([]RuntimeFinding, 0, len(a.Findings))
	for _, finding := range a.Findings {
		finding = NormalizeRuntimeFinding(finding)
		if finding.Kind == "" && finding.Detail == "" && finding.EvidenceStatus == "" {
			continue
		}
		findings = append(findings, finding)
	}
	a.Findings = findings
	if !a.CreatedAt.IsZero() {
		a.CreatedAt = a.CreatedAt.UTC()
	}
	return a
}

func NormalizeRuntimeAdjudications(values []RuntimeAdjudication) []RuntimeAdjudication {
	out := make([]RuntimeAdjudication, 0, len(values))
	for _, value := range values {
		value = NormalizeRuntimeAdjudication(value)
		if value.Kind == "" && len(value.Findings) == 0 && value.OperatorLabel == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func normalizeRuntimeText(value string) string {
	return strings.TrimSpace(value)
}

func normalizeRuntimeList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
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
