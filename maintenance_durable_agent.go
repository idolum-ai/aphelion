//go:build linux

package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	memstore "github.com/idolum-ai/aphelion/memory"
	aphruntime "github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

const durableAgentReconcileGrowthMarker = "APHELION_CHILD_RECONCILE_V1"

type durableAgentReconcileOptions struct {
	QueueGrowthPrompt bool
	Now               time.Time
}

type durableAgentReconcileResult struct {
	Count               int
	Active              int
	ProfilesSynced      int
	RootsRepaired       int
	BootstrapRepaired   int
	GrowthPromptsQueued int
	StatesReset         int
	GrantIssues         int
	RepairErrors        int
	Rows                []durableAgentReconcileRow
}

type durableAgentReconcileRow struct {
	AgentID              string
	Status               string
	ProfileRoot          string
	ProfileSynced        bool
	RootsRepaired        bool
	BootstrapRepaired    bool
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
	fmt.Fprintf(w, "growth_prompts_queued: %d\n", result.GrowthPromptsQueued)
	fmt.Fprintf(w, "states_reset: %d\n", result.StatesReset)
	fmt.Fprintf(w, "grant_issues: %d\n", result.GrantIssues)
	fmt.Fprintf(w, "repair_errors: %d\n", result.RepairErrors)
	for _, row := range result.Rows {
		fmt.Fprintf(
			w,
			"- agent_id=%s status=%s profile_synced=%t roots_repaired=%t bootstrap_repaired=%t growth_prompt_queued=%t state_reset=%t grant_issues=%d repair_errors=%d",
			row.AgentID,
			row.Status,
			row.ProfileSynced,
			row.RootsRepaired,
			row.BootstrapRepaired,
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

func runDurableAgentCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("durable-agent requires a subcommand: list, health, policy, enrollment, forensic, bootstrap, remote, wake, child-run, or reconcile")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "reconcile":
		return runDurableAgentReconcileCommand(args[1:])
	case "list":
		return runDurableAgentListCommand(args[1:])
	case "health":
		return runDurableAgentHealthCommand(args[1:])
	case "policy":
		return runDurableAgentPolicyCommand(args[1:])
	case "enrollment":
		return runDurableAgentEnrollmentCommand(args[1:])
	case "forensic":
		return runDurableAgentForensicCommand(args[1:])
	case "bootstrap":
		return runDurableAgentBootstrapCommand(args[1:])
	case "remote":
		return runDurableAgentRemoteCommand(args[1:])
	case "wake":
		return runDurableAgentWakeCommand(args[1:])
	case "child-run":
		return runDurableAgentChildCommand(args[1:])
	default:
		return fmt.Errorf("durable-agent subcommand must be one of list|health|policy|enrollment|forensic|bootstrap|remote|wake|child-run|reconcile")
	}
}

type durableAgentWakeRuntime interface {
	RunDurableAgentChildWake(context.Context, string, time.Time) error
}

type durableAgentWakeRuntimeFactory func(*config.Config) (durableAgentWakeRuntime, func(), error)

func runDurableAgentWakeCommand(args []string) error {
	return runDurableAgentWakeCommandWithFactory(args, newDurableAgentWakeRuntimeForCommand)
}

func runDurableAgentWakeCommandWithFactory(args []string, factory durableAgentWakeRuntimeFactory) error {
	fs := flag.NewFlagSet("durable-agent wake", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	agentID := fs.String("agent", "", "durable agent id")
	nowRaw := fs.String("now", "", "override wake timestamp (RFC3339 or RFC3339Nano)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*agentID) == "" {
		return fmt.Errorf("durable-agent wake requires --agent")
	}
	now, err := parseDurableChildWakeTime(*nowRaw)
	if err != nil {
		return err
	}
	cfg, configPath, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	if factory == nil {
		return fmt.Errorf("durable-agent wake runtime factory is unavailable")
	}
	rt, cleanup, err := factory(cfg)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}
	started := time.Now().UTC()
	if err := rt.RunDurableAgentChildWake(context.Background(), strings.TrimSpace(*agentID), now); err != nil {
		return err
	}
	completed := time.Now().UTC()
	fmt.Fprintf(os.Stdout, "action: durable-agent wake\n")
	fmt.Fprintf(os.Stdout, "agent_id: %s\n", strings.TrimSpace(*agentID))
	fmt.Fprintf(os.Stdout, "config: %s\n", configPath)
	fmt.Fprintf(os.Stdout, "wake_time: %s\n", now.UTC().Format(time.RFC3339Nano))
	fmt.Fprintf(os.Stdout, "started_at: %s\n", started.Format(time.RFC3339Nano))
	fmt.Fprintf(os.Stdout, "completed_at: %s\n", completed.Format(time.RFC3339Nano))
	fmt.Fprintf(os.Stdout, "status: completed\n")
	return nil
}

func newDurableAgentWakeRuntimeForCommand(cfg *config.Config) (durableAgentWakeRuntime, func(), error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("config is nil")
	}
	if err := prepareFilesystem(cfg); err != nil {
		return nil, nil, err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = store.Close() }

	httpClient := &http.Client{Timeout: 90 * time.Second}
	llm, err := buildNativeProviderChain(cfg, httpClient)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	sandboxRoots := sandbox.Roots{
		GlobalRoot:        cfg.Agent.PromptRoot,
		AdminExecRoot:     cfg.Agent.ExecRoot,
		SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
		UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
		UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
	}
	sandboxResolver, err := sandbox.NewResolver(sandboxRoots, aphruntime.SandboxProfilesFromConfig(cfg.Sandbox))
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	tools := tool.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Duration(cfg.Agent.ToolTimeout)*time.Second, sandboxResolver).
		WithUserAgent(config.EffectiveUserAgent(cfg, tool.DefaultNativeFetchUserAgent)).
		WithSessionStore(store).
		WithDurableAgentPrincipalFallback().
		WithDurableAgentBootstrapLLM(defaultDurableAgentBootstrapFromConfig(cfg))
	if manifestDir := strings.TrimSpace(cfg.Tools.ExternalManifestDir); manifestDir != "" {
		if _, err := tools.WithExternalToolManifestDir(manifestDir); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("load external tool manifests: %w", err)
		}
	}
	tools.WithSemanticEngine(memstore.NewSemanticEngine(memstore.SemanticOptions{
		Enabled:             cfg.Memory.Semantic.Enabled,
		DBPath:              memstore.DefaultSemanticDBPath(cfg.Sessions.DBPath),
		Sources:             cfg.Memory.Semantic.Sources,
		IncludeDailyNotes:   cfg.Memory.Semantic.IncludeDailyNotes,
		IncludeQuestions:    cfg.Memory.Semantic.IncludeQuestions,
		IncludeRhizome:      cfg.Memory.Semantic.IncludeRhizome,
		InteractiveTopK:     cfg.Memory.Semantic.InteractiveTopK,
		HeartbeatTopK:       cfg.Memory.Semantic.HeartbeatTopK,
		InteractiveMaxChars: cfg.Memory.Semantic.InteractiveMaxChars,
		HeartbeatMaxChars:   cfg.Memory.Semantic.HeartbeatMaxChars,
		DailyNotesDir:       cfg.Agent.DailyNotesDir,
	}))
	fileStore, retrievalStore, err := buildOpenAIPlatformServices(cfg, httpClient)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if fileStore != nil {
		tools.WithFileStore(fileStore, cfg.OpenAI.Files.Purpose)
	}
	if retrievalStore != nil {
		tools.WithRetrievalStore(retrievalStore, cfg.OpenAI.VectorStores.DefaultStore)
	}
	rt, err := aphruntime.New(cfg, store, llm, tools, durableChildNoopOutbound{})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	tools.WithCapabilityGrantObserver(rt.HandleCapabilityGrantActivated)
	return rt, cleanup, nil
}

func runDurableAgentListCommand(args []string) error {
	fs := flag.NewFlagSet("durable-agent list", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	agents, err := store.ListDurableAgents()
	if err != nil {
		return err
	}
	sort.Slice(agents, func(i, j int) bool {
		return strings.TrimSpace(agents[i].AgentID) < strings.TrimSpace(agents[j].AgentID)
	})

	fmt.Fprintf(os.Stdout, "action: durable-agent list\n")
	fmt.Fprintf(os.Stdout, "count: %d\n", len(agents))
	if len(agents) == 0 {
		fmt.Fprintf(os.Stdout, "no_agents: true\n")
		return nil
	}
	for i, agent := range agents {
		fmt.Fprintf(
			os.Stdout,
			"%d. agent_id=%s channel=%s status=%s review_target_chat_id=%d policy_version=%d outbound_mode=%s\n",
			i+1,
			strings.TrimSpace(agent.AgentID),
			strings.TrimSpace(agent.ChannelKind),
			firstNonEmpty(strings.TrimSpace(agent.Status), "active"),
			agent.ReviewTargetChatID,
			agent.PolicyVersion,
			strings.TrimSpace(agent.LivePolicy.OutboundMode),
		)
	}
	return nil
}

func runDurableAgentHealthCommand(args []string) error {
	fs := flag.NewFlagSet("durable-agent health", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	agentID := fs.String("agent", "", "durable agent id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*agentID) == "" {
		return fmt.Errorf("durable-agent health requires --agent")
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	agent, err := store.DurableAgent(strings.TrimSpace(*agentID))
	if err != nil {
		return err
	}

	var state *core.DurableAgentState
	state, err = store.DurableAgentState(agent.AgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	var enrollment *core.DurableAgentRemoteEnrollment
	enrollment, err = store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	health := durableAgentMaintenanceHealth(*agent, state, enrollment)

	fmt.Fprintf(os.Stdout, "action: durable-agent health\n")
	fmt.Fprintf(os.Stdout, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(os.Stdout, "health: %s\n", health)
	fmt.Fprintf(os.Stdout, "channel_kind: %s\n", strings.TrimSpace(agent.ChannelKind))
	fmt.Fprintf(os.Stdout, "status: %s\n", firstNonEmpty(strings.TrimSpace(agent.Status), "active"))
	fmt.Fprintf(os.Stdout, "review_target_chat_id: %d\n", agent.ReviewTargetChatID)
	fmt.Fprintf(os.Stdout, "wakeup_mode: %s\n", strings.TrimSpace(agent.WakeupMode))
	fmt.Fprintf(os.Stdout, "network_policy: %s\n", strings.TrimSpace(agent.NetworkPolicy))
	fmt.Fprintf(os.Stdout, "policy_version: %d\n", agent.PolicyVersion)
	fmt.Fprintf(os.Stdout, "policy_hash: %s\n", strings.TrimSpace(agent.PolicyHash))
	fmt.Fprintf(os.Stdout, "outbound_mode: %s\n", strings.TrimSpace(agent.LivePolicy.OutboundMode))
	fmt.Fprintf(os.Stdout, "drift_policy: %s\n", strings.TrimSpace(agent.LivePolicy.DriftPolicy))
	fmt.Fprintf(os.Stdout, "capabilities: %s\n", strings.Join(agent.LivePolicy.CapabilityEnvelope, ","))

	if state == nil {
		fmt.Fprintf(os.Stdout, "state: none\n")
	} else {
		fmt.Fprintf(os.Stdout, "state: present\n")
		if !state.LastWakeAt.IsZero() {
			fmt.Fprintf(os.Stdout, "last_wake_at: %s\n", state.LastWakeAt.UTC().Format(time.RFC3339Nano))
		}
		if !state.LastReviewAt.IsZero() {
			fmt.Fprintf(os.Stdout, "last_review_at: %s\n", state.LastReviewAt.UTC().Format(time.RFC3339Nano))
		}
		if !state.DormantAt.IsZero() {
			fmt.Fprintf(os.Stdout, "dormant_at: %s\n", state.DormantAt.UTC().Format(time.RFC3339Nano))
		}
		fmt.Fprintf(os.Stdout, "last_applied_policy_version: %d\n", state.LastAppliedPolicyVersion)
		if !state.LastAppliedPolicyAt.IsZero() {
			fmt.Fprintf(os.Stdout, "last_applied_policy_at: %s\n", state.LastAppliedPolicyAt.UTC().Format(time.RFC3339Nano))
		}
		fmt.Fprintf(os.Stdout, "last_apply_status: %s\n", strings.TrimSpace(state.LastApplyStatus))
		if strings.TrimSpace(state.LastApplyError) != "" {
			fmt.Fprintf(os.Stdout, "last_apply_error: %s\n", strings.TrimSpace(state.LastApplyError))
		}
	}

	if enrollment == nil {
		fmt.Fprintf(os.Stdout, "enrollment: none\n")
	} else {
		fmt.Fprintf(os.Stdout, "enrollment: present\n")
		fmt.Fprintf(os.Stdout, "enrollment_status: %s\n", strings.TrimSpace(enrollment.Status))
		fmt.Fprintf(os.Stdout, "enrollment_last_sequence: %d\n", enrollment.LastSequence)
		if !enrollment.LastSeenAt.IsZero() {
			fmt.Fprintf(os.Stdout, "enrollment_last_seen_at: %s\n", enrollment.LastSeenAt.UTC().Format(time.RFC3339Nano))
		}
		if !enrollment.RevokedAt.IsZero() {
			fmt.Fprintf(os.Stdout, "enrollment_revoked_at: %s\n", enrollment.RevokedAt.UTC().Format(time.RFC3339Nano))
		}
	}

	return nil
}

func durableAgentMaintenanceHealth(agent core.DurableAgent, state *core.DurableAgentState, enrollment *core.DurableAgentRemoteEnrollment) string {
	if !strings.EqualFold(strings.TrimSpace(agent.Status), "active") {
		return "inactive"
	}
	if state != nil {
		if strings.EqualFold(strings.TrimSpace(state.LastApplyStatus), "failed") || strings.TrimSpace(state.LastApplyError) != "" {
			return "degraded"
		}
	}
	if enrollment != nil {
		status := strings.ToLower(strings.TrimSpace(enrollment.Status))
		if status != "" && status != "active" {
			return "degraded"
		}
	}
	if state != nil && !state.DormantAt.IsZero() {
		return "dormant"
	}
	return "ok"
}

func runDurableAgentEnrollmentCommand(args []string) error {
	fs := flag.NewFlagSet("durable-agent enrollment", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	agentID := fs.String("agent", "", "durable agent id")
	secret := fs.String("secret", "", "new control-plane secret for rotate-secret")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*agentID) == "" {
		return fmt.Errorf("durable-agent enrollment requires --agent")
	}

	action := "show"
	if fs.NArg() > 0 {
		action = strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	enrollment, err := store.DurableAgentRemoteEnrollment(*agentID)
	if err != nil {
		return err
	}

	switch action {
	case "", "show":
		printDurableAgentEnrollment(os.Stdout, *enrollment)
		return nil
	case "revoke":
		enrollment.Status = "revoked"
		enrollment.RevokedAt = time.Now().UTC()
		if err := store.UpsertDurableAgentRemoteEnrollment(*enrollment); err != nil {
			return err
		}
		printDurableAgentEnrollment(os.Stdout, *enrollment)
		return nil
	case "reactivate":
		if enrollment.Status == "decommissioned" {
			return fmt.Errorf("durable-agent enrollment %s is decommissioned and cannot be reactivated", strings.TrimSpace(*agentID))
		}
		enrollment.Status = "active"
		enrollment.RevokedAt = time.Time{}
		if err := store.UpsertDurableAgentRemoteEnrollment(*enrollment); err != nil {
			return err
		}
		printDurableAgentEnrollment(os.Stdout, *enrollment)
		return nil
	case "decommission":
		enrollment.Status = "decommissioned"
		enrollment.RevokedAt = time.Now().UTC()
		if err := store.UpsertDurableAgentRemoteEnrollment(*enrollment); err != nil {
			return err
		}
		printDurableAgentEnrollment(os.Stdout, *enrollment)
		return nil
	case "rotate-secret":
		nextSecret := strings.TrimSpace(*secret)
		if nextSecret == "" {
			return fmt.Errorf("durable-agent enrollment rotate-secret requires --secret")
		}
		agent, err := store.DurableAgent(*agentID)
		if err != nil {
			return err
		}
		agent.ControlPlaneSecret = nextSecret
		if err := store.UpsertDurableAgent(*agent); err != nil {
			return err
		}
		printDurableAgentEnrollment(os.Stdout, *enrollment)
		return nil
	default:
		return fmt.Errorf("durable-agent enrollment action must be one of show|revoke|reactivate|decommission|rotate-secret")
	}
}

func runDurableAgentBootstrapCommand(args []string) error {
	fs := flag.NewFlagSet("durable-agent bootstrap", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	agentID := fs.String("agent", "", "durable agent id")
	path := fs.String("path", "", "bootstrap json output path")
	parentControlURL := fs.String("parent-control-url", "", "remote parent control-plane URL")
	enrollmentToken := fs.String("enrollment-token", "", "child enrollment token")
	keyFingerprint := fs.String("key-fingerprint", "", "child key fingerprint")
	if err := fs.Parse(args); err != nil {
		return err
	}

	action := "write"
	if fs.NArg() > 0 {
		action = strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	}
	if action != "write" {
		return fmt.Errorf("durable-agent bootstrap action must be write")
	}
	if strings.TrimSpace(*agentID) == "" {
		return fmt.Errorf("durable-agent bootstrap write requires --agent")
	}
	if strings.TrimSpace(*path) == "" {
		return fmt.Errorf("durable-agent bootstrap write requires --path")
	}
	if strings.TrimSpace(*parentControlURL) == "" {
		return fmt.Errorf("durable-agent bootstrap write requires --parent-control-url")
	}
	if strings.TrimSpace(*enrollmentToken) == "" {
		return fmt.Errorf("durable-agent bootstrap write requires --enrollment-token")
	}
	if strings.TrimSpace(*keyFingerprint) == "" {
		return fmt.Errorf("durable-agent bootstrap write requires --key-fingerprint")
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	agent, err := store.DurableAgent(*agentID)
	if err != nil {
		return err
	}
	agent.ControlPlaneSecret = strings.TrimSpace(*enrollmentToken)
	if err := store.UpsertDurableAgent(*agent); err != nil {
		return err
	}
	bootstrap := core.DurableAgentRemoteBootstrap{
		ReviewTargetChatID: agent.ReviewTargetChatID,
		AgentID:            agent.AgentID,
		ParentAgentID:      agent.ParentAgentID,
		ChannelKind:        agent.ChannelKind,
		ParentControlURL:   strings.TrimSpace(*parentControlURL),
		EnrollmentToken:    strings.TrimSpace(*enrollmentToken),
		KeyFingerprint:     strings.TrimSpace(*keyFingerprint),
		ProtocolVersion:    core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:       agent.BootstrapLLM,
		BootstrapCeiling:   agent.BootstrapCeiling,
		LocalStorageRoots:  append([]string(nil), agent.LocalStorageRoots...),
		SecretScopes:       append([]string(nil), agent.SecretScopes...),
		NetworkPolicy:      agent.NetworkPolicy,
	}
	if err := durableagent.WriteRemoteBootstrap(*path, bootstrap); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "action: durable-agent bootstrap write\n")
	fmt.Fprintf(os.Stdout, "agent_id: %s\n", bootstrap.AgentID)
	fmt.Fprintf(os.Stdout, "path: %s\n", strings.TrimSpace(*path))
	fmt.Fprintf(os.Stdout, "parent_control_url: %s\n", bootstrap.ParentControlURL)
	fmt.Fprintf(os.Stdout, "protocol_version: %s\n", bootstrap.ProtocolVersion)
	return nil
}

func runDurableAgentPolicyCommand(args []string) error {
	fs := flag.NewFlagSet("durable-agent policy", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	agentID := fs.String("agent", "", "durable agent id")
	reviewEventID := fs.Int64("review-event", 0, "source review event id for provenance")
	reason := fs.String("reason", "", "operator reason for policy change")
	charter := fs.String("charter", "", "updated charter")
	capabilities := fs.String("capabilities", "", "comma-separated capability envelope override")
	outboundMode := fs.String("outbound-mode", "", "outbound mode override")
	driftPolicy := fs.String("drift-policy", "", "drift policy override")
	publicSurfaceMode := fs.String("public-surface-mode", "", "public surface mode override")
	sharedInferenceReuse := fs.String("shared-inference-reuse", "", "shared inference reuse override")
	sharedInferenceReuseScope := fs.String("shared-inference-reuse-scope", "", "shared inference reuse scope override")
	history := fs.Int("history", 5, "recent policy update entries to show")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*agentID) == "" {
		return fmt.Errorf("durable-agent policy requires --agent")
	}

	action := "show"
	if fs.NArg() > 0 {
		action = strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	switch action {
	case "", "show":
		agent, err := store.DurableAgent(*agentID)
		if err != nil {
			return err
		}
		updates, err := store.DurableAgentPolicyUpdates(*agentID, *history)
		if err != nil {
			return err
		}
		printDurableAgentPolicy(os.Stdout, *agent, updates)
		return nil
	case "apply":
		agent, err := store.DurableAgent(*agentID)
		if err != nil {
			return err
		}
		if *reviewEventID > 0 {
			event, err := store.ReviewEventByID(*reviewEventID)
			if err != nil {
				return err
			}
			if event.SourceScope.Kind != session.ScopeKindDurableAgent || !durableAgentReviewTargetsAgent(*agentID, event.SourceScope) {
				return fmt.Errorf("review event %d does not belong to durable agent %s", *reviewEventID, strings.TrimSpace(*agentID))
			}
		}

		policy := agent.LivePolicy
		if strings.TrimSpace(*charter) != "" {
			policy.Charter = strings.TrimSpace(*charter)
		}
		if strings.TrimSpace(*capabilities) != "" {
			policy.CapabilityEnvelope = parseCSVValues(*capabilities)
		}
		if strings.TrimSpace(*outboundMode) != "" {
			policy.OutboundMode = strings.TrimSpace(*outboundMode)
		}
		if strings.TrimSpace(*driftPolicy) != "" {
			policy.DriftPolicy = strings.TrimSpace(*driftPolicy)
		}
		if strings.TrimSpace(*publicSurfaceMode) != "" {
			policy.PublicSurfaceMode = strings.TrimSpace(*publicSurfaceMode)
		}
		if strings.TrimSpace(*sharedInferenceReuse) != "" {
			policy.SharedInferenceReuse = strings.TrimSpace(*sharedInferenceReuse)
		}
		if strings.TrimSpace(*sharedInferenceReuseScope) != "" {
			policy.SharedInferenceReuseScope = strings.TrimSpace(*sharedInferenceReuseScope)
		}

		if strings.TrimSpace(*reason) == "" && *reviewEventID > 0 {
			*reason = fmt.Sprintf("ratified from review_event=%d", *reviewEventID)
		}
		updated, update, err := store.ApplyDurableAgentLivePolicy(*agentID, policy, *reviewEventID, *reason)
		if err != nil {
			return err
		}
		if update == nil {
			fmt.Fprintf(os.Stdout, "action: durable-agent policy apply\n")
			fmt.Fprintf(os.Stdout, "agent_id: %s\n", updated.AgentID)
			fmt.Fprintf(os.Stdout, "changed: false\n")
			fmt.Fprintf(os.Stdout, "policy_version: %d\n", updated.PolicyVersion)
			fmt.Fprintf(os.Stdout, "policy_hash: %s\n", updated.PolicyHash)
			return nil
		}
		fmt.Fprintf(os.Stdout, "action: durable-agent policy apply\n")
		fmt.Fprintf(os.Stdout, "agent_id: %s\n", updated.AgentID)
		fmt.Fprintf(os.Stdout, "changed: true\n")
		fmt.Fprintf(os.Stdout, "policy_version: %d\n", updated.PolicyVersion)
		fmt.Fprintf(os.Stdout, "policy_hash: %s\n", updated.PolicyHash)
		if update.SourceReviewEventID > 0 {
			fmt.Fprintf(os.Stdout, "source_review_event_id: %d\n", update.SourceReviewEventID)
		}
		if strings.TrimSpace(update.Reason) != "" {
			fmt.Fprintf(os.Stdout, "reason: %s\n", update.Reason)
		}
		return nil
	default:
		return fmt.Errorf("durable-agent policy action must be one of show|apply")
	}
}

func runDurableAgentForensicCommand(args []string) error {
	fs := flag.NewFlagSet("durable-agent forensic", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	agentID := fs.String("agent", "", "durable agent id")
	ref := fs.String("ref", "", "forensic reference to inspect")
	if err := fs.Parse(args); err != nil {
		return err
	}
	action := "show"
	if fs.NArg() > 0 {
		action = strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	}
	if action != "show" {
		return fmt.Errorf("durable-agent forensic action must be show")
	}
	if strings.TrimSpace(*agentID) == "" {
		return fmt.Errorf("durable-agent forensic show requires --agent")
	}
	if strings.TrimSpace(*ref) == "" {
		return fmt.Errorf("durable-agent forensic show requires --ref")
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	agent, err := store.DurableAgent(*agentID)
	if err != nil {
		return err
	}
	record, err := durableagent.ReadForensicRecord(*agent, *ref)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "action: durable-agent forensic show\n")
	fmt.Fprintf(os.Stdout, "agent_id: %s\n", record.AgentID)
	fmt.Fprintf(os.Stdout, "ref: %s\n", strings.TrimSpace(*ref))
	fmt.Fprintf(os.Stdout, "reason: %s\n", record.Reason)
	fmt.Fprintf(os.Stdout, "created_at: %s\n", record.CreatedAt.UTC().Format(time.RFC3339Nano))
	if len(record.RedactedFields) > 0 {
		fmt.Fprintf(os.Stdout, "redacted_fields: %s\n", strings.Join(record.RedactedFields, ","))
	}
	for _, key := range sortedMapKeys(record.Payload) {
		fmt.Fprintf(os.Stdout, "payload.%s: %s\n", key, record.Payload[key])
	}
	return nil
}

func printDurableAgentPolicy(w *os.File, agent core.DurableAgent, updates []session.DurableAgentPolicyUpdate) {
	fmt.Fprintf(w, "action: durable-agent policy show\n")
	fmt.Fprintf(w, "agent_id: %s\n", agent.AgentID)
	fmt.Fprintf(w, "channel_kind: %s\n", agent.ChannelKind)
	fmt.Fprintf(w, "policy_version: %d\n", agent.PolicyVersion)
	fmt.Fprintf(w, "policy_hash: %s\n", agent.PolicyHash)
	if !agent.PolicyIssuedAt.IsZero() {
		fmt.Fprintf(w, "policy_issued_at: %s\n", agent.PolicyIssuedAt.UTC().Format(time.RFC3339Nano))
	}
	fmt.Fprintf(w, "charter: %s\n", agent.LivePolicy.Charter)
	fmt.Fprintf(w, "capabilities: %s\n", strings.Join(agent.LivePolicy.CapabilityEnvelope, ","))
	fmt.Fprintf(w, "outbound_mode: %s\n", agent.LivePolicy.OutboundMode)
	fmt.Fprintf(w, "drift_policy: %s\n", agent.LivePolicy.DriftPolicy)
	fmt.Fprintf(w, "public_surface_mode: %s\n", agent.LivePolicy.PublicSurfaceMode)
	fmt.Fprintf(w, "shared_inference_reuse: %s\n", agent.LivePolicy.SharedInferenceReuse)
	fmt.Fprintf(w, "shared_inference_reuse_scope: %s\n", agent.LivePolicy.SharedInferenceReuseScope)
	fmt.Fprintf(w, "bootstrap_capabilities: %s\n", strings.Join(agent.BootstrapCeiling.CapabilityEnvelope, ","))
	fmt.Fprintf(w, "bootstrap_allowed_outbound_modes: %s\n", strings.Join(agent.BootstrapCeiling.AllowedOutboundModes, ","))
	fmt.Fprintf(w, "bootstrap_allowed_public_surface_modes: %s\n", strings.Join(agent.BootstrapCeiling.AllowedPublicSurfaceModes, ","))
	fmt.Fprintf(w, "bootstrap_allowed_shared_inference_reuse: %s\n", strings.Join(agent.BootstrapCeiling.AllowedSharedInferenceReuse, ","))
	fmt.Fprintf(w, "bootstrap_allowed_shared_inference_scopes: %s\n", strings.Join(agent.BootstrapCeiling.AllowedSharedInferenceScopes, ","))
	fmt.Fprintf(w, "bootstrap_llm_backend: %s\n", agent.BootstrapLLM.Backend)
	fmt.Fprintf(w, "bootstrap_native_provider: %s\n", agent.BootstrapLLM.NativeProvider)
	fmt.Fprintf(w, "bootstrap_model: %s\n", agent.BootstrapLLM.Model)
	if strings.TrimSpace(agent.BootstrapLLM.CodexHome) != "" {
		fmt.Fprintf(w, "bootstrap_codex_home: %s\n", agent.BootstrapLLM.CodexHome)
	}
	fmt.Fprintf(w, "policy_updates: %d\n", len(updates))
	for _, update := range updates {
		fmt.Fprintf(w, "- id=%d previous=%d new=%d", update.ID, update.PreviousVersion, update.NewVersion)
		if update.SourceReviewEventID > 0 {
			fmt.Fprintf(w, " review_event=%d", update.SourceReviewEventID)
		}
		if strings.TrimSpace(update.Reason) != "" {
			fmt.Fprintf(w, " reason=%s", update.Reason)
		}
		fmt.Fprintf(w, " applied_at=%s\n", update.AppliedAt.UTC().Format(time.RFC3339Nano))
	}
}

func printDurableAgentEnrollment(w *os.File, enrollment core.DurableAgentRemoteEnrollment) {
	fmt.Fprintf(w, "action: durable-agent enrollment\n")
	fmt.Fprintf(w, "agent_id: %s\n", enrollment.AgentID)
	fmt.Fprintf(w, "status: %s\n", enrollment.Status)
	fmt.Fprintf(w, "parent_control_url: %s\n", enrollment.ParentControlURL)
	fmt.Fprintf(w, "key_fingerprint: %s\n", enrollment.KeyFingerprint)
	fmt.Fprintf(w, "protocol_version: %s\n", enrollment.ProtocolVersion)
	fmt.Fprintf(w, "last_sequence: %d\n", enrollment.LastSequence)
	if !enrollment.EnrolledAt.IsZero() {
		fmt.Fprintf(w, "enrolled_at: %s\n", enrollment.EnrolledAt.UTC().Format(time.RFC3339))
	}
	if !enrollment.LastSeenAt.IsZero() {
		fmt.Fprintf(w, "last_seen_at: %s\n", enrollment.LastSeenAt.UTC().Format(time.RFC3339))
	}
	if !enrollment.RevokedAt.IsZero() {
		fmt.Fprintf(w, "revoked_at: %s\n", enrollment.RevokedAt.UTC().Format(time.RFC3339))
	}
}

func durableAgentReviewTargetsAgent(agentID string, scope session.ScopeRef) bool {
	agentID = strings.TrimSpace(agentID)
	return strings.TrimSpace(scope.DurableAgentID) == agentID || strings.TrimSpace(scope.ID) == agentID
}
