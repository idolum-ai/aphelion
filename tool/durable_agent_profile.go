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
	"github.com/idolum-ai/aphelion/session"
)

type durableAgentProfileSync struct {
	Root    string
	Written []string
}

type durableAgentProfileManifest struct {
	AgentID    string                             `json:"agent_id"`
	PolicyHash string                             `json:"policy_hash,omitempty"`
	UpdatedAt  string                             `json:"updated_at"`
	Files      []durableAgentProfileManifestEntry `json:"files"`
}

type durableAgentProfileManifestEntry struct {
	Path      string `json:"path"`
	Ownership string `json:"ownership"`
	Source    string `json:"source"`
}

func syncDurableAgentProfileFiles(agent core.DurableAgent, store *session.SQLiteStore) (durableAgentProfileSync, error) {
	memoryRoot, err := durableAgentMemoryRoot(agent, store)
	if err != nil {
		return durableAgentProfileSync{}, err
	}
	profileRoot := filepath.Join(memoryRoot, "profile")
	if err := os.MkdirAll(profileRoot, 0o755); err != nil {
		return durableAgentProfileSync{}, fmt.Errorf("create durable agent profile root: %w", err)
	}
	files := durableAgentManagedProfileFiles(agent)
	written := make([]string, 0, len(files))
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(profileRoot, filepath.FromSlash(name))
		content := durableAgentManagedProfileHeader(agent, name) + strings.TrimSpace(files[name]) + "\n"
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return durableAgentProfileSync{}, fmt.Errorf("write durable agent profile file %s: %w", name, err)
		}
		written = append(written, filepath.ToSlash(filepath.Join("profile", name)))
	}
	existingManifest := loadDurableAgentProfileManifest(profileRoot)
	childFiles := make([]string, 0)
	for _, entry := range existingManifest.Files {
		if strings.TrimSpace(entry.Ownership) == "child_authored" {
			childFiles = append(childFiles, strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(entry.Path)), "profile/"))
		}
	}
	if err := writeDurableAgentProfileManifest(profileRoot, durableAgentProfileManifest{
		AgentID:    strings.TrimSpace(agent.AgentID),
		PolicyHash: strings.TrimSpace(agent.PolicyHash),
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		Files:      durableAgentProfileManifestEntries(names, childFiles),
	}); err != nil {
		return durableAgentProfileSync{}, err
	}
	written = append(written, "profile/PROFILE.json")
	return durableAgentProfileSync{Root: profileRoot, Written: written}, nil
}

func durableAgentManagedProfileFiles(agent core.DurableAgent) map[string]string {
	policy := core.NormalizeDurableAgentLivePolicy(agent.LivePolicy)
	files := map[string]string{
		"charter.md": firstNonEmpty(strings.TrimSpace(policy.Charter), "No child charter has been ratified yet."),
		"policy.md": strings.Join([]string{
			"# Ratified live policy",
			"",
			"- outbound_mode: " + strings.TrimSpace(policy.OutboundMode),
			"- drift_policy: " + strings.TrimSpace(policy.DriftPolicy),
			"- public_surface_mode: " + strings.TrimSpace(policy.PublicSurfaceMode),
			"- shared_inference_reuse: " + strings.TrimSpace(policy.SharedInferenceReuse),
			"- shared_inference_reuse_scope: " + strings.TrimSpace(policy.SharedInferenceReuseScope),
		}, "\n"),
		"capabilities.md": durableAgentCapabilitiesProfile(policy.CapabilityEnvelope),
		"runtime.md": strings.Join([]string{
			"# Runtime profile",
			"",
			"- agent_id: " + strings.TrimSpace(agent.AgentID),
			"- channel_kind: " + strings.TrimSpace(agent.ChannelKind),
			"- wakeup_mode: " + strings.TrimSpace(agent.WakeupMode),
			"- network_policy: " + strings.TrimSpace(agent.NetworkPolicy),
			"- runtime_materialization: active capability grants with child_runtime contracts only",
		}, "\n"),
	}
	if surface := durableAgentSurfaceProfile(agent); strings.TrimSpace(surface) != "" {
		files["surface-rules.md"] = surface
	}
	return files
}

func durableAgentCapabilitiesProfile(capabilities []string) string {
	capabilities = normalizePolicyCapabilities(capabilities)
	if len(capabilities) == 0 {
		return "# Capability envelope\n\nNo ratified child capabilities."
	}
	lines := []string{"# Capability envelope", ""}
	for _, capability := range capabilities {
		lines = append(lines, "- "+strings.TrimSpace(capability))
	}
	return strings.Join(lines, "\n")
}

func durableAgentSurfaceProfile(agent core.DurableAgent) string {
	cfg := core.NormalizeDurableAgentChannelConfig(agent.ChannelConfig)
	if cfg.Email == nil {
		return ""
	}
	lines := []string{"# Channel surface rules", ""}
	if len(cfg.Email.SurfaceRules) > 0 {
		lines = append(lines, "Surface upward:")
		for _, rule := range cfg.Email.SurfaceRules {
			lines = append(lines, "- "+strings.TrimSpace(rule))
		}
	}
	if len(cfg.Email.NeverRetain) > 0 {
		lines = append(lines, "", "Never retain:")
		for _, rule := range cfg.Email.NeverRetain {
			lines = append(lines, "- "+strings.TrimSpace(rule))
		}
	}
	if len(lines) <= 2 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func durableAgentManagedProfileHeader(agent core.DurableAgent, name string) string {
	return strings.Join([]string{
		"<!-- profile_ownership: parent_managed -->",
		"<!-- profile_source: parent_policy -->",
		"<!-- agent_id: " + strings.TrimSpace(agent.AgentID) + " -->",
		"<!-- policy_hash: " + strings.TrimSpace(agent.PolicyHash) + " -->",
		"",
	}, "\n")
}

func durableAgentProfileManifestEntries(parentManaged []string, childAuthored []string) []durableAgentProfileManifestEntry {
	entries := make([]durableAgentProfileManifestEntry, 0, len(parentManaged)+len(childAuthored))
	for _, name := range parentManaged {
		entries = append(entries, durableAgentProfileManifestEntry{Path: filepath.ToSlash(filepath.Join("profile", name)), Ownership: "parent_managed", Source: "parent_policy"})
	}
	for _, name := range childAuthored {
		entries = append(entries, durableAgentProfileManifestEntry{Path: filepath.ToSlash(filepath.Join("profile", name)), Ownership: "child_authored", Source: "admin_approved_profile_edit"})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}

func writeDurableAgentProfileManifest(profileRoot string, manifest durableAgentProfileManifest) error {
	manifest = normalizeDurableAgentProfileManifest(manifest)
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode durable agent profile manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(profileRoot, "PROFILE.json"), append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write durable agent profile manifest: %w", err)
	}
	return nil
}

func loadDurableAgentProfileManifest(profileRoot string) durableAgentProfileManifest {
	raw, err := os.ReadFile(filepath.Join(profileRoot, "PROFILE.json"))
	if err != nil {
		return durableAgentProfileManifest{}
	}
	var manifest durableAgentProfileManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return durableAgentProfileManifest{}
	}
	return normalizeDurableAgentProfileManifest(manifest)
}

func normalizeDurableAgentProfileManifest(manifest durableAgentProfileManifest) durableAgentProfileManifest {
	manifest.AgentID = strings.TrimSpace(manifest.AgentID)
	manifest.PolicyHash = strings.TrimSpace(manifest.PolicyHash)
	manifest.UpdatedAt = strings.TrimSpace(manifest.UpdatedAt)
	if manifest.UpdatedAt == "" {
		manifest.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	seen := map[string]durableAgentProfileManifestEntry{}
	for _, entry := range manifest.Files {
		entry.Path = filepath.ToSlash(strings.TrimSpace(entry.Path))
		entry.Ownership = strings.TrimSpace(entry.Ownership)
		entry.Source = strings.TrimSpace(entry.Source)
		if entry.Path == "" {
			continue
		}
		seen[entry.Path] = entry
	}
	manifest.Files = make([]durableAgentProfileManifestEntry, 0, len(seen))
	for _, entry := range seen {
		manifest.Files = append(manifest.Files, entry)
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	return manifest
}

func applyDurableAgentProfileEdit(agent core.DurableAgent, store *session.SQLiteStore, targetFile string, content string, reason string) (durableAgentProfileSync, error) {
	targetFile = filepath.ToSlash(strings.TrimSpace(targetFile))
	if targetFile == "" {
		return durableAgentProfileSync{}, fmt.Errorf("profile_edit target_file is required")
	}
	allowed := map[string]struct{}{"persona.md": {}, "skills.md": {}, "notes.md": {}}
	if _, ok := allowed[targetFile]; !ok {
		return durableAgentProfileSync{}, fmt.Errorf("profile_edit target_file must be persona.md, skills.md, or notes.md")
	}
	if strings.TrimSpace(content) == "" {
		return durableAgentProfileSync{}, fmt.Errorf("profile_edit content is required")
	}
	memoryRoot, err := durableAgentMemoryRoot(agent, store)
	if err != nil {
		return durableAgentProfileSync{}, err
	}
	profileRoot := filepath.Join(memoryRoot, "profile")
	if err := os.MkdirAll(profileRoot, 0o755); err != nil {
		return durableAgentProfileSync{}, fmt.Errorf("create durable agent profile root: %w", err)
	}
	path := filepath.Join(profileRoot, filepath.FromSlash(targetFile))
	if rel, err := filepath.Rel(profileRoot, path); err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return durableAgentProfileSync{}, fmt.Errorf("profile_edit target_file escapes profile root")
	}
	header := strings.Join([]string{
		"<!-- profile_ownership: child_authored -->",
		"<!-- profile_source: admin_approved_profile_edit -->",
		"<!-- agent_id: " + strings.TrimSpace(agent.AgentID) + " -->",
		"<!-- reason: " + strings.TrimSpace(reason) + " -->",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(header+strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		return durableAgentProfileSync{}, fmt.Errorf("write durable agent profile edit: %w", err)
	}
	manifest := loadDurableAgentProfileManifest(profileRoot)
	manifest.AgentID = strings.TrimSpace(agent.AgentID)
	manifest.PolicyHash = strings.TrimSpace(agent.PolicyHash)
	manifest.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	manifest.Files = append(manifest.Files, durableAgentProfileManifestEntry{Path: filepath.ToSlash(filepath.Join("profile", targetFile)), Ownership: "child_authored", Source: "admin_approved_profile_edit"})
	if err := writeDurableAgentProfileManifest(profileRoot, manifest); err != nil {
		return durableAgentProfileSync{}, err
	}
	return durableAgentProfileSync{Root: profileRoot, Written: []string{filepath.ToSlash(filepath.Join("profile", targetFile)), "profile/PROFILE.json"}}, nil
}
