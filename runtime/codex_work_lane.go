//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/effectauth"
	runtimecodex "github.com/idolum-ai/aphelion/runtime/codex"
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

func codexWorkApprovalHandler(req WorkRequest) runtimecodex.ApprovalHandler {
	return func(method string, params map[string]any) runtimecodex.ApprovalDecision {
		decision := runtimecodex.ApprovalDecision{Method: method, Decision: "cancel"}
		switch method {
		case "item/commandExecution/requestApproval":
			decision.Command = runtimecodex.StringField(params, "command")
			decision.Reason = runtimecodex.StringField(params, "reason")
			if codexWorkCommandAllowed(req, decision.Command) {
				decision.Decision = "accept"
			} else {
				decision.Decision = "decline"
			}
		case "item/fileChange/requestApproval":
			decision.Reason = runtimecodex.StringField(params, "reason")
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
	return effectauth.AuthorizeWorkModeCommand(effectauth.WorkModeRequest{
		State:    req.State,
		Mode:     effectauth.WorkMode(req.Mode),
		RepoRoot: req.RepoRoot,
		Workdir:  req.Workdir,
		Command:  command,
		Now:      time.Now().UTC(),
	}).Allowed
}

func codexApprovalLogHasSideEffects(log []runtimecodex.ApprovalDecision) bool {
	return runtimecodex.ApprovalLogHasSideEffects(log)
}

func codexApprovedCommandHasSideEffects(command string) bool {
	return runtimecodex.ApprovedCommandHasSideEffects(command)
}

func codexWorkRequest(req WorkRequest) runtimecodex.WorkRequest {
	return runtimecodex.WorkRequest{
		OperationID: req.OperationID,
		RepoRoot:    req.RepoRoot,
		Workdir:     req.Workdir,
		Prompt:      req.Prompt,
		Mode:        runtimecodex.WorkMode(req.Mode),
		LeaseID:     req.LeaseID,
		State:       req.State,
	}
}
