//go:build linux

package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

const (
	defaultSystemLogLimit    = 120
	maxSystemLogLimit        = 500
	defaultSystemLogMaxBytes = 64 * 1024
	maxSystemLogMaxBytes     = 256 * 1024
)

type systemLogReadInput struct {
	Unit     string   `json:"unit"`
	System   bool     `json:"system,omitempty"`
	Since    string   `json:"since,omitempty"`
	Until    string   `json:"until,omitempty"`
	Priority string   `json:"priority,omitempty"`
	Include  []string `json:"include,omitempty"`
	Exclude  []string `json:"exclude,omitempty"`
	Limit    int      `json:"limit,omitempty"`
	MaxBytes int      `json:"max_bytes,omitempty"`
}

type systemLogCommandResult struct {
	Stdout string
	Stderr string
}

var runSystemLogCommand = defaultRunSystemLogCommand

func (r *Registry) systemLogRead(ctx context.Context, input json.RawMessage, p principal.Principal, key session.SessionKey) (string, error) {
	if !adminDiagnosticReadAllowed(p, key) {
		return "", fmt.Errorf("system_log_read requires admin diagnostic scope")
	}
	var in systemLogReadInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode system_log_read input: %w", err)
	}
	in.Unit = strings.TrimSpace(in.Unit)
	if in.Unit == "" {
		return "", fmt.Errorf("system_log_read unit is required")
	}
	if strings.ContainsAny(in.Unit, "\x00\r\n") {
		return "", fmt.Errorf("system_log_read unit contains invalid characters")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultSystemLogLimit
	}
	if limit > maxSystemLogLimit {
		limit = maxSystemLogLimit
	}
	maxBytes := in.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultSystemLogMaxBytes
	}
	if maxBytes > maxSystemLogMaxBytes {
		maxBytes = maxSystemLogMaxBytes
	}
	args := systemLogReadArgs(in)
	runCtx, cancel := context.WithTimeout(ctx, defaultTimeout(r.timeout))
	defer cancel()
	result, err := runSystemLogCommand(runCtx, args)
	filtered, partial := systemLogFilterLines(result.Stdout, in.Include, in.Exclude, limit)
	if len(filtered) == 0 && strings.TrimSpace(result.Stderr) != "" {
		filtered = append(filtered, "stderr: "+strings.TrimSpace(result.Stderr))
	}
	out := strings.Join(filtered, "\n")
	if len(out) > maxBytes {
		out = out[:maxBytes]
		partial = true
	}
	redacted := session.RedactEvidenceText(out).Text
	if strings.TrimSpace(redacted) == "" {
		redacted = "(no matching log lines)"
	}
	meta := []string{
		"system_log_read:",
		"unit: " + in.Unit,
		fmt.Sprintf("journal: %s", systemLogJournalLabel(in.System)),
		fmt.Sprintf("lines: %d", len(filtered)),
		fmt.Sprintf("partial: %t", partial),
	}
	if err != nil {
		safeErr := session.RedactEvidenceText(err.Error()).Text
		meta = append(meta, "status: failed")
		meta = append(meta, "error: "+safeErr)
		return strings.Join(append(meta, "", redacted), "\n"), fmt.Errorf("system_log_read journalctl: %s", safeErr)
	}
	meta = append(meta, "status: ok")
	return strings.Join(append(meta, "", redacted), "\n"), nil
}

func adminDiagnosticReadAllowed(p principal.Principal, key session.SessionKey) bool {
	if p.Role != principal.RoleAdmin {
		return false
	}
	if key.ChatID == 0 && key.Scope.IsZero() {
		return true
	}
	return adminExactExecApprovalAllowed(p, key)
}

func systemLogReadArgs(in systemLogReadInput) []string {
	args := []string{"--no-pager", "--output=short-iso"}
	if in.System {
		args = append(args, "--system")
	} else {
		args = append(args, "--user")
	}
	args = append(args, "-u", strings.TrimSpace(in.Unit))
	if since := strings.TrimSpace(in.Since); since != "" {
		args = append(args, "--since", since)
	}
	if until := strings.TrimSpace(in.Until); until != "" {
		args = append(args, "--until", until)
	}
	if priority := strings.TrimSpace(in.Priority); priority != "" {
		args = append(args, "--priority", priority)
	}
	return args
}

func defaultRunSystemLogCommand(ctx context.Context, args []string) (systemLogCommandResult, error) {
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return systemLogCommandResult{Stdout: stdout.String(), Stderr: stderr.String()}, err
}

func systemLogFilterLines(raw string, include []string, exclude []string, limit int) ([]string, bool) {
	include = normalizeSystemLogFilters(include)
	exclude = normalizeSystemLogFilters(exclude)
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	var matched []string
	for _, line := range lines {
		if !systemLogLineMatches(line, include, true) {
			continue
		}
		if systemLogLineMatches(line, exclude, false) {
			continue
		}
		matched = append(matched, line)
	}
	partial := false
	if limit > 0 && len(matched) > limit {
		partial = true
		matched = matched[len(matched)-limit:]
	}
	return matched, partial
}

func normalizeSystemLogFilters(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func systemLogLineMatches(line string, filters []string, emptyMatches bool) bool {
	if len(filters) == 0 {
		return emptyMatches
	}
	line = strings.ToLower(line)
	for _, filter := range filters {
		if strings.Contains(line, filter) {
			return true
		}
	}
	return false
}

func systemLogJournalLabel(system bool) string {
	if system {
		return "system"
	}
	return "user"
}
