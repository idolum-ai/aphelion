//go:build linux

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
)

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

func durableAgentReviewTargetsAgent(agentID string, scope session.ScopeRef) bool {
	agentID = strings.TrimSpace(agentID)
	return strings.TrimSpace(scope.DurableAgentID) == agentID || strings.TrimSpace(scope.ID) == agentID
}
