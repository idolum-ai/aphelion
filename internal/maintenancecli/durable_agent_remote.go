//go:build linux

package maintenancecli

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

func printDurableAgentEnrollment(w *os.File, enrollment core.DurableAgentRemoteEnrollment) {
	fmt.Fprintf(w, "action: durable-agent enrollment\n")
	fmt.Fprintf(w, "agent_id: %s\n", enrollment.AgentID)
	fmt.Fprintf(w, "status: %s\n", enrollment.Status)
	fmt.Fprintf(w, "parent_control_url: %s\n", enrollment.ParentControlURL)
	fmt.Fprintf(w, "protocol_version: %s\n", enrollment.ProtocolVersion)
	fmt.Fprintf(w, "last_sequence: %d\n", enrollment.LastSequence)
	if strings.TrimSpace(enrollment.TailnetIdentity.StableNodeID) != "" {
		fmt.Fprintf(w, "tailnet_stable_node_id: %s\n", enrollment.TailnetIdentity.StableNodeID)
	}
	if strings.TrimSpace(enrollment.TailnetIdentity.NodeName) != "" {
		fmt.Fprintf(w, "tailnet_node_name: %s\n", enrollment.TailnetIdentity.NodeName)
	}
	if strings.TrimSpace(enrollment.TailnetIdentity.ComputedName) != "" {
		fmt.Fprintf(w, "tailnet_computed_name: %s\n", enrollment.TailnetIdentity.ComputedName)
	}
	if strings.TrimSpace(enrollment.TailnetIdentity.LoginName) != "" {
		fmt.Fprintf(w, "tailnet_login_name: %s\n", enrollment.TailnetIdentity.LoginName)
	}
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
