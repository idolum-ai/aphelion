//go:build linux

package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

type durableAgentProfileSync struct {
	Root    string
	Written []string
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
		if err := os.WriteFile(path, []byte(strings.TrimSpace(files[name])+"\n"), 0o600); err != nil {
			return durableAgentProfileSync{}, fmt.Errorf("write durable agent profile file %s: %w", name, err)
		}
		written = append(written, filepath.ToSlash(filepath.Join("profile", name)))
	}
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
