//go:build linux

package runtime

import (
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
)

func toolManifest(registry agent.ToolRegistry) string {
	return toolManifestForRunKind(registry, session.TurnRunKindInteractive)
}

func toolManifestForRunKind(registry agent.ToolRegistry, runKind session.TurnRunKind) string {
	if registry == nil {
		return ""
	}
	type manifestProvider interface {
		Manifest() string
	}
	defs := definitionsForRunKind(registry, runKind)
	if provider, ok := registry.(manifestProvider); ok && isInteractiveToolLane(runKind) {
		return provider.Manifest()
	}
	return renderToolManifest(defs)
}

func toolCapabilities(registry agent.ToolRegistry) prompt.ToolCapabilities {
	return toolCapabilitiesForRunKind(registry, session.TurnRunKindInteractive)
}

func toolCapabilitiesForRunKind(registry agent.ToolRegistry, runKind session.TurnRunKind) prompt.ToolCapabilities {
	if registry == nil {
		return prompt.ToolCapabilities{}
	}
	return prompt.ToolCapabilitiesFromDefs(definitionsForRunKind(registry, runKind))
}

func definitionsForRunKind(registry agent.ToolRegistry, runKind session.TurnRunKind) []agent.ToolDef {
	if registry == nil {
		return nil
	}
	defs := registry.Definitions()
	if isInteractiveToolLane(runKind) {
		return defs
	}
	allowed := toolLaneAllowlist(runKind)
	if len(allowed) == 0 {
		return nil
	}
	filtered := make([]agent.ToolDef, 0, len(defs))
	for _, def := range defs {
		if _, ok := allowed[strings.TrimSpace(def.Name)]; ok {
			filtered = append(filtered, def)
		}
	}
	return filtered
}

func isInteractiveToolLane(runKind session.TurnRunKind) bool {
	return runKind == "" || runKind == session.TurnRunKindInteractive
}

func toolLaneAllowlist(runKind session.TurnRunKind) map[string]struct{} {
	names := []string(nil)
	switch runKind {
	case session.TurnRunKindHeartbeat, session.TurnRunKindCron:
		names = []string{"update_plan", "update_operation", "operation_artifact", "memory", "session_search", "semantic_search"}
	case session.TurnRunKindDoctor:
		names = []string{"read_file", "list_dir", "search", "session_search", "semantic_search", "operation_artifact"}
	case session.TurnRunKindRecovery:
		names = []string{"read_file", "operation_artifact"}
	default:
		return nil
	}
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		out[name] = struct{}{}
	}
	return out
}

func renderToolManifest(defs []agent.ToolDef) string {
	if len(defs) == 0 {
		return ""
	}

	names := make([]string, 0, len(defs))
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
