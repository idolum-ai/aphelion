//go:build linux

package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
)

const defaultMaxOutputBytes = 32 * 1024

type Registry struct {
	workspace      string
	timeout        time.Duration
	maxOutputBytes int
}

type execInput struct {
	Command    string `json:"command"`
	Workdir    string `json:"workdir,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

func NewRegistry(workspace string, timeout time.Duration) *Registry {
	return &Registry{
		workspace:      workspace,
		timeout:        timeout,
		maxOutputBytes: defaultMaxOutputBytes,
	}
}

func (r *Registry) Definitions() []agent.ToolDef {
	return []agent.ToolDef{{
		Name:        "exec",
		Description: "Run a shell command in the configured workspace. Use this for git, file inspection, builds, tests, and repository edits.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"command": {"type": "string", "description": "Shell command to run with bash -lc"},
				"workdir": {"type": "string", "description": "Optional subdirectory within the workspace"},
				"timeout_sec": {"type": "integer", "minimum": 1, "description": "Optional per-command timeout in seconds"}
			},
			"required": ["command"]
		}`),
	}}
}

func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	switch name {
	case "exec":
		return r.exec(ctx, input)
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func (r *Registry) exec(ctx context.Context, input json.RawMessage) (string, error) {
	var in execInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode exec input: %w", err)
	}
	if strings.TrimSpace(in.Command) == "" {
		return "", fmt.Errorf("exec command is required")
	}

	workdir, err := r.resolveWorkdir(in.Workdir)
	if err != nil {
		return "", err
	}

	timeout := r.timeout
	if in.TimeoutSec > 0 {
		timeout = time.Duration(in.TimeoutSec) * time.Second
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-lc", in.Command)
	cmd.Dir = workdir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	out := renderOutput(stdout.String(), stderr.String(), r.maxOutputBytes)
	if err == nil {
		return out, nil
	}

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("command timed out after %s", timeout)
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return out, fmt.Errorf("command failed with exit code %d", exitErr.ExitCode())
	}

	return out, fmt.Errorf("run command: %w", err)
}

func (r *Registry) resolveWorkdir(raw string) (string, error) {
	base, err := filepath.Abs(r.workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}

	target := base
	if strings.TrimSpace(raw) != "" {
		if filepath.IsAbs(raw) {
			target = filepath.Clean(raw)
		} else {
			target = filepath.Join(base, raw)
		}
	}

	target, err = filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve workdir: %w", err)
	}

	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", fmt.Errorf("check workdir: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workdir %q escapes workspace %q", raw, base)
	}

	return target, nil
}

func renderOutput(stdout, stderr string, limit int) string {
	parts := make([]string, 0, 2)
	if strings.TrimSpace(stdout) != "" {
		parts = append(parts, "stdout:\n"+truncate(stdout, limit))
	}
	if strings.TrimSpace(stderr) != "" {
		parts = append(parts, "stderr:\n"+truncate(stderr, limit))
	}
	if len(parts) == 0 {
		return "(no output)"
	}
	return strings.Join(parts, "\n\n")
}

func truncate(raw string, limit int) string {
	if len(raw) <= limit || limit <= 0 {
		return raw
	}
	if limit <= 64 {
		return raw[:limit]
	}
	head := limit / 2
	tail := limit / 2
	return raw[:head] + "\n...[truncated]...\n" + raw[len(raw)-tail:]
}
