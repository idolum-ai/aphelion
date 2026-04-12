//go:build linux

package face

import (
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

func SerializeFloorFallback(packet core.MaterialPacket, floorText string) string {
	trimmedFloor := strings.TrimSpace(floorText)
	if packet.Empty() {
		return FloorTextOrFallback(trimmedFloor)
	}
	if isLegacyFloorPacket(packet, trimmedFloor) {
		return FloorTextOrFallback(trimmedFloor)
	}

	sections := []struct {
		title string
		items []string
	}{
		{title: "What is true", items: packet.Facts},
		{title: "What I can do", items: packet.AllowedActions},
		{title: "What I'll do", items: packet.Commitments},
		{title: "What I won't do", items: packet.Refusals},
		{title: "Notes", items: packet.Notes},
	}

	out := make([]string, 0, len(sections))
	for _, section := range sections {
		rendered := renderFallbackSection(section.title, section.items)
		if rendered == "" {
			continue
		}
		out = append(out, rendered)
	}
	if len(out) == 0 {
		return FloorTextOrFallback(trimmedFloor)
	}
	return strings.Join(out, "\n\n")
}

func isLegacyFloorPacket(packet core.MaterialPacket, floorText string) bool {
	return len(packet.Facts) == 0 &&
		len(packet.AllowedActions) == 0 &&
		len(packet.Commitments) == 0 &&
		len(packet.Refusals) == 0 &&
		len(packet.SceneConstraints) == 0 &&
		len(packet.Notes) == 1 &&
		strings.TrimSpace(packet.Notes[0]) == strings.TrimSpace(floorText)
}

func renderFallbackSection(title string, items []string) string {
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
