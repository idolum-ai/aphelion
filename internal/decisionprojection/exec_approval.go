//go:build linux

package decisionprojection

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/session"
)

const (
	ExecCommandClassRepoRead       = "repo_read"
	ExecCommandClassRepoEdit       = "repo_edit"
	ExecCommandClassFocusedTests   = "focused_tests"
	ExecCommandClassGitCommit      = "git_commit"
	ExecCommandClassRemoteMutation = "remote_mutation"
	ExecCommandClassDeployRestart  = "deploy_restart"
	ExecCommandClassStateMutation  = "state_mutation"
	ExecCommandClassUnknown        = "unknown"
)

var commandSplitPattern = regexp.MustCompile(`\s*(\|\||&&|[;\|\n\r])\s*`)

func FormatExecApprovalDetails(proposal session.OperationProposal, reason string, command string, workdir string) string {
	command = strings.TrimSpace(command)
	workdir = strings.TrimSpace(workdir)
	commandClass := ExecCommandClass(command)
	intent := execApprovalIntent(proposal.Kind, commandClass, proposal.Summary)

	lines := make([]string, 0, 14)
	if summary := strings.TrimSpace(proposal.Summary); summary != "" {
		lines = append(lines, summary)
	}
	if kind := strings.TrimSpace(proposal.Kind); kind != "" {
		lines = append(lines, "Kind: "+kind)
	}
	if intent != "" {
		lines = append(lines, "", "Intent:", intent)
	}
	if commandClass != "" {
		lines = append(lines, "", "Command class:", commandClass)
	}
	if workdir != "" {
		lines = append(lines, "", "Workdir:", workdir)
	}
	if whyNow := strings.TrimSpace(proposal.WhyNow); whyNow != "" {
		lines = append(lines, "", "Why now:", whyNow)
	}
	if bounded := strings.TrimSpace(proposal.BoundedEffect); bounded != "" {
		lines = append(lines, "", "If approved:", bounded)
	}
	if reason := strings.TrimSpace(reason); reason != "" {
		lines = append(lines, "", "Trigger:", reason)
	}
	if command != "" {
		lines = append(lines, "", "Command:", command)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func ExecCommandClass(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	lower := strings.ToLower(command)
	switch {
	case containsAny(lower, "curl ", "wget ") && strings.Contains(lower, "|") && containsAny(lower, " sh", " bash"):
		return ExecCommandClassStateMutation
	case containsAny(lower, "git push", "gh pr merge", "gh release", "npm publish"):
		return ExecCommandClassRemoteMutation
	case containsAny(lower, "systemctl restart", "systemctl --user restart", " park-restart", " verify-deploy"):
		return ExecCommandClassDeployRestart
	case containsAny(lower, "make install-user-service", "make restart-user-service", "make install-release", "make update-release"):
		return ExecCommandClassDeployRestart
	case containsAny(lower, "rm -rf", "rm -fr", "drop table", "drop database", "truncate table", "delete from"):
		return ExecCommandClassStateMutation
	case strings.Contains(lower, "git commit"):
		return ExecCommandClassGitCommit
	case containsAny(lower, "go test", "make test", "make architecture", "make design-principles", "make taste"):
		return ExecCommandClassFocusedTests
	case containsAny(lower, "gofmt ", "go fmt", "go mod tidy", "git add", "apply_patch"):
		return ExecCommandClassRepoEdit
	}

	switch effect := commandeffect.Classify(command); effect.Kind {
	case commandeffect.KindReadOnlyInspection:
		if effect.ReadOnlyAllowed() {
			return ExecCommandClassRepoRead
		}
	case commandeffect.KindValidation:
		return ExecCommandClassFocusedTests
	case commandeffect.KindWorkspaceMutation:
		return ExecCommandClassRepoEdit
	case commandeffect.KindRepoHistory:
		if effect.Reason == commandeffect.ReasonGitCommit {
			return ExecCommandClassGitCommit
		}
		if effect.Reason == commandeffect.ReasonGitPush {
			return ExecCommandClassRemoteMutation
		}
		return ExecCommandClassStateMutation
	case commandeffect.KindExternal, commandeffect.KindExternalAccount, commandeffect.KindRemoteHost, commandeffect.KindCredential:
		return ExecCommandClassRemoteMutation
	case commandeffect.KindService:
		return ExecCommandClassDeployRestart
	case commandeffect.KindDatabase, commandeffect.KindHighImpactStorage:
		return ExecCommandClassStateMutation
	}

	segments := commandSegments(command)
	if len(segments) == 0 {
		return ExecCommandClassUnknown
	}
	allReadOnly := true
	for _, segment := range segments {
		if !segmentLooksReadOnly(segment) {
			allReadOnly = false
			break
		}
	}
	if allReadOnly {
		return ExecCommandClassRepoRead
	}
	return ExecCommandClassUnknown
}

func execApprovalIntent(kind string, commandClass string, fallback string) string {
	kind = strings.TrimSpace(kind)
	commandClass = strings.TrimSpace(commandClass)
	if kind != "workspace_escape" {
		return ""
	}
	switch commandClass {
	case ExecCommandClassRepoRead:
		return "Read repository files outside the configured workspace."
	case ExecCommandClassFocusedTests:
		return "Run focused tests outside the configured workspace."
	case ExecCommandClassRepoEdit:
		return "Edit repository files outside the configured workspace."
	case ExecCommandClassGitCommit:
		return "Create a local repository commit outside the configured workspace."
	case ExecCommandClassRemoteMutation:
		return "Run a remote repository mutation outside the configured workspace."
	case ExecCommandClassDeployRestart:
		return "Run a deploy or service restart command outside the configured workspace."
	case ExecCommandClassStateMutation:
		return "Run a local state mutation outside the configured workspace."
	}
	return strings.TrimSpace(fallback)
}

func commandSegments(command string) []string {
	parts := commandSplitPattern.Split(strings.TrimSpace(command), -1)
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			segments = append(segments, part)
		}
	}
	return segments
}

func segmentLooksReadOnly(segment string) bool {
	fields := strings.Fields(strings.TrimSpace(segment))
	if len(fields) == 0 {
		return false
	}
	cmd := normalizeCommandName(fields[0])
	args := fields[1:]
	switch cmd {
	case "rg", "grep", "egrep", "fgrep", "cat", "nl", "head", "tail", "less", "more", "wc", "pwd", "ls", "find":
		return !containsAny(strings.ToLower(strings.Join(args, " ")), " -delete", " -exec", " -ok")
	case "sed":
		for _, arg := range args {
			if arg == "-i" || strings.HasPrefix(arg, "-i") {
				return false
			}
		}
		return true
	case "sqlite3":
		joined := strings.ToLower(strings.Join(args, " "))
		joined = strings.NewReplacer(`"`, " ", `'`, " ").Replace(joined)
		return !containsAny(joined, " insert ", " update ", " delete ", " alter ", " drop ", " vacuum", " reindex", " create ")
	case "git":
		if len(args) == 0 {
			return false
		}
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

func firstGitSubcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		token := strings.Trim(strings.TrimSpace(args[i]), `"'`)
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
		return normalizeCommandName(token)
	}
	return ""
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

func normalizeCommandName(command string) string {
	command = strings.Trim(strings.TrimSpace(command), `"'`)
	if command == "" {
		return ""
	}
	return strings.ToLower(filepath.Base(command))
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
