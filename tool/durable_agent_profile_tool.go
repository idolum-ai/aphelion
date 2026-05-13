//go:build linux

package tool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Registry) showDurableAgentProfile(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for profile_show")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	memoryRoot, err := durableAgentMemoryRoot(*agent, r.store)
	if err != nil {
		return "", err
	}
	profileRoot := filepath.Join(memoryRoot, "profile")
	manifest := loadDurableAgentProfileManifest(profileRoot)
	return renderDurableAgentProfile("show", *agent, profileRoot, manifest, nil), nil
}

func (r *Registry) applyDurableAgentProfile(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for profile_apply")
	}
	if in.ProfileEdit == nil {
		return "", fmt.Errorf("durable_agent profile_apply requires profile_edit")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	reason := firstNonEmpty(strings.TrimSpace(in.ProfileEdit.Reason), strings.TrimSpace(in.Reason))
	sync, err := applyDurableAgentProfileEdit(*agent, r.store, in.ProfileEdit.TargetFile, in.ProfileEdit.Content, reason)
	if err != nil {
		return "", err
	}
	manifest := loadDurableAgentProfileManifest(sync.Root)
	return renderDurableAgentProfile("apply", *agent, sync.Root, manifest, sync.Written), nil
}

func (r *Registry) createDurableAgentSnapshot(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for snapshot_create")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	var state *core.DurableAgentState
	existingState, err := r.store.DurableAgentState(agent.AgentID)
	if err == nil {
		state = existingState
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	dbPath := strings.TrimSpace(r.store.DBPath())
	reason := strings.TrimSpace(in.Reason)
	if in.Snapshot != nil && strings.TrimSpace(in.Snapshot.Reason) != "" {
		reason = strings.TrimSpace(in.Snapshot.Reason)
	}
	manifest, err := durableagent.CreateSnapshot(*agent, state, dbPath, reason, time.Now().UTC())
	if err != nil {
		return "", err
	}
	return renderDurableAgentSnapshotCreate(*agent, *manifest), nil
}

func (r *Registry) listDurableAgentSnapshots(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for snapshot_list")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	limit := 10
	if in.Snapshot != nil && in.Snapshot.Limit > 0 {
		limit = in.Snapshot.Limit
	}
	if limit > 50 {
		limit = 50
	}
	records, err := durableagent.ListSnapshots(*agent, strings.TrimSpace(r.store.DBPath()), limit)
	if err != nil {
		return "", err
	}
	return renderDurableAgentSnapshotList(*agent, records), nil
}

func (r *Registry) restoreDurableAgentSnapshot(
	ctx context.Context,
	in durableAgentInput,
	p principal.Principal,
	key session.SessionKey,
) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for snapshot_restore")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	snapshotID := ""
	if in.Snapshot != nil {
		snapshotID = strings.TrimSpace(in.Snapshot.SnapshotID)
	}
	if snapshotID == "" {
		return "", fmt.Errorf("durable_agent snapshot.snapshot_id is required for snapshot_restore")
	}
	manifest, _, err := durableagent.LoadSnapshot(*agent, strings.TrimSpace(r.store.DBPath()), snapshotID)
	if err != nil {
		return "", err
	}
	if r.durableSnapshotRestoreApprover == nil {
		return "", fmt.Errorf("durable_agent snapshot_restore requires an interactive admin approval channel")
	}
	restoreReason := strings.TrimSpace(in.Reason)
	if in.Snapshot != nil && strings.TrimSpace(in.Snapshot.Reason) != "" {
		restoreReason = strings.TrimSpace(in.Snapshot.Reason)
	}
	approval, err := r.durableSnapshotRestoreApprover.ConfirmDurableSnapshotRestore(ctx, DurableSnapshotRestoreApprovalRequest{
		Principal:         p,
		SessionKey:        key,
		Agent:             *agent,
		SnapshotID:        strings.TrimSpace(manifest.SnapshotID),
		SnapshotReason:    firstNonEmpty(restoreReason, strings.TrimSpace(manifest.Reason)),
		SnapshotCreatedAt: manifest.CreatedAt.UTC(),
	})
	if err != nil {
		return "", err
	}
	if !approval.Approved {
		return renderDurableAgentSnapshotRestore(*agent, *manifest, approval, false), nil
	}
	restoredManifest, err := durableagent.RestoreSnapshot(*agent, strings.TrimSpace(r.store.DBPath()), snapshotID, time.Now().UTC())
	if err != nil {
		return "", err
	}
	if err := r.store.UpsertDurableAgent(restoredManifest.Agent); err != nil {
		return "", err
	}
	if restoredManifest.State != nil {
		if err := r.store.SaveDurableAgentState(*restoredManifest.State); err != nil {
			return "", err
		}
	}
	return renderDurableAgentSnapshotRestore(*agent, *restoredManifest, approval, true), nil
}

func renderDurableAgentProfile(action string, agent core.DurableAgent, profileRoot string, manifest durableAgentProfileManifest, written []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "action: durable-agent profile %s\n", strings.TrimSpace(action))
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "profile_root: %s\n", strings.TrimSpace(profileRoot))
	fmt.Fprintf(&b, "policy_hash: %s\n", strings.TrimSpace(manifest.PolicyHash))
	fmt.Fprintf(&b, "manifest_updated_at: %s\n", strings.TrimSpace(manifest.UpdatedAt))
	if len(written) > 0 {
		fmt.Fprintf(&b, "written: %s\n", strings.Join(written, ","))
	}
	b.WriteString("files:\n")
	if len(manifest.Files) == 0 {
		b.WriteString("- none\n")
		return b.String()
	}
	for _, entry := range manifest.Files {
		fmt.Fprintf(&b, "- path=%s ownership=%s source=%s\n", entry.Path, entry.Ownership, entry.Source)
	}
	return b.String()
}

func renderDurableAgentSnapshotCreate(agent core.DurableAgent, manifest durableagent.SnapshotManifest) string {
	var b strings.Builder
	b.WriteString("action: durable-agent snapshot create\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "snapshot_id: %s\n", strings.TrimSpace(manifest.SnapshotID))
	if !manifest.CreatedAt.IsZero() {
		fmt.Fprintf(&b, "created_at: %s\n", manifest.CreatedAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "reason: %s\n", firstNonEmpty(strings.TrimSpace(manifest.Reason), "-"))
	if manifest.State != nil {
		b.WriteString("state_saved: true\n")
	} else {
		b.WriteString("state_saved: false\n")
	}
	b.WriteString("next: snapshot_list or snapshot_restore\n")
	return b.String()
}

func renderDurableAgentSnapshotList(agent core.DurableAgent, records []durableagent.SnapshotRecord) string {
	var b strings.Builder
	b.WriteString("action: durable-agent snapshot list\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "count: %d\n", len(records))
	if len(records) == 0 {
		b.WriteString("snapshots: -\n")
		b.WriteString("next: snapshot_create\n")
		return b.String()
	}
	b.WriteString("snapshots:\n")
	for _, record := range records {
		created := "-"
		if !record.CreatedAt.IsZero() {
			created = record.CreatedAt.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(
			&b,
			"- snapshot_id=%s created_at=%s reason=%s\n",
			strings.TrimSpace(record.SnapshotID),
			created,
			firstNonEmpty(strings.TrimSpace(record.Reason), "-"),
		)
	}
	b.WriteString("next: snapshot_restore\n")
	return b.String()
}

func renderDurableAgentSnapshotRestore(agent core.DurableAgent, manifest durableagent.SnapshotManifest, approval DurableSnapshotRestoreApprovalDecision, changed bool) string {
	var b strings.Builder
	b.WriteString("action: durable-agent snapshot restore\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "snapshot_id: %s\n", strings.TrimSpace(manifest.SnapshotID))
	if !manifest.CreatedAt.IsZero() {
		fmt.Fprintf(&b, "snapshot_created_at: %s\n", manifest.CreatedAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "approved: %t\n", approval.Approved)
	fmt.Fprintf(&b, "timed_out: %t\n", approval.TimedOut)
	fmt.Fprintf(&b, "changed: %t\n", changed)
	fmt.Fprintf(&b, "reason: %s\n", firstNonEmpty(strings.TrimSpace(manifest.Reason), "-"))
	return b.String()
}
