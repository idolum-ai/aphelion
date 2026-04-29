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
	Approved bool
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
		Kind:          "destructive_mutation",
		Summary:       "Perform a destructive change",
		WhyNow:        "This command deletes existing local state.",
		BoundedEffect: "The system will irreversibly remove targeted files or directories and continue from the changed state.",
	}},
	{re: regexp.MustCompile(`\brm\s+--recursive\b`), reason: "recursive delete", proposal: session.OperationProposal{
		Kind:          "destructive_mutation",
		Summary:       "Perform a destructive change",
		WhyNow:        "This command deletes existing local state.",
		BoundedEffect: "The system will irreversibly remove targeted files or directories and continue from the changed state.",
	}},
	{re: regexp.MustCompile(`\bmkfs\b`), reason: "format filesystem", proposal: session.OperationProposal{
		Kind:          "destructive_mutation",
		Summary:       "Perform a destructive change",
		WhyNow:        "This command reformats or destroys existing storage state.",
		BoundedEffect: "The system will irreversibly alter storage contents and continue from the changed state.",
	}},
	{re: regexp.MustCompile(`\bdd\s+.*if=`), reason: "disk copy", proposal: session.OperationProposal{
		Kind:          "destructive_mutation",
		Summary:       "Perform a destructive change",
		WhyNow:        "This command can overwrite low-level storage or copy disk images in a risky way.",
		BoundedEffect: "The system will irreversibly alter low-level storage state and continue from the changed state.",
	}},
	{re: regexp.MustCompile(`\bdrop\s+(table|database)\b`), reason: "sql drop", proposal: session.OperationProposal{
		Kind:          "destructive_mutation",
		Summary:       "Perform a destructive change",
		WhyNow:        "This command destroys existing database state.",
		BoundedEffect: "The system will irreversibly remove the targeted database objects and continue from the changed state.",
	}},
	{re: regexp.MustCompile(`\btruncate\s+(table\s+)?\w`), reason: "sql truncate", proposal: session.OperationProposal{
		Kind:          "destructive_mutation",
		Summary:       "Perform a destructive change",
		WhyNow:        "This command destroys existing database contents.",
		BoundedEffect: "The system will irreversibly remove the targeted database contents and continue from the changed state.",
	}},
	{re: regexp.MustCompile(`\bsystemctl\s+(stop|disable|mask)\b`), reason: "stop or disable system service", proposal: session.OperationProposal{
		Kind:          "destructive_mutation",
		Summary:       "Perform a destructive change",
		WhyNow:        "This command disables or interrupts an existing system service.",
		BoundedEffect: "The system will stop or disable the targeted service and continue from the changed state.",
	}},
	{re: regexp.MustCompile(`\bkill\s+-9\s+-1\b`), reason: "kill all processes", proposal: session.OperationProposal{
		Kind:          "destructive_mutation",
		Summary:       "Perform a destructive change",
		WhyNow:        "This command forcefully terminates running processes.",
		BoundedEffect: "The system will terminate running processes and continue from the changed state.",
	}},
	{re: regexp.MustCompile(`\bfind\b.*-delete\b`), reason: "bulk delete via find -delete", proposal: session.OperationProposal{
		Kind:          "destructive_mutation",
		Summary:       "Perform a destructive change",
		WhyNow:        "This command bulk-deletes existing filesystem state.",
		BoundedEffect: "The system will irreversibly remove the targeted files and continue from the changed state.",
	}},
	{re: regexp.MustCompile(`\b(curl|wget)\b.*\|\s*(ba)?sh\b`), reason: "pipe remote content to shell", proposal: session.OperationProposal{
		Kind:          "destructive_mutation",
		Summary:       "Run high-impact remote shell content",
		WhyNow:        "This command executes remote content directly in the shell.",
		BoundedEffect: "The system will execute remote shell content with local side effects and continue from the changed state.",
	}},
}

func proposalForCommand(command string) (session.OperationProposal, string) {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return session.OperationProposal{}, ""
	}
	if commandContainsGitCommit(lower) {
		return session.OperationProposal{
			Kind:          "repo_history_mutation",
			Summary:       "Create a local git commit",
			WhyNow:        "Saving this work as a commit gives us a clean review and rollback point before continuing.",
			BoundedEffect: "Create or amend one local git commit for the current operation. This approval will not push to any remote.",
		}, "repository commit"
	}
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
	for _, pattern := range destructiveMutationPatterns {
		if pattern.re.MatchString(lower) {
			return pattern.proposal, pattern.reason
		}
	}
	if strings.Contains(lower, "delete from") && !strings.Contains(lower, " where ") {
		return session.OperationProposal{
			Kind:          "destructive_mutation",
			Summary:       "Perform a destructive change",
			WhyNow:        "This command deletes database rows without a narrowing clause.",
			BoundedEffect: "The system will irreversibly remove database rows and continue from the changed state.",
		}, "sql delete without where"
	}
	return session.OperationProposal{}, ""
}

func commandContainsGitCommit(command string) bool {
	fields := shellishFields(command)
	for i, field := range fields {
		if trimShellishToken(field) == "git" && gitArgsContainCommit(fields[i+1:]) {
			return true
		}
	}
	return false
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

func shellishFields(command string) []string {
	replacer := strings.NewReplacer(
		"\n", " ",
		"\r", " ",
		"\t", " ",
		";", " ",
		"&&", " ",
		"||", " ",
		"(", " ",
		")", " ",
	)
	return strings.Fields(replacer.Replace(command))
}

func trimShellishToken(token string) string {
	return strings.Trim(token, `"'`)
}
