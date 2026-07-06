//go:build linux

package session

const (
	JudgmentKindShellEffectPlan              = "shell_effect_plan"
	JudgmentKindBrokerageControlFlow         = "brokerage_control_flow"
	JudgmentKindPerceptionBudgetContract     = "perception_budget_contract"
	JudgmentKindAdaptiveRecallSelection      = "adaptive_recall_selection"
	JudgmentKindEvidenceHydrationSelection   = "evidence_hydration_selection"
	JudgmentKindConstitutionViolationCheck   = "constitution_violation_check"
	JudgmentKindMaterialFloorInterpretation  = "material_floor_interpretation"
	JudgmentKindRecoveryCandidateArbitration = "recovery_candidate_arbitration"
	JudgmentKindReentryRecommendation        = "reentry_recommendation_selection"
	JudgmentKindBudgetRecoveryScope          = "budget_recovery_scope_selection"
	JudgmentKindCuriosityCandidateSelection  = "curiosity_candidate_selection"
)

const (
	ConsumerToolExecDispatch                 = "tool.exec.dispatch"
	ConsumerRuntimeBrokerageControlFlow      = "runtime.brokerage.control_flow"
	ConsumerRuntimePerceptionBudgetAdmission = "runtime.perception_budget.context_admission"
	ConsumerRuntimeAdaptiveRecallAdmission   = "runtime.aggressive_memory.context_admission"
	ConsumerEvidenceHydrationAdmission       = "session.evidence_hydration.context_admission"
	ConsumerRuntimeConstitutionRepair        = "runtime.constitution.repair"
	ConsumerRuntimeMaterialFloorPresentation = "runtime.material_floor.presentation"
	ConsumerRuntimeRecoveryCandidate         = "runtime.recovery_candidate_arbitration"
	ConsumerRuntimeReentryPresentation       = "runtime.reentry_recommendation.presentation"
	ConsumerRuntimeBudgetRecoveryScope       = "runtime.budget_recovery.scope"
	ConsumerRuntimeCuriositySelection        = "runtime.curiosity.selection"
)

const (
	ChallengeAdapterAppendOnlyEvents                  = "append_only_challenge_events"
	ChallengeAdapterBoundedRetryAndFailover           = "bounded_retry_and_failover"
	ChallengeAdapterCurrentRequestDisambiguation      = "current_request_disambiguation"
	ChallengeAdapterDecorrelatedEvidence              = "decorrelated_evidence_challenge"
	ChallengeAdapterDenyEscapeOrSymlink               = "deny_escape_or_symlink"
	ChallengeAdapterDenyUnmatchedPrincipal            = "deny_unmatched_principal"
	ChallengeAdapterDeterministicCandidateSuppression = "deterministic_candidate_suppression"
	ChallengeAdapterDeterministicDecorrelationRules   = "deterministic_decorrelation_rules"
	ChallengeAdapterDropUnsafeMedia                   = "drop_unsafe_media"
	ChallengeAdapterEffectAttemptAndStateChallenge    = "effect_attempt_and_durable_state_challenge"
	ChallengeAdapterEffectAttemptReconciliation       = "effect_attempt_reconciliation"
	ChallengeAdapterEffectEvidenceReconciliation      = "effect_evidence_reconciliation"
	ChallengeAdapterEvalReplay                        = "eval_replay"
	ChallengeAdapterLocalArgumentation                = "local_argumentation_and_stable_contract"
	ChallengeAdapterLocalRepair                       = "local_repair_and_visible_fallback"
	ChallengeAdapterMissingEvidencePartialJudgment    = "missing_evidence_partial_judgment"
	ChallengeAdapterNonHydratableOrOperatorOnly       = "non_hydratable_or_operator_only_class"
	ChallengeAdapterOperatorDisambiguation            = "operator_disambiguation"
	ChallengeAdapterExplicitResumeOrCurrentIntent     = "operator_explicit_resume_or_current_intent"
	ChallengeAdapterOperatorOverrideAndBakeoff        = "operator_override_and_bakeoff"
	ChallengeAdapterPhaseRepairOrBlock                = "phase_repair_or_block"
	ChallengeAdapterPromotionReview                   = "promotion_abstain_or_operator_review"
	ChallengeAdapterRejectPartialInput                = "reject_partial_input"
	ChallengeAdapterStaleCallbackTerminalization      = "stale_callback_terminalization"
	ChallengeAdapterStrandedHandoffDiagnostics        = "stranded_handoff_diagnostics"
	ChallengeAdapterSupersessionTerminalization       = "supersession_terminalization"
	ChallengeAdapterTypedEffectDecisionRegeneration   = "typed_effect_decision_regeneration"
	ChallengeAdapterTypedRepairOrBlock                = "typed_repair_or_block"
	ChallengeAdapterVerificationRequired              = "verification_required"
)

func RegisteredInterpretationChallengeAdapters() []string {
	return []string{
		ChallengeAdapterAppendOnlyEvents,
		ChallengeAdapterBoundedRetryAndFailover,
		ChallengeAdapterCurrentRequestDisambiguation,
		ChallengeAdapterDecorrelatedEvidence,
		ChallengeAdapterDenyEscapeOrSymlink,
		ChallengeAdapterDenyUnmatchedPrincipal,
		ChallengeAdapterDeterministicCandidateSuppression,
		ChallengeAdapterDeterministicDecorrelationRules,
		ChallengeAdapterDropUnsafeMedia,
		ChallengeAdapterEffectAttemptAndStateChallenge,
		ChallengeAdapterEffectAttemptReconciliation,
		ChallengeAdapterEffectEvidenceReconciliation,
		ChallengeAdapterEvalReplay,
		ChallengeAdapterLocalArgumentation,
		ChallengeAdapterLocalRepair,
		ChallengeAdapterMissingEvidencePartialJudgment,
		ChallengeAdapterNonHydratableOrOperatorOnly,
		ChallengeAdapterOperatorDisambiguation,
		ChallengeAdapterExplicitResumeOrCurrentIntent,
		ChallengeAdapterOperatorOverrideAndBakeoff,
		ChallengeAdapterPhaseRepairOrBlock,
		ChallengeAdapterPromotionReview,
		ChallengeAdapterRejectPartialInput,
		ChallengeAdapterStaleCallbackTerminalization,
		ChallengeAdapterStrandedHandoffDiagnostics,
		ChallengeAdapterSupersessionTerminalization,
		ChallengeAdapterTypedEffectDecisionRegeneration,
		ChallengeAdapterTypedRepairOrBlock,
		ChallengeAdapterVerificationRequired,
	}
}
