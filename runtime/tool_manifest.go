//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
)

type toolManifestProvider interface {
	Manifest() string
}

func toolManifest(registry agent.ToolRegistry) string {
	return toolManifestForRunKind(registry, session.TurnRunKindInteractive)
}

func toolManifestForRunKind(registry agent.ToolRegistry, runKind session.TurnRunKind) string {
	if registry == nil {
		return ""
	}
	defs := definitionsForRunKind(registry, runKind)
	if provider, ok := registry.(toolManifestProvider); ok && isInteractiveToolLane(runKind) {
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

type runKindToolRegistry struct {
	base    agent.ToolRegistry
	runKind session.TurnRunKind
	allowed map[string]struct{}
}

func toolRegistryForRunKind(registry agent.ToolRegistry, runKind session.TurnRunKind) agent.ToolRegistry {
	if registry == nil {
		return nil
	}
	if isInteractiveToolLane(runKind) {
		return registry
	}
	return &runKindToolRegistry{
		base:    registry,
		runKind: runKind,
		allowed: toolLaneAllowlist(runKind),
	}
}

func (r *runKindToolRegistry) Definitions() []agent.ToolDef {
	if r == nil || r.base == nil {
		return nil
	}
	return filterToolDefinitions(r.base.Definitions(), r.allowed)
}

func (r *runKindToolRegistry) Manifest() string {
	return renderToolManifest(r.Definitions())
}

func (r *runKindToolRegistry) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	if r == nil || r.base == nil {
		return "", fmt.Errorf("tool registry unavailable")
	}
	if !toolAllowedByName(name, r.allowed) {
		return "", fmt.Errorf("tool %q is not available for %s turn lane", strings.TrimSpace(name), strings.TrimSpace(string(r.runKind)))
	}
	return r.base.Execute(ctx, name, input)
}

func (r *runKindToolRegistry) SupportsParallelToolCall(name string, input json.RawMessage) bool {
	if r == nil || r.base == nil || !toolAllowedByName(name, r.allowed) {
		return false
	}
	parallelSafe, ok := r.base.(agent.ParallelSafeToolRegistry)
	if !ok {
		return false
	}
	return parallelSafe.SupportsParallelToolCall(name, input)
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
	return filterToolDefinitions(defs, allowed)
}

func filterToolDefinitions(defs []agent.ToolDef, allowed map[string]struct{}) []agent.ToolDef {
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

func toolAllowedByName(name string, allowed map[string]struct{}) bool {
	if len(allowed) == 0 {
		return false
	}
	_, ok := allowed[strings.TrimSpace(name)]
	return ok
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
