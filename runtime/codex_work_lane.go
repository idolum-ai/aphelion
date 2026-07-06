//go:build linux

package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/idolum-ai/aphelion/commandeffect"
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

func codexWorkApprovalHandler(req WorkRequest, runtimes ...*Runtime) runtimecodex.ApprovalHandler {
	var rt *Runtime
	if len(runtimes) > 0 {
		rt = runtimes[0]
	}
	ordinal := 0
	var ordinalMu sync.Mutex
	nextOrdinal := func() int {
		ordinalMu.Lock()
		defer ordinalMu.Unlock()
		ordinal++
		return ordinal
	}
	return func(method string, params map[string]any) runtimecodex.ApprovalDecision {
		decision := runtimecodex.ApprovalDecision{Method: method, Decision: "cancel"}
		switch method {
		case "item/commandExecution/requestApproval":
			decision.Command = runtimecodex.StringField(params, "command")
			decision.Reason = runtimecodex.StringField(params, "reason")
			auth := codexWorkCommandAuthority(req, decision.Command)
			if !auth.Allowed {
				decision.Decision = "decline"
				return decision
			}
			approvalOrdinal := nextOrdinal()
			if rt != nil {
				if err := rt.recordCodexCommandApprovalAttempt(req, decision.Command, auth, params, approvalOrdinal, time.Now().UTC()); err != nil {
					decision.Decision = "decline"
					decision.Reason = firstNonEmptyContinuation(decision.Reason, "effect attempt ledger write failed")
					return decision
				}
			}
			decision.Decision = "accept"
		case "item/fileChange/requestApproval":
			decision.Reason = runtimecodex.StringField(params, "reason")
			auth := codexWorkFileChangeAuthority(req, params)
			if !auth.Allowed {
				decision.Decision = "cancel"
				return decision
			}
			approvalOrdinal := nextOrdinal()
			if rt != nil {
				if err := rt.recordCodexFileChangeApprovalAttempt(req, params, auth, approvalOrdinal, time.Now().UTC()); err != nil {
					decision.Decision = "cancel"
					decision.Reason = firstNonEmptyContinuation(decision.Reason, "effect attempt ledger write failed")
					return decision
				}
			}
			decision.Decision = "accept"
		default:
			decision.Decision = "cancel"
		}
		return decision
	}
}

func codexWorkFileChangeAllowed(req WorkRequest, params map[string]any) bool {
	return codexWorkFileChangeAuthority(req, params).Allowed
}

func codexWorkFileChangeAuthority(req WorkRequest, params map[string]any) effectauth.Decision {
	switch req.Mode {
	case WorkModeWorkspaceWrite, WorkModeCommit, WorkModeDeploy:
	default:
		return effectauth.Decision{Allowed: false, Reason: "file_change_requires_write_mode"}
	}
	if !codexFileChangePathsWithinScope(req, params) {
		return effectauth.Decision{Allowed: false, Reason: "file_change_path_outside_scope"}
	}
	return effectauth.AuthorizeWorkModeCommand(effectauth.WorkModeRequest{
		State:    req.State,
		Mode:     effectauth.WorkMode(req.Mode),
		RepoRoot: req.RepoRoot,
		Workdir:  req.Workdir,
		Command:  "touch .aphelion-file-change",
		Now:      time.Now().UTC(),
	})
}

func codexFileChangePathsWithinScope(req WorkRequest, params map[string]any) bool {
	paths := codexFileChangePaths(params)
	if len(paths) == 0 {
		return false
	}
	root := firstNonEmptyContinuation(strings.TrimSpace(req.Workdir), strings.TrimSpace(req.RepoRoot))
	cleanRoot := filepath.Clean(root)
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			return false
		}
		absPath := path
		if filepath.IsAbs(path) {
			if root == "" {
				return false
			}
			rel, err := filepath.Rel(cleanRoot, filepath.Clean(path))
			if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
				return false
			}
		} else {
			clean := filepath.Clean(path)
			if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
				return false
			}
			if root != "" {
				absPath = filepath.Join(cleanRoot, clean)
			}
		}
		if root != "" {
			if !codexPathResolvedBeneathRoot(cleanRoot, absPath) {
				return false
			}
		}
	}
	return true
}

func codexPathResolvedBeneathRoot(cleanRoot string, absPath string) bool {
	cleanRoot = filepath.Clean(cleanRoot)
	cleanPath := filepath.Clean(absPath)
	rootResolved, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		if os.IsNotExist(err) {
			rel, relErr := filepath.Rel(cleanRoot, cleanPath)
			return relErr == nil && rel != ".." && !strings.HasPrefix(rel, "../")
		}
		return false
	}
	existing := cleanPath
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(existing)
		if err == nil {
			candidate := filepath.Clean(resolved)
			for i := len(suffix) - 1; i >= 0; i-- {
				candidate = filepath.Join(candidate, suffix[i])
			}
			rel, relErr := filepath.Rel(filepath.Clean(rootResolved), filepath.Clean(candidate))
			return relErr == nil && rel != ".." && !strings.HasPrefix(rel, "../")
		}
		if !os.IsNotExist(err) {
			return false
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return false
		}
		suffix = append(suffix, filepath.Base(existing))
		existing = parent
	}
}

func codexFileChangePaths(params map[string]any) []string {
	var out []string
	for _, key := range []string{"path", "file", "file_path", "relative_path"} {
		if value := runtimecodex.StringField(params, key); value != "" {
			out = append(out, value)
		}
	}
	for _, key := range []string{"paths", "files"} {
		switch value := params[key].(type) {
		case []string:
			out = append(out, value...)
		case []any:
			for _, item := range value {
				if text, ok := item.(string); ok {
					out = append(out, text)
				}
			}
		}
	}
	return out
}

func codexWorkCommandAllowed(req WorkRequest, command string) bool {
	return codexWorkCommandAuthority(req, command).Allowed
}

func codexWorkCommandAuthority(req WorkRequest, command string) effectauth.Decision {
	plan := commandeffect.PlanCommand(command)
	if plan.Dynamic {
		return effectauth.Decision{Allowed: false, Reason: "dynamic_effect_plan_unbounded", EffectKind: string(commandeffect.KindUnknown)}
	}
	if plan.MultipleAuthorities {
		return effectauth.Decision{Allowed: false, Reason: "effect_plan_requires_split", EffectKind: string(commandeffect.KindUnknown)}
	}
	for _, effect := range plan.Effects {
		if effect.SideEffects && effect.Kind == commandeffect.KindUnknown {
			return effectauth.Decision{Allowed: false, Reason: "unknown_effect_requires_split_or_typed_executor", EffectKind: string(effect.Kind)}
		}
	}
	return effectauth.AuthorizeWorkModeCommand(effectauth.WorkModeRequest{
		State:    req.State,
		Mode:     effectauth.WorkMode(req.Mode),
		RepoRoot: req.RepoRoot,
		Workdir:  req.Workdir,
		Command:  command,
		Now:      time.Now().UTC(),
	})
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
