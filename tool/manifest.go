//go:build linux

package tool

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/principal"
)

type schemaEnvelope struct {
	Properties map[string]schemaProperty `json:"properties"`
	Required   []string                  `json:"required"`
}

type schemaProperty struct {
	Type string `json:"type"`
}

// Manifest renders a concise machine-generated summary of active tools and
// execution constraints.
func (r *Registry) Manifest() string {
	defs := r.Definitions()
	lines := []string{RenderManifest(defs, r.externalManifests, r.externalExecutor), "", "exec constraints:"}

	execRoot := r.workspace
	if abs, err := filepath.Abs(r.workspace); err == nil {
		execRoot = abs
	}

	lines = append(lines,
		fmt.Sprintf("- exec_root: %s", execRoot),
		fmt.Sprintf("- default_timeout_sec: %d", int(defaultTimeout(r.timeout).Seconds())),
		fmt.Sprintf("- max_output_bytes: %d", r.maxOutputBytes),
	)
	return strings.Join(lines, "\n")
}

func (r *Registry) ManifestForPrincipal(p principal.Principal) string {
	defs := r.nativeDefinitionsForPrincipal(p)
	external := r.externalManifestsForPrincipal(p)
	lines := []string{RenderManifest(defs, external, r.externalExecutor), "", "exec constraints:"}

	execRoot := r.workspace
	if abs, err := filepath.Abs(r.workspace); err == nil {
		execRoot = abs
	}

	lines = append(lines,
		fmt.Sprintf("- exec_root: %s", execRoot),
		fmt.Sprintf("- default_timeout_sec: %d", int(defaultTimeout(r.timeout).Seconds())),
		fmt.Sprintf("- max_output_bytes: %d", r.maxOutputBytes),
	)
	return strings.Join(lines, "\n")
}

// RenderManifest renders tool definitions as stable plain text.
func RenderManifest(defs []agent.ToolDef, external []ExternalToolManifest, executor ExternalToolExecutor) string {
	if len(defs) == 0 && len(external) == 0 {
		return "tools:\n- (none)"
	}

	lines := []string{"tools:"}
	for _, def := range defs {
		description := strings.TrimSpace(def.Description)
		if description == "" {
			description = "(no description)"
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", def.Name, description))

		params := summarizeParameters(def.Parameters)
		if len(params) == 0 {
			lines = append(lines, "  params: (none)")
			continue
		}
		lines = append(lines, "  params: "+strings.Join(params, ", "))
	}
	for _, manifest := range external {
		manifest = NormalizeExternalToolManifest(manifest)
		description := fmt.Sprintf("external tool owned by %s", firstNonEmpty(strings.TrimSpace(manifest.Owner), "(unknown owner)"))
		lines = append(lines, fmt.Sprintf("- %s: %s", manifest.Name, description))
		params := summarizeParameters(manifest.IO.InputSchema)
		if len(params) == 0 {
			lines = append(lines, "  params: (none)")
		} else {
			lines = append(lines, "  params: "+strings.Join(params, ", "))
		}
		executable := executor != nil && executor.Supports(manifest)
		lines = append(lines, fmt.Sprintf("  executable: %t", executable))
		if !executable {
			lines = append(lines, "  reason: external manifest is visible but executor support is not wired yet")
		}
	}
	return strings.Join(lines, "\n")
}

func summarizeParameters(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}

	var schema schemaEnvelope
	if err := json.Unmarshal(raw, &schema); err != nil {
		return []string{"(invalid schema)"}
	}
	if len(schema.Properties) == 0 {
		return nil
	}

	required := make(map[string]struct{}, len(schema.Required))
	for _, name := range schema.Required {
		required[name] = struct{}{}
	}

	keys := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, name := range keys {
		prop := schema.Properties[name]
		propType := strings.TrimSpace(prop.Type)
		if propType == "" {
			propType = "any"
		}
		reqState := "optional"
		if _, ok := required[name]; ok {
			reqState = "required"
		}
		out = append(out, fmt.Sprintf("%s(%s,%s)", name, propType, reqState))
	}
	return out
}
