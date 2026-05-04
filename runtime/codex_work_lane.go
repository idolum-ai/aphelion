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
	return strings.TrimSpace(fmt.Sprintf(`You are Codex running as Aphelion's governed work executor.
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
		"Aphelion remains the authority layer. Do not widen scope without a fresh approval.",
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
	compact := strings.Join(strings.Fields(strings.TrimSpace(command)), " ")
	if compact == "" {
		return false
	}
	if req.Mode == WorkModeReadOnly {
		return codexAppServerCommandAllowed(compact) ||
			strings.HasPrefix(compact, "git status") ||
			strings.HasPrefix(compact, "git diff") ||
			strings.HasPrefix(compact, "rg ") ||
			strings.HasPrefix(compact, "go test")
	}
	lower := strings.ToLower(compact)
	if strings.Contains(lower, "git push") {
		return false
	}
	if strings.Contains(lower, "systemctl") && req.Mode != WorkModeDeploy {
		return false
	}
	if strings.Contains(lower, "git commit") && req.Mode != WorkModeCommit && req.Mode != WorkModeDeploy {
		return false
	}
	if strings.HasPrefix(lower, "rm -rf /") || strings.Contains(lower, " rm -rf /") {
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
	compact := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(command)), " "))
	if compact == "" {
		return false
	}
	markers := []string{
		"apply_patch",
		"git add",
		"git checkout",
		"git cherry-pick",
		"git clean",
		"git commit",
		"git merge",
		"git mv",
		"git push",
		"git rebase",
		"git reset",
		"git restore",
		"git revert",
		"git rm",
		"git switch",
		"go build",
		"go generate",
		"go install",
		"make install",
		"make restart",
		"make update",
		"npm install",
		"npm run build",
		"pnpm install",
		"systemctl",
		"yarn install",
	}
	for _, marker := range markers {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	return false
}
