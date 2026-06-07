//go:build linux

package core

import "strings"

type MaterialPacketKind string

const (
	MaterialPacketKindUnspecified  MaterialPacketKind = ""
	MaterialPacketKindGeneral      MaterialPacketKind = "general"
	MaterialPacketKindStatusReport MaterialPacketKind = "status_report"
	MaterialPacketKindRelational   MaterialPacketKind = "relational"
	MaterialPacketKindCreative     MaterialPacketKind = "creative"
)

type MaterialPacket struct {
	Kind             MaterialPacketKind
	Facts            []string
	AllowedActions   []string
	Commitments      []string
	Refusals         []string
	SceneConstraints []string
	Notes            []string
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

func (m MaterialPacket) Empty() bool {
	return len(m.Facts) == 0 &&
		len(m.AllowedActions) == 0 &&
		len(m.Commitments) == 0 &&
		len(m.Refusals) == 0 &&
		len(m.SceneConstraints) == 0 &&
		len(m.Notes) == 0
}

func (m MaterialPacket) Text() string {
	sections := []struct {
		title string
		items []string
	}{
		{title: "FACTS", items: m.Facts},
		{title: "ALLOWED_ACTIONS", items: m.AllowedActions},
		{title: "COMMITMENTS", items: m.Commitments},
		{title: "REFUSALS", items: m.Refusals},
		{title: "SCENE_CONSTRAINTS", items: m.SceneConstraints},
		{title: "NOTES", items: m.Notes},
	}

	out := make([]string, 0, len(sections))
	for _, section := range sections {
		rendered := renderMaterialSection(section.title, section.items)
		if rendered == "" {
			continue
		}
		out = append(out, rendered)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n\n")
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
