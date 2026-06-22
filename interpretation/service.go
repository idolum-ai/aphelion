//go:build linux

package interpretation

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

type Store interface {
	RecordJudgment(session.JudgmentInput) (session.Judgment, error)
	RecordJudgmentWithUse(session.JudgmentInput, session.JudgmentUseInput) (session.Judgment, session.JudgmentUse, error)
	RecordJudgmentUseCommitment(session.JudgmentUseInput) (session.JudgmentUse, error)
	UpsertEffectAttemptWithJudgmentUse(session.EffectAttemptInput, session.JudgmentUseInput) (session.EffectAttempt, session.JudgmentUse, error)
	AppendJudgmentChallengeEvent(session.JudgmentChallengeEventInput) (session.JudgmentChallengeEvent, error)
	MarkJudgmentUsesForJudgmentReconciliation(string, session.JudgmentUseReconciliationStatus, string, time.Time) error
	JudgmentGroundProfile(string, int) (session.JudgmentGroundProfile, error)
}

type Service struct {
	store Store
}

func NewService(store Store) Service {
	return Service{store: store}
}

func (s Service) RecordJudgment(input session.JudgmentInput) (session.Judgment, error) {
	if s.store == nil {
		return session.Judgment{}, fmt.Errorf("interpretation store unavailable")
	}
	input, err := validateJudgmentInput(input)
	if err != nil {
		return session.Judgment{}, err
	}
	return s.store.RecordJudgment(input)
}

func (s Service) RecordUse(input session.JudgmentUseInput) (session.JudgmentUse, error) {
	if s.store == nil {
		return session.JudgmentUse{}, fmt.Errorf("interpretation store unavailable")
	}
	input, err := validateJudgmentUseInput(input)
	if err != nil {
		return session.JudgmentUse{}, err
	}
	return s.store.RecordJudgmentUseCommitment(input)
}

func (s Service) RecordJudgmentAndUse(judgmentInput session.JudgmentInput, useInput session.JudgmentUseInput) (session.Judgment, session.JudgmentUse, error) {
	if s.store == nil {
		return session.Judgment{}, session.JudgmentUse{}, fmt.Errorf("interpretation store unavailable")
	}
	if strings.TrimSpace(useInput.ID) != "" {
		return session.Judgment{}, session.JudgmentUse{}, fmt.Errorf("judgment/use commitment id is service-managed")
	}
	if useInput.Key != (session.SessionKey{}) && useInput.Key != judgmentInput.Key {
		return session.Judgment{}, session.JudgmentUse{}, fmt.Errorf("judgment use key does not match judgment key")
	}
	judgmentInput, err := validateJudgmentInput(judgmentInput)
	if err != nil {
		return session.Judgment{}, session.JudgmentUse{}, err
	}
	useInput = bindUseToJudgmentInput(judgmentInput, useInput)
	useInput, err = validateJudgmentUseInput(useInput)
	if err != nil {
		return session.Judgment{}, session.JudgmentUse{}, err
	}
	judgment, use, err := s.store.RecordJudgmentWithUse(judgmentInput, useInput)
	if err != nil {
		return session.Judgment{}, session.JudgmentUse{}, err
	}
	return judgment, use, nil
}

func (s Service) RecordEffectAttemptWithUse(attemptInput session.EffectAttemptInput, useInput session.JudgmentUseInput) (session.EffectAttempt, session.JudgmentUse, error) {
	if s.store == nil {
		return session.EffectAttempt{}, session.JudgmentUse{}, fmt.Errorf("interpretation store unavailable")
	}
	if strings.TrimSpace(useInput.ID) != "" {
		return session.EffectAttempt{}, session.JudgmentUse{}, fmt.Errorf("effect-attempt judgment use id is service-managed")
	}
	attemptInput, useInput, err := validateEffectAttemptUseInputs(attemptInput, useInput)
	if err != nil {
		return session.EffectAttempt{}, session.JudgmentUse{}, err
	}
	useInput, err = validateJudgmentUseInput(useInput)
	if err != nil {
		return session.EffectAttempt{}, session.JudgmentUse{}, err
	}
	return s.store.UpsertEffectAttemptWithJudgmentUse(attemptInput, useInput)
}

func (s Service) AppendChallengeEvent(input session.JudgmentChallengeEventInput) (session.JudgmentChallengeEvent, error) {
	if s.store == nil {
		return session.JudgmentChallengeEvent{}, fmt.Errorf("interpretation store unavailable")
	}
	if err := validateChallengeEventStateValues(input); err != nil {
		return session.JudgmentChallengeEvent{}, err
	}
	return s.store.AppendJudgmentChallengeEvent(input)
}

func (s Service) MarkUsesForJudgmentReconciliation(judgmentID string, status session.JudgmentUseReconciliationStatus, reason string, at time.Time) error {
	if s.store == nil {
		return fmt.Errorf("interpretation store unavailable")
	}
	if err := validateReconciliationStatus(status); err != nil {
		return err
	}
	return s.store.MarkJudgmentUsesForJudgmentReconciliation(judgmentID, status, reason, at)
}

func (s Service) JudgmentGroundProfile(judgmentID string, maxDepth int) (session.JudgmentGroundProfile, error) {
	if s.store == nil {
		return session.JudgmentGroundProfile{}, fmt.Errorf("interpretation store unavailable")
	}
	return s.store.JudgmentGroundProfile(judgmentID, maxDepth)
}

type DecorrelatedQualificationInput struct {
	Irreversible bool
	Challenged   session.JudgmentGroundProfile
	Support      session.JudgmentGroundProfile
	SupportRefs  []session.JudgmentDependencyRef
	Qualified    string
	Blocked      string
}

type QualificationDecision struct {
	Status         session.JudgmentUseQualificationStatus
	Reason         string
	DependencyRefs []session.JudgmentDependencyRef
	Decorrelated   session.JudgmentDecorrelatedGroundDecision
}

func (s Service) QualifyDecorrelatedUse(input DecorrelatedQualificationInput) (QualificationDecision, error) {
	qualified := strings.TrimSpace(input.Qualified)
	if qualified == "" {
		qualified = "use qualified by decorrelated ground"
	}
	if !input.Irreversible {
		return QualificationDecision{
			Status:         session.JudgmentUseQualificationQualified,
			Reason:         qualified,
			DependencyRefs: append([]session.JudgmentDependencyRef(nil), input.SupportRefs...),
		}, nil
	}
	blocked := strings.TrimSpace(input.Blocked)
	if blocked == "" {
		blocked = "irreversible use lacks decorrelated ground"
	}
	decision := session.DecorrelatedGroundForJudgment(input.Challenged, input.Support)
	out := QualificationDecision{
		Decorrelated:   decision,
		DependencyRefs: append([]session.JudgmentDependencyRef(nil), input.SupportRefs...),
	}
	if decision.Decorrelated {
		out.Status = session.JudgmentUseQualificationQualified
		out.Reason = qualified
		return out, nil
	}
	out.Status = session.JudgmentUseQualificationBlocked
	out.Reason = blocked + ": " + decision.Reason
	return out, fmt.Errorf("%s: %s", blocked, decision.Reason)
}

func validateJudgmentInput(input session.JudgmentInput) (session.JudgmentInput, error) {
	if err := validateJudgmentStateValues(input); err != nil {
		return session.JudgmentInput{}, err
	}
	input, err := session.NormalizeJudgmentInput(input)
	if err != nil {
		return session.JudgmentInput{}, err
	}
	switch input.Completeness {
	case session.JudgmentCompletenessComplete:
		if len(input.Unknowns) > 0 {
			return session.JudgmentInput{}, fmt.Errorf("complete judgment %s cannot carry unknown predicates", input.Kind)
		}
	case session.JudgmentCompletenessPartial:
		if len(input.Unknowns) == 0 {
			return session.JudgmentInput{}, fmt.Errorf("partial judgment %s must name unknown predicates", input.Kind)
		}
	case session.JudgmentCompletenessAbstain:
	default:
		return session.JudgmentInput{}, fmt.Errorf("judgment %s has invalid completeness %q", input.Kind, input.Completeness)
	}
	if len(input.DependencyRefs) == 0 {
		return session.JudgmentInput{}, fmt.Errorf("judgment %s requires dependency refs through interpretation service", input.Kind)
	}
	if len(input.SourceFaultDomains) == 0 {
		return session.JudgmentInput{}, fmt.Errorf("judgment %s requires source fault domains through interpretation service", input.Kind)
	}
	return input, nil
}

func validateJudgmentUseInput(input session.JudgmentUseInput) (session.JudgmentUseInput, error) {
	if err := validateJudgmentUseStateValues(input); err != nil {
		return session.JudgmentUseInput{}, err
	}
	input, err := session.NormalizeJudgmentUseInput(input)
	if err != nil {
		return session.JudgmentUseInput{}, err
	}
	if len(input.DependencyRefs) == 0 {
		return session.JudgmentUseInput{}, fmt.Errorf("judgment use %s requires dependency refs through interpretation service", input.ConsumerID)
	}
	if strings.TrimSpace(input.PolicyRef) == "" {
		return session.JudgmentUseInput{}, fmt.Errorf("judgment use %s requires policy_ref through interpretation service", input.ConsumerID)
	}
	if strings.TrimSpace(input.ResultRef) == "" {
		return session.JudgmentUseInput{}, fmt.Errorf("judgment use %s requires result_ref through interpretation service", input.ConsumerID)
	}
	return input, nil
}

func bindUseToJudgmentInput(judgmentInput session.JudgmentInput, useInput session.JudgmentUseInput) session.JudgmentUseInput {
	if useInput.Key == (session.SessionKey{}) {
		useInput.Key = judgmentInput.Key
	}
	if strings.TrimSpace(useInput.SessionID) == "" {
		useInput.SessionID = judgmentInput.SessionID
	}
	required := session.JudgmentRef(judgmentInput.ID)
	if required == "" {
		return useInput
	}
	for _, ref := range useInput.JudgmentRefs {
		if strings.TrimSpace(ref) == required {
			return useInput
		}
	}
	useInput.JudgmentRefs = append(useInput.JudgmentRefs, required)
	return useInput
}

func validateEffectAttemptUseInputs(attemptInput session.EffectAttemptInput, useInput session.JudgmentUseInput) (session.EffectAttemptInput, session.JudgmentUseInput, error) {
	if err := validateEffectAttemptStateValues(attemptInput); err != nil {
		return session.EffectAttemptInput{}, session.JudgmentUseInput{}, err
	}
	attemptInput = session.NormalizeEffectAttemptInput(attemptInput)
	expectedRef := session.JudgmentUseRef("effect_attempt", attemptInput.AttemptID)
	if got := strings.TrimSpace(useInput.ResultRef); got != "" && got != expectedRef {
		return session.EffectAttemptInput{}, session.JudgmentUseInput{}, fmt.Errorf("judgment use result_ref %q does not match effect attempt %q", got, expectedRef)
	}
	useInput.ResultRef = expectedRef
	if useInput.Key == (session.SessionKey{}) {
		useInput.Key = attemptInput.Key
	}
	sessionID := session.SessionIDForKey(attemptInput.Key)
	if strings.TrimSpace(useInput.SessionID) == "" {
		useInput.SessionID = sessionID
	} else if strings.TrimSpace(useInput.SessionID) != sessionID {
		return session.EffectAttemptInput{}, session.JudgmentUseInput{}, fmt.Errorf("judgment use session_id %q does not match effect attempt session_id %q", strings.TrimSpace(useInput.SessionID), sessionID)
	}
	return attemptInput, useInput, nil
}

func validateJudgmentStateValues(input session.JudgmentInput) error {
	if raw := stateToken(string(input.Completeness)); raw != "" {
		switch session.JudgmentCompleteness(raw) {
		case session.JudgmentCompletenessComplete, session.JudgmentCompletenessPartial, session.JudgmentCompletenessAbstain:
		default:
			return fmt.Errorf("judgment %s has invalid completeness %q", input.Kind, input.Completeness)
		}
	}
	return nil
}

func validateJudgmentUseStateValues(input session.JudgmentUseInput) error {
	if raw := stateToken(string(input.Consequence)); raw != "" {
		switch session.JudgmentUseConsequence(raw) {
		case session.JudgmentUseConsequenceAuthority,
			session.JudgmentUseConsequenceExecution,
			session.JudgmentUseConsequencePresentation,
			session.JudgmentUseConsequenceModelContextAdmission,
			session.JudgmentUseConsequenceRecoverySelection,
			session.JudgmentUseConsequenceCompletion,
			session.JudgmentUseConsequenceDurableState,
			session.JudgmentUseConsequenceDiagnostic,
			session.JudgmentUseConsequenceControlFlow:
		default:
			return fmt.Errorf("judgment use %s has invalid consequence %q", input.ConsumerID, input.Consequence)
		}
	}
	if raw := stateToken(string(input.QualificationStatus)); raw != "" {
		switch session.JudgmentUseQualificationStatus(raw) {
		case session.JudgmentUseQualificationQualified,
			session.JudgmentUseQualificationBlocked,
			session.JudgmentUseQualificationSuspended,
			session.JudgmentUseQualificationRejected,
			session.JudgmentUseQualificationNeedsVerification:
		default:
			return fmt.Errorf("judgment use %s has invalid qualification_status %q", input.ConsumerID, input.QualificationStatus)
		}
	}
	return validateReconciliationStatus(input.ReconciliationStatus)
}

func validateReconciliationStatus(status session.JudgmentUseReconciliationStatus) error {
	if raw := stateToken(string(status)); raw != "" {
		switch session.JudgmentUseReconciliationStatus(raw) {
		case session.JudgmentUseReconciliationNotRequired,
			session.JudgmentUseReconciliationPending,
			session.JudgmentUseReconciliationReconciled,
			session.JudgmentUseReconciliationEscalated:
		default:
			return fmt.Errorf("judgment use has invalid reconciliation_status %q", status)
		}
	}
	return nil
}

func validateChallengeEventStateValues(input session.JudgmentChallengeEventInput) error {
	if raw := stateToken(string(input.EventKind)); raw != "" {
		switch session.JudgmentChallengeEventKind(raw) {
		case session.JudgmentChallengeOpened,
			session.JudgmentChallengeGroundAttached,
			session.JudgmentChallengeAdjudicationRecorded,
			session.JudgmentChallengeOperationalResponseRecorded:
		default:
			return fmt.Errorf("judgment challenge event has invalid event_kind %q", input.EventKind)
		}
	}
	if raw := stateToken(string(input.Disposition)); raw != "" {
		switch session.JudgmentChallengeDisposition(raw) {
		case session.JudgmentChallengeSupported,
			session.JudgmentChallengeContradicted,
			session.JudgmentChallengeUnresolved:
		default:
			return fmt.Errorf("judgment challenge event has invalid disposition %q", input.Disposition)
		}
	}
	if raw := stateToken(string(input.EligibilityStatus)); raw != "" {
		switch session.JudgmentEligibilityStatus(raw) {
		case session.JudgmentEligibilityEligible,
			session.JudgmentEligibilitySuspended,
			session.JudgmentEligibilitySuperseded,
			session.JudgmentEligibilityExpired:
		default:
			return fmt.Errorf("judgment challenge event has invalid eligibility_status %q", input.EligibilityStatus)
		}
	}
	if raw := stateToken(string(input.OperationalResponse)); raw != "" {
		switch session.JudgmentOperationalResponse(raw) {
		case session.JudgmentOperationalResponseNone,
			session.JudgmentOperationalResponseRecompute,
			session.JudgmentOperationalResponseBlock,
			session.JudgmentOperationalResponseVerify,
			session.JudgmentOperationalResponseRetract,
			session.JudgmentOperationalResponseForwardCorrect,
			session.JudgmentOperationalResponseEscalate:
		default:
			return fmt.Errorf("judgment challenge event has invalid operational_response %q", input.OperationalResponse)
		}
	}
	return nil
}

func validateEffectAttemptStateValues(input session.EffectAttemptInput) error {
	if raw := strings.TrimSpace(string(input.Status)); raw != "" {
		switch session.EffectAttemptStatus(raw) {
		case "succeeded",
			session.EffectAttemptStatusAttempted,
			session.EffectAttemptStatusExecuted,
			session.EffectAttemptStatusFailed,
			session.EffectAttemptStatusUncertain,
			session.EffectAttemptStatusVerified,
			session.EffectAttemptStatusRejected,
			session.EffectAttemptStatusSuperseded:
		default:
			return fmt.Errorf("effect attempt %s has invalid status %q", input.AttemptID, input.Status)
		}
	}
	return nil
}

func stateToken(value string) string {
	return strings.TrimSpace(value)
}
