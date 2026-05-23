//go:build linux

package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	return r.executeWithRoot(ctx, name, input, r.workspace)
}

func (r *Registry) SupportsParallelToolCall(name string, _ json.RawMessage) bool {
	switch strings.TrimSpace(name) {
	case "read_file", "list_dir", "search":
		return true
	default:
		return false
	}
}

func (r *Registry) ExecuteForPrincipal(ctx context.Context, p principal.Principal, name string, input json.RawMessage) (string, error) {
	return r.ExecuteForSessionPrincipal(ctx, p, session.SessionKey{}, name, input)
}

func (r *Registry) ExecuteForSessionPrincipal(ctx context.Context, p principal.Principal, key session.SessionKey, name string, input json.RawMessage) (string, error) {
	if r.sandbox == nil {
		return "", fmt.Errorf("principal-aware execution requires sandbox resolver")
	}

	scope, err := r.scopeForPrincipalToolExecution(p)
	if err != nil {
		return "", err
	}
	if err := ensureScopeReady(scope); err != nil {
		return "", err
	}
	if r.runner == nil {
		if !(r.durableAgentPrincipalFallback && p.Role == principal.RoleDurableAgent && name != "exec") {
			return "", fmt.Errorf("principal-aware execution requires sandbox runner")
		}
	} else if !r.runner.Supports(scope) {
		if !(r.durableAgentPrincipalFallback && p.Role == principal.RoleDurableAgent && name != "exec") {
			return "", fmt.Errorf("no supported sandbox backend for principal role %q", p.Role)
		}
	}
	return r.executeWithScopeAndPrincipal(ctx, name, input, scope, p, key)
}

func (r *Registry) executeWithRoot(ctx context.Context, name string, input json.RawMessage, root string) (string, error) {
	return r.executeWithScopeAndPrincipal(ctx, name, input, sandbox.Scope{
		WorkingRoot:      root,
		SharedMemoryRoot: root,
	}, principal.Principal{}, session.SessionKey{})
}

func (r *Registry) executeWithScopeAndPrincipal(ctx context.Context, name string, input json.RawMessage, scope sandbox.Scope, p principal.Principal, key session.SessionKey) (string, error) {
	authorityGrant, authorityManaged, err := r.requireAuthorityToolAccess(name, p, key, input)
	if err != nil {
		return "", err
	}

	switch name {
	case "exec":
		return r.exec(ctx, input, scope, p, key)
	case "read_file":
		return r.readFile(ctx, input, scope)
	case "write_file":
		return r.writeFile(ctx, input, scope)
	case "list_dir":
		return r.listDir(ctx, input, scope)
	case "search":
		return r.searchFiles(ctx, input, scope)
	case "fetch_url":
		return r.fetchURL(ctx, input, scope, p)
	case "memory":
		return r.memory(ctx, input, scope)
	case "session_search":
		return r.sessionSearch(ctx, input, p, key)
	case "update_operation":
		return r.updateOperation(ctx, input, key)
	case "request_approval":
		return r.requestApproval(ctx, input, key)
	case "operation_artifact":
		return r.operationArtifact(ctx, input, scope, key)
	case "update_plan":
		return r.updatePlan(ctx, input, key)
	case missionLedgerToolName:
		return r.missionLedger(ctx, input, p, key)
	case "tool_authority":
		return r.toolAuthority(ctx, input, p, key, scope)
	case "capability_request":
		return r.capabilityRequest(ctx, input, p, key)
	case "capability_authority":
		return r.capabilityAuthority(ctx, input, p, key)
	case "semantic_search":
		return r.semanticSearch(ctx, input, scope)
	case "openai_file":
		return r.openAIFile(ctx, input, scope, p)
	case "openai_vector_store":
		return r.openAIVectorStore(ctx, input, p)
	case "durable_agent":
		return r.durableAgent(ctx, input, p, key, scope)
	case codexImageGenerationToolName:
		return r.codexImageGeneration(ctx, input, scope, p, key)
	case webSearchToolName:
		return r.webSearch(ctx, input, scope, p, key)
	case remoteHostToolName:
		return r.remoteHost(ctx, input, p, key)
	default:
		if manifest, ok := r.externalManifestByName(name); ok {
			if r.externalExecutor != nil && r.externalExecutor.Supports(manifest) {
				if err := r.requireDurableAgentProcessSandbox(p, manifest, scope); err != nil {
					return "", err
				}
				if err := r.ensureExternalToolFresh(manifest, scope); err != nil {
					return "", err
				}
				access := ExternalToolExecutionAccess{}
				if authorityManaged {
					var err error
					access, err = externalToolExecutionAccessFromGrant(p, authorityGrant)
					if err != nil {
						return "", err
					}
				}
				return r.externalExecutor.Execute(ctx, manifest, input, scope, r.runner, r.maxOutputBytes, access)
			}
			if err := validateExternalProcessPolicy(manifest); err != nil {
				return "", err
			}
			return "", fmt.Errorf("external tool %q is present in the manifest but not yet executable in core (owner=%s)", manifest.Name, manifest.Owner)
		}
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func (r *Registry) exec(ctx context.Context, input json.RawMessage, scope sandbox.Scope, p principal.Principal, key session.SessionKey) (string, error) {
	var in execInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode exec input: %w", err)
	}
	if strings.TrimSpace(in.Command) == "" {
		return "", fmt.Errorf("exec command is required")
	}

	workdir, escaped, err := resolveWorkdirForExec(scope.WorkingRoot, in.Workdir)
	if err != nil {
		return "", err
	}
	if escaped {
		if p.Role != principal.RoleAdmin {
			return "", fmt.Errorf("workdir %q escapes workspace %q", in.Workdir, filepath.Clean(scope.WorkingRoot))
		}
		proposal := session.OperationProposal{
			Kind:          "workspace_escape",
			Summary:       "Run command outside the configured workspace",
			WhyNow:        "The requested command needs an explicit admin-approved working directory outside the current sandbox root.",
			BoundedEffect: fmt.Sprintf("The command will run in %s for this execution only.", workdir),
		}
		if r.execApprover == nil {
			return "", fmt.Errorf("command requires an approved proposal: workspace escape")
		}
		if err := r.persistExecProposalState(key, proposal, session.ProposalStatusPending); err != nil {
			return "", err
		}
		decision, err := r.execApprover.ConfirmExec(ctx, ExecApprovalRequest{
			Principal:  p,
			SessionKey: key,
			Scope:      scope,
			Command:    in.Command,
			Workdir:    workdir,
			Reason:     "workspace escape",
			Proposal:   proposal,
		})
		if err != nil {
			if persistErr := r.persistExecProposalState(key, proposal, session.ProposalStatusExpired); persistErr != nil {
				return "", persistErr
			}
			return "", err
		}
		if !decision.Approved {
			if err := r.persistExecProposalState(key, proposal, session.ProposalStatusDenied); err != nil {
				return "", err
			}
			return "", fmt.Errorf("proposal denied: workspace escape")
		}
		if err := r.persistExecProposalState(key, proposal, session.ProposalStatusApproved); err != nil {
			return "", err
		}
	}
	if proposal, reason := proposalForCommand(in.Command); reason != "" {
		if r.execApprover == nil {
			return "", fmt.Errorf("command requires an approved proposal: %s", reason)
		}
		if err := r.persistExecProposalState(key, proposal, session.ProposalStatusPending); err != nil {
			return "", err
		}
		decision, err := r.execApprover.ConfirmExec(ctx, ExecApprovalRequest{
			Principal:  p,
			SessionKey: key,
			Scope:      scope,
			Command:    in.Command,
			Workdir:    workdir,
			Reason:     reason,
			Proposal:   proposal,
		})
		if err != nil {
			if persistErr := r.persistExecProposalState(key, proposal, session.ProposalStatusExpired); persistErr != nil {
				return "", persistErr
			}
			return "", err
		}
		if !decision.Approved {
			if err := r.persistExecProposalState(key, proposal, session.ProposalStatusDenied); err != nil {
				return "", err
			}
			return "", fmt.Errorf("proposal denied: %s", reason)
		}
		if err := r.persistExecProposalState(key, proposal, session.ProposalStatusApproved); err != nil {
			return "", err
		}
	}

	timeout := r.timeout
	if in.TimeoutSec > 0 {
		timeout = time.Duration(in.TimeoutSec) * time.Second
	}
	timeout = defaultTimeout(timeout)

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stdout, stderr, err := r.runCommand(runCtx, scope, in.Command, workdir)
	out := renderOutput(stdout, stderr, r.maxOutputBytes)
	if err == nil {
		return out, nil
	}

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("command timed out after %s", timeout)
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return out, fmt.Errorf("command failed with exit code %d", exitErr.ExitCode())
	}

	return out, fmt.Errorf("run command: %w", err)
}

func (r *Registry) runCommand(ctx context.Context, scope sandbox.Scope, command string, workdir string) (string, string, error) {
	if r.runner != nil && strings.TrimSpace(string(scope.Principal.Role)) != "" {
		res, err := r.runner.Run(ctx, sandbox.ExecRequest{
			Scope:   scope,
			Command: command,
			Workdir: workdir,
		})
		return res.Stdout, res.Stderr, err
	}

	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Dir = workdir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func defaultTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 60 * time.Second
	}
	return timeout
}

func ensureScopeReady(scope sandbox.Scope) error {
	if err := os.MkdirAll(scope.WorkingRoot, 0o755); err != nil {
		return fmt.Errorf("prepare working root %q: %w", scope.WorkingRoot, err)
	}
	if strings.TrimSpace(scope.UserMemory) != "" {
		if err := os.MkdirAll(scope.UserMemory, 0o755); err != nil {
			return fmt.Errorf("prepare user memory root %q: %w", scope.UserMemory, err)
		}
	}
	return nil
}

func resolveWorkdir(root, raw string) (string, error) {
	workdir, _, err := resolveWorkdirForExec(root, raw)
	return workdir, err
}

func resolveWorkdirForExec(root, raw string) (string, bool, error) {
	base, err := filepath.Abs(root)
	if err != nil {
		return "", false, fmt.Errorf("resolve workspace root: %w", err)
	}

	target := base
	if strings.TrimSpace(raw) != "" {
		if filepath.IsAbs(raw) {
			target = filepath.Clean(raw)
		} else {
			target = filepath.Join(base, raw)
		}
	}

	target, err = filepath.Abs(target)
	if err != nil {
		return "", false, fmt.Errorf("resolve workdir: %w", err)
	}

	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", false, fmt.Errorf("check workdir: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return target, true, nil
	}

	return target, false, nil
}

func renderOutput(stdout, stderr string, limit int) string {
	parts := make([]string, 0, 2)
	if strings.TrimSpace(stdout) != "" {
		parts = append(parts, "stdout:\n"+truncate(stdout, limit))
	}
	if strings.TrimSpace(stderr) != "" {
		parts = append(parts, "stderr:\n"+truncate(stderr, limit))
	}
	if len(parts) == 0 {
		return "(no output)"
	}
	return strings.Join(parts, "\n\n")
}

func truncate(raw string, limit int) string {
	if len(raw) <= limit || limit <= 0 {
		return raw
	}
	if limit <= 64 {
		return raw[:limit]
	}
	head := limit / 2
	tail := limit / 2
	return raw[:head] + "\n...[truncated]...\n" + raw[len(raw)-tail:]
}
