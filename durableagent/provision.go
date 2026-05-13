//go:build linux

package durableagent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/tailnet"
)

const DefaultRemoteChildPollInterval = "30s"

type ProvisionOptions struct {
	Agent        core.DurableAgent
	Bootstrap    core.DurableAgentRemoteBootstrap
	BinaryPath   string
	SSHHost      string
	SSHUser      string
	ChildRoot    string
	ServiceName  string
	PollInterval string
	Apply        bool
	Runner       tailnet.SSHRunner
}

type ProvisionPlan struct {
	AgentID          string
	SSHTarget        string
	SSHHost          string
	SSHUser          string
	BinaryPath       string
	ChildRoot        string
	ServiceName      string
	PollInterval     string
	ParentControlURL string
	RemoteBinary     string
	BootstrapPath    string
	DBPath           string
	InboxDir         string
}

type ProvisionResult struct {
	Plan   ProvisionPlan
	Output string
}

func BuildProvisionPlan(opts ProvisionOptions) (ProvisionPlan, error) {
	agent := opts.Agent
	agent.AgentID = strings.TrimSpace(agent.AgentID)
	if err := core.ValidateDurableAgentID(agent.AgentID); err != nil {
		return ProvisionPlan{}, err
	}
	policy := core.NormalizeDurableAgentLivePolicy(agent.LivePolicy)
	host := strings.ToLower(strings.Trim(strings.TrimSpace(opts.SSHHost), "."))
	if host == "" {
		host = strings.ToLower(strings.Trim(strings.TrimSpace(policy.TailnetHostname), "."))
	}
	if strings.TrimSpace(policy.TailnetMode) == "" {
		return ProvisionPlan{}, fmt.Errorf("durable agent %s has no tailnet policy", agent.AgentID)
	}
	if host == "" {
		return ProvisionPlan{}, fmt.Errorf("durable agent %s has no tailnet hostname", agent.AgentID)
	}
	if !safeTailnetSSHHost(host) {
		return ProvisionPlan{}, fmt.Errorf("tailnet ssh host %q is not safe", host)
	}
	sshUser := strings.TrimSpace(opts.SSHUser)
	if sshUser != "" && !safeTailnetSSHUser(sshUser) {
		return ProvisionPlan{}, fmt.Errorf("tailnet ssh user %q is not safe", sshUser)
	}
	target := host
	if sshUser != "" {
		target = sshUser + "@" + host
	}
	binaryPath := strings.TrimSpace(opts.BinaryPath)
	if binaryPath == "" {
		return ProvisionPlan{}, fmt.Errorf("provision binary path is required")
	}
	info, err := os.Stat(binaryPath)
	if err != nil {
		return ProvisionPlan{}, fmt.Errorf("stat provision binary: %w", err)
	}
	if info.IsDir() {
		return ProvisionPlan{}, fmt.Errorf("provision binary path is a directory")
	}
	childRoot := strings.TrimSpace(opts.ChildRoot)
	if childRoot == "" {
		childRoot = "~/.aphelion/children/" + agent.AgentID
	}
	if !safeChildRootPath(childRoot) {
		return ProvisionPlan{}, fmt.Errorf("child root %q is not safe", childRoot)
	}
	serviceName := strings.TrimSpace(opts.ServiceName)
	if serviceName == "" {
		serviceName = "aphelion-child-" + agent.AgentID
	}
	if !safeServiceName(serviceName) {
		return ProvisionPlan{}, fmt.Errorf("service name %q is not safe", serviceName)
	}
	pollInterval := strings.TrimSpace(opts.PollInterval)
	if pollInterval == "" {
		pollInterval = DefaultRemoteChildPollInterval
	}
	if _, err := time.ParseDuration(pollInterval); err != nil {
		return ProvisionPlan{}, fmt.Errorf("parse poll interval: %w", err)
	}
	bootstrap := core.NormalizeDurableAgentRemoteBootstrap(opts.Bootstrap)
	if err := core.ValidateDurableAgentRemoteBootstrap(bootstrap); err != nil {
		return ProvisionPlan{}, err
	}
	return ProvisionPlan{
		AgentID:          agent.AgentID,
		SSHTarget:        target,
		SSHHost:          host,
		SSHUser:          sshUser,
		BinaryPath:       binaryPath,
		ChildRoot:        childRoot,
		ServiceName:      serviceName,
		PollInterval:     pollInterval,
		ParentControlURL: bootstrap.ParentControlURL,
		RemoteBinary:     childRoot + "/bin/aphelion",
		BootstrapPath:    childRoot + "/remote-bootstrap.json",
		DBPath:           childRoot + "/state/sessions.db",
		InboxDir:         childRoot + "/inbox",
	}, nil
}

func ProvisionRemoteChild(ctx context.Context, opts ProvisionOptions) (ProvisionResult, error) {
	plan, err := BuildProvisionPlan(opts)
	if err != nil {
		return ProvisionResult{}, err
	}
	result := ProvisionResult{Plan: plan}
	if !opts.Apply {
		return result, nil
	}
	if opts.Runner == nil {
		return ProvisionResult{}, fmt.Errorf("tailnet ssh runner is required")
	}
	payload, err := provisionArchive(plan.BinaryPath, opts.Bootstrap)
	if err != nil {
		return ProvisionResult{}, err
	}
	sshResult, err := opts.Runner.Run(ctx, plan.SSHTarget, []string{
		"bash",
		"-c",
		remoteProvisionScript,
		"--",
		plan.ChildRoot,
		plan.ServiceName,
		plan.PollInterval,
	}, payload)
	result.Output = sshResult.Output
	if err != nil {
		return result, err
	}
	return result, nil
}

func provisionArchive(binaryPath string, bootstrap core.DurableAgentRemoteBootstrap) ([]byte, error) {
	binary, err := os.ReadFile(filepath.Clean(strings.TrimSpace(binaryPath)))
	if err != nil {
		return nil, fmt.Errorf("read provision binary: %w", err)
	}
	bootstrap = core.NormalizeDurableAgentRemoteBootstrap(bootstrap)
	if err := core.ValidateDurableAgentRemoteBootstrap(bootstrap); err != nil {
		return nil, err
	}
	bootstrapRaw, err := json.MarshalIndent(bootstrap, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal remote bootstrap: %w", err)
	}
	bootstrapRaw = append(bootstrapRaw, '\n')
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, file := range []struct {
		Name string
		Mode int64
		Data []byte
	}{
		{Name: "aphelion", Mode: 0o755, Data: binary},
		{Name: "remote-bootstrap.json", Mode: 0o600, Data: bootstrapRaw},
	} {
		if err := tw.WriteHeader(&tar.Header{
			Name: file.Name,
			Mode: file.Mode,
			Size: int64(len(file.Data)),
		}); err != nil {
			return nil, fmt.Errorf("write provision archive header: %w", err)
		}
		if _, err := tw.Write(file.Data); err != nil {
			return nil, fmt.Errorf("write provision archive file: %w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close provision archive tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("close provision archive gzip: %w", err)
	}
	return buf.Bytes(), nil
}

func hasShellSpace(value string) bool {
	return strings.ContainsAny(value, " \t\r\n")
}

func safeServiceName(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '@':
		default:
			return false
		}
	}
	return true
}

func safeTailnetSSHHost(value string) bool {
	value = strings.ToLower(strings.Trim(strings.TrimSpace(value), "."))
	if value == "" || strings.HasPrefix(value, "-") || len(value) > 253 {
		return false
	}
	labels := strings.Split(value, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z':
			case r >= '0' && r <= '9':
			case r == '-':
			default:
				return false
			}
		}
	}
	return true
}

func safeTailnetSSHUser(value string) bool {
	if value == "" || len(value) > 32 || strings.HasPrefix(value, "-") {
		return false
	}
	for i, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r == '_':
		case i > 0 && r >= '0' && r <= '9':
		case i > 0 && (r == '-' || r == '.'):
		default:
			return false
		}
	}
	return true
}

func safeChildRootPath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || hasShellSpace(value) || strings.Contains(value, "%") || strings.Contains(value, ";") {
		return false
	}
	if !(strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~/")) {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '/', r == '.', r == '-', r == '_', r == '~':
		default:
			return false
		}
	}
	return true
}

const remoteProvisionScript = `
set -euo pipefail
child_root="$1"
service_name="$2"
poll_interval="$3"
case "${child_root}" in
  "~") child_root="${HOME}" ;;
  "~/"*) child_root="${HOME}/${child_root#~/}" ;;
esac
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT
tar -xzf - -C "${tmp_dir}"
install -d -m 0700 "${child_root}" "${child_root}/bin" "${child_root}/state" "${child_root}/inbox"
install -m 0755 "${tmp_dir}/aphelion" "${child_root}/bin/aphelion"
install -m 0600 "${tmp_dir}/remote-bootstrap.json" "${child_root}/remote-bootstrap.json"
"${child_root}/bin/aphelion" durable-agent remote --bootstrap "${child_root}/remote-bootstrap.json" --db "${child_root}/state/sessions.db" sync
service_dir="${XDG_CONFIG_HOME:-${HOME}/.config}/systemd/user"
install -d -m 0755 "${service_dir}"
service_path="${service_dir}/${service_name}.service"
cat > "${service_path}" <<UNIT
[Unit]
Description=Aphelion durable child ${service_name}
After=network-online.target

[Service]
Type=simple
WorkingDirectory="${child_root}"
ExecStart="${child_root}/bin/aphelion" durable-agent remote --bootstrap "${child_root}/remote-bootstrap.json" --db "${child_root}/state/sessions.db" --inbox-dir "${child_root}/inbox" --poll-interval "${poll_interval}" loop
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
UNIT
systemctl --user daemon-reload
systemctl --user enable --now "${service_name}.service"
systemctl --user restart "${service_name}.service"
for _ in 1 2 3 4 5 6 7 8 9 10; do
  if systemctl --user is-active --quiet "${service_name}.service"; then
    break
  fi
  sleep 1
done
systemctl --user is-active --quiet "${service_name}.service"
"${child_root}/bin/aphelion" durable-agent remote --bootstrap "${child_root}/remote-bootstrap.json" --db "${child_root}/state/sessions.db" sync
printf 'child_root: %s\nservice: %s\nstatus: active\n' "${child_root}" "${service_name}.service"
`
