//go:build linux

package core

import "strings"

type MaterialPacketKind string
type MaterialContinuityKind string
type MaterialContinuityVisibility string

const (
	MaterialPacketKindUnspecified  MaterialPacketKind = ""
	MaterialPacketKindGeneral      MaterialPacketKind = "general"
	MaterialPacketKindStatusReport MaterialPacketKind = "status_report"
	MaterialPacketKindRelational   MaterialPacketKind = "relational"
	MaterialPacketKindCreative     MaterialPacketKind = "creative"

	MaterialContinuityKindUnspecified  MaterialContinuityKind = ""
	MaterialContinuityKindRecovery     MaterialContinuityKind = "recovery"
	MaterialContinuityKindContinuation MaterialContinuityKind = "continuation"
	MaterialContinuityKindEvidence     MaterialContinuityKind = "evidence"
	MaterialContinuityKindWarning      MaterialContinuityKind = "warning"
	MaterialContinuityKindHandoff      MaterialContinuityKind = "handoff"

	MaterialContinuityVisibilityInternal     MaterialContinuityVisibility = "internal"
	MaterialContinuityVisibilityUserRelevant MaterialContinuityVisibility = "user_relevant"
	MaterialContinuityVisibilityMustSurface  MaterialContinuityVisibility = "must_surface"
)

type MaterialPacket struct {
	Kind              MaterialPacketKind
	Facts             []string
	AllowedActions    []string
	Commitments       []string
	Refusals          []string
	SceneConstraints  []string
	ContinuityContext []MaterialContinuityContext
	Notes             []string
}

type MaterialContinuityContext struct {
	Kind        MaterialContinuityKind
	Visibility  MaterialContinuityVisibility
	Reason      string
	EvidenceRef string
}

func NormalizeMaterialPacketKind(kind string) MaterialPacketKind {
	normalized := strings.ToLower(strings.TrimSpace(kind))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	switch normalized {
	case "", "unspecified", "unknown", "none":
		return MaterialPacketKindUnspecified
	case "general", "general_render":
		return MaterialPacketKindGeneral
	case "status", "status_report", "completion", "completion_report", "evidence_report":
		return MaterialPacketKindStatusReport
	case "relational", "relationship", "relational_reply", "relational_scene":
		return MaterialPacketKindRelational
	case "creative", "creative_scene", "story", "poem", "essay":
		return MaterialPacketKindCreative
	default:
		return MaterialPacketKindUnspecified
	}
}

func NormalizeMaterialContinuityKind(kind string) MaterialContinuityKind {
	normalized := strings.ToLower(strings.TrimSpace(kind))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	switch normalized {
	case "", "unspecified", "unknown", "none":
		return MaterialContinuityKindUnspecified
	case "recovery", "repair", "budget_recovery", "rollover":
		return MaterialContinuityKindRecovery
	case "continuation", "resume", "resumption":
		return MaterialContinuityKindContinuation
	case "evidence", "continuity_evidence", "state_evidence":
		return MaterialContinuityKindEvidence
	case "warning", "blocker", "blocked", "stale", "approval_required":
		return MaterialContinuityKindWarning
	case "handoff", "transfer", "handover":
		return MaterialContinuityKindHandoff
	default:
		return MaterialContinuityKindUnspecified
	}
}

func NormalizeMaterialContinuityVisibility(visibility string) MaterialContinuityVisibility {
	normalized := strings.ToLower(strings.TrimSpace(visibility))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	switch normalized {
	case "", "internal", "background", "hidden", "delivery_context":
		return MaterialContinuityVisibilityInternal
	case "user_relevant", "user_visible", "visible", "asked", "requested":
		return MaterialContinuityVisibilityUserRelevant
	case "must_surface", "surface", "must_show", "blocker", "blocking", "requires_action":
		return MaterialContinuityVisibilityMustSurface
	default:
		return MaterialContinuityVisibilityInternal
	}
}

func (m MaterialPacket) Empty() bool {
	return len(m.Facts) == 0 &&
		len(m.AllowedActions) == 0 &&
		len(m.Commitments) == 0 &&
		len(m.Refusals) == 0 &&
		len(m.SceneConstraints) == 0 &&
		len(m.ContinuityContext) == 0 &&
		len(m.Notes) == 0
}

func (c MaterialContinuityContext) Empty() bool {
	return c.Kind == "" &&
		c.Visibility == "" &&
		strings.TrimSpace(c.Reason) == "" &&
		strings.TrimSpace(c.EvidenceRef) == ""
}

func (c MaterialContinuityContext) Normalized() MaterialContinuityContext {
	c.Kind = NormalizeMaterialContinuityKind(string(c.Kind))
	c.Visibility = NormalizeMaterialContinuityVisibility(string(c.Visibility))
	c.Reason = strings.TrimSpace(c.Reason)
	c.EvidenceRef = strings.TrimSpace(c.EvidenceRef)
	return c
}

func (m MaterialPacket) Text() string {
	out := make([]string, 0, 7)
	out = appendNonEmptyMaterialSection(out, renderMaterialSection("FACTS", m.Facts))
	out = appendNonEmptyMaterialSection(out, renderMaterialSection("ALLOWED_ACTIONS", m.AllowedActions))
	out = appendNonEmptyMaterialSection(out, renderMaterialSection("COMMITMENTS", m.Commitments))
	out = appendNonEmptyMaterialSection(out, renderMaterialSection("REFUSALS", m.Refusals))
	out = appendNonEmptyMaterialSection(out, renderMaterialSection("SCENE_CONSTRAINTS", m.SceneConstraints))
	out = appendNonEmptyMaterialSection(out, renderMaterialContinuitySection(m.ContinuityContext))
	out = appendNonEmptyMaterialSection(out, renderMaterialSection("NOTES", m.Notes))
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n\n")
}

func appendNonEmptyMaterialSection(out []string, rendered string) []string {
	if rendered == "" {
		return out
	}
	return append(out, rendered)
}

func TextMaterialPacket(text string) MaterialPacket {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return MaterialPacket{}
	}
	return MaterialPacket{Notes: []string{trimmed}}
}

func renderMaterialSection(title string, items []string) string {
	clean := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		clean = append(clean, "- "+trimmed)
	}
	if len(clean) == 0 {
		return ""
	}
	return title + ":\n" + strings.Join(clean, "\n")
}

func renderMaterialContinuitySection(items []MaterialContinuityContext) string {
	clean := make([]string, 0, len(items))
	for _, item := range items {
		rendered := renderMaterialContinuityItem(item)
		if rendered == "" {
			continue
		}
		clean = append(clean, "- "+rendered)
	}
	if len(clean) == 0 {
		return ""
	}
	return "CONTINUITY_CONTEXT:\n" + strings.Join(clean, "\n")
}

func renderMaterialContinuityItem(item MaterialContinuityContext) string {
	if item.Empty() {
		return ""
	}
	item = item.Normalized()
	parts := make([]string, 0, 4)
	if item.Kind != "" {
		parts = append(parts, "kind="+string(item.Kind))
	}
	parts = append(parts, "visibility="+string(item.Visibility))
	if item.Reason != "" {
		parts = append(parts, "reason="+item.Reason)
	}
	if item.EvidenceRef != "" {
		parts = append(parts, "evidence_ref="+item.EvidenceRef)
	}
	return strings.Join(parts, "; ")
}
