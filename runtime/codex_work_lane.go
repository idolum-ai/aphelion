//go:build linux

package runtime

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

func codexWorkThreadStartParams(req WorkRequest) map[string]any {
	return map[string]any{
		"baseInstructions":      codexWorkBaseInstructions(req),
		"developerInstructions": codexWorkDeveloperInstructions(req),
		"approvalPolicy":        "on-request",
		"sandbox":               codexWorkSandbox(req.Mode),
		"serviceName":           "aphelion-work-lane",
		"cwd":                   codexWorkCWD(req),
	}
}

func codexWorkThreadResumeParams(req WorkRequest) map[string]any {
	return map[string]any{
		"approvalPolicy": "on-request",
		"sandbox":        codexWorkSandbox(req.Mode),
		"cwd":            codexWorkCWD(req),
	}
}

func codexWorkTurnStartParams(req WorkRequest) map[string]any {
	return map[string]any{
		"approvalPolicy": "on-request",
		"sandbox":        codexWorkSandbox(req.Mode),
		"cwd":            codexWorkCWD(req),
	}
}

func codexWorkBaseInstructions(req WorkRequest) string {
	return strings.TrimSpace(fmt.Sprintf(`You are Codex running as the governed work executor.
Stay inside the approved operation and lease.
Operation id: %s
Lease id: %s
Mode: %s
Report changed files, commands run, test results, and remaining risk.`, strings.TrimSpace(req.OperationID), strings.TrimSpace(req.LeaseID), strings.TrimSpace(string(req.Mode))))
}

func codexWorkDeveloperInstructions(req WorkRequest) string {
	state := session.NormalizeContinuationState(req.State)
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	lines := []string{
		"The runtime remains the authority layer. Do not widen scope without a fresh approval.",
		"Stop after the bounded action and report evidence.",
	}
	if effect := strings.TrimSpace(proposal.BoundedEffect); effect != "" {
		lines = append(lines, "Bounded effect: "+effect)
	}
	if len(proposal.AllowedActions) > 0 {
		lines = append(lines, "Allowed actions: "+strings.Join(proposal.AllowedActions, ", "))
	}
	if len(proposal.ForbiddenActions) > 0 {
		lines = append(lines, "Forbidden actions: "+strings.Join(proposal.ForbiddenActions, ", "))
	}
	return strings.Join(lines, "\n")
}

func codexWorkSandbox(mode WorkMode) string {
	switch mode {
	case WorkModeWorkspaceWrite, WorkModeCommit, WorkModeDeploy:
		return "workspace-write"
	default:
		return "read-only"
	}
}

func codexWorkCWD(req WorkRequest) string {
	if workdir := strings.TrimSpace(req.Workdir); workdir != "" {
		return workdir
	}
	if root := strings.TrimSpace(req.RepoRoot); root != "" {
		return root
	}
	return "/"
}

func codexWorkApprovalHandler(req WorkRequest) codexAppServerApprovalHandler {
	return func(method string, params map[string]any) codexAppServerApprovalDecision {
		decision := codexAppServerApprovalDecision{Method: method, Decision: "cancel"}
		switch method {
		case "item/commandExecution/requestApproval":
			decision.Command = stringField(params, "command")
			decision.Reason = stringField(params, "reason")
			if codexWorkCommandAllowed(req, decision.Command) {
				decision.Decision = "accept"
			} else {
				decision.Decision = "decline"
			}
		case "item/fileChange/requestApproval":
			decision.Reason = stringField(params, "reason")
			switch req.Mode {
			case WorkModeWorkspaceWrite, WorkModeCommit, WorkModeDeploy:
				decision.Decision = "accept"
			default:
				decision.Decision = "cancel"
			}
		default:
			decision.Decision = "cancel"
		}
		return decision
	}
}

func codexWorkCommandAllowed(req WorkRequest, command string) bool {
	compact := normalizeCodexCommand(command)
	if compact == "" {
		return false
	}
	effect := classifyCodexCommandEffect(compact)
	if req.Mode == WorkModeReadOnly {
		return effect.ReadOnlyAllowed()
	}
	if effect.Kind == codexCommandEffectRepoHistory && effect.Reason == "git push" {
		return false
	}
	if effect.Kind == codexCommandEffectService && req.Mode != WorkModeDeploy {
		return false
	}
	if effect.Kind == codexCommandEffectRepoHistory && effect.Reason == "git commit" && req.Mode != WorkModeCommit && req.Mode != WorkModeDeploy {
		return false
	}
	if effect.Kind == codexCommandEffectHighImpactStorage {
		return false
	}
	return commandWithinWorkRoot(req, compact)
}

func commandWithinWorkRoot(req WorkRequest, _ string) bool {
	root := strings.TrimSpace(req.RepoRoot)
	workdir := strings.TrimSpace(req.Workdir)
	if root == "" || workdir == "" {
		return true
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(workdir))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../"))
}

func codexApprovalLogHasSideEffects(log []codexAppServerApprovalDecision) bool {
	for _, decision := range log {
		if decision.Decision != "accept" {
			continue
		}
		if decision.Method == "item/fileChange/requestApproval" {
			return true
		}
		cmd := strings.ToLower(strings.TrimSpace(decision.Command))
		if cmd == "" {
			continue
		}
		if codexApprovedCommandHasSideEffects(cmd) {
			return true
		}
	}
	return false
}

func codexApprovedCommandHasSideEffects(command string) bool {
	effect := classifyCodexCommandEffect(command)
	return effect.SideEffects
}

type codexCommandEffectKind string

const (
	codexCommandEffectReadOnly          codexCommandEffectKind = "read_only_inspection"
	codexCommandEffectValidation        codexCommandEffectKind = "validation_execution"
	codexCommandEffectBuildArtifact     codexCommandEffectKind = "build_or_generated_artifact"
	codexCommandEffectWorkspaceMutation codexCommandEffectKind = "workspace_mutation"
	codexCommandEffectRepoHistory       codexCommandEffectKind = "repo_or_history_mutation"
	codexCommandEffectExternal          codexCommandEffectKind = "network_or_external_contact"
	codexCommandEffectService           codexCommandEffectKind = "process_or_service_change"
	codexCommandEffectCapability        codexCommandEffectKind = "capability_acquisition"
	codexCommandEffectCredential        codexCommandEffectKind = "credential_or_config_effect"
	codexCommandEffectDatabase          codexCommandEffectKind = "database_or_state_mutation"
	codexCommandEffectHighImpactStorage codexCommandEffectKind = "high_impact_storage"
	codexCommandEffectUnknown           codexCommandEffectKind = "unknown_or_unclassified"
)

type codexCommandEffect struct {
	Kind        codexCommandEffectKind
	Reason      string
	SideEffects bool
}

func (e codexCommandEffect) ReadOnlyAllowed() bool {
	return e.Kind == codexCommandEffectReadOnly && !e.SideEffects
}

func classifyCodexCommandEffect(command string) codexCommandEffect {
	compact := normalizeCodexCommand(command)
	if compact == "" {
		return codexCommandEffect{Kind: codexCommandEffectUnknown, Reason: "empty command", SideEffects: true}
	}
	if codexAppServerCommandAllowed(compact) {
		return codexCommandEffect{Kind: codexCommandEffectReadOnly, Reason: "status inspection"}
	}
	lower := strings.ToLower(compact)
	if codexCommandHasHighImpactStorageMarker(lower) {
		return codexCommandEffect{Kind: codexCommandEffectHighImpactStorage, Reason: "high-impact storage command", SideEffects: true}
	}
	if codexCommandHasRedirection(lower) {
		return codexCommandEffect{Kind: codexCommandEffectBuildArtifact, Reason: "shell redirection", SideEffects: true}
	}
	segments := codexCommandSegments(compact)
	if len(segments) == 0 {
		return codexCommandEffect{Kind: codexCommandEffectUnknown, Reason: "unclassified command", SideEffects: true}
	}
	out := codexCommandEffect{Kind: codexCommandEffectReadOnly, Reason: "read-only inspection"}
	for _, segment := range segments {
		effect := classifyCodexCommandSegment(segment)
		if effect.SideEffects || effect.Kind != codexCommandEffectReadOnly {
			return effect
		}
		out = effect
	}
	return out
}

func classifyCodexCommandSegment(segment string) codexCommandEffect {
	fields := strings.Fields(strings.TrimSpace(segment))
	if len(fields) == 0 {
		return codexCommandEffect{Kind: codexCommandEffectReadOnly, Reason: "empty segment"}
	}
	idx := codexCommandTokenIndex(fields)
	if idx < 0 || idx >= len(fields) {
		return codexCommandEffect{Kind: codexCommandEffectUnknown, Reason: "unclassified wrapper", SideEffects: true}
	}
	cmd := normalizeCodexCommandToken(fields[idx])
	args := fields[idx+1:]
	lowerSegment := strings.ToLower(strings.Join(fields[idx:], " "))
	switch cmd {
	case "git":
		return classifyCodexGitCommand(args)
	case "rg", "grep", "egrep", "fgrep", "cat", "nl", "head", "tail", "wc", "pwd", "ls", "find":
		if cmd == "find" && (codexArgsContain(args, "-delete") || codexArgsContain(args, "-exec")) {
			return codexCommandEffect{Kind: codexCommandEffectWorkspaceMutation, Reason: "find mutation", SideEffects: true}
		}
		return codexCommandEffect{Kind: codexCommandEffectReadOnly, Reason: cmd + " inspection"}
	case "sed":
		if codexArgsContainPrefix(args, "-i") {
			return codexCommandEffect{Kind: codexCommandEffectWorkspaceMutation, Reason: "sed in-place edit", SideEffects: true}
		}
		return codexCommandEffect{Kind: codexCommandEffectReadOnly, Reason: "sed inspection"}
	case "go":
		return classifyCodexGoCommand(args)
	case "npm", "pnpm", "yarn", "make", "pytest", "python", "python3", "uv", "pip", "pip3", "cargo", "playwright", "npx":
		return classifyCodexBuildTestPackageCommand(cmd, args)
	case "curl", "wget", "ssh", "scp", "rsync":
		return codexCommandEffect{Kind: codexCommandEffectExternal, Reason: cmd + " external contact", SideEffects: true}
	case "rm", "mv", "cp", "mkdir", "touch", "chmod", "chown", "ln", "tee", "apply_patch":
		return codexCommandEffect{Kind: codexCommandEffectWorkspaceMutation, Reason: cmd + " filesystem mutation", SideEffects: true}
	case "systemctl", "service", "launchctl", "kill", "pkill", "docker", "docker-compose", "kubectl":
		return codexCommandEffect{Kind: codexCommandEffectService, Reason: cmd + " process/service effect", SideEffects: true}
	case "ps", "df", "du", "uname", "hostname", "uptime", "date", "whoami", "id", "env", "printenv":
		return codexCommandEffect{Kind: codexCommandEffectReadOnly, Reason: cmd + " status inspection"}
	case "sqlite3", "psql", "mysql":
		if codexSQLLooksMutating(lowerSegment) {
			return codexCommandEffect{Kind: codexCommandEffectDatabase, Reason: "database mutation", SideEffects: true}
		}
		return codexCommandEffect{Kind: codexCommandEffectUnknown, Reason: cmd + " database command", SideEffects: true}
	case "gh", "aws", "gcloud", "az", "op":
		if codexCommandContainsAny(lowerSegment, " login", " logout", " auth ", " configure", " token", " secret") {
			return codexCommandEffect{Kind: codexCommandEffectCredential, Reason: cmd + " credential/config effect", SideEffects: true}
		}
		return codexCommandEffect{Kind: codexCommandEffectExternal, Reason: cmd + " external account command", SideEffects: true}
	default:
		if codexSQLLooksMutating(lowerSegment) {
			return codexCommandEffect{Kind: codexCommandEffectDatabase, Reason: "database mutation", SideEffects: true}
		}
		return codexCommandEffect{Kind: codexCommandEffectUnknown, Reason: cmd + " unclassified", SideEffects: true}
	}
}

func classifyCodexGitCommand(args []string) codexCommandEffect {
	subcmd := firstCodexGitSubcommand(args)
	switch subcmd {
	case "status", "diff", "log", "show", "grep", "rev-parse", "cat-file", "branch", "describe", "ls-files", "show-ref", "remote":
		return codexCommandEffect{Kind: codexCommandEffectReadOnly, Reason: "git " + subcmd + " inspection"}
	case "fetch", "pull", "clone", "ls-remote", "submodule":
		return codexCommandEffect{Kind: codexCommandEffectExternal, Reason: "git " + subcmd, SideEffects: true}
	case "commit":
		return codexCommandEffect{Kind: codexCommandEffectRepoHistory, Reason: "git commit", SideEffects: true}
	case "push":
		return codexCommandEffect{Kind: codexCommandEffectRepoHistory, Reason: "git push", SideEffects: true}
	case "add", "checkout", "cherry-pick", "clean", "merge", "mv", "rebase", "reset", "restore", "revert", "rm", "switch", "stash", "tag", "worktree":
		return codexCommandEffect{Kind: codexCommandEffectRepoHistory, Reason: "git " + subcmd, SideEffects: true}
	case "":
		return codexCommandEffect{Kind: codexCommandEffectUnknown, Reason: "git command without subcommand", SideEffects: true}
	default:
		return codexCommandEffect{Kind: codexCommandEffectUnknown, Reason: "git " + subcmd + " unclassified", SideEffects: true}
	}
}

func classifyCodexGoCommand(args []string) codexCommandEffect {
	if len(args) == 0 {
		return codexCommandEffect{Kind: codexCommandEffectUnknown, Reason: "go command without subcommand", SideEffects: true}
	}
	subcmd := strings.ToLower(strings.Trim(args[0], `"'`))
	switch subcmd {
	case "test", "vet":
		return codexCommandEffect{Kind: codexCommandEffectValidation, Reason: "go " + subcmd, SideEffects: true}
	case "build", "generate", "run":
		return codexCommandEffect{Kind: codexCommandEffectBuildArtifact, Reason: "go " + subcmd, SideEffects: true}
	case "install", "get", "mod", "work":
		return codexCommandEffect{Kind: codexCommandEffectCapability, Reason: "go " + subcmd, SideEffects: true}
	case "version", "env", "list":
		return codexCommandEffect{Kind: codexCommandEffectReadOnly, Reason: "go " + subcmd + " inspection"}
	default:
		return codexCommandEffect{Kind: codexCommandEffectUnknown, Reason: "go " + subcmd + " unclassified", SideEffects: true}
	}
}

func classifyCodexBuildTestPackageCommand(cmd string, args []string) codexCommandEffect {
	joined := strings.ToLower(strings.Join(args, " "))
	subcmd := ""
	if len(args) > 0 {
		subcmd = strings.ToLower(strings.Trim(args[0], `"'`))
	}
	switch cmd {
	case "pytest":
		return codexCommandEffect{Kind: codexCommandEffectValidation, Reason: "pytest", SideEffects: true}
	case "python", "python3":
		if strings.Contains(joined, "-m pip install") || strings.Contains(joined, "-m playwright install") {
			return codexCommandEffect{Kind: codexCommandEffectCapability, Reason: cmd + " package install", SideEffects: true}
		}
		if strings.Contains(joined, "pytest") || strings.Contains(joined, "unittest") {
			return codexCommandEffect{Kind: codexCommandEffectValidation, Reason: cmd + " test runner", SideEffects: true}
		}
	case "npm", "pnpm", "yarn":
		if subcmd == "test" || (subcmd == "run" && strings.Contains(joined, "test")) {
			return codexCommandEffect{Kind: codexCommandEffectValidation, Reason: cmd + " test", SideEffects: true}
		}
		if subcmd == "build" || (subcmd == "run" && strings.Contains(joined, "build")) {
			return codexCommandEffect{Kind: codexCommandEffectBuildArtifact, Reason: cmd + " build", SideEffects: true}
		}
		if subcmd == "install" || subcmd == "add" || subcmd == "ci" {
			return codexCommandEffect{Kind: codexCommandEffectCapability, Reason: cmd + " install", SideEffects: true}
		}
	case "make":
		if strings.Contains(joined, "test") || subcmd == "test" {
			return codexCommandEffect{Kind: codexCommandEffectValidation, Reason: "make test", SideEffects: true}
		}
		if strings.Contains(joined, "build") || subcmd == "build" {
			return codexCommandEffect{Kind: codexCommandEffectBuildArtifact, Reason: "make build", SideEffects: true}
		}
		if strings.Contains(joined, "install") || strings.Contains(joined, "update") || strings.Contains(joined, "restart") {
			return codexCommandEffect{Kind: codexCommandEffectCapability, Reason: "make capability/service target", SideEffects: true}
		}
	case "uv", "pip", "pip3", "cargo", "playwright", "npx":
		if strings.Contains(joined, "install") || subcmd == "install" || subcmd == "add" {
			return codexCommandEffect{Kind: codexCommandEffectCapability, Reason: cmd + " install", SideEffects: true}
		}
		if strings.Contains(joined, "test") || subcmd == "test" {
			return codexCommandEffect{Kind: codexCommandEffectValidation, Reason: cmd + " test", SideEffects: true}
		}
		if strings.Contains(joined, "build") || subcmd == "build" {
			return codexCommandEffect{Kind: codexCommandEffectBuildArtifact, Reason: cmd + " build", SideEffects: true}
		}
	}
	return codexCommandEffect{Kind: codexCommandEffectUnknown, Reason: cmd + " unclassified", SideEffects: true}
}

func normalizeCodexCommand(command string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(command)), " ")
}

func codexCommandSegments(command string) []string {
	command = strings.ReplaceAll(command, "&&", "\n")
	command = strings.ReplaceAll(command, "||", "\n")
	replacer := strings.NewReplacer(";", "\n", "|", "\n", "\r", "\n")
	command = replacer.Replace(command)
	parts := strings.Split(command, "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func codexCommandTokenIndex(fields []string) int {
	for i := 0; i < len(fields); i++ {
		token := normalizeCodexCommandToken(fields[i])
		if token == "" {
			continue
		}
		if strings.Contains(token, "=") && !strings.HasPrefix(token, "-") && strings.Index(token, "=") > 0 {
			continue
		}
		switch token {
		case "sudo", "command", "nohup", "time":
			continue
		case "env":
			continue
		case "timeout":
			if i+1 < len(fields) {
				i++
			}
			continue
		default:
			return i
		}
	}
	return -1
}

func normalizeCodexCommandToken(token string) string {
	token = strings.Trim(strings.TrimSpace(token), `"'`)
	if token == "" {
		return ""
	}
	base := filepath.Base(token)
	return strings.ToLower(strings.TrimSpace(base))
}

func firstCodexGitSubcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		token := strings.Trim(strings.TrimSpace(args[i]), `"'`)
		if token == "" || token == "--" {
			continue
		}
		if codexGitGlobalOptionConsumesValue(token) {
			i++
			continue
		}
		if codexGitGlobalOptionHasInlineValue(token) || strings.HasPrefix(token, "-") {
			continue
		}
		return strings.ToLower(token)
	}
	return ""
}

func codexGitGlobalOptionConsumesValue(token string) bool {
	switch token {
	case "-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path":
		return true
	default:
		return false
	}
}

func codexGitGlobalOptionHasInlineValue(token string) bool {
	return strings.HasPrefix(token, "-C") && len(token) > len("-C") ||
		strings.HasPrefix(token, "-c") && len(token) > len("-c") ||
		strings.HasPrefix(token, "--git-dir=") ||
		strings.HasPrefix(token, "--work-tree=") ||
		strings.HasPrefix(token, "--namespace=") ||
		strings.HasPrefix(token, "--exec-path=")
}

func codexArgsContain(args []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, arg := range args {
		if strings.ToLower(strings.Trim(arg, `"'`)) == want {
			return true
		}
	}
	return false
}

func codexArgsContainPrefix(args []string, prefix string) bool {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	for _, arg := range args {
		if strings.HasPrefix(strings.ToLower(strings.Trim(arg, `"'`)), prefix) {
			return true
		}
	}
	return false
}

func codexCommandHasRedirection(command string) bool {
	return strings.Contains(command, " >") || strings.Contains(command, "> ") || strings.Contains(command, ">>") || strings.Contains(command, " 2>") || strings.Contains(command, "1>")
}

func codexCommandHasHighImpactStorageMarker(command string) bool {
	return strings.Contains(command, "rm -rf /") ||
		strings.Contains(command, " rm -rf /") ||
		strings.Contains(command, "mkfs") ||
		strings.Contains(command, " dd ") ||
		strings.HasPrefix(command, "dd ")
}

func codexSQLLooksMutating(command string) bool {
	return strings.Contains(command, "drop table") ||
		strings.Contains(command, "drop database") ||
		strings.Contains(command, "truncate table") ||
		strings.Contains(command, "delete from") ||
		strings.Contains(command, "insert into") ||
		strings.Contains(command, "update ") ||
		strings.Contains(command, "alter table") ||
		strings.Contains(command, "create table") ||
		strings.Contains(command, "migrate")
}

func codexCommandContainsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
