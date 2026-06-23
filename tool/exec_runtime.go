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

	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/interpretation"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

var ErrExecRejectedBeforeDispatch = errors.New("exec rejected before dispatch")

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

func (r *Registry) executeWithScopeAndPrincipal(ctx context.Context, name string, input json.RawMessage, scope sandbox.Scope, p principal.Principal, key session.SessionKey) (out string, err error) {
	input, err = normalizeToolInput(input)
	if err != nil {
		return "", err
	}
	authorityGrant, authorityPermit, authorityManaged, err := r.requireAuthorityToolAccess(ctx, name, p, key, input)
	if err != nil {
		return "", err
	}

	switch name {
	case "exec":
		return r.exec(ctx, input, scope, p, key)
	case "read_file":
		return r.readFile(ctx, input, scope, p, key)
	case "write_file":
		return r.writeFile(ctx, input, scope, p, key)
	case "list_dir":
		return r.listDir(ctx, input, scope, p, key)
	case "search":
		return r.searchFiles(ctx, input, scope, p, key)
	case "fetch_url":
		return r.fetchURL(ctx, input, scope, p)
	case "memory":
		return r.memory(ctx, input, scope)
	case "session_search":
		return r.sessionSearch(ctx, input, p, key)
	case "evidence_hydrate":
		return r.evidenceHydrate(ctx, input, key)
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
				out, err := r.externalExecutor.Execute(ctx, manifest, input, scope, r.runner, r.maxOutputBytes, access)
				if authorityManaged {
					status := "completed"
					errText := ""
					if err != nil {
						status = "failed"
						errText = err.Error()
					}
					if recordErr := r.recordAuthorityManagedToolOutcome(authorityPermit, status, errText); recordErr != nil && err == nil {
						err = recordErr
					}
				}
				return out, err
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

	approvalGround := execQualificationGround{}
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
		if proposal.ID == "" {
			proposal.ID = generatedOperationID("exec-proposal")
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
			status := session.ProposalStatusDenied
			if decision.TimedOut {
				status = session.ProposalStatusExpired
			}
			if err := r.persistExecProposalState(key, proposal, status); err != nil {
				return "", err
			}
			return "", execApprovalDeniedError("workspace escape", decision)
		}
		if err := r.persistExecProposalState(key, proposal, session.ProposalStatusApproved); err != nil {
			return "", err
		}
		approvalGround = execQualificationGround{
			Kind:       "operator_approval",
			ProposalID: proposal.ID,
			DecisionID: decision.DecisionID,
			Choice:     decision.Choice,
		}
	}
	plan := commandeffect.PlanCommand(strings.TrimSpace(in.Command))
	var shellJudgment session.Judgment
	if err := r.validateContinuationExecAuthority(ctx, in.Command, plan); err != nil {
		return "", preDispatchExecError(err)
	}
	if err := validateExecEffectPlanDispatchable(plan); err != nil {
		if r != nil && r.store != nil && toolSessionKeyHasIdentity(key) {
			var recordErr error
			shellJudgment, plan, recordErr = r.recordShellEffectJudgment(ctx, key, in.Command)
			if recordErr != nil {
				return "", preDispatchExecError(fmt.Errorf("%w (and failed to record shell effect judgment: %v)", err, recordErr))
			}
		}
		return "", preDispatchExecError(r.recordRejectedShellAlternative(ctx, key, in.Command, in.Workdir, scope.WorkingRoot, plan, shellJudgment, err))
	}
	if proposal, reason := proposalForCommand(in.Command); reason != "" {
		if r.execApprover == nil {
			return "", preDispatchExecError(fmt.Errorf("command requires an approved proposal: %s", reason))
		}
		if proposal.ID == "" {
			proposal.ID = generatedOperationID("exec-proposal")
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
			return "", preDispatchExecError(err)
		}
		if !decision.Approved {
			status := session.ProposalStatusDenied
			if decision.TimedOut {
				status = session.ProposalStatusExpired
			}
			if err := r.persistExecProposalState(key, proposal, status); err != nil {
				return "", err
			}
			return "", preDispatchExecError(execApprovalDeniedError(reason, decision))
		}
		if err := r.persistExecProposalState(key, proposal, session.ProposalStatusApproved); err != nil {
			return "", err
		}
		approvalGround = execQualificationGround{
			Kind:       "operator_approval",
			ProposalID: proposal.ID,
			DecisionID: decision.DecisionID,
			Choice:     decision.Choice,
		}
	}
	shellJudgment, plan, err = r.recordShellEffectJudgment(ctx, key, in.Command)
	if err != nil {
		return "", preDispatchExecError(err)
	}
	if err := r.recordExecPreDispatchAttempt(ctx, p, key, "exec", in.Command, shellJudgment, plan, approvalGround); err != nil {
		return "", preDispatchExecError(err)
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

func (r *Registry) recordShellEffectJudgment(ctx context.Context, key session.SessionKey, command string) (session.Judgment, commandeffect.EffectPlan, error) {
	rawCommand := strings.TrimSpace(command)
	plan := commandeffect.PlanCommand(rawCommand)
	if rawCommand == "" {
		return session.Judgment{}, plan, nil
	}
	if r == nil || r.store == nil {
		if commandeffect.RepresentativeEffect(plan).SideEffects {
			return session.Judgment{}, plan, fmt.Errorf("interpretation store unavailable for side-effecting exec")
		}
		return session.Judgment{}, plan, nil
	}
	now := time.Now().UTC()
	invocationRef := execToolInvocationRef(ctx, now)
	commandHash := session.EffectAttemptCommandHash(rawCommand)
	resultJSON, err := json.Marshal(shellEffectPlanJudgmentResult(rawCommand, plan))
	if err != nil {
		return session.Judgment{}, plan, fmt.Errorf("encode shell effect judgment: %w", err)
	}
	judgment, err := r.interpretationService().RecordJudgment(session.JudgmentInput{
		Key:                key,
		TurnRunID:          invocationRef.TurnRunID,
		Kind:               session.JudgmentKindShellEffectPlan,
		SchemaVersion:      "v1",
		SubjectKey:         "exec:" + commandHash,
		ClaimKey:           "command_effect_plan",
		InterpreterID:      "commandeffect.plan_command",
		InterpreterVersion: "v1",
		InputRefs:          []string{session.JudgmentUseRef("command_hash", commandHash)},
		InputHash:          commandHash,
		ResultJSON:         string(resultJSON),
		Completeness:       shellEffectPlanCompleteness(plan),
		Unknowns:           shellEffectPlanUnknowns(plan),
		DependencyRefs: []session.JudgmentDependencyRef{
			{Kind: "command_hash", Ref: commandHash, Role: "subject"},
		},
		SourceFaultDomains: []string{"shell_text", "commandeffect_plan_v1"},
		Sensitivity:        "redacted_command_metadata",
		AsOf:               now,
		CreatedAt:          now,
	})
	if err != nil {
		return session.Judgment{}, plan, err
	}
	return judgment, plan, nil
}

type execQualificationGround struct {
	Kind       string
	ProposalID string
	DecisionID string
	Choice     string
	LeaseID    string
	ApprovedBy int64
}

func (r *Registry) recordExecPreDispatchAttempt(ctx context.Context, p principal.Principal, key session.SessionKey, toolName string, command string, shellJudgment session.Judgment, plan commandeffect.EffectPlan, approvalGround execQualificationGround) error {
	rawCommand := strings.TrimSpace(command)
	if rawCommand == "" {
		return nil
	}
	effect := commandeffect.RepresentativeEffect(plan)
	if !effect.SideEffects {
		return nil
	}
	if r == nil || r.store == nil {
		return fmt.Errorf("interpretation store unavailable for side-effecting exec")
	}
	safeCommand := session.RedactEvidenceText(commandeffect.NormalizeCommand(rawCommand)).Text
	boundaryKind := ""
	if boundary, ok := commandeffect.BoundaryForPlan(plan); ok {
		boundaryKind = string(boundary.Kind)
	}
	now := time.Now().UTC()
	invocationRef := execToolInvocationRef(ctx, now)
	attemptID := execPreDispatchAttemptID(key, strings.TrimSpace(toolName), safeCommand, invocationRef)
	attemptInput := session.EffectAttemptInput{
		AttemptID:    attemptID,
		Key:          key,
		TurnRunID:    invocationRef.TurnRunID,
		Executor:     "tool",
		Tool:         strings.TrimSpace(toolName),
		Command:      safeCommand,
		EffectKind:   string(effect.Kind),
		EffectReason: effect.Reason,
		BoundaryKind: boundaryKind,
		SubjectJSON:  session.RedactEvidenceText(execEffectAttemptSubjectJSON(rawCommand, shellJudgment.ID, shellJudgment.ContentHash)).Text,
		Status:       session.EffectAttemptStatusAttempted,
		EvidenceRefs: execEffectAttemptEvidenceRefs(shellJudgment),
		StartedAt:    now,
		UpdatedAt:    now,
	}
	irreversible := execEffectRequiresPreCommitQualification(effect.Kind, effect.Reason, boundaryKind)
	qualification, qualificationReason, qualificationDeps, err := r.qualifyExecJudgmentUse(ctx, p, key, rawCommand, plan, shellJudgment, irreversible, approvalGround)
	if err != nil {
		return err
	}
	useInput := session.JudgmentUseInput{
		Key:                  key,
		TurnRunID:            invocationRef.TurnRunID,
		ConsumerID:           session.ConsumerToolExecDispatch,
		Consequence:          session.JudgmentUseConsequenceExecution,
		JudgmentRefs:         execJudgmentRefs(rawCommand, effect.Kind, effect.Reason, shellJudgment),
		DependencyRefs:       append(execJudgmentDependencyRefs(rawCommand, effect.Kind, effect.Reason, boundaryKind, invocationRef, shellJudgment), qualificationDeps...),
		PolicyRef:            "exec_pre_dispatch_v1",
		ResultRef:            session.JudgmentUseRef("effect_attempt", attemptID),
		Irreversible:         irreversible,
		QualificationStatus:  qualification,
		ReconciliationStatus: session.JudgmentUseReconciliationNotRequired,
		Reason:               qualificationReason,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	_, _, err = r.interpretationService().RecordEffectAttemptWithUse(attemptInput, useInput)
	if err != nil {
		return fmt.Errorf("record exec judgment use and effect attempt before dispatch: %w", err)
	}
	return nil
}

func execToolInvocationRef(ctx context.Context, now time.Time) ToolInvocationRef {
	if ref, ok := ToolInvocationRefFromContext(ctx); ok {
		return ref
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return ToolInvocationRef{InvocationID: fmt.Sprintf("direct:%d", now.UnixNano())}
}

func execPreDispatchAttemptID(key session.SessionKey, toolName string, command string, ref ToolInvocationRef) string {
	toolKey := strings.Join([]string{"exec_pre_dispatch", strings.TrimSpace(toolName), strings.TrimSpace(ref.InvocationID)}, ":")
	return session.EffectAttemptID(session.SessionIDForKey(key), ref.TurnRunID, toolKey, command)
}

func shellEffectPlanJudgmentResult(command string, plan commandeffect.EffectPlan) map[string]any {
	effects := make([]map[string]any, 0, len(plan.Effects))
	for _, effect := range plan.Effects {
		effects = append(effects, map[string]any{
			"kind":           string(effect.Kind),
			"reason":         strings.TrimSpace(effect.Reason),
			"command":        session.RedactEvidenceText(effect.Command).Text,
			"git_subcommand": strings.TrimSpace(effect.GitSubcommand),
			"action":         strings.TrimSpace(effect.Action),
			"provider":       strings.TrimSpace(effect.Provider),
			"target":         session.RedactEvidenceText(effect.Target).Text,
			"subject":        session.RedactEvidenceText(effect.Subject).Text,
			"side_effects":   effect.SideEffects,
		})
	}
	return map[string]any{
		"normalized_command":  session.RedactEvidenceText(commandeffect.NormalizeCommand(command)).Text,
		"command_hash":        session.EffectAttemptCommandHash(command),
		"effects":             effects,
		"dynamic":             plan.Dynamic,
		"dynamic_reason":      strings.TrimSpace(plan.DynamicReason),
		"multiple_authority":  plan.MultipleAuthorities,
		"planner_contract_id": "commandeffect_plan_v1",
	}
}

func shellEffectPlanCompleteness(plan commandeffect.EffectPlan) session.JudgmentCompleteness {
	if plan.Dynamic || plan.MultipleAuthorities {
		return session.JudgmentCompletenessPartial
	}
	for _, effect := range plan.Effects {
		if effect.Kind == commandeffect.KindUnknown {
			return session.JudgmentCompletenessPartial
		}
	}
	return session.JudgmentCompletenessComplete
}

func shellEffectPlanUnknowns(plan commandeffect.EffectPlan) []session.UnknownPredicate {
	var unknowns []session.UnknownPredicate
	if plan.Dynamic {
		unknowns = append(unknowns, session.UnknownPredicate{Kind: "dynamic_shell", Target: plan.Command, Reason: plan.DynamicReason})
	}
	if plan.MultipleAuthorities {
		unknowns = append(unknowns, session.UnknownPredicate{Kind: "multiple_authorities", Target: plan.Command, Reason: "command must be split or represented as typed operation"})
	}
	for _, effect := range plan.Effects {
		if effect.Kind == commandeffect.KindUnknown {
			unknowns = append(unknowns, session.UnknownPredicate{Kind: "unknown_effect", Target: effect.Command, Reason: effect.Reason})
		}
	}
	return unknowns
}

func execEffectAttemptEvidenceRefs(judgment session.Judgment) []string {
	refs := []string{"exec_pre_dispatch"}
	if strings.TrimSpace(judgment.ID) != "" {
		refs = append(refs, session.JudgmentRef(judgment.ID))
	}
	return refs
}

func execEffectAttemptSubjectJSON(command string, judgmentID string, judgmentHash string) string {
	payload := map[string]any{
		"kind":         "exec_command",
		"command_hash": session.EffectAttemptCommandHash(command),
	}
	if strings.TrimSpace(judgmentID) != "" {
		payload["judgment_id"] = strings.TrimSpace(judgmentID)
	}
	if strings.TrimSpace(judgmentHash) != "" {
		payload["judgment_content_hash"] = strings.TrimSpace(judgmentHash)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func execJudgmentRefs(command string, kind commandeffect.Kind, reason string, judgment session.Judgment) []string {
	normalized := commandeffect.NormalizeCommand(command)
	refs := []string{
		session.JudgmentUseHashRef("effect_plan", strings.Join([]string{normalized, string(kind), strings.TrimSpace(reason)}, "\x00")),
		session.JudgmentUseHashRef("command_effect", strings.Join([]string{normalized, string(kind)}, "\x00")),
	}
	if strings.TrimSpace(judgment.ID) != "" {
		refs = append([]string{session.JudgmentRef(judgment.ID)}, refs...)
	}
	return refs
}

func execJudgmentDependencyRefs(command string, kind commandeffect.Kind, reason string, boundaryKind string, invocationRef ToolInvocationRef, judgment session.Judgment) []session.JudgmentDependencyRef {
	refs := []session.JudgmentDependencyRef{
		{Kind: "command_hash", Ref: session.EffectAttemptCommandHash(command), Role: "subject"},
		{Kind: "effect_kind", Ref: string(kind), Role: "qualifies"},
	}
	if strings.TrimSpace(judgment.ID) != "" {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "judgment", Ref: judgment.ID, Role: "qualifies", Hash: judgment.ContentHash})
	}
	if strings.TrimSpace(reason) != "" {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "effect_reason", Ref: strings.TrimSpace(reason), Role: "qualifies"})
	}
	if strings.TrimSpace(boundaryKind) != "" {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "boundary_kind", Ref: strings.TrimSpace(boundaryKind), Role: "qualifies"})
	}
	if invocationRef.TurnRunID > 0 {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "turn_run", Ref: fmt.Sprintf("%d", invocationRef.TurnRunID), Role: "scope"})
	}
	if strings.TrimSpace(invocationRef.InvocationID) != "" {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "tool_invocation", Ref: strings.TrimSpace(invocationRef.InvocationID), Role: "subject"})
	}
	return refs
}

func execEffectRequiresPreCommitQualification(kind commandeffect.Kind, reason string, boundaryKind string) bool {
	switch kind {
	case commandeffect.KindExternal, commandeffect.KindExternalAccount, commandeffect.KindRemoteHost, commandeffect.KindService,
		commandeffect.KindCapability, commandeffect.KindCredential, commandeffect.KindDatabase, commandeffect.KindHighImpactStorage:
		return true
	case commandeffect.KindRepoHistory:
		return strings.TrimSpace(reason) == commandeffect.ReasonGitPush || strings.TrimSpace(boundaryKind) == string(commandeffect.BoundaryGitPush)
	default:
		return false
	}
}

func (r *Registry) qualifyExecJudgmentUse(ctx context.Context, _ principal.Principal, _ session.SessionKey, command string, plan commandeffect.EffectPlan, shellJudgment session.Judgment, irreversible bool, approvalGround execQualificationGround) (session.JudgmentUseQualificationStatus, string, []session.JudgmentDependencyRef, error) {
	if !irreversible {
		return session.JudgmentUseQualificationQualified, "exec effect plan recorded before dispatch", nil, nil
	}
	challenged, err := r.execJudgmentGroundProfile(shellJudgment)
	if err != nil {
		return session.JudgmentUseQualificationBlocked, "irreversible exec qualification ground unavailable", nil, err
	}
	if strings.TrimSpace(approvalGround.ProposalID) != "" || strings.TrimSpace(approvalGround.DecisionID) != "" {
		approvalGround.Kind = "operator_approval"
		if strings.TrimSpace(approvalGround.ProposalID) == "" || strings.TrimSpace(approvalGround.DecisionID) == "" || strings.TrimSpace(approvalGround.Choice) != "approve" {
			refs := execQualificationGroundRefs(approvalGround, "blocks")
			return session.JudgmentUseQualificationBlocked, "irreversible exec approval ground is incomplete", refs, fmt.Errorf("irreversible exec approval ground requires approved operator decision")
		}
		support := execQualificationSupportProfile(approvalGround)
		qualification, qualifyErr := r.interpretationService().QualifyDecorrelatedUse(interpretation.DecorrelatedQualificationInput{
			Irreversible: true,
			Challenged:   challenged,
			Support:      support,
			Qualified:    "irreversible exec qualified by decorrelated operator approval",
			Blocked:      "irreversible exec operator approval ground is not decorrelated",
		})
		refs := execQualificationDecorrelatedRefs(approvalGround, shellJudgment, qualification.Decorrelated)
		return qualification.Status, qualification.Reason, refs, qualifyErr
	}
	if state, ok := ContinuationExecAuthorityFromContext(ctx); ok {
		decision := ContinuationExecAuthorityDecisionForPlan(state, command, plan, time.Now().UTC())
		if decision.Allowed {
			ground := execQualificationGround{
				Kind:       "continuation_authority",
				LeaseID:    strings.TrimSpace(state.ContinuationLease.ID),
				ProposalID: strings.TrimSpace(firstNonEmptyString(state.ActionProposal.ID, state.ActionProposal.OperationID, state.ContinuationLease.ProposalID)),
				ApprovedBy: firstNonZeroInt64(state.ContinuationLease.ApprovedBy, state.ApprovedBy),
			}
			if ground.LeaseID == "" || ground.ApprovedBy == 0 {
				refs := execQualificationGroundRefs(ground, "blocks")
				return session.JudgmentUseQualificationBlocked, "irreversible exec continuation authority lacks operator-approved support ref", refs, fmt.Errorf("irreversible exec continuation authority lacks operator-approved support ref")
			}
			support := execQualificationSupportProfile(ground)
			qualification, qualifyErr := r.interpretationService().QualifyDecorrelatedUse(interpretation.DecorrelatedQualificationInput{
				Irreversible: true,
				Challenged:   challenged,
				Support:      support,
				Qualified:    "irreversible exec qualified by decorrelated active continuation authority",
				Blocked:      "irreversible exec continuation authority ground is not decorrelated",
			})
			refs := execQualificationDecorrelatedRefs(ground, shellJudgment, qualification.Decorrelated)
			return qualification.Status, qualification.Reason, refs, qualifyErr
		}
	}
	return session.JudgmentUseQualificationBlocked, "irreversible exec lacks decorrelated qualification ground", nil, fmt.Errorf("irreversible exec lacks approved proposal or active continuation authority")
}

func (r *Registry) execJudgmentGroundProfile(judgment session.Judgment) (session.JudgmentGroundProfile, error) {
	if strings.TrimSpace(judgment.ID) != "" && r != nil && r.store != nil {
		return r.store.JudgmentGroundProfile(judgment.ID, 0)
	}
	return session.JudgmentGroundProfileForJudgment(judgment), nil
}

func execQualificationSupportProfile(ground execQualificationGround) session.JudgmentGroundProfile {
	ground = normalizeExecQualificationGround(ground)
	refs := execQualificationGroundRefs(ground, "qualifies")
	sourceDomains := []string{ground.Kind}
	switch ground.Kind {
	case "operator_approval":
		sourceDomains = append(sourceDomains, "operator_approval_event")
	case "continuation_authority":
		sourceDomains = append(sourceDomains, "operator_approved_continuation")
	}
	externalRef := ""
	if ground.DecisionID != "" {
		externalRef = session.JudgmentUseRef("operator_decision", ground.DecisionID)
	} else if ground.LeaseID != "" {
		externalRef = session.JudgmentUseRef("continuation_lease", ground.LeaseID)
	}
	return session.JudgmentGroundProfile{
		DependencyRefs:      refs,
		SourceFaultDomains:  sourceDomains,
		ExternalEvidenceRef: externalRef,
	}
}

func execQualificationDecorrelatedRefs(ground execQualificationGround, shellJudgment session.Judgment, decision session.JudgmentDecorrelatedGroundDecision) []session.JudgmentDependencyRef {
	ground = normalizeExecQualificationGround(ground)
	status := "not_decorrelated"
	if decision.Decorrelated {
		status = "decorrelated"
	}
	seed := strings.Join([]string{
		strings.TrimSpace(shellJudgment.ID),
		strings.TrimSpace(ground.Kind),
		strings.TrimSpace(ground.ProposalID),
		strings.TrimSpace(ground.DecisionID),
		strings.TrimSpace(ground.LeaseID),
		fmt.Sprint(ground.ApprovedBy),
		status,
		strings.TrimSpace(decision.Reason),
		strings.Join(decision.Shared, "|"),
	}, "\x00")
	refs := append(execQualificationGroundRefs(ground, "qualifies"),
		session.JudgmentDependencyRef{Kind: "decorrelation_decision", Ref: session.JudgmentUseHashRef("decorrelation", seed), Role: "qualifies", Scope: status},
	)
	for _, shared := range decision.Shared {
		if strings.TrimSpace(shared) != "" {
			refs = append(refs, session.JudgmentDependencyRef{Kind: "decorrelation_shared", Ref: strings.TrimSpace(shared), Role: "blocks"})
		}
	}
	return refs
}

func normalizeExecQualificationGround(ground execQualificationGround) execQualificationGround {
	ground.Kind = strings.TrimSpace(ground.Kind)
	ground.ProposalID = strings.TrimSpace(ground.ProposalID)
	ground.DecisionID = strings.TrimSpace(ground.DecisionID)
	ground.Choice = strings.TrimSpace(ground.Choice)
	ground.LeaseID = strings.TrimSpace(ground.LeaseID)
	return ground
}

func execQualificationGroundRefs(ground execQualificationGround, role string) []session.JudgmentDependencyRef {
	ground = normalizeExecQualificationGround(ground)
	role = strings.TrimSpace(role)
	var refs []session.JudgmentDependencyRef
	if ground.ProposalID != "" {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "operation_proposal", Ref: ground.ProposalID, Role: role})
	}
	if ground.DecisionID != "" {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "operator_decision", Ref: ground.DecisionID, Role: role, Scope: ground.Choice})
	}
	if ground.LeaseID != "" {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "continuation_lease", Ref: ground.LeaseID, Role: role})
	}
	if ground.ApprovedBy != 0 {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "operator_approver", Ref: fmt.Sprint(ground.ApprovedBy), Role: role})
	}
	return refs
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func validateExecEffectPlanDispatchable(plan commandeffect.EffectPlan) error {
	if plan.Dynamic {
		return fmt.Errorf("dynamic shell command is not dispatchable through raw exec: %s", strings.TrimSpace(plan.DynamicReason))
	}
	if plan.MultipleAuthorities {
		return fmt.Errorf("multi-effect shell command must be split before execution")
	}
	for _, effect := range plan.Effects {
		if effect.SideEffects && effect.Kind == commandeffect.KindUnknown {
			return fmt.Errorf("unknown shell command effect is not dispatchable through raw exec: %s", strings.TrimSpace(effect.Reason))
		}
	}
	return nil
}

func preDispatchExecError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrExecRejectedBeforeDispatch) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrExecRejectedBeforeDispatch, err)
}

func (r *Registry) validateContinuationExecAuthority(ctx context.Context, command string, plan commandeffect.EffectPlan) error {
	state, ok := ContinuationExecAuthorityFromContext(ctx)
	if !ok {
		return nil
	}
	decision := ContinuationExecAuthorityDecisionForPlan(state, command, plan, time.Now().UTC())
	return ContinuationExecAuthorityError(decision)
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
