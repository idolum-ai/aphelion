//go:build linux

package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
)

type doctorMaintainerDelegate struct {
	Agent        core.DurableAgent
	MemoryRoot   string
	ProfileRoot  string
	RuntimeRules string
	Charter      string
	Capabilities string
}

type doctorMaintainerArchetypeProvenance struct {
	Name string `json:"name"`
}

type doctorArtifactManifest struct {
	AgentID   string                        `json:"agent_id"`
	UpdatedAt time.Time                     `json:"updated_at"`
	Artifacts []doctorArtifactManifestEntry `json:"artifacts"`
}

type doctorArtifactManifestEntry struct {
	Path      string    `json:"path"`
	Kind      string    `json:"kind,omitempty"`
	Source    string    `json:"source,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	SHA256    string    `json:"sha256"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (r *Runtime) doctorMaintainerDelegate() (*doctorMaintainerDelegate, error) {
	if r == nil || r.store == nil {
		return nil, nil
	}
	agents, err := r.store.ListDurableAgents()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(agents, func(i, j int) bool {
		return strings.TrimSpace(agents[i].AgentID) < strings.TrimSpace(agents[j].AgentID)
	})
	for _, agent := range agents {
		if !strings.EqualFold(strings.TrimSpace(agent.Status), "active") {
			continue
		}
		delegate, ok, err := r.doctorMaintainerDelegateFromAgent(agent)
		if err != nil {
			return nil, err
		}
		if ok {
			return delegate, nil
		}
	}
	return nil, nil
}

func (r *Runtime) doctorMaintainerDelegateFromAgent(agent core.DurableAgent) (*doctorMaintainerDelegate, bool, error) {
	memoryRoot, err := r.doctorDurableAgentMemoryRoot(agent)
	if err != nil {
		return nil, false, err
	}
	profileRoot := filepath.Join(memoryRoot, "profile")
	raw, err := os.ReadFile(filepath.Join(profileRoot, "ARCHETYPE.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read maintainer archetype provenance for %s: %w", strings.TrimSpace(agent.AgentID), err)
	}
	var provenance doctorMaintainerArchetypeProvenance
	if err := json.Unmarshal(raw, &provenance); err != nil {
		return nil, false, fmt.Errorf("decode maintainer archetype provenance for %s: %w", strings.TrimSpace(agent.AgentID), err)
	}
	if !strings.EqualFold(strings.TrimSpace(provenance.Name), doctorMaintainerArchetype) {
		return nil, false, nil
	}
	return &doctorMaintainerDelegate{
		Agent:        agent,
		MemoryRoot:   memoryRoot,
		ProfileRoot:  profileRoot,
		RuntimeRules: readDoctorProfileFile(filepath.Join(profileRoot, "archetype", "profile", "runtime.md")),
		Charter:      readDoctorProfileFile(filepath.Join(profileRoot, "archetype", "profile", "charter.md")),
		Capabilities: readDoctorProfileFile(filepath.Join(profileRoot, "archetype", "profile", "capabilities.md")),
	}, true, nil
}

func (r *Runtime) doctorDurableAgentMemoryRoot(agent core.DurableAgent) (string, error) {
	_, memoryRoot := durableagent.LocalRoots(agent.AgentID, agent.LocalStorageRoots)
	if strings.TrimSpace(memoryRoot) == "" && r != nil && r.store != nil {
		if dbPath := strings.TrimSpace(r.store.DBPath()); dbPath != "" {
			_, memoryRoot = durableagent.DefaultLocalRoots(dbPath, strings.TrimSpace(agent.AgentID))
		}
	}
	if strings.TrimSpace(memoryRoot) == "" {
		return "", fmt.Errorf("durable agent %q has no local memory root", strings.TrimSpace(agent.AgentID))
	}
	return memoryRoot, nil
}

func readDoctorProfileFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func (r *Runtime) writeDoctorMaintainerReport(maintainer doctorMaintainerDelegate, report string, telegramReport string, now time.Time) (string, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	artifactRoot := filepath.Join(maintainer.MemoryRoot, "artifacts")
	rel := filepath.ToSlash(filepath.Join("reports", now.UTC().Format("20060102T150405Z")+"-doctor.md"))
	target := filepath.Join(artifactRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", fmt.Errorf("create doctor maintainer artifact directory: %w", err)
	}
	content := strings.Join([]string{
		"# Doctor Report",
		"",
		"generated_at_utc: " + now.UTC().Format(time.RFC3339),
		"delegate_agent_id: " + strings.TrimSpace(maintainer.Agent.AgentID),
		"delegate_archetype: " + doctorMaintainerArchetype,
		"mode: read_only",
		"",
		"## Telegram Summary",
		"",
		strings.TrimSpace(telegramReport),
		"",
		"## Full Report",
		"",
		strings.TrimSpace(report),
		"",
	}, "\n")
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("write doctor maintainer artifact: %w", err)
	}
	sum := sha256.Sum256([]byte(content))
	hash := "sha256:" + hex.EncodeToString(sum[:])
	if err := writeDoctorMaintainerArtifactManifest(artifactRoot, strings.TrimSpace(maintainer.Agent.AgentID), doctorArtifactManifestEntry{
		Path:      rel,
		Kind:      "doctor_report",
		Source:    "doctor_delegate",
		Reason:    "/health diagnose delegated read-only diagnosis",
		SHA256:    hash,
		UpdatedAt: now.UTC(),
	}); err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join("artifacts", rel)), nil
}

func writeDoctorMaintainerArtifactManifest(artifactRoot string, agentID string, entry doctorArtifactManifestEntry) error {
	manifestPath := filepath.Join(artifactRoot, "ARTIFACTS.json")
	manifest := doctorArtifactManifest{
		AgentID:   strings.TrimSpace(agentID),
		Artifacts: []doctorArtifactManifestEntry{},
	}
	if raw, err := os.ReadFile(manifestPath); err == nil {
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return fmt.Errorf("decode doctor maintainer artifact manifest: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read doctor maintainer artifact manifest: %w", err)
	}
	manifest.AgentID = strings.TrimSpace(agentID)
	entry.Path = strings.TrimSpace(filepath.ToSlash(entry.Path))
	entry.Kind = strings.TrimSpace(entry.Kind)
	entry.Source = strings.TrimSpace(entry.Source)
	entry.Reason = strings.TrimSpace(entry.Reason)
	entry.SHA256 = strings.TrimSpace(entry.SHA256)
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = time.Now().UTC()
	}
	replaced := false
	for i := range manifest.Artifacts {
		if manifest.Artifacts[i].Path == entry.Path {
			manifest.Artifacts[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		manifest.Artifacts = append(manifest.Artifacts, entry)
	}
	sort.SliceStable(manifest.Artifacts, func(i, j int) bool {
		return manifest.Artifacts[i].Path < manifest.Artifacts[j].Path
	})
	manifest.UpdatedAt = entry.UpdatedAt.UTC()
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode doctor maintainer artifact manifest: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(artifactRoot, 0o700); err != nil {
		return fmt.Errorf("create doctor maintainer artifact root: %w", err)
	}
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		return fmt.Errorf("write doctor maintainer artifact manifest: %w", err)
	}
	return nil
}

func writeDoctorMaintainerDelegate(b *strings.Builder, maintainer *doctorMaintainerDelegate) {
	if maintainer == nil {
		writeDoctorKV(b, "maintainer_delegate_status", "absent")
		writeDoctorLine(b, "maintainer_delegate_next=\"create and activate a durable_agent from archetype aphelion-maintainer to route /health diagnose through the maintained child profile\"")
		return
	}
	writeDoctorKV(b, "maintainer_delegate_status", "active")
	writeDoctorKV(b, "maintainer_delegate_agent_id", strings.TrimSpace(maintainer.Agent.AgentID))
	writeDoctorKV(b, "maintainer_delegate_archetype", doctorMaintainerArchetype)
	writeDoctorKV(b, "maintainer_delegate_memory_root", strings.TrimSpace(maintainer.MemoryRoot))
	writeDoctorKV(b, "maintainer_delegate_profile_root", strings.TrimSpace(maintainer.ProfileRoot))
	writeDoctorKV(b, "maintainer_delegate_channel_kind", strings.TrimSpace(maintainer.Agent.ChannelKind))
	writeDoctorKV(b, "maintainer_delegate_outbound_mode", strings.TrimSpace(maintainer.Agent.LivePolicy.OutboundMode))
	writeDoctorKV(b, "maintainer_delegate_capabilities", strings.Join(maintainer.Agent.LivePolicy.CapabilityEnvelope, ","))
	if strings.TrimSpace(maintainer.RuntimeRules) != "" {
		writeDoctorLine(b, "Maintainer runtime boundary:")
		writeDoctorLine(b, truncatePreview(maintainer.RuntimeRules, 1200))
	}
	if strings.TrimSpace(maintainer.Charter) != "" {
		writeDoctorLine(b, "Maintainer charter:")
		writeDoctorLine(b, truncatePreview(maintainer.Charter, 700))
	}
	if strings.TrimSpace(maintainer.Capabilities) != "" {
		writeDoctorLine(b, "Maintainer archetype capabilities:")
		writeDoctorLine(b, truncatePreview(maintainer.Capabilities, 700))
	}
}
