//go:build linux

package runtime

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
)

func shouldUseMaterialFloorContract(faceBackend face.Backend, policy faceTurnPolicy) bool {
	if faceBackend == face.BackendGovernorPassthrough {
		return false
	}
	return policy.Proposal || policy.Brokerage
}

func governorMaterialArtifact(text string, useContract bool) (core.MaterialPacket, string, bool) {
	trimmed := strings.TrimSpace(text)
	if !useContract {
		return core.MaterialPacket{}, face.CanonicalOrFallback(trimmed), false
	}
	packet, err := parseMaterialPacket(trimmed)
	if err != nil {
		return core.LegacyMaterialPacket(trimmed), face.CanonicalOrFallback(trimmed), false
	}
	sidecar := strings.TrimSpace(packet.Text())
	if sidecar == "" {
		sidecar = face.CanonicalOrFallback(trimmed)
	}
	return packet, sidecar, true
}

func materialFloorHeuristicText(packet core.MaterialPacket, fallback string) string {
	if packet.Empty() {
		return fallback
	}
	parts := make([]string, 0, len(packet.Facts)+len(packet.AllowedActions)+len(packet.Commitments)+len(packet.Refusals)+len(packet.SceneConstraints)+len(packet.Notes))
	parts = append(parts, packet.Facts...)
	parts = append(parts, packet.AllowedActions...)
	parts = append(parts, packet.Commitments...)
	parts = append(parts, packet.Refusals...)
	parts = append(parts, packet.SceneConstraints...)
	parts = append(parts, packet.Notes...)
	joined := strings.TrimSpace(strings.Join(parts, " "))
	if joined == "" {
		return fallback
	}
	return joined
}

func parseMaterialPacket(text string) (core.MaterialPacket, error) {
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
		switch normalizeMaterialHeading(line) {
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
		case "notes":
			current = "notes"
			recognized++
			continue
		}

		item := parseMaterialItem(line)
		if item == "" {
			if current == "notes" {
				item = line
			} else {
				continue
			}
		}
		switch current {
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
		case "notes":
			packet.Notes = append(packet.Notes, item)
		}
	}

	if recognized == 0 || packet.Empty() {
		return core.MaterialPacket{}, fmt.Errorf("no structured material packet found")
	}
	return packet, nil
}

func normalizeMaterialHeading(line string) string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(line), ":")
	trimmed = strings.ToUpper(trimmed)
	switch trimmed {
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
	case "NOTES":
		return "notes"
	default:
		return ""
	}
}

func parseMaterialItem(line string) string {
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
