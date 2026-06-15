//go:build linux

package pipeline

import (
	"strings"
	"unicode"

	"github.com/idolum-ai/aphelion/core"
)

const internalContinuitySceneConstraint = "Lead with the task outcome, respected boundary, and any next user-relevant action; treat successful continuity repair as background context."

// ShapeInternalContinuityForPresentation returns a presentation-only material
// packet that keeps successful recovery mechanics out of the visible headline.
func ShapeInternalContinuityForPresentation(packet core.MaterialPacket, floorText string) (core.MaterialPacket, string, bool) {
	if packet.Empty() {
		return packet, floorText, false
	}
	shaped := packet
	changed := false
	if facts, ok := rewriteInternalContinuityItems(packet.Facts); ok {
		shaped.Facts = facts
		changed = true
	}
	if notes, ok := rewriteInternalContinuityItems(packet.Notes); ok {
		shaped.Notes = notes
		changed = true
	}
	if !changed {
		return packet, floorText, false
	}
	shaped.SceneConstraints = appendUniqueMaterialItem(shaped.SceneConstraints, internalContinuitySceneConstraint)
	shapedText := strings.TrimSpace(shaped.Text())
	if shapedText == "" {
		shapedText = FloorTextOrFallback(floorText)
	}
	return shaped, shapedText, true
}

func rewriteInternalContinuityItems(items []string) ([]string, bool) {
	out := make([]string, 0, len(items))
	changed := false
	for _, item := range items {
		rewritten, ok := rewriteInternalContinuityItem(item)
		if ok {
			changed = true
		}
		if rewritten = strings.TrimSpace(rewritten); rewritten != "" {
			out = append(out, rewritten)
		}
	}
	return out, changed
}

func rewriteInternalContinuityItem(item string) (string, bool) {
	trimmed := strings.TrimSpace(item)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	for _, rewrite := range []struct {
		prefix      string
		replacement string
	}{
		{prefix: "recovered cleanly. ", replacement: ""},
		{prefix: "recovered and completed ", replacement: "Completed "},
		{prefix: "recovered and ", replacement: ""},
		{prefix: "recovery evidence says ", replacement: ""},
		{prefix: "continuity repair completed; ", replacement: ""},
		{prefix: "continuity repair succeeded; ", replacement: ""},
		{prefix: "budget recovery completed; ", replacement: ""},
		{prefix: "budget recovery finished; ", replacement: ""},
		{prefix: "the recovery completed; ", replacement: ""},
	} {
		if strings.HasPrefix(lower, rewrite.prefix) {
			suffix := strings.TrimSpace(trimmed[len(rewrite.prefix):])
			if suffix == "" {
				return "", true
			}
			if rewrite.replacement != "" {
				return rewrite.replacement + suffix, true
			}
			return upperInitialPresentation(suffix), true
		}
	}
	return trimmed, false
}

func upperInitialPresentation(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return ""
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func appendUniqueMaterialItem(items []string, item string) []string {
	item = strings.TrimSpace(item)
	if item == "" {
		return items
	}
	key := normalizeFallbackKey(item)
	for _, existing := range items {
		if normalizeFallbackKey(existing) == key {
			return items
		}
	}
	return append(items, item)
}
