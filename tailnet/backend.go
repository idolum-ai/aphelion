//go:build linux

package tailnet

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

const DefaultCommandTimeout = 5 * time.Second

type Backend interface {
	Snapshot(ctx context.Context) (core.TailnetStatusSnapshot, error)
}

type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

type CLIOptions struct {
	CLIPath          string
	CommandTimeout   time.Duration
	ExpectedTailnet  string
	ExpectedHostname string
	ExpectedTags     []string
	Runner           CommandRunner
	Now              func() time.Time
}

type CLIBackend struct {
	cliPath          string
	commandTimeout   time.Duration
	expectedTailnet  string
	expectedHostname string
	expectedTags     []string
	runner           CommandRunner
	now              func() time.Time
}

func NewCLIBackend(opts CLIOptions) *CLIBackend {
	cliPath := strings.TrimSpace(opts.CLIPath)
	if cliPath == "" {
		cliPath = "tailscale"
	}
	timeout := opts.CommandTimeout
	if timeout <= 0 {
		timeout = DefaultCommandTimeout
	}
	runner := opts.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &CLIBackend{
		cliPath:          cliPath,
		commandTimeout:   timeout,
		expectedTailnet:  strings.TrimSpace(opts.ExpectedTailnet),
		expectedHostname: strings.TrimSpace(opts.ExpectedHostname),
		expectedTags:     normalizeList(opts.ExpectedTags),
		runner:           runner,
		now:              now,
	}
}

func DisabledSnapshot(now time.Time) core.TailnetStatusSnapshot {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return core.TailnetStatusSnapshot{
		GeneratedAt: now.UTC(),
		Enabled:     false,
		Backend:     "disabled",
		Status:      "disabled",
		Summary:     "Tailscale integration is disabled.",
	}
}

func (b *CLIBackend) Snapshot(ctx context.Context) (core.TailnetStatusSnapshot, error) {
	if b == nil {
		return DisabledSnapshot(time.Now().UTC()), nil
	}
	now := b.currentTime()
	snapshot := core.TailnetStatusSnapshot{
		GeneratedAt:      now,
		Enabled:          true,
		Backend:          "cli",
		Status:           "unknown",
		ExpectedTailnet:  b.expectedTailnet,
		ExpectedHostname: b.expectedHostname,
		ExpectedTags:     append([]string(nil), b.expectedTags...),
	}

	if versionOut, err := b.runReadOnly(ctx, "version"); err == nil {
		snapshot.TailscaleVersion = firstNonEmptyLine(string(versionOut))
	} else {
		snapshot.Issues = append(snapshot.Issues, classifyCommandIssue("version", err, string(versionOut)))
	}

	statusOut, statusErr := b.runReadOnly(ctx, "status", "--json")
	if statusErr != nil {
		snapshot.RawStatusError = trimCommandError(statusErr, string(statusOut))
		snapshot.Issues = append(snapshot.Issues, classifyCommandIssue("status", statusErr, string(statusOut)))
		snapshot.Status = summarizeTailnetStatus(snapshot.Issues)
		snapshot.Summary = summarizeTailnetSnapshot(snapshot)
		return snapshot, nil
	}
	parsed, err := ParseStatusJSON(statusOut)
	if err != nil {
		snapshot.RawStatusError = err.Error()
		snapshot.Issues = append(snapshot.Issues, core.TailnetIssue{
			Code:     "status_json_invalid",
			Severity: "error",
			Summary:  "tailscale status --json returned malformed JSON.",
		})
		snapshot.Status = summarizeTailnetStatus(snapshot.Issues)
		snapshot.Summary = summarizeTailnetSnapshot(snapshot)
		return snapshot, nil
	}
	mergeParsedStatus(&snapshot, parsed)

	if ipOut, err := b.runReadOnly(ctx, "ip"); err == nil {
		if ips := parseIPOutput(string(ipOut)); len(ips) > 0 {
			snapshot.TailscaleIPs = ips
		}
	} else {
		snapshot.RawIPError = trimCommandError(err, string(ipOut))
		snapshot.Issues = append(snapshot.Issues, classifyCommandIssue("ip", err, string(ipOut)))
	}

	if netcheckOut, err := b.runReadOnly(ctx, "netcheck"); err == nil {
		snapshot.NetcheckAvailable = true
		snapshot.NetcheckSummary = summarizeNetcheckOutput(string(netcheckOut))
	} else {
		snapshot.RawNetcheckError = trimCommandError(err, string(netcheckOut))
		snapshot.Issues = append(snapshot.Issues, classifyCommandIssue("netcheck", err, string(netcheckOut)))
	}

	snapshot.Issues = append(snapshot.Issues, evaluateExpectedState(snapshot)...)
	snapshot.Status = summarizeTailnetStatus(snapshot.Issues)
	snapshot.Summary = summarizeTailnetSnapshot(snapshot)
	return snapshot, nil
}

func (b *CLIBackend) runReadOnly(parent context.Context, args ...string) ([]byte, error) {
	if !ReadOnlyCommandAllowed(args) {
		return nil, fmt.Errorf("tailscale command is not read-only: %s", strings.Join(args, " "))
	}
	ctx := parent
	cancel := func() {}
	if b.commandTimeout > 0 {
		ctx, cancel = context.WithTimeout(parent, b.commandTimeout)
	}
	defer cancel()
	return b.runner.Run(ctx, b.cliPath, args...)
}

func ReadOnlyCommandAllowed(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch strings.TrimSpace(args[0]) {
	case "version", "status", "ip", "netcheck", "whois", "ping":
		for _, arg := range args[1:] {
			switch strings.TrimSpace(arg) {
			case "up", "set", "serve", "funnel", "ssh", "file", "cert", "lock", "logout":
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (b *CLIBackend) currentTime() time.Time {
	if b.now != nil {
		return b.now().UTC()
	}
	return time.Now().UTC()
}

func classifyCommandIssue(command string, err error, output string) core.TailnetIssue {
	summary := strings.TrimSpace(output)
	if summary == "" && err != nil {
		summary = err.Error()
	}
	summary = truncate(summary, 220)
	code := "command_failed"
	severity := "warning"
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || strings.Contains(strings.ToLower(err.Error()), "executable file not found") {
			code = "cli_unavailable"
			severity = "error"
			summary = "tailscale CLI is not available to the runtime."
		} else if errors.Is(err, context.DeadlineExceeded) {
			code = "command_timeout"
			severity = "error"
			summary = "tailscale " + command + " timed out."
		} else if command == "status" {
			code = "status_unavailable"
			severity = "error"
			if summary == "" {
				summary = "tailscale status is unavailable."
			}
		} else {
			code = command + "_unavailable"
		}
	}
	return core.TailnetIssue{
		Code:     code,
		Severity: severity,
		Summary:  summary,
	}
}

func evaluateExpectedState(snapshot core.TailnetStatusSnapshot) []core.TailnetIssue {
	var issues []core.TailnetIssue
	if state := strings.TrimSpace(snapshot.BackendState); state != "" && !strings.EqualFold(state, "Running") {
		severity := "warning"
		code := "backend_not_running"
		summary := "tailscale backend state is " + state + "."
		if strings.EqualFold(state, "NeedsLogin") || strings.EqualFold(state, "Stopped") {
			severity = "error"
			code = "daemon_not_authenticated"
			summary = "tailscale daemon is not authenticated."
		}
		issues = append(issues, core.TailnetIssue{Code: code, Severity: severity, Summary: summary})
	}
	if strings.TrimSpace(snapshot.ExpectedTailnet) != "" && strings.TrimSpace(snapshot.TailnetName) != "" && !strings.EqualFold(snapshot.ExpectedTailnet, snapshot.TailnetName) {
		issues = append(issues, core.TailnetIssue{
			Code:     "tailnet_mismatch",
			Severity: "error",
			Summary:  fmt.Sprintf("expected tailnet %q but observed %q.", snapshot.ExpectedTailnet, snapshot.TailnetName),
		})
	}
	if strings.TrimSpace(snapshot.ExpectedHostname) != "" && strings.TrimSpace(snapshot.HostName) != "" && !strings.EqualFold(snapshot.ExpectedHostname, snapshot.HostName) {
		issues = append(issues, core.TailnetIssue{
			Code:     "hostname_mismatch",
			Severity: "error",
			Summary:  fmt.Sprintf("expected hostname %q but observed %q.", snapshot.ExpectedHostname, snapshot.HostName),
		})
	}
	for _, tag := range snapshot.ExpectedTags {
		if !containsString(snapshot.Tags, tag) {
			issues = append(issues, core.TailnetIssue{
				Code:     "tag_missing",
				Severity: "warning",
				Summary:  "expected tag missing: " + tag,
			})
		}
	}
	if len(snapshot.TailscaleIPs) == 0 {
		issues = append(issues, core.TailnetIssue{
			Code:     "ip_missing",
			Severity: "warning",
			Summary:  "no Tailscale IPs were observed.",
		})
	}
	if strings.TrimSpace(snapshot.DNSName) == "" {
		issues = append(issues, core.TailnetIssue{
			Code:     "magicdns_missing",
			Severity: "warning",
			Summary:  "no MagicDNS name was observed.",
		})
	}
	return issues
}

func summarizeTailnetStatus(issues []core.TailnetIssue) string {
	status := "healthy"
	for _, issue := range issues {
		switch strings.ToLower(strings.TrimSpace(issue.Severity)) {
		case "error":
			return "degraded"
		case "warning":
			status = "degraded"
		}
	}
	return status
}

func summarizeTailnetSnapshot(snapshot core.TailnetStatusSnapshot) string {
	if !snapshot.Enabled {
		return "Tailscale integration is disabled."
	}
	host := firstNonEmpty(snapshot.DNSName, snapshot.HostName, "unknown node")
	status := strings.TrimSpace(snapshot.Status)
	if status == "" {
		status = "unknown"
	}
	if len(snapshot.Issues) == 0 {
		return fmt.Sprintf("%s is %s on tailnet %s.", host, status, firstNonEmpty(snapshot.TailnetName, "unknown"))
	}
	return fmt.Sprintf("%s is %s with %d issue(s).", host, status, len(snapshot.Issues))
}

func parseIPOutput(raw string) []string {
	var ips []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		ips = append(ips, line)
	}
	return normalizeList(ips)
}

func summarizeNetcheckOutput(raw string) string {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	parts := make([]string, 0, 4)
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "*"))
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "udp:") || strings.HasPrefix(lower, "ipv4:") || strings.HasPrefix(lower, "ipv6:") || strings.HasPrefix(lower, "mappingvariesbydestip:") {
			parts = append(parts, line)
		}
		if len(parts) >= 4 {
			break
		}
	}
	if len(parts) == 0 {
		return truncate(strings.Join(nonEmptyLines(raw), "; "), 220)
	}
	return strings.Join(parts, "; ")
}

func trimCommandError(err error, output string) string {
	text := strings.TrimSpace(output)
	if text == "" && err != nil {
		text = err.Error()
	}
	return truncate(text, 500)
}

func nonEmptyLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func normalizeList(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func containsString(values []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptyLine(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func truncate(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return strings.TrimSpace(text[:limit-3]) + "..."
}
