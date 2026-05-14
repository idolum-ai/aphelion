//go:build linux

package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool"
)

const durableAgentReconcileGrowthMarker = "APHELION_CHILD_RECONCILE_V1"

type durableAgentReconcileOptions struct {
	QueueGrowthPrompt bool
	Now               time.Time
}

type durableAgentReconcileResult struct {
	Count                int
	Active               int
	ProfilesSynced       int
	RootsRepaired        int
	BootstrapRepaired    int
	SnapshotsScanned     int
	SnapshotsMigrated    int
	SnapshotsPresent     int
	SnapshotsRejected    int
	SnapshotRootsRemoved int
	GrowthPromptsQueued  int
	StatesReset          int
	GrantIssues          int
	RepairErrors         int
	Rows                 []durableAgentReconcileRow
}

type durableAgentReconcileRow struct {
	AgentID              string
	Status               string
	ProfileRoot          string
	ProfileSynced        bool
	RootsRepaired        bool
	BootstrapRepaired    bool
	SnapshotsScanned     int
	SnapshotsMigrated    int
	SnapshotsPresent     int
	SnapshotsRejected    int
	SnapshotRootRemoved  bool
	GrowthPromptQueued   bool
	StateReset           bool
	GrantIssues          []string
	RepairErrorSummaries []string
}

func runDurableAgentReconcileCommand(args []string) error {
	fs := flag.NewFlagSet("durable-agent reconcile", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	queueGrowthPrompt := fs.Bool("queue-growth", true, "queue one parent growth prompt for each active durable child")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	result, err := reconcileDurableAgentsForConfig(cfg, durableAgentReconcileOptions{
		QueueGrowthPrompt: *queueGrowthPrompt,
		Now:               time.Now().UTC(),
	})
	printDurableAgentReconcileResult(os.Stdout, result)
	return err
}

func reconcileDurableAgentsForConfig(cfg *config.Config, opts durableAgentReconcileOptions) (*durableAgentReconcileResult, error) {
	result := &durableAgentReconcileResult{}
	if cfg == nil {
		return result, fmt.Errorf("config is nil")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return result, err
	}
	defer store.Close()

	agents, err := store.ListDurableAgents()
	if err != nil {
		return result, err
	}
	sort.Slice(agents, func(i, j int) bool {
		return strings.TrimSpace(agents[i].AgentID) < strings.TrimSpace(agents[j].AgentID)
	})
	result.Count = len(agents)

	defaultBootstrap := core.NormalizeNodeLLMBootstrap(defaultDurableAgentBootstrapFromConfig(cfg))
	for _, agent := range agents {
		row := reconcileDurableAgentRecord(store, cfg, defaultBootstrap, agent, opts.QueueGrowthPrompt, now.UTC())
		result.Rows = append(result.Rows, row)
		if strings.EqualFold(row.Status, "active") {
			result.Active++
		}
		if row.ProfileSynced {
			result.ProfilesSynced++
		}
		if row.RootsRepaired {
			result.RootsRepaired++
		}
		if row.BootstrapRepaired {
			result.BootstrapRepaired++
		}
		result.SnapshotsScanned += row.SnapshotsScanned
		result.SnapshotsMigrated += row.SnapshotsMigrated
		result.SnapshotsPresent += row.SnapshotsPresent
		result.SnapshotsRejected += row.SnapshotsRejected
		if row.SnapshotRootRemoved {
			result.SnapshotRootsRemoved++
		}
		if row.GrowthPromptQueued {
			result.GrowthPromptsQueued++
		}
		if row.StateReset {
			result.StatesReset++
		}
		result.GrantIssues += len(row.GrantIssues)
		result.RepairErrors += len(row.RepairErrorSummaries)
	}
	if result.RepairErrors > 0 {
		return result, fmt.Errorf("durable-agent reconcile found %d repair error(s)", result.RepairErrors)
	}
	return result, nil
}

func reconcileDurableAgentRecord(store *session.SQLiteStore, cfg *config.Config, defaultBootstrap core.NodeLLMBootstrap, agent core.DurableAgent, queueGrowthPrompt bool, now time.Time) durableAgentReconcileRow {
	status := firstNonEmpty(strings.TrimSpace(agent.Status), "active")
	row := durableAgentReconcileRow{
		AgentID: strings.TrimSpace(agent.AgentID),
		Status:  status,
	}
	migration, err := durableagent.MigrateChildMemorySnapshots(agent, cfg.Sessions.DBPath)
	if err != nil {
		row.RepairErrorSummaries = append(row.RepairErrorSummaries, "migrate snapshots: "+err.Error())
	} else {
		row.SnapshotsScanned = migration.Scanned
		row.SnapshotsMigrated = migration.Migrated
		row.SnapshotsPresent = migration.AlreadyPresent
		row.SnapshotsRejected = migration.Rejected
		row.SnapshotRootRemoved = migration.SourceRemoved
	}
	if !strings.EqualFold(status, "active") {
		return row
	}

	workspaceRoot, memoryRoot := durableagent.LocalRoots(agent.AgentID, agent.LocalStorageRoots)
	if strings.TrimSpace(workspaceRoot) == "" || strings.TrimSpace(memoryRoot) == "" {
		workspaceRoot, memoryRoot = durableagent.DefaultLocalRoots(cfg.Sessions.DBPath, agent.AgentID)
		agent.LocalStorageRoots = []string{workspaceRoot, memoryRoot}
		row.RootsRepaired = true
	}
	for _, root := range []string{workspaceRoot, memoryRoot} {
		if strings.TrimSpace(root) == "" {
			continue
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			row.RepairErrorSummaries = append(row.RepairErrorSummaries, fmt.Sprintf("mkdir %s: %v", root, err))
		}
	}

	if !core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM).Configured() && defaultBootstrap.Configured() {
		agent.BootstrapLLM = defaultBootstrap
		row.BootstrapRepaired = true
	}
	if row.RootsRepaired || row.BootstrapRepaired {
		if err := store.UpsertDurableAgent(agent); err != nil {
			row.RepairErrorSummaries = append(row.RepairErrorSummaries, "upsert repaired agent: "+err.Error())
		}
	}

	sync, err := tool.SyncDurableAgentProfileFiles(agent, store)
	if err != nil {
		row.RepairErrorSummaries = append(row.RepairErrorSummaries, "sync profile files: "+err.Error())
	} else {
		row.ProfileSynced = true
		row.ProfileRoot = sync.Root
	}

	if queueGrowthPrompt {
		queued, reset, err := reconcileDurableAgentState(store, agent.AgentID, now.UTC())
		if err != nil {
			row.RepairErrorSummaries = append(row.RepairErrorSummaries, "reconcile state: "+err.Error())
		}
		row.GrowthPromptQueued = queued
		row.StateReset = reset
	}

	issues, err := durableAgentGrantMaterializationIssues(agent, store)
	if err != nil {
		row.RepairErrorSummaries = append(row.RepairErrorSummaries, "inspect grants: "+err.Error())
	} else {
		row.GrantIssues = issues
	}
	return row
}

func reconcileDurableAgentState(store *session.SQLiteStore, agentID string, now time.Time) (bool, bool, error) {
	state, err := store.DurableAgentState(agentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, false, err
	}
	if state == nil {
		state = &core.DurableAgentState{AgentID: strings.TrimSpace(agentID)}
	}

	reset := false
	if strings.EqualFold(strings.TrimSpace(state.Status), "awake") {
		state.Status = "dormant"
		state.DormantAt = now.UTC()
		reset = true
	}

	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		return false, reset, err
	}
	queued := false
	if !durableAgentContinuityHasReconcilePrompt(continuity) {
		continuity = continuity.WithConversationMessage("parent", durableAgentReconcileGrowthPrompt(agentID), now.UTC())
		raw, err := continuity.Marshal()
		if err != nil {
			return false, reset, err
		}
		state.StateJSON = raw
		queued = true
	}
	if queued || reset {
		if err := store.SaveDurableAgentState(*state); err != nil {
			return false, false, err
		}
	}
	return queued, reset, nil
}

func durableAgentContinuityHasReconcilePrompt(state core.DurableAgentContinuityState) bool {
	if state.Conversation == nil {
		return false
	}
	for _, message := range state.Conversation.Messages {
		if strings.Contains(message.Text, durableAgentReconcileGrowthMarker) {
			return true
		}
	}
	return false
}

func durableAgentReconcileGrowthPrompt(agentID string) string {
	return strings.Join([]string{
		durableAgentReconcileGrowthMarker,
		"You may have been revived after a reinstall, deploy, or service interruption.",
		"On your next wake, read profile/growth.md, profile/capability-ledger.md, and profile/scorecard.md before reporting capability.",
		"Verify actual runtime grants and materialized child_runtime before claiming you can act.",
		"If blocked, send one concise delegation_request or delegation_report with evidence, the smallest useful capability, a success metric, and a reversible trial boundary.",
		"Suppress stale issues that are already fixed; report current blockers with concrete evidence.",
		"agent_id: " + strings.TrimSpace(agentID),
	}, "\n")
}

func durableAgentGrantMaterializationIssues(agent core.DurableAgent, store *session.SQLiteStore) ([]string, error) {
	if store == nil {
		return nil, nil
	}
	principalID := core.DurableAgentPrincipal(agent.AgentID)
	grants, err := store.CapabilityGrants(100, session.CapabilityGrantStatusActive, "", principalID)
	if err != nil {
		return nil, err
	}
	issues := make([]string, 0)
	for _, grant := range grants {
		if !durableAgentGrantNeedsChildRuntime(grant) {
			continue
		}
		_, found, err := core.ExtractChildRuntimeContract(grant.Contract, grant.Constraints)
		if err != nil {
			issues = append(issues, fmt.Sprintf("grant_id=%s child_runtime=invalid: %v", strings.TrimSpace(grant.GrantID), err))
			continue
		}
		if !found {
			issues = append(issues, fmt.Sprintf("grant_id=%s child_runtime=missing", strings.TrimSpace(grant.GrantID)))
		}
	}
	return issues, nil
}

func durableAgentGrantNeedsChildRuntime(grant session.CapabilityGrant) bool {
	switch grant.Kind {
	case session.CapabilityKindTool, session.CapabilityKindExternalAccount, session.CapabilityKindLocalDevice, session.CapabilityKindFileAccess, session.CapabilityKindNetworkAccess:
	default:
		return false
	}
	for _, action := range grant.AllowedActions {
		action = strings.ToLower(strings.TrimSpace(action))
		if action == "invoke" || action == "connection_test" || action == "read" || action == "write" || action == "execute" {
			return true
		}
	}
	return false
}

func printDurableAgentReconcileResult(w io.Writer, result *durableAgentReconcileResult) {
	if result == nil {
		return
	}
	fmt.Fprintf(w, "action: durable-agent reconcile\n")
	fmt.Fprintf(w, "count: %d\n", result.Count)
	fmt.Fprintf(w, "active: %d\n", result.Active)
	fmt.Fprintf(w, "profiles_synced: %d\n", result.ProfilesSynced)
	fmt.Fprintf(w, "roots_repaired: %d\n", result.RootsRepaired)
	fmt.Fprintf(w, "bootstrap_repaired: %d\n", result.BootstrapRepaired)
	fmt.Fprintf(w, "snapshots_scanned: %d\n", result.SnapshotsScanned)
	fmt.Fprintf(w, "snapshots_migrated: %d\n", result.SnapshotsMigrated)
	fmt.Fprintf(w, "snapshots_present: %d\n", result.SnapshotsPresent)
	fmt.Fprintf(w, "snapshots_rejected: %d\n", result.SnapshotsRejected)
	fmt.Fprintf(w, "snapshot_roots_removed: %d\n", result.SnapshotRootsRemoved)
	fmt.Fprintf(w, "growth_prompts_queued: %d\n", result.GrowthPromptsQueued)
	fmt.Fprintf(w, "states_reset: %d\n", result.StatesReset)
	fmt.Fprintf(w, "grant_issues: %d\n", result.GrantIssues)
	fmt.Fprintf(w, "repair_errors: %d\n", result.RepairErrors)
	for _, row := range result.Rows {
		fmt.Fprintf(
			w,
			"- agent_id=%s status=%s profile_synced=%t roots_repaired=%t bootstrap_repaired=%t snapshots_scanned=%d snapshots_migrated=%d snapshots_rejected=%d growth_prompt_queued=%t state_reset=%t grant_issues=%d repair_errors=%d",
			row.AgentID,
			row.Status,
			row.ProfileSynced,
			row.RootsRepaired,
			row.BootstrapRepaired,
			row.SnapshotsScanned,
			row.SnapshotsMigrated,
			row.SnapshotsRejected,
			row.GrowthPromptQueued,
			row.StateReset,
			len(row.GrantIssues),
			len(row.RepairErrorSummaries),
		)
		if strings.TrimSpace(row.ProfileRoot) != "" {
			fmt.Fprintf(w, " profile_root=%s", row.ProfileRoot)
		}
		fmt.Fprint(w, "\n")
		for _, issue := range row.GrantIssues {
			fmt.Fprintf(w, "  - grant_issue: %s\n", issue)
		}
		for _, repairErr := range row.RepairErrorSummaries {
			fmt.Fprintf(w, "  - repair_error: %s\n", repairErr)
		}
	}
}
