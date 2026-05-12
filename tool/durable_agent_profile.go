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

type DurableAgentProfileSync struct {
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
	profileRoot, err = safeDirectoryUnderRootNoSymlink(memoryRoot, "profile")
	if err != nil {
		return durableAgentProfileSync{}, fmt.Errorf("create durable agent profile root: %w", err)
	}
	files := durableAgentManagedProfileFiles(agent, store)
	written := make([]string, 0, len(files))
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		content := durableAgentManagedProfileHeader(agent, name) + strings.TrimSpace(files[name]) + "\n"
		if _, err := safeWriteFileUnderRootNoSymlink(memoryRoot, filepath.ToSlash(filepath.Join("profile", name)), []byte(content), 0o600); err != nil {
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
	if err := writeDurableAgentProfileManifest(memoryRoot, durableAgentProfileManifest{
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

func SyncDurableAgentProfileFiles(agent core.DurableAgent, store *session.SQLiteStore) (DurableAgentProfileSync, error) {
	sync, err := syncDurableAgentProfileFiles(agent, store)
	if err != nil {
		return DurableAgentProfileSync{}, err
	}
	return DurableAgentProfileSync{
		Root:    sync.Root,
		Written: append([]string(nil), sync.Written...),
	}, nil
}

func durableAgentManagedProfileFiles(agent core.DurableAgent, store *session.SQLiteStore) map[string]string {
	policy := core.NormalizeDurableAgentLivePolicy(agent.LivePolicy)
	tailnetHostname := durableAgentProfileTailnetHostname(agent, policy)
	policyLines := []string{
		"# Ratified live policy",
		"",
		"- outbound_mode: " + strings.TrimSpace(policy.OutboundMode),
		"- drift_policy: " + strings.TrimSpace(policy.DriftPolicy),
		"- public_surface_mode: " + strings.TrimSpace(policy.PublicSurfaceMode),
		"- shared_inference_reuse: " + strings.TrimSpace(policy.SharedInferenceReuse),
		"- shared_inference_reuse_scope: " + strings.TrimSpace(policy.SharedInferenceReuseScope),
	}
	if strings.TrimSpace(policy.TailnetMode) != "" {
		policyLines = append(policyLines,
			"- tailnet_mode: "+strings.TrimSpace(policy.TailnetMode),
			"- tailnet_hostname: "+tailnetHostname,
			"- tailnet_tags: "+strings.Join(policy.TailnetTags, ","),
			"- tailnet_surface_policy: "+strings.TrimSpace(policy.TailnetSurfacePolicy),
		)
	}
	runtimeLines := []string{
		"# Runtime profile",
		"",
		"- agent_id: " + strings.TrimSpace(agent.AgentID),
		"- channel_kind: " + strings.TrimSpace(agent.ChannelKind),
		"- wakeup_mode: " + strings.TrimSpace(agent.WakeupMode),
		"- network_policy: " + strings.TrimSpace(agent.NetworkPolicy),
		"- child_runtime: active capability grants with child_runtime contracts only",
	}
	if strings.TrimSpace(policy.TailnetMode) != "" {
		runtimeLines = append(runtimeLines,
			"- tailnet_mode: "+strings.TrimSpace(policy.TailnetMode),
			"- tailnet_hostname: "+tailnetHostname,
			"- tailnet_surface_policy: "+strings.TrimSpace(policy.TailnetSurfacePolicy),
			"- tailnet_materialization: declared only until an observed tailnet surface appears in the parent registry",
		)
	}
	files := map[string]string{
		"charter.md":           firstNonEmpty(strings.TrimSpace(policy.Charter), "No child charter has been ratified yet."),
		"policy.md":            strings.Join(policyLines, "\n"),
		"capabilities.md":      durableAgentCapabilitiesProfile(policy.CapabilityEnvelope),
		"capability-ledger.md": durableAgentCapabilityLedgerProfile(agent, store),
		"growth.md":            durableAgentGrowthProfile(agent),
		"runtime.md":           strings.Join(runtimeLines, "\n"),
		"scorecard.md":         durableAgentScorecardProfile(agent),
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
	external := cfg.ExternalConfig()
	policy := core.NormalizeDurableAgentLivePolicy(agent.LivePolicy)
	tailnetHostname := durableAgentProfileTailnetHostname(agent, policy)
	lines := []string{"# Channel surface rules", ""}
	if external != nil && len(external.SurfaceRules) > 0 {
		lines = append(lines, "Surface upward:")
		for _, rule := range external.SurfaceRules {
			lines = append(lines, "- "+strings.TrimSpace(rule))
		}
	}
	if external != nil && len(external.NeverRetain) > 0 {
		lines = append(lines, "", "Never retain:")
		for _, rule := range external.NeverRetain {
			lines = append(lines, "- "+strings.TrimSpace(rule))
		}
	}
	if strings.TrimSpace(policy.TailnetMode) != "" {
		lines = append(lines, "", "Tailnet declaration:")
		lines = append(lines,
			"- tailnet_mode: "+strings.TrimSpace(policy.TailnetMode),
			"- tailnet_hostname: "+tailnetHostname,
			"- tailnet_tags: "+strings.Join(policy.TailnetTags, ","),
			"- tailnet_surface_policy: "+strings.TrimSpace(policy.TailnetSurfacePolicy),
			"- Treat this as declared only; verify actual materialization in the parent tailnet registry before claiming reachability.",
		)
	}
	if len(lines) <= 2 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func durableAgentProfileTailnetHostname(agent core.DurableAgent, policy core.DurableAgentLivePolicy) string {
	if hostname := strings.ToLower(strings.TrimSpace(policy.TailnetHostname)); hostname != "" {
		return hostname
	}
	hostname := strings.ToLower(strings.TrimSpace(agent.AgentID))
	hostname = strings.ReplaceAll(hostname, "_", "-")
	hostname = strings.ReplaceAll(hostname, ":", "-")
	hostname = strings.ReplaceAll(hostname, " ", "-")
	return hostname
}

func durableAgentGrowthProfile(agent core.DurableAgent) string {
	return strings.Join([]string{
		"# Growth protocol",
		"",
		"On wake or reinstall recovery:",
		"- Verify your actual profile files, grants, and runtime materialization before claiming you can act.",
		"- Prefer local read-only diagnosis and draft artifacts before asking for new surface area.",
		"- If blocked, submit one minimal delegation_request or delegation_report with evidence, the smallest useful capability, a success metric, and a rollback or trial boundary.",
		"- Ask upward only for reusable capability surfaces, not one-off feature-specific code paths.",
		"- Never self-grant, bypass parent/admin approval, or perform write/external effects without an active grant that materializes in this runtime.",
		"- When scripting around JSON, read JSON from a file/stdin and parse it with json.loads; never paste raw JSON literals with null/true/false directly into Python code.",
		"",
		"Useful request shape:",
		"- current_blocker: what you tried and what failed",
		"- evidence: file, grant, log, or transcript reference",
		"- minimal_capability: exact target_resource and allowed action",
		"- success_metric: how the parent can tell the request worked",
		"- trial_boundary: duration, path, account, or rate limit that keeps the test reversible",
	}, "\n")
}

func durableAgentScorecardProfile(agent core.DurableAgent) string {
	return strings.Join([]string{
		"# Child scorecard",
		"",
		"Parent/admin should reward:",
		"- Accurate statements about actual grants and runtime limits.",
		"- Small, evidence-backed capability requests that preserve rollback paths.",
		"- Clear uncertainty, concise status reports, and follow-through after approval.",
		"- Suppressing stale or fixed issues instead of re-reporting them as live blockers.",
		"",
		"Parent/admin should reject or downgrade:",
		"- Overclaiming capability, looping on near-success, or hiding missing materialization.",
		"- Broad requests when a narrower trial would prove the same thing.",
		"- Feature-specific harness changes that should emerge from governed conversation and grants.",
		"- Treating transient service errors as durable memory without verification.",
	}, "\n")
}

func durableAgentCapabilityLedgerProfile(agent core.DurableAgent, store *session.SQLiteStore) string {
	lines := []string{
		"# Capability ledger",
		"",
		"This ledger is parent-managed runtime evidence. Treat it as a snapshot and verify live behavior before relying on it.",
		"",
	}
	if store == nil {
		return strings.Join(append(lines, "No session store was available while rendering this ledger."), "\n")
	}

	principalID := core.DurableAgentPrincipal(agent.AgentID)
	grants, err := store.CapabilityGrants(100, session.CapabilityGrantStatusActive, "", principalID)
	if err != nil {
		return strings.Join(append(lines, "Could not load active capability grants: "+err.Error()), "\n")
	}
	lines = append(lines, "Active grants:")
	if len(grants) == 0 {
		lines = append(lines, "- none")
	}
	for _, grant := range grants {
		materialization := "child_runtime=missing"
		if strings.TrimSpace(grant.Contract) != "" || strings.TrimSpace(grant.Constraints) != "" {
			if _, found, err := core.ExtractChildRuntimeContract(grant.Contract, grant.Constraints); err != nil {
				materialization = "child_runtime=invalid"
			} else if found {
				materialization = "child_runtime=present"
			} else {
				materialization = "child_runtime=missing"
			}
		}
		lines = append(lines, fmt.Sprintf(
			"- grant_id=%s kind=%s target=%s actions=%s %s",
			strings.TrimSpace(grant.GrantID),
			strings.TrimSpace(string(grant.Kind)),
			strings.TrimSpace(grant.TargetResource),
			strings.Join(grant.AllowedActions, ","),
			materialization,
		))
	}

	agreements, err := store.DurableChildAgreementsForAgent(agent.AgentID, 20)
	if err != nil {
		lines = append(lines, "", "Agreements:", "- could not load agreements: "+err.Error())
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "", "Recent agreements:")
	if len(agreements) == 0 {
		lines = append(lines, "- none")
	}
	for _, agreement := range agreements {
		lines = append(lines, fmt.Sprintf(
			"- agreement_id=%s status=%s summary=%s",
			strings.TrimSpace(agreement.AgreementID),
			strings.TrimSpace(string(agreement.Status)),
			compactDurableAgentProfileText(agreement.Summary, 180),
		))
	}
	return strings.Join(lines, "\n")
}

func compactDurableAgentProfileText(raw string, limit int) string {
	clean := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if limit <= 0 || len(clean) <= limit {
		return clean
	}
	if limit <= 3 {
		return clean[:limit]
	}
	return clean[:limit-3] + "..."
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

func writeDurableAgentProfileManifest(memoryRoot string, manifest durableAgentProfileManifest) error {
	if _, err := safeDirectoryUnderRootNoSymlink(memoryRoot, "profile"); err != nil {
		return fmt.Errorf("create durable agent profile root: %w", err)
	}
	manifest = normalizeDurableAgentProfileManifest(manifest)
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode durable agent profile manifest: %w", err)
	}
	if _, err := safeWriteFileUnderRootNoSymlink(memoryRoot, filepath.ToSlash(filepath.Join("profile", "PROFILE.json")), append(raw, '\n'), 0o600); err != nil {
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
	profileRoot, err = safeDirectoryUnderRootNoSymlink(memoryRoot, "profile")
	if err != nil {
		return durableAgentProfileSync{}, fmt.Errorf("create durable agent profile root: %w", err)
	}
	header := strings.Join([]string{
		"<!-- profile_ownership: child_authored -->",
		"<!-- profile_source: admin_approved_profile_edit -->",
		"<!-- agent_id: " + strings.TrimSpace(agent.AgentID) + " -->",
		"<!-- reason: " + strings.TrimSpace(reason) + " -->",
		"",
	}, "\n")
	if _, err := safeWriteFileUnderRootNoSymlink(memoryRoot, filepath.ToSlash(filepath.Join("profile", targetFile)), []byte(header+strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		return durableAgentProfileSync{}, fmt.Errorf("write durable agent profile edit: %w", err)
	}
	manifest := loadDurableAgentProfileManifest(profileRoot)
	manifest.AgentID = strings.TrimSpace(agent.AgentID)
	manifest.PolicyHash = strings.TrimSpace(agent.PolicyHash)
	manifest.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	manifest.Files = append(manifest.Files, durableAgentProfileManifestEntry{Path: filepath.ToSlash(filepath.Join("profile", targetFile)), Ownership: "child_authored", Source: "admin_approved_profile_edit"})
	if err := writeDurableAgentProfileManifest(memoryRoot, manifest); err != nil {
		return durableAgentProfileSync{}, err
	}
	return durableAgentProfileSync{Root: profileRoot, Written: []string{filepath.ToSlash(filepath.Join("profile", targetFile)), "profile/PROFILE.json"}}, nil
}
