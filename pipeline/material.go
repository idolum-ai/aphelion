//go:build linux

package pipeline

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

// BuildFloorFromGovernor parses governor output into a material floor artifact
// and fallback text.
func BuildFloorFromGovernor(text string, useContract bool) (core.MaterialPacket, string, bool) {
	trimmed := strings.TrimSpace(text)
	if !useContract {
		return core.MaterialPacket{}, FloorTextOrFallback(trimmed), false
	}
	packet, err := ParseMaterialPacket(trimmed)
	if err != nil {
		return core.TextMaterialPacket(trimmed), FloorTextOrFallback(trimmed), false
	}
	sidecar := strings.TrimSpace(packet.Text())
	if sidecar == "" {
		sidecar = FloorTextOrFallback(trimmed)
	}
	return packet, sidecar, true
}

// FloorTextOrFallback returns canonical floor fallback text for empty floors.
func FloorTextOrFallback(text string) string {
	floor := strings.TrimSpace(text)
	if floor == "" {
		return "(no response)"
	}
	return floor
}

// ParseMaterialPacket parses structured `FACTS`/`ALLOWED_ACTIONS`-style floor
// output into a material packet.
func ParseMaterialPacket(text string) (core.MaterialPacket, error) {
	packet := core.MaterialPacket{}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return packet, fmt.Errorf("empty material packet")
	}

	recognized := 0
	current := ""
	for _, rawLine := range strings.Split(trimmed, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if kind, ok := parseInlineMaterialKind(line); ok {
			packet.Kind = kind
			current = "kind"
			recognized++
			continue
		}
		switch NormalizeMaterialHeading(line) {
		case "kind":
			current = "kind"
			recognized++
			continue
		case "facts":
			current = "facts"
			recognized++
			continue
		case "allowed_actions":
			current = "allowed_actions"
			recognized++
			continue
		case "commitments":
			current = "commitments"
			recognized++
			continue
		case "refusals":
			current = "refusals"
			recognized++
			continue
		case "scene_constraints":
			current = "scene_constraints"
			recognized++
			continue
		case "continuity_context":
			current = "continuity_context"
			recognized++
			continue
		case "notes":
			current = "notes"
			recognized++
			continue
		}

		item := ParseMaterialItem(line)
		if item == "" {
			if current == "notes" || current == "continuity_context" || current == "kind" {
				item = line
			} else {
				continue
			}
		}
		switch current {
		case "kind":
			packet.Kind = core.NormalizeMaterialPacketKind(item)
		case "facts":
			packet.Facts = append(packet.Facts, item)
		case "allowed_actions":
			packet.AllowedActions = append(packet.AllowedActions, item)
		case "commitments":
			packet.Commitments = append(packet.Commitments, item)
		case "refusals":
			packet.Refusals = append(packet.Refusals, item)
		case "scene_constraints":
			packet.SceneConstraints = append(packet.SceneConstraints, item)
		case "continuity_context":
			packet.ContinuityContext = append(packet.ContinuityContext, parseMaterialContinuityContextItem(item))
		case "notes":
			packet.Notes = append(packet.Notes, item)
		}
	}

	if recognized == 0 || packet.Empty() {
		return core.MaterialPacket{}, fmt.Errorf("no structured material packet found")
	}
	return packet, nil
}

func parseInlineMaterialKind(line string) (core.MaterialPacketKind, bool) {
	key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
	if !ok || strings.TrimSpace(value) == "" {
		return core.MaterialPacketKindUnspecified, false
	}
	if NormalizeMaterialHeading(key+":") != "kind" {
		return core.MaterialPacketKindUnspecified, false
	}
	return core.NormalizeMaterialPacketKind(value), true
}

// NormalizeMaterialHeading normalizes recognized section headings.
func NormalizeMaterialHeading(line string) string {
	trimmed := strings.ToUpper(strings.TrimSuffix(strings.TrimSpace(line), ":"))
	switch trimmed {
	case "KIND", "MATERIAL_KIND", "MATERIAL KIND", "PACKET_KIND", "PACKET KIND":
		return "kind"
	case "FACTS":
		return "facts"
	case "ALLOWED_ACTIONS", "ALLOWED ACTIONS":
		return "allowed_actions"
	case "COMMITMENTS":
		return "commitments"
	case "REFUSALS":
		return "refusals"
	case "SCENE_CONSTRAINTS", "SCENE CONSTRAINTS":
		return "scene_constraints"
	case "CONTINUITY_CONTEXT", "CONTINUITY CONTEXT", "INTERNAL_CONTINUITY", "INTERNAL CONTINUITY":
		return "continuity_context"
	case "NOTES":
		return "notes"
	default:
		return ""
	}
}

// ParseMaterialItem parses item lines such as `- value` or `1. value`.
func ParseMaterialItem(line string) string {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "):
		return strings.TrimSpace(line[2:])
	}
	dot := strings.Index(line, ". ")
	if dot <= 0 {
		return ""
	}
	for _, ch := range line[:dot] {
		if ch < '0' || ch > '9' {
			return ""
		}
	}
	return strings.TrimSpace(line[dot+2:])
}

func parseMaterialContinuityContextItem(item string) core.MaterialContinuityContext {
	trimmed := strings.TrimSpace(item)
	if trimmed == "" {
		return core.MaterialContinuityContext{}
	}
	fields := make(map[string]string)
	for _, rawPart := range strings.Split(trimmed, ";") {
		part := strings.TrimSpace(rawPart)
		if part == "" {
			continue
		}
		key, value, ok := cutMaterialKeyValue(part)
		if !ok {
			continue
		}
		fields[normalizeMaterialKey(key)] = strings.TrimSpace(value)
	}
	if len(fields) == 0 {
		if legacy, ok := shapeLegacyContinuityStrings(trimmed); ok {
			return legacy
		}
		return core.MaterialContinuityContext{
			Kind:        core.MaterialContinuityKindEvidence,
			Visibility:  core.MaterialContinuityVisibilityInternal,
			Reason:      "legacy continuity prose quarantined for compatibility",
			EvidenceRef: "legacy_continuity_context_prose",
		}
	}
	return core.MaterialContinuityContext{
		Kind:        core.NormalizeMaterialContinuityKind(fields["kind"]),
		Visibility:  core.NormalizeMaterialContinuityVisibility(fields["visibility"]),
		Reason:      firstNonEmptyMaterialField(fields["reason"], fields["why"], fields["text"], fields["summary"], fields["detail"]),
		EvidenceRef: firstNonEmptyMaterialField(fields["evidence_ref"], fields["evidence"], fields["ref"]),
	}
}

// shapeLegacyContinuityStrings quarantines older prose-shaped recovery lines.
// New material floors should use typed continuity fields and visibility policy.
func shapeLegacyContinuityStrings(text string) (core.MaterialContinuityContext, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return core.MaterialContinuityContext{}, false
	}
	lower := strings.ToLower(trimmed)
	for _, marker := range []string{
		"recovered cleanly",
		"recovered and completed",
		"recovered and ",
		"recovery completed",
		"recovery evidence",
		"continuity repair completed",
		"continuity repair succeeded",
		"budget recovery completed",
		"budget recovery finished",
	} {
		if strings.HasPrefix(lower, marker) {
			return core.MaterialContinuityContext{
				Kind:        core.MaterialContinuityKindRecovery,
				Visibility:  core.MaterialContinuityVisibilityInternal,
				Reason:      "legacy recovery prose quarantined for compatibility",
				EvidenceRef: "legacy_recovery_prose",
			}, true
		}
	}
	return core.MaterialContinuityContext{}, false
}

func cutMaterialKeyValue(part string) (string, string, bool) {
	if key, value, ok := strings.Cut(part, "="); ok {
		return key, value, true
	}
	if key, value, ok := strings.Cut(part, ":"); ok {
		return key, value, true
	}
	return "", "", false
}

func normalizeMaterialKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, " ", "_")
	return key
}

func firstNonEmptyMaterialField(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
