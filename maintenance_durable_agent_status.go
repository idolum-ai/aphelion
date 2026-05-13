//go:build linux

package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

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
