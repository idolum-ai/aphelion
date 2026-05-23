//go:build linux

package main

import (
	"fmt"
	"io"
	"strings"
)

type cliUsageError struct {
	Text string
}

func (e *cliUsageError) Error() string {
	return strings.TrimSpace(e.Text)
}

var cliCommandGroups = []struct {
	Title    string
	Commands []string
}{
	{Title: "Run", Commands: []string{
		"aphelion --config <path>",
		"aphelion --check-config --config <path>",
		"aphelion quickstart [--detect-admin] [--install-service]",
	}},
	{Title: "Setup and deploy", Commands: []string{
		"quickstart", "init", "paths", "park-restart", "verify-deploy", "version",
	}},
	{Title: "Repair and maintenance", Commands: []string{
		"repair-live-state", "repair-capability-grants", "repair-review-redactions", "telegram-threads sanitize", "gc", "forget", "reset",
	}},
	{Title: "Memory import", Commands: []string{
		"import-audit", "import-semantic", "import-codex-sessions",
	}},
	{Title: "Governed control", Commands: []string{
		"authority", "durable-agent", "tailnet", "github-app status", "sandbox-net check", "sandbox-net helper serve", "telegram-child-bot", "agency-eval",
	}},
}

func printTopLevelHelp(w io.Writer, note string) {
	text := renderTopLevelHelp(note)
	if strings.TrimSpace(text) == "" {
		return
	}
	fmt.Fprintln(w, text)
}

func renderTopLevelHelp(note string) string {
	lines := []string{"Aphelion", "Status: ready", "Why: Telegram is the operator control link; CLI is the local maintenance surface.", "Next: run a command below, or start the service with --config.", ""}
	if note = strings.TrimSpace(note); note != "" {
		lines = append(lines, note, "")
	}
	lines = append(lines, "Commands:")
	for _, group := range cliCommandGroups {
		lines = append(lines, group.Title+":")
		for _, command := range group.Commands {
			lines = append(lines, "  "+command)
		}
	}
	lines = append(lines, "", "Examples:")
	lines = append(lines, "  aphelion --config ~/.aphelion/aphelion.toml")
	lines = append(lines, "  aphelion --version")
	lines = append(lines, "  aphelion version --json")
	lines = append(lines, "  aphelion quickstart --detect-admin --install-service")
	lines = append(lines, "  aphelion durable-agent list --config ~/.aphelion/aphelion.toml")
	lines = append(lines, "  aphelion tailnet surfaces --config ~/.aphelion/aphelion.toml")
	lines = append(lines, "  aphelion github-app status --config ~/.aphelion/aphelion.toml")
	lines = append(lines, "  aphelion sandbox-net check --config ~/.aphelion/aphelion.toml")
	lines = append(lines, "  sudo aphelion sandbox-net helper serve --allowed-uid $(id -u)")
	return strings.Join(lines, "\n")
}

func topLevelVersionRequested(args []string) bool {
	if len(args) == 0 {
		return false
	}
	first := strings.TrimSpace(args[0])
	return first == "--version" || first == "-v"
}

func topLevelVersionArgs(args []string) []string {
	if len(args) <= 1 {
		return nil
	}
	return append([]string(nil), args[1:]...)
}

func unknownTopLevelFlag(args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	first := strings.TrimSpace(args[0])
	if first == "" || !strings.HasPrefix(first, "-") {
		return "", false
	}
	if topLevelHelpRequested(args) || topLevelVersionRequested(args) || knownDaemonFlag(first) {
		return "", false
	}
	return first, true
}

func knownDaemonFlag(flag string) bool {
	flag = strings.TrimSpace(flag)
	if idx := strings.Index(flag, "="); idx >= 0 {
		flag = flag[:idx]
	}
	switch flag {
	case "--config", "-config", "--check-config", "-check-config":
		return true
	default:
		return false
	}
}

func topLevelHelpRequested(args []string) bool {
	if len(args) == 0 {
		return false
	}
	first := strings.TrimSpace(args[0])
	return first == "-h" || first == "--help" || first == "help"
}

func renderUnknownCommandHelp(command string) string {
	command = strings.TrimSpace(command)
	note := "Unknown command: " + command
	if suggestion := nearestCLICommand(command); suggestion != "" {
		note += "\nDid you mean: " + suggestion + "?"
	}
	return renderTopLevelHelp(note)
}

func renderUnknownFlagHelp(flag string) string {
	flag = strings.TrimSpace(flag)
	note := "Unknown flag: " + flag
	switch flag {
	case "--versoin", "--verison", "--verson":
		note += "\nDid you mean: aphelion --version?"
	default:
		note += "\nTry: aphelion --help"
	}
	return renderTopLevelHelp(note)
}

func nearestCLICommand(command string) string {
	command = strings.ToLower(strings.TrimSpace(command))
	if command == "" {
		return ""
	}
	best := ""
	bestDistance := 4
	for _, candidate := range knownCLICommands() {
		distance := editDistance(command, candidate)
		if distance < bestDistance {
			best = candidate
			bestDistance = distance
		}
	}
	return best
}

func knownCLICommands() []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 24)
	for _, group := range cliCommandGroups {
		for _, raw := range group.Commands {
			command := strings.Fields(raw)
			if len(command) == 0 {
				continue
			}
			name := strings.TrimSpace(command[0])
			if name == "aphelion" && len(command) > 1 {
				name = strings.TrimSpace(command[1])
			}
			name = strings.TrimLeft(name, "-")
			if name == "" || strings.Contains(name, "<") {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	return out
}

func editDistance(a string, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return len(b)
	}
	if b == "" {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			cur[j] = minInt(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

func minInt(values ...int) int {
	if len(values) == 0 {
		return 0
	}
	out := values[0]
	for _, value := range values[1:] {
		if value < out {
			out = value
		}
	}
	return out
}
