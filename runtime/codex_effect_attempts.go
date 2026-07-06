//go:build linux

package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/effectauth"
	runtimecodex "github.com/idolum-ai/aphelion/runtime/codex"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) recordCodexCommandApprovalAttempt(req WorkRequest, command string, decision effectauth.Decision, params map[string]any, ordinal int, now time.Time) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("effect attempt store unavailable")
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	effect := commandeffect.Classify(command)
	if !effect.SideEffects {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	key := workRequestEffectAttemptKey(req)
	safeCommand := redactRuntimeEvidenceText(commandeffect.NormalizeCommand(command))
	boundaryKind := ""
	if boundary, ok := commandeffect.BoundaryForCommand(command); ok {
		boundaryKind = string(boundary.Kind)
	}
	attemptID := codexApprovalAttemptID(key, req, "command", command, params, ordinal)
	attemptInput := session.EffectAttemptInput{
		AttemptID:           attemptID,
		Key:                 key,
		OperationID:         firstNonEmptyContinuation(req.OperationID, req.Operation.ID),
		PhaseID:             effectAttemptPhaseID(req),
		LeaseID:             req.LeaseID,
		ProposalID:          req.State.ActionProposal.ID,
		WorkMode:            string(req.Mode),
		Executor:            "codex",
		Tool:                "codex_command_approval",
		Command:             safeCommand,
		EffectKind:          string(effect.Kind),
		EffectReason:        effect.Reason,
		BoundaryKind:        boundaryKind,
		AuthorizationReason: strings.TrimSpace(decision.Reason),
		SubjectJSON:         effectAttemptSubjectJSON(command),
		Status:              session.EffectAttemptStatusAttempted,
		EvidenceRefs:        codexApprovalEvidenceRefs(params),
		StartedAt:           now,
		UpdatedAt:           now,
	}
	judgment, err := r.recordCodexCommandPlanJudgment(req, key, command, effect, boundaryKind, decision, params, now)
	if err != nil {
		return err
	}
	useInput := codexApprovalJudgmentUseInput(req, key, attemptID, "runtime.codex.command_approval", command, effect.Kind, effect.Reason, boundaryKind, decision, params, judgment.ID, now)
	_, _, err = r.interpretationService().RecordEffectAttemptWithUse(attemptInput, useInput)
	if err != nil {
		return err
	}
	return nil
}

func (r *Runtime) recordCodexFileChangeApprovalAttempt(req WorkRequest, params map[string]any, decision effectauth.Decision, ordinal int, now time.Time) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("effect attempt store unavailable")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	key := workRequestEffectAttemptKey(req)
	paths := codexFileChangePaths(params)
	fingerprint := codexFileChangeFingerprint(params, paths)
	command := "codex file_change " + fingerprint
	subject := codexFileChangeSubjectJSON(params, paths, fingerprint)
	attemptID := codexApprovalAttemptID(key, req, "file_change", command, params, ordinal)
	attemptInput := session.EffectAttemptInput{
		AttemptID:           attemptID,
		Key:                 key,
		OperationID:         firstNonEmptyContinuation(req.OperationID, req.Operation.ID),
		PhaseID:             effectAttemptPhaseID(req),
		LeaseID:             req.LeaseID,
		ProposalID:          req.State.ActionProposal.ID,
		WorkMode:            string(req.Mode),
		Executor:            "codex",
		Tool:                "codex_file_change_approval",
		Command:             command,
		EffectKind:          string(commandeffect.KindWorkspaceMutation),
		EffectReason:        "codex file change approval",
		AuthorizationReason: strings.TrimSpace(decision.Reason),
		SubjectJSON:         subject,
		Status:              session.EffectAttemptStatusAttempted,
		EvidenceRefs:        codexApprovalEvidenceRefs(params),
		StartedAt:           now,
		UpdatedAt:           now,
	}
	judgment, err := r.recordCodexFileChangePlanJudgment(req, key, params, paths, fingerprint, decision, now)
	if err != nil {
		return err
	}
	useInput := codexApprovalJudgmentUseInput(req, key, attemptID, "runtime.codex.file_change_approval", command, commandeffect.KindWorkspaceMutation, "codex file change approval", "", decision, params, judgment.ID, now)
	useInput.DependencyRefs = append(useInput.DependencyRefs, session.JudgmentDependencyRef{Kind: "file_change_fingerprint", Ref: fingerprint, Role: "subject"})
	for _, path := range paths {
		useInput.DependencyRefs = append(useInput.DependencyRefs, session.JudgmentDependencyRef{Kind: "file_path", Ref: path, Role: "subject"})
	}
	_, _, err = r.interpretationService().RecordEffectAttemptWithUse(attemptInput, useInput)
	if err != nil {
		return err
	}
	return nil
}

func workRequestEffectAttemptKey(req WorkRequest) session.SessionKey {
	key := req.Key
	if key.ChatID == 0 {
		key.ChatID = req.ChatID
	}
	if key.ChatID != 0 && strings.TrimSpace(string(key.Scope.Kind)) == "" && strings.TrimSpace(key.Scope.ID) == "" {
		key.Scope = telegramDMScopeRef(key.ChatID)
	}
	return key
}

func codexApprovalAttemptID(key session.SessionKey, req WorkRequest, kind string, command string, params map[string]any, ordinal int) string {
	seed := strings.Join([]string{
		session.SessionIDForKey(key),
		firstNonEmptyContinuation(req.OperationID, req.Operation.ID),
		effectAttemptPhaseID(req),
		strings.TrimSpace(req.LeaseID),
		strings.TrimSpace(req.State.ActionProposal.ID),
		string(req.Mode),
		strings.TrimSpace(kind),
		firstNonEmptyContinuation(
			runtimecodex.StringField(params, "item_id"),
			runtimecodex.StringField(params, "itemId"),
			runtimecodex.StringField(params, "request_id"),
			runtimecodex.StringField(params, "requestId"),
			fmt.Sprintf("ordinal:%d", ordinal),
		),
		session.EffectAttemptCommandHash(command),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "eff_" + hex.EncodeToString(sum[:16])
}

func codexApprovalEvidenceRefs(params map[string]any) []string {
	var refs []string
	for _, key := range []string{"item_id", "itemId", "request_id", "requestId", "turn_id", "turnId"} {
		if value := runtimecodex.StringField(params, key); strings.TrimSpace(value) != "" {
			refs = append(refs, "codex_approval:"+strings.TrimSpace(value))
		}
	}
	return refs
}

func (r *Runtime) recordCodexCommandPlanJudgment(req WorkRequest, key session.SessionKey, command string, effect commandeffect.Effect, boundaryKind string, decision effectauth.Decision, params map[string]any, now time.Time) (session.Judgment, error) {
	plan := commandeffect.PlanCommand(command)
	safeCommand := redactRuntimeEvidenceText(commandeffect.NormalizeCommand(command))
	safePlan := plan
	safePlan.Command = safeCommand
	for i := range safePlan.Effects {
		safePlan.Effects[i].Command = redactRuntimeEvidenceText(commandeffect.NormalizeCommand(safePlan.Effects[i].Command))
		safePlan.Effects[i].Target = redactRuntimeEvidenceText(safePlan.Effects[i].Target)
		safePlan.Effects[i].Subject = redactRuntimeEvidenceText(safePlan.Effects[i].Subject)
	}
	payload := map[string]any{
		"command":       safeCommand,
		"effect_kind":   effect.Kind,
		"effect_reason": effect.Reason,
		"boundary_kind": strings.TrimSpace(boundaryKind),
		"decision":      codexApprovalDecisionStatus(decision),
		"reason":        strings.TrimSpace(decision.Reason),
		"plan":          safePlan,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return session.Judgment{}, err
	}
	deps := codexApprovalJudgmentDependencyRefs(req, command, effect.Kind, effect.Reason, boundaryKind, params)
	return r.interpretationService().RecordJudgment(session.JudgmentInput{
		Key:                key,
		OperationID:        firstNonEmptyContinuation(req.OperationID, req.Operation.ID),
		Kind:               "codex_command_effect_plan",
		SchemaVersion:      "v1",
		SubjectKey:         "codex_command:" + session.EffectAttemptCommandHash(command),
		ClaimKey:           "codex_command_execution_effect",
		InterpreterID:      "runtime.codex.command_approval",
		InterpreterVersion: "v1",
		InputRefs:          []string{session.JudgmentUseHashRef("codex_command", command)},
		InputHash:          session.EffectAttemptCommandHash(command),
		ResultJSON:         string(raw),
		Completeness:       session.JudgmentCompletenessComplete,
		DependencyRefs:     deps,
		SourceFaultDomains: []string{"codex_provider_request", "commandeffect_plan_v1"},
		Sensitivity:        "execution_metadata",
		AsOf:               now,
		CreatedAt:          now,
	})
}

func (r *Runtime) recordCodexFileChangePlanJudgment(req WorkRequest, key session.SessionKey, params map[string]any, paths []string, fingerprint string, decision effectauth.Decision, now time.Time) (session.Judgment, error) {
	shortFingerprint := strings.TrimPrefix(strings.TrimSpace(fingerprint), "sha256:")
	if len(shortFingerprint) > 24 {
		shortFingerprint = shortFingerprint[:24]
	}
	payload := map[string]any{
		"kind":        "codex_file_change",
		"paths":       paths,
		"patch_hash":  fingerprint,
		"decision":    codexApprovalDecisionStatus(decision),
		"reason":      strings.TrimSpace(decision.Reason),
		"provider_id": firstNonEmptyContinuation(runtimecodex.StringField(params, "item_id"), runtimecodex.StringField(params, "itemId"), runtimecodex.StringField(params, "request_id"), runtimecodex.StringField(params, "requestId")),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return session.Judgment{}, err
	}
	deps := []session.JudgmentDependencyRef{{Kind: "file_change_fingerprint", Ref: fingerprint, Role: "subject"}}
	for _, path := range paths {
		deps = append(deps, session.JudgmentDependencyRef{Kind: "file_path", Ref: path, Role: "subject"})
	}
	for _, ref := range codexApprovalEvidenceRefs(params) {
		deps = append(deps, session.JudgmentDependencyRef{Kind: "provider_event", Ref: ref, Role: "support"})
	}
	return r.interpretationService().RecordJudgment(session.JudgmentInput{
		Key:                key,
		OperationID:        firstNonEmptyContinuation(req.OperationID, req.Operation.ID),
		Kind:               "codex_file_change_plan",
		SchemaVersion:      "v1",
		SubjectKey:         "codex_file_change:" + shortFingerprint,
		ClaimKey:           "codex_file_change_execution_effect",
		InterpreterID:      "runtime.codex.file_change_approval",
		InterpreterVersion: "v1",
		InputRefs:          []string{session.JudgmentUseHashRef("codex_file_change", fingerprint)},
		InputHash:          fingerprint,
		ResultJSON:         string(raw),
		Completeness:       session.JudgmentCompletenessComplete,
		DependencyRefs:     deps,
		SourceFaultDomains: []string{"codex_provider_request", "codex_file_change_plan_v1"},
		Sensitivity:        "execution_metadata",
		AsOf:               now,
		CreatedAt:          now,
	})
}

func codexApprovalDecisionStatus(decision effectauth.Decision) string {
	if decision.Allowed {
		return "allowed"
	}
	return "denied"
}

func codexApprovalJudgmentDependencyRefs(req WorkRequest, command string, kind commandeffect.Kind, reason string, boundaryKind string, params map[string]any) []session.JudgmentDependencyRef {
	refs := []session.JudgmentDependencyRef{
		{Kind: "command_hash", Ref: session.EffectAttemptCommandHash(command), Role: "subject"},
		{Kind: "effect_kind", Ref: string(kind), Role: "qualifies"},
	}
	if strings.TrimSpace(reason) != "" {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "effect_reason", Ref: strings.TrimSpace(reason), Role: "qualifies"})
	}
	if strings.TrimSpace(boundaryKind) != "" {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "boundary_kind", Ref: strings.TrimSpace(boundaryKind), Role: "qualifies"})
	}
	for _, ref := range codexApprovalEvidenceRefs(params) {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "provider_event", Ref: ref, Role: "support"})
	}
	if req.LeaseID != "" {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "lease", Ref: req.LeaseID, Role: "qualifies"})
	}
	if req.State.ActionProposal.ID != "" {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "proposal", Ref: req.State.ActionProposal.ID, Role: "qualifies"})
	}
	return refs
}

func codexApprovalJudgmentUseInput(req WorkRequest, key session.SessionKey, attemptID string, consumerID string, command string, kind commandeffect.Kind, reason string, boundaryKind string, decision effectauth.Decision, params map[string]any, judgmentID string, now time.Time) session.JudgmentUseInput {
	normalized := commandeffect.NormalizeCommand(command)
	refs := []session.JudgmentDependencyRef{
		{Kind: "command_hash", Ref: session.EffectAttemptCommandHash(command), Role: "subject"},
		{Kind: "effect_kind", Ref: string(kind), Role: "qualifies"},
	}
	if strings.TrimSpace(reason) != "" {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "effect_reason", Ref: strings.TrimSpace(reason), Role: "qualifies"})
	}
	if strings.TrimSpace(boundaryKind) != "" {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "boundary_kind", Ref: strings.TrimSpace(boundaryKind), Role: "qualifies"})
	}
	for _, ref := range codexApprovalEvidenceRefs(params) {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "provider_event", Ref: ref, Role: "support"})
	}
	if req.LeaseID != "" {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "lease", Ref: req.LeaseID, Role: "qualifies"})
	}
	if req.State.ActionProposal.ID != "" {
		refs = append(refs, session.JudgmentDependencyRef{Kind: "proposal", Ref: req.State.ActionProposal.ID, Role: "qualifies"})
	}
	// Codex approvals are recorded inside the provider approval callback after
	// effectauth has accepted the concrete provider request. Raw exec performs
	// an additional local decorrelation gate because Aphelion is about to
	// dispatch shell directly from an operator proposal.
	return session.JudgmentUseInput{
		Key:                  key,
		OperationID:          firstNonEmptyContinuation(req.OperationID, req.Operation.ID),
		PhaseID:              effectAttemptPhaseID(req),
		LeaseID:              req.LeaseID,
		ProposalID:           req.State.ActionProposal.ID,
		ConsumerID:           consumerID,
		Consequence:          session.JudgmentUseConsequenceExecution,
		JudgmentRefs:         []string{session.JudgmentRef(judgmentID), session.JudgmentUseHashRef("codex_effect_plan", strings.Join([]string{normalized, string(kind), strings.TrimSpace(reason)}, "\x00"))},
		DependencyRefs:       refs,
		PolicyRef:            "codex_approval_write_ahead_v1",
		ResultRef:            session.JudgmentUseRef("effect_attempt", attemptID),
		Irreversible:         true,
		QualificationStatus:  session.JudgmentUseQualificationQualified,
		ReconciliationStatus: session.JudgmentUseReconciliationNotRequired,
		Reason:               strings.TrimSpace(decision.Reason),
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}

func codexFileChangeFingerprint(params map[string]any, paths []string) string {
	patch := firstNonEmptyContinuation(
		runtimecodex.StringField(params, "patch"),
		runtimecodex.StringField(params, "diff"),
		runtimecodex.StringField(params, "preview"),
		runtimecodex.StringField(params, "reason"),
	)
	seed := strings.Join(append(append([]string(nil), paths...), patch), "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func codexFileChangeSubjectJSON(params map[string]any, paths []string, fingerprint string) string {
	payload := map[string]any{
		"kind":        "codex_file_change",
		"paths":       paths,
		"patch_hash":  strings.TrimSpace(fingerprint),
		"operation":   firstNonEmptyContinuation(runtimecodex.StringField(params, "operation"), runtimecodex.StringField(params, "op")),
		"provider_id": firstNonEmptyContinuation(runtimecodex.StringField(params, "item_id"), runtimecodex.StringField(params, "itemId"), runtimecodex.StringField(params, "request_id"), runtimecodex.StringField(params, "requestId")),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return session.RedactEvidenceText(string(raw)).Text
}
