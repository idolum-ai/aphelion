//go:build linux

package tailnet

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type SSHRunner interface {
	Run(ctx context.Context, target string, args []string, stdin []byte) (SSHResult, error)
}

type SSHResult struct {
	Target   string
	Args     []string
	Output   string
	ExitCode int
}

type SSHCommandRunner interface {
	Run(ctx context.Context, name string, args []string, stdin []byte) ([]byte, int, error)
}

type SSHOptions struct {
	CLIPath        string
	CommandTimeout time.Duration
	Runner         SSHCommandRunner
}

type SSHClient struct {
	cliPath        string
	commandTimeout time.Duration
	runner         SSHCommandRunner
}

type ExecSSHCommandRunner struct{}

func NewSSHClient(opts SSHOptions) *SSHClient {
	cliPath := strings.TrimSpace(opts.CLIPath)
	if cliPath == "" {
		cliPath = "tailscale"
	}
	timeout := opts.CommandTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	runner := opts.Runner
	if runner == nil {
		runner = ExecSSHCommandRunner{}
	}
	return &SSHClient{cliPath: cliPath, commandTimeout: timeout, runner: runner}
}

func (c *SSHClient) Run(ctx context.Context, target string, args []string, stdin []byte) (SSHResult, error) {
	if c == nil {
		return SSHResult{}, fmt.Errorf("tailnet ssh client is nil")
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return SSHResult{}, fmt.Errorf("tailnet ssh target is required")
	}
	if unsafeSSHTarget(target) {
		return SSHResult{}, fmt.Errorf("tailnet ssh target %q is not safe", target)
	}
	argv := make([]string, 0, len(args)+3)
	argv = append(argv, "ssh", "--", target)
	commandArgs := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.TrimSpace(arg) == "" {
			continue
		}
		argv = append(argv, arg)
		commandArgs = append(commandArgs, arg)
	}
	if len(commandArgs) == 0 {
		return SSHResult{}, fmt.Errorf("tailnet ssh command is required")
	}
	runCtx := ctx
	cancel := func() {}
	if c.commandTimeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, c.commandTimeout)
	}
	defer cancel()
	output, code, err := c.runner.Run(runCtx, c.cliPath, argv, stdin)
	result := SSHResult{
		Target:   target,
		Args:     commandArgs,
		Output:   strings.TrimSpace(string(output)),
		ExitCode: code,
	}
	if err != nil {
		if result.ExitCode == 0 {
			result.ExitCode = 1
		}
		return result, fmt.Errorf("tailnet ssh %s failed: %w", target, err)
	}
	return result, nil
}

func unsafeSSHTarget(target string) bool {
	if target == "" || strings.HasPrefix(target, "-") || strings.ContainsAny(target, " \t\r\n") {
		return true
	}
	if _, host, ok := strings.Cut(target, "@"); ok && strings.HasPrefix(host, "-") {
		return true
	}
	return false
}

func (ExecSSHCommandRunner) Run(ctx context.Context, name string, args []string, stdin []byte) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return out, 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return out, exitErr.ExitCode(), err
	}
	return out, 1, err
}
