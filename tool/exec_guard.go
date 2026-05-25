//go:build linux

package tool

import (
	"context"
	"regexp"
	"strings"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type ExecApprover interface {
	ConfirmExec(ctx context.Context, req ExecApprovalRequest) (ExecApprovalDecision, error)
}

type ExecApprovalRequest struct {
	Principal  principal.Principal
	SessionKey session.SessionKey
	Scope      sandbox.Scope
	Command    string
	Workdir    string
	Reason     string
	Proposal   session.OperationProposal
}

type ExecApprovalDecision struct {
	Approved             bool
	DecisionID           string
	Choice               string
	TimedOut             bool
	DefaultChoice        string
	RequiredApprovalKind string
}

type execProposalPattern struct {
	re       *regexp.Regexp
	proposal session.OperationProposal
	reason   string
}

var capabilityAcquisitionPatterns = []execProposalPattern{
	{re: regexp.MustCompile(`\bpython(?:3)?\s+-m\s+pip\s+install\b`), reason: "dependency installation", proposal: session.OperationProposal{
		Kind:          "capability_acquisition",
		Summary:       "Acquire or change local tooling",
		WhyNow:        "This command installs or updates dependencies or tooling needed for the current operation.",
		BoundedEffect: "The system will install or update local dependencies in the workspace and continue the operation using them.",
	}},
	{re: regexp.MustCompile(`\b(pip|pip3|uv)\s+install\b`), reason: "dependency installation", proposal: session.OperationProposal{
		Kind:          "capability_acquisition",
		Summary:       "Acquire or change local tooling",
		WhyNow:        "This command installs or updates dependencies or tooling needed for the current operation.",
		BoundedEffect: "The system will install or update local dependencies in the workspace and continue the operation using them.",
	}},
	{re: regexp.MustCompile(`\b(npm\s+(install|add)|pnpm\s+add|yarn\s+add|playwright\s+install|npx\s+playwright)\b`), reason: "dependency installation", proposal: session.OperationProposal{
		Kind:          "capability_acquisition",
		Summary:       "Acquire or change local tooling",
		WhyNow:        "This command installs or updates dependencies or tooling needed for the current operation.",
		BoundedEffect: "The system will install or update local dependencies in the workspace and continue the operation using them.",
	}},
	{re: regexp.MustCompile(`\b(apt(-get)?\s+install|brew\s+install|go\s+install|cargo\s+install)\b`), reason: "dependency installation", proposal: session.OperationProposal{
		Kind:          "capability_acquisition",
		Summary:       "Acquire or change local tooling",
		WhyNow:        "This command installs or updates dependencies or tooling needed for the current operation.",
		BoundedEffect: "The system will install or update local dependencies in the workspace and continue the operation using them.",
	}},
}

var externalOperationPatterns = []execProposalPattern{
	{re: regexp.MustCompile(`\b(curl|wget)\b`), reason: "external network operation", proposal: session.OperationProposal{
		Kind:          "external_operation",
		Summary:       "Use external network access",
		WhyNow:        "This command reaches outside the local workspace to browse, download, or query a remote system.",
		BoundedEffect: "The system will contact an external service or site and continue the operation using the fetched result.",
	}},
	{re: regexp.MustCompile(`\bgit\s+clone\s+https?://`), reason: "external network operation", proposal: session.OperationProposal{
		Kind:          "external_operation",
		Summary:       "Use external network access",
		WhyNow:        "This command reaches outside the local workspace to browse, download, or query a remote system.",
		BoundedEffect: "The system will contact an external service or site and continue the operation using the fetched result.",
	}},
	{re: regexp.MustCompile(`\b(playwright|chromium|google-chrome|firefox)\b.*https?://`), reason: "external browsing operation", proposal: session.OperationProposal{
		Kind:          "external_operation",
		Summary:       "Use external network access",
		WhyNow:        "This command drives a browser or fetch flow against an external site.",
		BoundedEffect: "The system will visit an external site, gather the requested result, and continue the operation with that material.",
	}},
}

var destructiveMutationPatterns = []execProposalPattern{
	{re: regexp.MustCompile(`\brm\s+-[^\n\r\s]*r`), reason: "recursive delete", proposal: session.OperationProposal{
		Kind:          "possible_delete_command",
		Summary:       "Approve command with possible delete pattern",
		WhyNow:        "This command text matched a pattern that may delete local state.",
		BoundedEffect: "Approving allows this command once. It does not approve unrelated edits, deploys, restarts, or account actions.",
	}},
	{re: regexp.MustCompile(`\brm\s+--recursive\b`), reason: "recursive delete", proposal: session.OperationProposal{
		Kind:          "possible_delete_command",
		Summary:       "Approve command with possible delete pattern",
		WhyNow:        "This command text matched a pattern that may delete local state.",
		BoundedEffect: "Approving allows this command once. It does not approve unrelated edits, deploys, restarts, or account actions.",
	}},
	{re: regexp.MustCompile(`\bmkfs\b`), reason: "format filesystem", proposal: session.OperationProposal{
		Kind:          "high_impact_storage_command",
		Summary:       "Approve high-impact storage command",
		WhyNow:        "This command reformats or destroys existing storage state.",
		BoundedEffect: "Approving allows this command once. It does not approve unrelated edits, deploys, restarts, or account actions.",
	}},
	{re: regexp.MustCompile(`\bdd\s+.*if=`), reason: "disk copy", proposal: session.OperationProposal{
		Kind:          "high_impact_storage_command",
		Summary:       "Approve high-impact storage command",
		WhyNow:        "This command can overwrite low-level storage or copy disk images in a risky way.",
		BoundedEffect: "Approving allows this command once. It does not approve unrelated edits, deploys, restarts, or account actions.",
	}},
	{re: regexp.MustCompile(`\bdrop\s+(table|database)\b`), reason: "sql drop", proposal: session.OperationProposal{
		Kind:          "possible_database_delete_command",
		Summary:       "Approve command with possible database delete pattern",
		WhyNow:        "This command destroys existing database state.",
		BoundedEffect: "Approving allows this command once. It does not approve unrelated edits, deploys, restarts, or account actions.",
	}},
	{re: regexp.MustCompile(`\btruncate\s+(table\s+)?\w`), reason: "sql truncate", proposal: session.OperationProposal{
		Kind:          "possible_database_delete_command",
		Summary:       "Approve command with possible database delete pattern",
		WhyNow:        "This command destroys existing database contents.",
		BoundedEffect: "Approving allows this command once. It does not approve unrelated edits, deploys, restarts, or account actions.",
	}},
	{re: regexp.MustCompile(`\bsystemctl\s+(stop|disable|mask)\b`), reason: "stop or disable system service", proposal: session.OperationProposal{
		Kind:          "service_interruption_command",
		Summary:       "Approve command that may interrupt a service",
		WhyNow:        "This command disables or interrupts an existing system service.",
		BoundedEffect: "Approving allows this command once. It does not approve unrelated edits, deploys, restarts, or account actions.",
	}},
	{re: regexp.MustCompile(`\bkill\s+-9\s+-1\b`), reason: "kill all processes", proposal: session.OperationProposal{
		Kind:          "process_interruption_command",
		Summary:       "Approve command that may interrupt processes",
		WhyNow:        "This command forcefully terminates running processes.",
		BoundedEffect: "Approving allows this command once. It does not approve unrelated edits, deploys, restarts, or account actions.",
	}},
	{re: regexp.MustCompile(`\bfind\b.*-delete\b`), reason: "bulk delete via find -delete", proposal: session.OperationProposal{
		Kind:          "possible_delete_command",
		Summary:       "Approve command with possible delete pattern",
		WhyNow:        "This command bulk-deletes existing filesystem state.",
		BoundedEffect: "Approving allows this command once. It does not approve unrelated edits, deploys, restarts, or account actions.",
	}},
	{re: regexp.MustCompile(`\b(curl|wget)\b.*\|\s*(ba)?sh\b`), reason: "pipe remote content to shell", proposal: session.OperationProposal{
		Kind:          "remote_shell_execution",
		Summary:       "Run high-impact remote shell content",
		WhyNow:        "This command executes remote content directly in the shell.",
		BoundedEffect: "Approving allows this command once. It does not approve unrelated edits, deploys, restarts, or account actions.",
	}},
}

func proposalForCommand(command string) (session.OperationProposal, string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return session.OperationProposal{}, ""
	}
	unquotedLower := strings.ToLower(unquotedShellContent(command))
	for _, pattern := range destructiveMutationPatterns {
		if pattern.reason == "pipe remote content to shell" && pattern.re.MatchString(unquotedLower) {
			return pattern.proposal, pattern.reason
		}
	}
	for _, segment := range shellishCommandSegments(command) {
		if proposal, reason := proposalForCommandSegment(segment); reason != "" {
			return proposal, reason
		}
	}
	return session.OperationProposal{}, ""
}

func proposalForCommandSegment(segment string) (session.OperationProposal, string) {
	tokens := shellishTokens(segment)
	if len(tokens) == 0 {
		return session.OperationProposal{}, ""
	}
	cmdIdx := shellishCommandTokenIndex(tokens)
	if cmdIdx < 0 || cmdIdx >= len(tokens) {
		return session.OperationProposal{}, ""
	}
	cmd := normalizeShellishCommandToken(tokens[cmdIdx].Text)
	if cmd == "" {
		return session.OperationProposal{}, ""
	}
	if cmd == "sh" || cmd == "bash" {
		if script := shellCommandStringArg(tokens[cmdIdx+1:]); script != "" {
			return proposalForCommand(script)
		}
	}
	if readOnlyInspectionCommand(cmd, tokens[cmdIdx+1:]) {
		return session.OperationProposal{}, ""
	}
	if cmd == "git" && gitArgsContainCommit(shellTokenTexts(tokens[cmdIdx+1:])) {
		return session.OperationProposal{
			Kind:          "repo_history_mutation",
			Summary:       "Create a local git commit",
			WhyNow:        "Saving this work as a commit gives us a clean review and rollback point before continuing.",
			BoundedEffect: "Create or amend one local git commit for the current operation. This approval will not push to any remote.",
		}, "repository commit"
	}
	lower := strings.ToLower(strings.TrimSpace(unquotedShellContent(segment)))
	for _, pattern := range capabilityAcquisitionPatterns {
		if pattern.re.MatchString(lower) {
			return pattern.proposal, pattern.reason
		}
	}
	for _, pattern := range externalOperationPatterns {
		if pattern.re.MatchString(lower) {
			return pattern.proposal, pattern.reason
		}
	}
	if cmd == "rm" && shellTokensContainRecursiveFlag(tokens[cmdIdx+1:]) {
		return destructiveMutationPatterns[0].proposal, destructiveMutationPatterns[0].reason
	}
	if cmd == "find" && shellTokensContain(tokens[cmdIdx+1:], "-delete") {
		return destructiveMutationPatterns[8].proposal, destructiveMutationPatterns[8].reason
	}
	if cmd == "systemctl" && shellTokensContainAny(tokens[cmdIdx+1:], "stop", "disable", "mask") {
		return destructiveMutationPatterns[6].proposal, destructiveMutationPatterns[6].reason
	}
	for _, pattern := range destructiveMutationPatterns {
		if pattern.reason == "pipe remote content to shell" {
			continue
		}
		if pattern.re.MatchString(lower) {
			return pattern.proposal, pattern.reason
		}
	}
	if strings.Contains(lower, "delete from") && !strings.Contains(lower, " where ") {
		return session.OperationProposal{
			Kind:          "possible_database_delete_command",
			Summary:       "Approve command with possible database delete pattern",
			WhyNow:        "This command deletes database rows without a narrowing clause.",
			BoundedEffect: "Approving allows this command once. It does not approve unrelated edits, deploys, restarts, or account actions.",
		}, "sql delete without where"
	}
	return session.OperationProposal{}, ""
}

func gitArgsContainCommit(args []string) bool {
	for i := 0; i < len(args); i++ {
		token := trimShellishToken(args[i])
		if token == "" || token == "--" {
			continue
		}
		if gitGlobalOptionConsumesValue(token) {
			i++
			continue
		}
		if gitGlobalOptionHasInlineValue(token) || strings.HasPrefix(token, "-") {
			continue
		}
		return token == "commit"
	}
	return false
}

func gitGlobalOptionConsumesValue(token string) bool {
	switch token {
	case "-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path":
		return true
	default:
		return false
	}
}

func gitGlobalOptionHasInlineValue(token string) bool {
	return strings.HasPrefix(token, "-C") && len(token) > len("-C") ||
		strings.HasPrefix(token, "-c") && len(token) > len("-c") ||
		strings.HasPrefix(token, "--git-dir=") ||
		strings.HasPrefix(token, "--work-tree=") ||
		strings.HasPrefix(token, "--namespace=") ||
		strings.HasPrefix(token, "--exec-path=")
}

func trimShellishToken(token string) string {
	return strings.Trim(token, `"'`)
}

type shellToken struct {
	Text   string
	Quoted bool
}

func shellishCommandSegments(command string) []string {
	var segments []string
	var b strings.Builder
	var quote rune
	escaped := false
	skipNext := false
	flush := func() {
		if segment := strings.TrimSpace(b.String()); segment != "" {
			segments = append(segments, segment)
		}
		b.Reset()
	}
	for i, r := range command {
		if skipNext {
			skipNext = false
			continue
		}
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			b.WriteRune(r)
			escaped = true
			continue
		}
		if quote != 0 {
			b.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			b.WriteRune(r)
		case '\n', '\r', ';', '|':
			flush()
			if r == '|' && i+1 < len(command) && command[i+1] == '|' {
				skipNext = true
				continue
			}
		case '&':
			if i+1 < len(command) && command[i+1] == '&' {
				flush()
				skipNext = true
				continue
			}
			b.WriteRune(r)
		case '(', ')':
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return segments
}

func shellishTokens(command string) []shellToken {
	var tokens []shellToken
	var b strings.Builder
	var quote rune
	escaped := false
	quoted := false
	flush := func() {
		if b.Len() == 0 {
			quoted = false
			return
		}
		tokens = append(tokens, shellToken{Text: b.String(), Quoted: quoted})
		b.Reset()
		quoted = false
	}
	for _, r := range command {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			quoted = true
		case ' ', '\t', '\n', '\r':
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return tokens
}

func shellishCommandTokenIndex(tokens []shellToken) int {
	for i := 0; i < len(tokens); i++ {
		token := strings.TrimSpace(tokens[i].Text)
		if token == "" {
			continue
		}
		if strings.Contains(token, "=") && !strings.HasPrefix(token, "-") && strings.Index(token, "=") > 0 {
			continue
		}
		switch normalizeShellishCommandToken(token) {
		case "sudo":
			i = skipSudoArgs(tokens, i)
			continue
		case "env":
			i = skipEnvArgs(tokens, i)
			continue
		case "time":
			i = skipOptionArgs(tokens, i)
			continue
		case "timeout":
			i = skipTimeoutArgs(tokens, i)
			continue
		case "command", "nohup":
			continue
		default:
			return i
		}
	}
	return -1
}

func skipSudoArgs(tokens []shellToken, idx int) int {
	for idx+1 < len(tokens) {
		next := strings.TrimSpace(tokens[idx+1].Text)
		if next == "" {
			idx++
			continue
		}
		if !strings.HasPrefix(next, "-") || next == "--" {
			break
		}
		idx++
		if shellOptionConsumesValue(next, "-C", "-D", "-g", "-h", "-p", "-T", "-t", "-U", "-u") && idx+1 < len(tokens) {
			idx++
		}
	}
	return idx
}

func skipEnvArgs(tokens []shellToken, idx int) int {
	for idx+1 < len(tokens) {
		next := strings.TrimSpace(tokens[idx+1].Text)
		if next == "" {
			idx++
			continue
		}
		if strings.Contains(next, "=") && !strings.HasPrefix(next, "-") && strings.Index(next, "=") > 0 {
			idx++
			continue
		}
		if !strings.HasPrefix(next, "-") || next == "--" {
			break
		}
		idx++
		if shellOptionConsumesValue(next, "-S", "-u") && idx+1 < len(tokens) {
			idx++
		}
	}
	return idx
}

func skipOptionArgs(tokens []shellToken, idx int) int {
	for idx+1 < len(tokens) {
		next := strings.TrimSpace(tokens[idx+1].Text)
		if next == "" {
			idx++
			continue
		}
		if !strings.HasPrefix(next, "-") || next == "--" {
			break
		}
		idx++
	}
	return idx
}

func skipTimeoutArgs(tokens []shellToken, idx int) int {
	idx = skipOptionArgs(tokens, idx)
	if idx+1 < len(tokens) {
		next := strings.TrimSpace(tokens[idx+1].Text)
		if next != "" && !strings.HasPrefix(next, "-") {
			idx++
		}
	}
	return idx
}

func shellOptionConsumesValue(token string, options ...string) bool {
	for _, option := range options {
		if token == option {
			return true
		}
	}
	return false
}

func shellCommandStringArg(tokens []shellToken) string {
	for i := 0; i < len(tokens); i++ {
		token := strings.TrimSpace(tokens[i].Text)
		if token == "" {
			continue
		}
		if token == "-c" && i+1 < len(tokens) {
			return strings.TrimSpace(tokens[i+1].Text)
		}
	}
	return ""
}

func readOnlyInspectionCommand(cmd string, args []shellToken) bool {
	switch cmd {
	case "rg", "grep", "egrep", "fgrep", "cat", "nl", "head", "tail", "less", "more", "wc", "pwd", "ls":
		return true
	case "sed":
		return !shellTokensContain(args, "-i") && !shellTokensContainPrefix(args, "-i")
	case "git":
		subcmd := firstGitSubcommand(args)
		switch subcmd {
		case "grep", "show", "diff", "log", "status", "rev-parse", "cat-file", "branch", "describe", "ls-files", "show-ref":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func firstGitSubcommand(args []shellToken) string {
	values := shellTokenTexts(args)
	for i := 0; i < len(values); i++ {
		token := trimShellishToken(values[i])
		if token == "" || token == "--" {
			continue
		}
		if gitGlobalOptionConsumesValue(token) {
			i++
			continue
		}
		if gitGlobalOptionHasInlineValue(token) || strings.HasPrefix(token, "-") {
			continue
		}
		return normalizeShellishCommandToken(token)
	}
	return ""
}

func shellTokensContainRecursiveFlag(tokens []shellToken) bool {
	for _, token := range tokens {
		value := strings.TrimSpace(token.Text)
		if value == "--recursive" {
			return true
		}
		if strings.HasPrefix(value, "-") && !strings.HasPrefix(value, "--") && strings.Contains(value, "r") {
			return true
		}
	}
	return false
}

func shellTokensContain(tokens []shellToken, want string) bool {
	want = strings.TrimSpace(want)
	for _, token := range tokens {
		if strings.TrimSpace(token.Text) == want {
			return true
		}
	}
	return false
}

func shellTokensContainPrefix(tokens []shellToken, want string) bool {
	want = strings.TrimSpace(want)
	for _, token := range tokens {
		if strings.HasPrefix(strings.TrimSpace(token.Text), want) {
			return true
		}
	}
	return false
}

func shellTokensContainAny(tokens []shellToken, wants ...string) bool {
	for _, want := range wants {
		if shellTokensContain(tokens, want) {
			return true
		}
	}
	return false
}

func shellTokenTexts(tokens []shellToken) []string {
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if text := strings.TrimSpace(token.Text); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func normalizeShellishCommandToken(token string) string {
	token = strings.TrimSpace(strings.Trim(token, `"'`))
	if idx := strings.LastIndex(token, "/"); idx >= 0 {
		token = token[idx+1:]
	}
	return strings.ToLower(token)
}

func unquotedShellContent(command string) string {
	var b strings.Builder
	var quote rune
	escaped := false
	for _, r := range command {
		if escaped {
			if quote == 0 {
				b.WriteRune(r)
			}
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			if quote == 0 {
				b.WriteRune(r)
			}
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
