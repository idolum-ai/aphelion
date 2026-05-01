//go:build linux

package core

import (
	"encoding/json"
	"strings"
)

const ReviewEventKindMissionControlProposal = "mission_control_proposal"

type MissionControlProposal struct {
	Kind              string   `json:"kind,omitempty"`
	MissionID         string   `json:"mission_id,omitempty"`
	Title             string   `json:"title,omitempty"`
	Objective         string   `json:"objective,omitempty"`
	WhyProposed       string   `json:"why_proposed,omitempty"`
	Scope             string   `json:"scope,omitempty"`
	Owner             string   `json:"owner,omitempty"`
	Origin            string   `json:"origin,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	SourceRefs        []string `json:"source_refs,omitempty"`
	SuccessCriteria   []string `json:"success_criteria,omitempty"`
	NextAllowedAction string   `json:"next_allowed_action,omitempty"`
	NotIncluded       []string `json:"not_included,omitempty"`
	RiskClass         string   `json:"risk_class,omitempty"`
}

func NormalizeMissionControlProposal(p MissionControlProposal) MissionControlProposal {
	p.Kind = strings.TrimSpace(p.Kind)
	if p.Kind == "" {
		p.Kind = ReviewEventKindMissionControlProposal
	}
	p.MissionID = strings.TrimSpace(p.MissionID)
	p.Title = strings.TrimSpace(p.Title)
	p.Objective = strings.TrimSpace(p.Objective)
	p.WhyProposed = strings.TrimSpace(p.WhyProposed)
	p.Scope = strings.TrimSpace(p.Scope)
	if p.Scope == "" {
		p.Scope = "principal"
	}
	p.Owner = strings.TrimSpace(p.Owner)
	p.Origin = strings.TrimSpace(p.Origin)
	if p.Origin == "" {
		p.Origin = "proposed"
	}
	p.Tags = normalizeStringList(p.Tags)
	p.SourceRefs = normalizeStringList(p.SourceRefs)
	p.SuccessCriteria = normalizeStringList(p.SuccessCriteria)
	p.NextAllowedAction = strings.TrimSpace(p.NextAllowedAction)
	p.NotIncluded = normalizeStringList(p.NotIncluded)
	p.RiskClass = strings.TrimSpace(p.RiskClass)
	return p
}

func MissionControlProposalMetadataJSON(p MissionControlProposal) (string, error) {
	p = NormalizeMissionControlProposal(p)
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func MissionControlProposalFromMetadataJSON(raw string) (MissionControlProposal, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return MissionControlProposal{}, false
	}
	var p MissionControlProposal
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return MissionControlProposal{}, false
	}
	p = NormalizeMissionControlProposal(p)
	if p.Kind != ReviewEventKindMissionControlProposal || strings.TrimSpace(p.Objective) == "" {
		return MissionControlProposal{}, false
	}
	return p, true
}

func normalizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
