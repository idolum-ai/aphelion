//go:build linux

package maintenancecli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type sandboxNetCheckResult struct {
	Action     string                       `json:"action"`
	ConfigPath string                       `json:"config_path"`
	Backend    sandbox.NetworkBackendStatus `json:"backend"`
	Profiles   []sandboxNetProfileStatus    `json:"profiles"`
}

type sandboxNetProfileStatus struct {
	Role         string   `json:"role"`
	Mode         string   `json:"mode"`
	Network      string   `json:"network"`
	NetworkAllow []string `json:"network_allow,omitempty"`
	Ready        bool     `json:"ready"`
	Reason       string   `json:"reason,omitempty"`
}

func runSandboxNetCommand(args []string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "check" {
		if len(args) > 0 {
			args = args[1:]
		}
		return runSandboxNetCheckCommand(args)
	}
	if strings.TrimSpace(args[0]) == "helper" {
		return runSandboxNetHelperCommand(args[1:])
	}
	return fmt.Errorf("unknown sandbox-net command %q", args[0])
}

func runSandboxNetHelperCommand(args []string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) != "serve" {
		return fmt.Errorf("unknown sandbox-net helper command %q", firstArgOrEmpty(args))
	}
	return runSandboxNetHelperServeCommand(args[1:])
}

func runSandboxNetHelperServeCommand(args []string) error {
	fs := flag.NewFlagSet("sandbox-net helper serve", flag.ContinueOnError)
	socketPathFlag := fs.String("socket", sandbox.DefaultNetworkHelperSocketPath, "Unix socket path")
	socketGroupFlag := fs.String("socket-group", "", "Unix socket group name")
	socketModeFlag := fs.String("socket-mode", "0660", "Unix socket mode")
	allowedUIDFlag := fs.Int("allowed-uid", -1, "only accept run requests from this peer UID")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if extra, ok := firstPositionalArg(fs.Args()); ok {
		return fmt.Errorf("unknown argument %q for sandbox-net helper serve", extra)
	}
	socketMode, err := parseOctalFileMode(*socketModeFlag)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return sandbox.ServeNetworkHelper(ctx, sandbox.NetworkHelperServeOptions{
		SocketPath:        *socketPathFlag,
		SocketGroup:       *socketGroupFlag,
		SocketMode:        socketMode,
		AllowedUID:        *allowedUIDFlag,
		EnforceAllowedUID: *allowedUIDFlag >= 0,
	})
}

func runSandboxNetCheckCommand(args []string) error {
	fs := flag.NewFlagSet("sandbox-net check", flag.ContinueOnError)
	configPathFlag := fs.String("config", "", "path to config.toml")
	formatFlag := fs.String("format", commandOutputHuman, "output format: human, kv, json")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if extra, ok := firstPositionalArg(fs.Args()); ok {
		return fmt.Errorf("unknown argument %q for sandbox-net check", extra)
	}
	format, err := normalizeCommandOutputFormat(*formatFlag, *jsonOutput)
	if err != nil {
		return err
	}
	configPath, err := config.ResolveConfigPath(*configPathFlag)
	if err != nil {
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return &ConfigStartupError{Path: configPath, Err: err}
	}
	result, err := buildSandboxNetCheckResult(context.Background(), configPath, cfg)
	if err != nil {
		return err
	}
	switch format {
	case commandOutputJSON:
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	case commandOutputKV:
		renderSandboxNetCheckKV(result)
		return nil
	default:
		fmt.Fprintln(os.Stdout, renderSandboxNetCheckHuman(result))
		return nil
	}
}

func buildSandboxNetCheckResult(ctx context.Context, configPath string, cfg *config.Config) (sandboxNetCheckResult, error) {
	profiles, err := sandboxProfilesFromConfig(cfg.Sandbox)
	if err != nil {
		return sandboxNetCheckResult{}, err
	}
	runner := sandbox.NewRunner()
	status := runner.NetworkBackendStatus(ctx)
	result := sandboxNetCheckResult{
		Action:     "sandbox-net check",
		ConfigPath: configPath,
		Backend:    status,
	}
	for _, entry := range []struct {
		role    string
		profile sandbox.Profile
	}{
		{role: "admin", profile: profiles.Admin},
		{role: "approved_user", profile: profiles.ApprovedUser},
		{role: "durable_agent", profile: profiles.DurableAgent},
	} {
		profileStatus := sandboxNetProfileStatus{
			Role:         entry.role,
			Mode:         string(entry.profile.Mode),
			Network:      string(entry.profile.Network),
			NetworkAllow: networkDestinationStrings(entry.profile.NetworkAllow),
			Ready:        true,
		}
		if entry.profile.Mode == sandbox.ModeIsolated && entry.profile.Network == sandbox.NetworkAllowlist {
			if runner.Stage(sandbox.Scope{Profile: entry.profile}) == sandbox.StageUnavailable {
				profileStatus.Ready = false
				profileStatus.Reason = "bubblewrap is unavailable"
			} else if len(entry.profile.NetworkAllow) == 0 {
				profileStatus.Ready = false
				profileStatus.Reason = "network_allow is empty"
			} else if !status.Available {
				profileStatus.Ready = false
				profileStatus.Reason = "network backend unavailable: " + strings.TrimSpace(status.Reason)
			}
		}
		result.Profiles = append(result.Profiles, profileStatus)
	}
	return result, nil
}

func renderSandboxNetCheckKV(result sandboxNetCheckResult) {
	fmt.Fprintf(os.Stdout, "action: %s\n", result.Action)
	fmt.Fprintf(os.Stdout, "config_path: %s\n", result.ConfigPath)
	fmt.Fprintf(os.Stdout, "backend: %s\n", result.Backend.Name)
	fmt.Fprintf(os.Stdout, "backend_available: %t\n", result.Backend.Available)
	fmt.Fprintf(os.Stdout, "backend_reason: %s\n", strings.TrimSpace(result.Backend.Reason))
	for _, profile := range result.Profiles {
		prefix := "profile_" + profile.Role + "_"
		fmt.Fprintf(os.Stdout, "%smode: %s\n", prefix, profile.Mode)
		fmt.Fprintf(os.Stdout, "%snetwork: %s\n", prefix, profile.Network)
		fmt.Fprintf(os.Stdout, "%sready: %t\n", prefix, profile.Ready)
		fmt.Fprintf(os.Stdout, "%sreason: %s\n", prefix, strings.TrimSpace(profile.Reason))
		if len(profile.NetworkAllow) > 0 {
			fmt.Fprintf(os.Stdout, "%snetwork_allow: %s\n", prefix, strings.Join(profile.NetworkAllow, ","))
		}
	}
}

func renderSandboxNetCheckHuman(result sandboxNetCheckResult) string {
	state := "ready"
	if !result.Backend.Available {
		state = "unavailable"
	}
	details := []string{
		"Backend: " + result.Backend.Name,
		fmt.Sprintf("Available: %t", result.Backend.Available),
	}
	if reason := strings.TrimSpace(result.Backend.Reason); reason != "" {
		details = append(details, "Reason: "+reason)
	}
	for _, profile := range result.Profiles {
		line := fmt.Sprintf("%s: mode=%s network=%s ready=%t", profile.Role, profile.Mode, profile.Network, profile.Ready)
		if len(profile.NetworkAllow) > 0 {
			line += " allow=" + strings.Join(profile.NetworkAllow, ",")
		}
		if reason := strings.TrimSpace(profile.Reason); reason != "" {
			line += " reason=" + reason
		}
		details = append(details, line)
	}
	return face.RenderOperatorPanel(face.OperatorPanel{
		Title:   "Aphelion Sandbox Network",
		State:   state,
		Why:     "Isolated network allowlists require a host-enforced namespace and firewall backend.",
		Next:    "Use network=deny unless this check reports the allowlist backend as available for the configured profile.",
		Details: details,
	})
}

func networkDestinationStrings(destinations []sandbox.NetworkDestination) []string {
	out := make([]string, 0, len(destinations))
	for _, destination := range destinations {
		out = append(out, destination.Canonical())
	}
	return out
}

func parseOctalFileMode(raw string) (os.FileMode, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, fmt.Errorf("file mode is required")
	}
	parsed, err := strconv.ParseUint(value, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("file mode %q must be octal: %w", raw, err)
	}
	return os.FileMode(parsed), nil
}

func firstArgOrEmpty(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
