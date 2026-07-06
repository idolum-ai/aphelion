//go:build linux

package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/session"
)

type runtimeJudgmentUseInput struct {
	Kind               string
	SchemaVersion      string
	SubjectKey         string
	ClaimKey           string
	InterpreterID      string
	InterpreterVersion string
	InputRefs          []string
	Result             any
	Completeness       session.JudgmentCompleteness
	Unknowns           []session.UnknownPredicate
	DependencyRefs     []session.JudgmentDependencyRef
	SourceFaultDomains []string
	Sensitivity        string

	TurnRunID   int64
	OperationID string
	ConsumerID  string
	Consequence session.JudgmentUseConsequence
	PolicyRef   string
	ResultRef   string
	Reason      string
}

func (r *Runtime) recordRuntimeJudgmentUse(key session.SessionKey, input runtimeJudgmentUseInput, now time.Time) (session.Judgment, session.JudgmentUse, error) {
	if r == nil || r.store == nil {
		return session.Judgment{}, session.JudgmentUse{}, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	raw, err := json.Marshal(input.Result)
	if err != nil {
		return session.Judgment{}, session.JudgmentUse{}, fmt.Errorf("encode %s judgment result: %w", input.Kind, err)
	}
	service := r.interpretationService()
	judgmentInput := session.JudgmentInput{
		Key:                key,
		TurnRunID:          input.TurnRunID,
		OperationID:        strings.TrimSpace(input.OperationID),
		Kind:               input.Kind,
		SchemaVersion:      firstNonEmpty(strings.TrimSpace(input.SchemaVersion), "v1"),
		SubjectKey:         input.SubjectKey,
		ClaimKey:           input.ClaimKey,
		InterpreterID:      input.InterpreterID,
		InterpreterVersion: firstNonEmpty(strings.TrimSpace(input.InterpreterVersion), "v1"),
		InputRefs:          input.InputRefs,
		InputHash:          runtimeJudgmentHash(input.InputRefs, raw),
		ResultJSON:         string(raw),
		Completeness:       input.Completeness,
		Unknowns:           input.Unknowns,
		DependencyRefs:     input.DependencyRefs,
		SourceFaultDomains: input.SourceFaultDomains,
		Sensitivity:        firstNonEmpty(strings.TrimSpace(input.Sensitivity), "interpretation_metadata"),
		AsOf:               now,
		CreatedAt:          now,
	}
	useInput := session.JudgmentUseInput{
		Key:                  key,
		TurnRunID:            input.TurnRunID,
		OperationID:          strings.TrimSpace(input.OperationID),
		ConsumerID:           input.ConsumerID,
		Consequence:          input.Consequence,
		DependencyRefs:       input.DependencyRefs,
		PolicyRef:            input.PolicyRef,
		ResultRef:            input.ResultRef,
		Irreversible:         false,
		QualificationStatus:  session.JudgmentUseQualificationQualified,
		ReconciliationStatus: session.JudgmentUseReconciliationNotRequired,
		Reason:               input.Reason,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	return service.RecordJudgmentAndUse(judgmentInput, useInput)
}

func runtimeJudgmentHash(refs []string, raw []byte) string {
	seed := strings.Join(append(append([]string(nil), refs...), string(raw)), "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func runtimeJudgmentShortHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:24]
}

func (r *Runtime) recordPerceptionBudgetJudgmentUse(key session.SessionKey, runID int64, contract memstore.PerceptionBudgetContract, now time.Time) error {
	if len(contract.Admitted) == 0 && len(contract.Suppressed) == 0 {
		return nil
	}
	deps := []session.JudgmentDependencyRef{
		{Kind: "turn_run", Ref: strconv.FormatInt(runID, 10), Role: "subject"},
	}
	for _, layer := range contract.Admitted {
		if source := strings.TrimSpace(layer.Source); source != "" {
			deps = append(deps, session.JudgmentDependencyRef{Kind: "perception_layer", Ref: string(layer.Name), Role: "admitted", Scope: source})
		}
	}
	for _, layer := range contract.Suppressed {
		if source := strings.TrimSpace(layer.Source); source != "" {
			deps = append(deps, session.JudgmentDependencyRef{Kind: "perception_layer", Ref: string(layer.Name), Role: "suppressed", Scope: source})
		}
	}
	_, _, err := r.recordRuntimeJudgmentUse(key, runtimeJudgmentUseInput{
		Kind:               session.JudgmentKindPerceptionBudgetContract,
		SubjectKey:         "turn_run:" + strconv.FormatInt(runID, 10) + ":perception_budget",
		ClaimKey:           "model_context_layer_admission",
		InterpreterID:      "runtime.perception_budget",
		InputRefs:          []string{session.JudgmentUseRef("turn_run", strconv.FormatInt(runID, 10))},
		Result:             contract,
		Completeness:       session.JudgmentCompletenessComplete,
		DependencyRefs:     deps,
		SourceFaultDomains: []string{"prompt_assembly", "memory_governor", "runtime_perception_budget_v1"},
		Sensitivity:        "perception_metadata",
		TurnRunID:          runID,
		ConsumerID:         session.ConsumerRuntimePerceptionBudgetAdmission,
		Consequence:        session.JudgmentUseConsequenceModelContextAdmission,
		PolicyRef:          "perception_budget_contract_v1",
		ResultRef:          session.JudgmentUseRef("turn_run_perception", strconv.FormatInt(runID, 10)),
		Reason:             "perception layers admitted to model context",
	}, now)
	return err
}

func (r *Runtime) recordAdaptiveRecallJudgmentUse(key session.SessionKey, runID int64, plan memstore.AdaptiveRecallPlan, hits []memstore.SemanticHit, now time.Time) error {
	if len(hits) == 0 {
		return nil
	}
	type recallHit struct {
		Source     string  `json:"source"`
		Kind       string  `json:"kind"`
		Score      float64 `json:"score"`
		Provenance string  `json:"provenance,omitempty"`
	}
	payload := struct {
		Plan memstore.AdaptiveRecallPlan `json:"plan"`
		Hits []recallHit                 `json:"hits"`
	}{Plan: plan}
	deps := []session.JudgmentDependencyRef{{Kind: "turn_run", Ref: strconv.FormatInt(runID, 10), Role: "subject"}}
	for _, hit := range hits {
		payload.Hits = append(payload.Hits, recallHit{Source: hit.Source, Kind: hit.Kind, Score: hit.Score, Provenance: hit.Provenance})
		if source := strings.TrimSpace(hit.Source); source != "" {
			deps = append(deps, session.JudgmentDependencyRef{Kind: "semantic_memory", Ref: source, Role: "admitted", Scope: hit.Kind})
		}
	}
	_, _, err := r.recordRuntimeJudgmentUse(key, runtimeJudgmentUseInput{
		Kind:               session.JudgmentKindAdaptiveRecallSelection,
		SubjectKey:         "turn_run:" + strconv.FormatInt(runID, 10) + ":adaptive_recall",
		ClaimKey:           "semantic_memory_recall_admission",
		InterpreterID:      "runtime.aggressive_memory_prefetch",
		InputRefs:          []string{session.JudgmentUseRef("turn_run", strconv.FormatInt(runID, 10))},
		Result:             payload,
		Completeness:       session.JudgmentCompletenessComplete,
		DependencyRefs:     deps,
		SourceFaultDomains: []string{"semantic_memory", "memory_governor", "runtime_aggressive_recall_v1"},
		Sensitivity:        "perception_metadata",
		TurnRunID:          runID,
		ConsumerID:         session.ConsumerRuntimeAdaptiveRecallAdmission,
		Consequence:        session.JudgmentUseConsequenceModelContextAdmission,
		PolicyRef:          "adaptive_recall_selection_v1",
		ResultRef:          session.JudgmentUseRef("turn_run_recall", strconv.FormatInt(runID, 10)),
		Reason:             "semantic recall admitted to model context",
	}, now)
	return err
}

func (r *Runtime) recordMaterialFloorJudgmentUse(key session.SessionKey, runID int64, rawText string, packet core.MaterialPacket, floorText string, structured bool, now time.Time) error {
	if strings.TrimSpace(rawText) == "" && strings.TrimSpace(floorText) == "" && packet.Empty() {
		return nil
	}
	payload := map[string]any{
		"structured": structured,
		"packet":     packet,
		"floor_text": floorText,
	}
	completeness := session.JudgmentCompletenessComplete
	var unknowns []session.UnknownPredicate
	if !structured {
		completeness = session.JudgmentCompletenessPartial
		unknowns = append(unknowns, session.UnknownPredicate{Kind: "unstructured_material_floor", Reason: "governor output did not parse as structured material packet"})
	}
	_, _, err := r.recordRuntimeJudgmentUse(key, runtimeJudgmentUseInput{
		Kind:               session.JudgmentKindMaterialFloorInterpretation,
		SubjectKey:         "turn_run:" + strconv.FormatInt(runID, 10) + ":material_floor",
		ClaimKey:           "material_floor_visibility",
		InterpreterID:      "runtime.material_floor",
		InputRefs:          []string{session.JudgmentUseHashRef("governor_text", rawText)},
		Result:             payload,
		Completeness:       completeness,
		Unknowns:           unknowns,
		DependencyRefs:     []session.JudgmentDependencyRef{{Kind: "turn_run", Ref: strconv.FormatInt(runID, 10), Role: "subject"}},
		SourceFaultDomains: []string{"governor_model_output", "pipeline_material_parser_v1"},
		Sensitivity:        "presentation_metadata",
		TurnRunID:          runID,
		ConsumerID:         session.ConsumerRuntimeMaterialFloorPresentation,
		Consequence:        session.JudgmentUseConsequencePresentation,
		PolicyRef:          "material_floor_visibility_v1",
		ResultRef:          session.JudgmentUseRef("turn_run_material_floor", strconv.FormatInt(runID, 10)),
		Reason:             "material floor interpreted for visible reply rendering",
	}, now)
	return err
}

func (r *Runtime) recordConstitutionJudgmentUse(key session.SessionKey, runID int64, surface string, violations []pipeline.ConstitutionViolation, now time.Time) error {
	if len(violations) == 0 {
		return nil
	}
	payload := map[string]any{
		"surface":    strings.TrimSpace(surface),
		"violations": violations,
	}
	deps := []session.JudgmentDependencyRef{{Kind: "turn_run", Ref: strconv.FormatInt(runID, 10), Role: "subject"}}
	for _, violation := range violations {
		deps = append(deps, session.JudgmentDependencyRef{Kind: "constitution_rule", Ref: strings.TrimSpace(violation.Rule), Role: "violation", Scope: strings.TrimSpace(violation.Surface)})
	}
	_, _, err := r.recordRuntimeJudgmentUse(key, runtimeJudgmentUseInput{
		Kind:               session.JudgmentKindConstitutionViolationCheck,
		SubjectKey:         "turn_run:" + strconv.FormatInt(runID, 10) + ":constitution:" + runtimeJudgmentShortHash(surface, fmt.Sprint(violations)),
		ClaimKey:           "visible_reply_constitution",
		InterpreterID:      "runtime.constitution_gate",
		InputRefs:          []string{session.JudgmentUseRef("turn_run", strconv.FormatInt(runID, 10))},
		Result:             payload,
		Completeness:       session.JudgmentCompletenessComplete,
		DependencyRefs:     deps,
		SourceFaultDomains: []string{"visible_reply_text", "pipeline_constitution_rules_v1"},
		Sensitivity:        "presentation_metadata",
		TurnRunID:          runID,
		ConsumerID:         session.ConsumerRuntimeConstitutionRepair,
		Consequence:        session.JudgmentUseConsequencePresentation,
		PolicyRef:          "constitution_visible_reply_v1",
		ResultRef:          session.JudgmentUseHashRef("constitution_violation", fmt.Sprintf("%d:%s:%v", runID, surface, violations)),
		Reason:             "constitution violation shaped visible reply repair or fallback",
	}, now)
	return err
}

func (r *Runtime) recordBudgetRecoveryScopeJudgmentUse(key session.SessionKey, msg core.InboundMessage, opState session.OperationState, scope string, payload map[string]any, now time.Time) error {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil
	}
	opState = session.NormalizeOperationState(opState)
	if payload == nil {
		payload = map[string]any{}
	}
	result := map[string]any{
		"scope":       scope,
		"payload":     payload,
		"request_ref": runtimeJudgmentShortHash(msg.Text),
	}
	deps := []session.JudgmentDependencyRef{
		{Kind: "operator_message", Ref: runtimeJudgmentShortHash(msg.Text), Role: "request"},
		{Kind: "recovery_scope", Ref: scope, Role: "result"},
	}
	if id := strings.TrimSpace(opState.ID); id != "" {
		deps = append(deps, session.JudgmentDependencyRef{Kind: "operation_state", Ref: id, Role: "candidate"})
	}
	_, _, err := r.recordRuntimeJudgmentUse(key, runtimeJudgmentUseInput{
		Kind:               session.JudgmentKindBudgetRecoveryScope,
		SubjectKey:         "budget_recovery:" + runtimeJudgmentShortHash(session.SessionIDForKey(key), scope, msg.Text),
		ClaimKey:           "budget_recovery_scope",
		InterpreterID:      "runtime.turn_budget_recovery_scope",
		InputRefs:          []string{session.JudgmentUseHashRef("operator_message", msg.Text)},
		Result:             result,
		Completeness:       session.JudgmentCompletenessComplete,
		DependencyRefs:     deps,
		SourceFaultDomains: []string{"turn_recovery_state", "operation_state", "operator_message", "runtime_budget_recovery_scope_v1"},
		Sensitivity:        "recovery_metadata",
		OperationID:        strings.TrimSpace(opState.ID),
		ConsumerID:         session.ConsumerRuntimeBudgetRecoveryScope,
		Consequence:        session.JudgmentUseConsequenceRecoverySelection,
		PolicyRef:          "budget_recovery_scope_v1",
		ResultRef:          session.JudgmentUseHashRef("budget_recovery_scope", session.SessionIDForKey(key)+"|"+scope),
		Reason:             "budget recovery scope selected",
	}, now)
	return err
}

func (r *Runtime) recordCuriositySelectionJudgmentUse(key session.SessionKey, lease session.CuriosityLease, candidate curiosityCandidate, now time.Time) error {
	if strings.TrimSpace(candidate.ID) == "" {
		return nil
	}
	payload := curiosityCandidatePayload(candidate)
	payload["selection_policy"] = curiositySelectionPolicyIntensityFirst
	payload["lease_id"] = lease.ID
	deps := []session.JudgmentDependencyRef{
		{Kind: "curiosity_lease", Ref: lease.ID, Role: "authority"},
		{Kind: "curiosity_candidate", Ref: candidate.ID, Role: "selected", Scope: candidate.SourceKind},
	}
	if candidate.SourceRef != "" {
		deps = append(deps, session.JudgmentDependencyRef{Kind: "curiosity_source", Ref: candidate.SourceRef, Role: "support", Scope: candidate.SourceKind})
	}
	_, _, err := r.recordRuntimeJudgmentUse(key, runtimeJudgmentUseInput{
		Kind:               session.JudgmentKindCuriosityCandidateSelection,
		SubjectKey:         "curiosity_candidate:" + candidate.ID,
		ClaimKey:           "curiosity_salience_selection",
		InterpreterID:      "runtime.curiosity_selection",
		InputRefs:          []string{session.JudgmentUseRef("curiosity_lease", lease.ID), session.JudgmentUseRef("curiosity_candidate", candidate.ID)},
		Result:             payload,
		Completeness:       session.JudgmentCompletenessComplete,
		DependencyRefs:     deps,
		SourceFaultDomains: []string{"interior_signal_state", "curiosity_history", "runtime_curiosity_selection_v1"},
		Sensitivity:        "curiosity_metadata",
		ConsumerID:         session.ConsumerRuntimeCuriositySelection,
		Consequence:        session.JudgmentUseConsequenceModelContextAdmission,
		PolicyRef:          "curiosity_candidate_selection_v1",
		ResultRef:          session.JudgmentUseRef("curiosity_candidate", candidate.ID),
		Reason:             "curiosity candidate selected for read-only attention lane",
	}, now)
	return err
}
