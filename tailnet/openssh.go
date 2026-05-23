//go:build linux

package tailnet

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type OpenSSHRunner interface {
	RunOpenSSH(ctx context.Context, req OpenSSHRequest) (SSHResult, error)
}

type OpenSSHRequest struct {
	Host  string
	User  string
	Port  int
	Args  []string
	Stdin []byte
}

type OpenSSHOptions struct {
	SSHPath        string
	CommandTimeout time.Duration
	Runner         SSHCommandRunner
}

type OpenSSHClient struct {
	sshPath        string
	commandTimeout time.Duration
	runner         SSHCommandRunner
}

func NewOpenSSHClient(opts OpenSSHOptions) *OpenSSHClient {
	sshPath := strings.TrimSpace(opts.SSHPath)
	if sshPath == "" {
		sshPath = "ssh"
	}
	timeout := opts.CommandTimeout
	if timeout <= 0 {
		timeout = DefaultCommandTimeout
	}
	runner := opts.Runner
	if runner == nil {
		runner = ExecSSHCommandRunner{}
	}
	return &OpenSSHClient{sshPath: sshPath, commandTimeout: timeout, runner: runner}
}

func (c *OpenSSHClient) RunOpenSSH(ctx context.Context, req OpenSSHRequest) (SSHResult, error) {
	if c == nil {
		return SSHResult{}, fmt.Errorf("openssh client is nil")
	}
	req.Host = strings.ToLower(strings.Trim(strings.TrimSpace(req.Host), "."))
	req.User = strings.TrimSpace(req.User)
	if !SafeSSHHost(req.Host) {
		return SSHResult{}, fmt.Errorf("openssh host %q is not safe", req.Host)
	}
	if !SafeSSHUser(req.User) {
		return SSHResult{}, fmt.Errorf("openssh user %q is not safe", req.User)
	}
	if req.Port < 0 || req.Port > 65535 {
		return SSHResult{}, fmt.Errorf("openssh port %d is invalid", req.Port)
	}
	commandArgs := make([]string, 0, len(req.Args))
	for _, arg := range req.Args {
		if strings.TrimSpace(arg) == "" {
			continue
		}
		commandArgs = append(commandArgs, arg)
	}
	if len(commandArgs) == 0 {
		return SSHResult{}, fmt.Errorf("openssh command is required")
	}

	target := req.User + "@" + req.Host
	argv := make([]string, 0, len(commandArgs)+5)
	if req.Port > 0 {
		argv = append(argv, "-p", strconv.Itoa(req.Port))
	}
	argv = append(argv, "--", target, openSSHRemoteCommand(commandArgs))

	runCtx := ctx
	cancel := func() {}
	if c.commandTimeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, c.commandTimeout)
	}
	defer cancel()
	output, code, err := c.runner.Run(runCtx, c.sshPath, argv, req.Stdin)
	result := SSHResult{
		Target:   target,
		Args:     append([]string(nil), commandArgs...),
		Output:   strings.TrimSpace(string(output)),
		ExitCode: code,
	}
	if err != nil {
		if result.ExitCode == 0 {
			result.ExitCode = 1
		}
		return result, fmt.Errorf("openssh %s failed: %w", target, err)
	}
	return result, nil
}

func openSSHRemoteCommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, openSSHShellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func openSSHShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func SafeSSHHost(value string) bool {
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

func SafeSSHUser(value string) bool {
	if value == "" || len(value) > 64 || strings.HasPrefix(value, "-") {
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
