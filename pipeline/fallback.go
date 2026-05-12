//go:build linux

package pipeline

import (
	"strings"
	"unicode"

	"github.com/idolum-ai/aphelion/core"
)

// FallbackOptions controls floor-fallback serialization details.
type FallbackOptions struct {
	Channel string
	Voice   bool
}

type fallbackSectionKind string

const (
	fallbackFacts       fallbackSectionKind = "facts"
	fallbackAllowed     fallbackSectionKind = "allowed"
	fallbackCommitments fallbackSectionKind = "commitments"
	fallbackRefusals    fallbackSectionKind = "refusals"
	fallbackNotes       fallbackSectionKind = "notes"
)

type fallbackSection struct {
	Kind  fallbackSectionKind
	Items []string
}

// SerializeFloorFallback converts a material floor into canonical fallback text.
func SerializeFloorFallback(packet core.MaterialPacket, floorText string, opts FallbackOptions) string {
	trimmedFloor := strings.TrimSpace(floorText)
	if packet.Empty() {
		return FloorTextOrFallback(trimmedFloor)
	}
	if isPlainTextFloorPacket(packet, trimmedFloor) {
		return FloorTextOrFallback(trimmedFloor)
	}

	sections := fallbackSections(packet)
	if len(sections) == 0 {
		return FloorTextOrFallback(trimmedFloor)
	}
	if opts.Voice {
		return serializeVoiceFallback(sections, trimmedFloor)
	}
	return serializeTextFallback(sections, trimmedFloor, opts)
}

func fallbackSections(packet core.MaterialPacket) []fallbackSection {
	seen := make(map[string]struct{})
	sections := []fallbackSection{
		{Kind: fallbackFacts, Items: uniqueFallbackItems(packet.Facts, seen)},
		{Kind: fallbackAllowed, Items: uniqueFallbackItems(packet.AllowedActions, seen)},
		{Kind: fallbackCommitments, Items: uniqueFallbackItems(packet.Commitments, seen)},
		{Kind: fallbackRefusals, Items: uniqueFallbackItems(packet.Refusals, seen)},
		{Kind: fallbackNotes, Items: uniqueFallbackItems(packet.Notes, seen)},
	}

	out := make([]fallbackSection, 0, len(sections))
	for _, section := range sections {
		if len(section.Items) == 0 {
			continue
		}
		out = append(out, section)
	}
	return out
}

func uniqueFallbackItems(items []string, seen map[string]struct{}) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		key := normalizeFallbackKey(trimmed)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func serializeTextFallback(sections []fallbackSection, floorText string, opts FallbackOptions) string {
	lines := make([]string, 0, len(sections))
	for _, section := range sections {
		switch section.Kind {
		case fallbackNotes:
			if len(section.Items) == 1 {
				lines = append(lines, "Note: "+ensureSentence(section.Items[0]))
				continue
			}
		}
		title := fallbackTextTitle(section.Kind, opts)
		if title == "" {
			continue
		}
		lines = append(lines, title+":\n"+renderFallbackBullets(section.Items))
	}
	if len(lines) == 0 {
		return FloorTextOrFallback(floorText)
	}
	return strings.Join(lines, "\n\n")
}

func serializeVoiceFallback(sections []fallbackSection, floorText string) string {
	sentences := make([]string, 0, len(sections))
	for _, section := range sections {
		sentence := renderVoiceSection(section)
		if sentence == "" {
			continue
		}
		sentences = append(sentences, sentence)
	}
	if len(sentences) == 0 {
		return FloorTextOrFallback(floorText)
	}
	return strings.Join(sentences, " ")
}

func fallbackTextTitle(kind fallbackSectionKind, opts FallbackOptions) string {
	switch strings.ToLower(strings.TrimSpace(opts.Channel)) {
	case "", "telegram":
		switch kind {
		case fallbackFacts:
			return "What matters"
		case fallbackAllowed:
			return "Next"
		case fallbackCommitments:
			return "Committed"
		case fallbackRefusals:
			return "Limits"
		case fallbackNotes:
			return "Notes"
		}
	default:
		switch kind {
		case fallbackFacts:
			return "What matters"
		case fallbackAllowed:
			return "Next"
		case fallbackCommitments:
			return "Committed"
		case fallbackRefusals:
			return "Limits"
		case fallbackNotes:
			return "Notes"
		}
	}
	return ""
}

func renderFallbackBullets(items []string) string {
	clean := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		clean = append(clean, "- "+trimmed)
	}
	return strings.Join(clean, "\n")
}

func renderVoiceSection(section fallbackSection) string {
	if len(section.Items) == 0 {
		return ""
	}
	switch section.Kind {
	case fallbackFacts:
		return "Here's what matters: " + voiceList(section.Items)
	case fallbackAllowed:
		return "Next, I can " + voiceActionList(section.Items)
	case fallbackCommitments:
		return "I'll " + voiceActionList(section.Items)
	case fallbackRefusals:
		return "I won't " + voiceActionList(section.Items)
	case fallbackNotes:
		return "One note: " + voiceList(section.Items)
	default:
		return ""
	}
}

func voiceList(items []string) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := trimVoiceTerminal(item)
		if trimmed == "" {
			continue
		}
		parts = append(parts, trimmed)
	}
	return ensureSentence(strings.Join(parts, "; "))
}

func voiceActionList(items []string) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := trimVoiceTerminal(item)
		if trimmed == "" {
			continue
		}
		parts = append(parts, lowerInitial(trimmed))
	}
	return ensureSentence(strings.Join(parts, "; "))
}

func trimVoiceTerminal(text string) string {
	trimmed := strings.TrimSpace(text)
	return strings.TrimRight(trimmed, ".!? ")
}

func ensureSentence(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	last := trimmed[len(trimmed)-1]
	switch last {
	case '.', '!', '?':
		return trimmed
	default:
		return trimmed + "."
	}
}

func lowerInitial(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return ""
	}
	if !unicode.IsUpper(runes[0]) {
		return string(runes)
	}
	if len(runes) > 1 && unicode.IsUpper(runes[1]) {
		return string(runes)
	}
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

func normalizeFallbackKey(text string) string {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	trimmed = strings.TrimRight(trimmed, ".!? ")
	return trimmed
}

func isPlainTextFloorPacket(packet core.MaterialPacket, floorText string) bool {
	return len(packet.Facts) == 0 &&
		len(packet.AllowedActions) == 0 &&
		len(packet.Commitments) == 0 &&
		len(packet.Refusals) == 0 &&
		len(packet.SceneConstraints) == 0 &&
		len(packet.Notes) == 1 &&
		strings.TrimSpace(packet.Notes[0]) == strings.TrimSpace(floorText)
}
