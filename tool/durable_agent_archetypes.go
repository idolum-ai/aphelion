//go:build linux

package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
)

type durableAgentArchetypeProvenance struct {
	Name        string   `json:"name"`
	SourceRoot  string   `json:"source_root,omitempty"`
	InstalledAt string   `json:"installed_at"`
	Files       []string `json:"files"`
}

func (r *Registry) listDurableAgentArchetypes() (string, error) {
	summariesByName := make(map[string]durableagent.ArchetypeSummary)
	for _, root := range r.durableAgentArchetypeRoots() {
		summaries, err := durableagent.ListArchetypes(root)
		if err != nil {
			return "", err
		}
		for _, summary := range summaries {
			if _, exists := summariesByName[summary.Name]; exists {
				continue
			}
			summariesByName[summary.Name] = summary
		}
	}
	names := make([]string, 0, len(summariesByName))
	for name := range summariesByName {
		names = append(names, name)
	}
	sort.Strings(names)
	summaries := make([]durableagent.ArchetypeSummary, 0, len(names))
	for _, name := range names {
		summaries = append(summaries, summariesByName[name])
	}
	return renderDurableAgentArchetypeList(summaries), nil
}

func (r *Registry) showDurableAgentArchetype(in durableAgentInput) (string, error) {
	archetype, err := r.loadDurableAgentArchetype(in.Archetype)
	if err != nil {
		return "", err
	}
	return renderDurableAgentArchetypeShow(archetype), nil
}

func (r *Registry) createDurableAgentFromArchetype(in durableAgentInput, key session.SessionKey) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for create_from_archetype")
	}
	archetype, err := r.loadDurableAgentArchetype(in.Archetype)
	if err != nil {
		return "", err
	}
	createInput := durableAgentCreateInputFromArchetype(in, archetype)
	if _, err := r.createDurableAgent(createInput, key); err != nil {
		return "", err
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	provenance, profileRoot, err := installDurableAgentArchetypeTemplate(*agent, r.store, archetype)
	if err != nil {
		return "", err
	}
	return renderDurableAgentArchetypeCreate(*agent, archetype, profileRoot, provenance), nil
}

func (r *Registry) durableAgentArchetypeRoots() []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 2)
	add := func(root string) {
		root = strings.TrimSpace(root)
		if root == "" {
			return
		}
		root = filepath.Clean(root)
		if _, exists := seen[root]; exists {
			return
		}
		seen[root] = struct{}{}
		out = append(out, root)
	}
	add(filepath.Join(r.workspace, "agents", "archetypes"))
	if cwd, err := os.Getwd(); err == nil {
		add(filepath.Join(cwd, "agents", "archetypes"))
	}
	return out
}

func (r *Registry) loadDurableAgentArchetype(name string) (durableagent.Archetype, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return durableagent.Archetype{}, fmt.Errorf("durable_agent archetype is required")
	}
	var lastErr error
	for _, root := range r.durableAgentArchetypeRoots() {
		archetype, err := durableagent.LoadArchetype(root, name)
		if err == nil {
			return archetype, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return durableagent.Archetype{}, lastErr
	}
	return durableagent.Archetype{}, fmt.Errorf("durable_agent archetype %q not found", name)
}

func durableAgentCreateInputFromArchetype(in durableAgentInput, archetype durableagent.Archetype) durableAgentInput {
	createInput := in
	createInput.Action = "create"
	createInput.ChannelKind = firstNonEmpty(strings.TrimSpace(createInput.ChannelKind), "external_channel")

	defaultPolicy := parseDurableAgentArchetypePolicy(archetype.Profile["policy.md"])
	defaultCapabilities := parseDurableAgentArchetypeCapabilities(archetype.Profile["capabilities.md"])

	if createInput.PolicyPatch == nil {
		createInput.PolicyPatch = &durableAgentPolicyPatchInput{}
	} else {
		patch := *createInput.PolicyPatch
		createInput.PolicyPatch = &patch
	}
	if strings.TrimSpace(createInput.PolicyPatch.Charter) == "" {
		createInput.PolicyPatch.Charter = strings.TrimSpace(archetype.Profile["charter.md"])
	}
	if createInput.PolicyPatch.Capabilities == nil && len(defaultCapabilities) > 0 {
		createInput.PolicyPatch.Capabilities = defaultCapabilities
	}
	if strings.TrimSpace(createInput.PolicyPatch.DriftPolicy) == "" {
		createInput.PolicyPatch.DriftPolicy = firstNonEmpty(defaultPolicy["drift_policy"], "admin_review")
	}

	if createInput.PolicyOverrides == nil {
		createInput.PolicyOverrides = &durableAgentPolicyOverridesInput{}
	} else {
		overrides := *createInput.PolicyOverrides
		createInput.PolicyOverrides = &overrides
	}
	if strings.TrimSpace(createInput.PolicyOverrides.OutboundMode) == "" {
		createInput.PolicyOverrides.OutboundMode = firstNonEmpty(defaultPolicy["outbound_mode"], "read_only")
	}
	if strings.TrimSpace(createInput.PolicyOverrides.PublicSurfaceMode) == "" {
		createInput.PolicyOverrides.PublicSurfaceMode = firstNonEmpty(defaultPolicy["public_surface_mode"], "explicit_parent_relay_only")
	}
	if strings.TrimSpace(createInput.PolicyOverrides.SharedInferenceReuse) == "" {
		createInput.PolicyOverrides.SharedInferenceReuse = firstNonEmpty(defaultPolicy["shared_inference_reuse"], "disabled")
	}
	if strings.TrimSpace(createInput.PolicyOverrides.SharedInferenceReuseScope) == "" {
		createInput.PolicyOverrides.SharedInferenceReuseScope = firstNonEmpty(defaultPolicy["shared_inference_reuse_scope"], "public_prefix_only")
	}
	return createInput
}

func parseDurableAgentArchetypePolicy(raw string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	return out
}

func parseDurableAgentArchetypeCapabilities(raw string) []string {
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "-") {
			continue
		}
		capability := strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if capability == "" {
			continue
		}
		if before, _, ok := strings.Cut(capability, "#"); ok {
			capability = strings.TrimSpace(before)
		}
		if capability == "" {
			continue
		}
		key := strings.ToLower(capability)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, capability)
	}
	sort.Strings(out)
	return out
}

func installDurableAgentArchetypeTemplate(agent core.DurableAgent, store *session.SQLiteStore, archetype durableagent.Archetype) (durableAgentArchetypeProvenance, string, error) {
	memoryRoot, err := durableAgentMemoryRoot(agent, store)
	if err != nil {
		return durableAgentArchetypeProvenance{}, "", err
	}
	profileRoot := filepath.Join(memoryRoot, "profile")
	templateRoot := filepath.Join(profileRoot, "archetype")
	if err := os.MkdirAll(templateRoot, 0o755); err != nil {
		return durableAgentArchetypeProvenance{}, "", fmt.Errorf("create durable agent archetype template root: %w", err)
	}
	files := make([]string, 0, len(archetype.Files))
	for rel, content := range archetype.Files {
		target, err := durableAgentArchetypeTemplateTarget(templateRoot, rel)
		if err != nil {
			return durableAgentArchetypeProvenance{}, "", err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return durableAgentArchetypeProvenance{}, "", fmt.Errorf("create durable agent archetype template directory: %w", err)
		}
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			return durableAgentArchetypeProvenance{}, "", fmt.Errorf("write durable agent archetype template %s: %w", rel, err)
		}
		files = append(files, filepath.ToSlash(filepath.Join("profile", "archetype", rel)))
	}
	sort.Strings(files)
	provenance := durableAgentArchetypeProvenance{
		Name:        archetype.Name,
		SourceRoot:  archetype.Root,
		InstalledAt: time.Now().UTC().Format(time.RFC3339Nano),
		Files:       files,
	}
	raw, err := json.MarshalIndent(provenance, "", "  ")
	if err != nil {
		return durableAgentArchetypeProvenance{}, "", fmt.Errorf("encode durable agent archetype provenance: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(profileRoot, "ARCHETYPE.json"), raw, 0o600); err != nil {
		return durableAgentArchetypeProvenance{}, "", fmt.Errorf("write durable agent archetype provenance: %w", err)
	}
	return provenance, profileRoot, nil
}

func durableAgentArchetypeTemplateTarget(root string, rel string) (string, error) {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, "\x00") || strings.Contains(rel, "\\") {
		return "", fmt.Errorf("durable agent archetype template path %q is not allowed", rel)
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("durable agent archetype template path %q escapes template root", rel)
	}
	target := filepath.Clean(filepath.Join(root, filepath.FromSlash(clean)))
	targetRel, err := filepath.Rel(filepath.Clean(root), target)
	if err != nil {
		return "", fmt.Errorf("resolve durable agent archetype template path: %w", err)
	}
	if targetRel == ".." || strings.HasPrefix(targetRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("durable agent archetype template path %q escapes template root", rel)
	}
	return target, nil
}

func renderDurableAgentArchetypeList(summaries []durableagent.ArchetypeSummary) string {
	var b strings.Builder
	b.WriteString("action: durable-agent archetype list\n")
	fmt.Fprintf(&b, "count: %d\n", len(summaries))
	b.WriteString("archetypes:\n")
	if len(summaries) == 0 {
		b.WriteString("- none\n")
		return b.String()
	}
	for _, summary := range summaries {
		fmt.Fprintf(&b, "- name=%s examples=%d root=%s\n", summary.Name, len(summary.Examples), summary.Root)
	}
	return b.String()
}

func renderDurableAgentArchetypeShow(archetype durableagent.Archetype) string {
	summary := archetype.Summary()
	var b strings.Builder
	b.WriteString("action: durable-agent archetype show\n")
	fmt.Fprintf(&b, "name: %s\n", summary.Name)
	fmt.Fprintf(&b, "root: %s\n", summary.Root)
	b.WriteString("required_files:\n")
	for _, rel := range summary.RequiredFiles {
		fmt.Fprintf(&b, "- %s\n", rel)
	}
	b.WriteString("examples:\n")
	if len(summary.Examples) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, rel := range summary.Examples {
			fmt.Fprintf(&b, "- %s\n", rel)
		}
	}
	if runtime := strings.TrimSpace(archetype.Profile["runtime.md"]); runtime != "" {
		fmt.Fprintf(&b, "runtime: %s\n", truncateCompact(runtime, 600))
	}
	if agent := strings.TrimSpace(archetype.Files["AGENT.md"]); agent != "" {
		fmt.Fprintf(&b, "summary: %s\n", truncateCompact(agent, 600))
	}
	return b.String()
}

func renderDurableAgentArchetypeCreate(agent core.DurableAgent, archetype durableagent.Archetype, profileRoot string, provenance durableAgentArchetypeProvenance) string {
	var b strings.Builder
	b.WriteString("action: durable-agent archetype create\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "archetype: %s\n", strings.TrimSpace(archetype.Name))
	fmt.Fprintf(&b, "status: %s\n", strings.TrimSpace(agent.Status))
	fmt.Fprintf(&b, "channel_kind: %s\n", strings.TrimSpace(agent.ChannelKind))
	fmt.Fprintf(&b, "profile_root: %s\n", profileRoot)
	fmt.Fprintf(&b, "copied_files: %d\n", len(provenance.Files))
	b.WriteString("next: inspect profile/ARCHETYPE.json, then activate only after runtime surfaces and capability gates are ready\n")
	return b.String()
}
