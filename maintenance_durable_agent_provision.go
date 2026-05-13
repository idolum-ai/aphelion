//go:build linux

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tailnet"
)

var newDurableAgentProvisionSSHRunner = func(cfg *config.Config, timeout time.Duration) tailnet.SSHRunner {
	cliPath := ""
	if cfg != nil {
		cliPath = cfg.Tailscale.CLIPath
	}
	return tailnet.NewSSHClient(tailnet.SSHOptions{
		CLIPath:        cliPath,
		CommandTimeout: timeout,
	})
}

func runDurableAgentProvisionCommand(args []string) error {
	fs := flag.NewFlagSet("durable-agent provision", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	agentID := fs.String("agent", "", "durable agent id")
	apply := fs.Bool("apply", false, "apply provisioning over Tailscale SSH")
	host := fs.String("host", "", "override Tailnet host; defaults to durable child tailnet_hostname")
	sshUser := fs.String("ssh-user", currentOSUserName(), "Tailscale SSH user")
	binaryPath := fs.String("binary", "", "local aphelion binary to install on the child")
	childRoot := fs.String("child-root", "", "remote child root; defaults to ~/.aphelion/children/<agent>")
	serviceName := fs.String("service-name", "", "remote user service name")
	parentControlURL := fs.String("parent-control-url", "", "parent Tailnet control-plane URL; defaults to configured parent tsnet /control URL")
	pollInterval := fs.String("poll-interval", durableagent.DefaultRemoteChildPollInterval, "remote child poll interval")
	timeoutRaw := fs.String("timeout", "5m", "Tailscale SSH provisioning timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*agentID) == "" {
		return fmt.Errorf("durable-agent provision requires --agent")
	}
	if extra, ok := firstPositionalArg(fs.Args()); ok {
		return fmt.Errorf("unknown argument %q for durable-agent provision", extra)
	}
	timeout, err := time.ParseDuration(strings.TrimSpace(*timeoutRaw))
	if err != nil {
		return fmt.Errorf("parse durable-agent provision timeout: %w", err)
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
	secretSource := "existing"
	secret := strings.TrimSpace(agent.ControlPlaneSecret)
	if secret == "" {
		if *apply {
			secret, err = generateProvisionSecret()
			if err != nil {
				return err
			}
			agent.ControlPlaneSecret = secret
			if err := store.UpsertDurableAgent(*agent); err != nil {
				return err
			}
			secretSource = "generated"
		} else {
			secret = "generated-on-apply"
			secretSource = "will_generate_on_apply"
		}
	}
	binary := strings.TrimSpace(*binaryPath)
	if binary == "" {
		binary, err = os.Executable()
		if err != nil {
			return fmt.Errorf("resolve current executable: %w", err)
		}
	}
	parentURL := strings.TrimSpace(*parentControlURL)
	if parentURL == "" {
		parentURL = defaultParentControlURL(cfg)
	}
	if parentURL == "" {
		return fmt.Errorf("parent control URL is required; set tailscale.parent hostname/expected_tailnet or pass --parent-control-url")
	}
	resolvedHost := resolveProvisionHost(*agent, *host)
	bootstrap := core.DurableAgentRemoteBootstrap{
		ReviewTargetChatID: agent.ReviewTargetChatID,
		AgentID:            agent.AgentID,
		ParentAgentID:      agent.ParentAgentID,
		ChannelKind:        agent.ChannelKind,
		ParentControlURL:   parentURL,
		EnrollmentToken:    secret,
		ProtocolVersion:    core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:       agent.BootstrapLLM,
		BootstrapCeiling:   agent.BootstrapCeiling,
		SecretScopes:       append([]string(nil), agent.SecretScopes...),
		NetworkPolicy:      agent.NetworkPolicy,
	}
	opts := durableagent.ProvisionOptions{
		Agent:        *agent,
		Bootstrap:    bootstrap,
		BinaryPath:   binary,
		SSHHost:      resolvedHost,
		SSHUser:      *sshUser,
		ChildRoot:    *childRoot,
		ServiceName:  *serviceName,
		PollInterval: *pollInterval,
		Apply:        *apply,
		Runner:       newDurableAgentProvisionSSHRunner(cfg, timeout),
	}
	if *apply {
		recordDurableAgentProvisionEvent(store, *agent, core.ExecutionEventDurableProvisionStarted, "started", nil)
	}
	result, err := durableagent.ProvisionRemoteChild(context.Background(), opts)
	if err != nil {
		if *apply {
			recordDurableAgentProvisionEvent(store, *agent, core.ExecutionEventDurableProvisionFailed, "failed", map[string]string{"error": err.Error()})
		}
		return err
	}
	if *apply {
		if err := verifyDurableAgentProvisionEnrollment(store, agent.AgentID, parentURL); err != nil {
			recordDurableAgentProvisionEvent(store, *agent, core.ExecutionEventDurableProvisionFailed, "failed", map[string]string{"error": err.Error()})
			return err
		}
		recordDurableAgentProvisionEvent(store, *agent, core.ExecutionEventDurableProvisionCompleted, "completed", nil)
	}
	printDurableAgentProvision(os.Stdout, result, *apply, secretSource)
	return nil
}

func printDurableAgentProvision(w *os.File, result durableagent.ProvisionResult, applied bool, secretSource string) {
	plan := result.Plan
	fmt.Fprintf(w, "action: durable-agent provision\n")
	fmt.Fprintf(w, "agent_id: %s\n", plan.AgentID)
	fmt.Fprintf(w, "status: %s\n", mapBool(applied, "applied", "dry_run"))
	fmt.Fprintf(w, "ssh_target: %s\n", plan.SSHTarget)
	fmt.Fprintf(w, "child_root: %s\n", plan.ChildRoot)
	fmt.Fprintf(w, "service_name: %s\n", plan.ServiceName)
	fmt.Fprintf(w, "binary_path: %s\n", plan.BinaryPath)
	fmt.Fprintf(w, "parent_control_url: %s\n", plan.ParentControlURL)
	fmt.Fprintf(w, "remote_binary: %s\n", plan.RemoteBinary)
	fmt.Fprintf(w, "bootstrap_path: %s\n", plan.BootstrapPath)
	fmt.Fprintf(w, "db_path: %s\n", plan.DBPath)
	fmt.Fprintf(w, "inbox_dir: %s\n", plan.InboxDir)
	fmt.Fprintf(w, "poll_interval: %s\n", plan.PollInterval)
	fmt.Fprintf(w, "control_secret_source: %s\n", strings.TrimSpace(secretSource))
	if strings.TrimSpace(result.Output) != "" {
		fmt.Fprintf(w, "remote_output: %s\n", oneLine(result.Output))
	}
}

func defaultParentControlURL(cfg *config.Config) string {
	if cfg == nil || !cfg.Tailscale.Enabled || !cfg.Tailscale.Parent.Enabled {
		return ""
	}
	base := tailnet.ParentMagicDNSURL(cfg.Tailscale.Parent.Hostname, cfg.Tailscale.ExpectedTailnet, cfg.Tailscale.Parent.ListenAddr)
	if strings.TrimSpace(base) == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/control"
}

func resolveProvisionHost(agent core.DurableAgent, override string) string {
	if strings.TrimSpace(override) != "" {
		return strings.Trim(strings.TrimSpace(override), ".")
	}
	return strings.Trim(strings.TrimSpace(agent.LivePolicy.TailnetHostname), ".")
}

func generateProvisionSecret() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate durable child enrollment token: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func verifyDurableAgentProvisionEnrollment(store *session.SQLiteStore, agentID string, parentControlURL string) error {
	enrollment, err := store.DurableAgentRemoteEnrollment(agentID)
	if err != nil {
		return fmt.Errorf("verify durable child enrollment: %w", err)
	}
	if enrollment.Status != "active" {
		return fmt.Errorf("verify durable child enrollment: status=%s", enrollment.Status)
	}
	if strings.TrimSpace(parentControlURL) != "" && strings.TrimSpace(enrollment.ParentControlURL) != strings.TrimSpace(parentControlURL) {
		return fmt.Errorf("verify durable child enrollment: parent_control_url=%s", enrollment.ParentControlURL)
	}
	return nil
}

func recordDurableAgentProvisionEvent(store *session.SQLiteStore, agent core.DurableAgent, eventType string, status string, extra map[string]string) {
	if store == nil {
		return
	}
	payload := map[string]string{
		"agent_id": strings.TrimSpace(agent.AgentID),
	}
	for key, value := range extra {
		payload[key] = value
	}
	raw, _ := json.Marshal(payload)
	_, _ = store.AppendExecutionEvent(session.SessionKey{
		ChatID: -1,
		Scope: session.ScopeRef{
			Kind:           session.ScopeKindDurableAgent,
			ID:             strings.TrimSpace(agent.AgentID),
			DurableAgentID: strings.TrimSpace(agent.AgentID),
		},
	}, session.ExecutionEventInput{
		EventType:   eventType,
		Stage:       "durable_agent_provision",
		Status:      status,
		PayloadJSON: string(raw),
		CreatedAt:   time.Now().UTC(),
	})
}

func currentOSUserName() string {
	current, err := user.Current()
	if err != nil || current == nil {
		return ""
	}
	return strings.TrimSpace(current.Username)
}

func mapBool(ok bool, yes string, no string) string {
	if ok {
		return yes
	}
	return no
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}
