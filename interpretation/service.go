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

func (s Service) Available() bool {
	return s.store != nil
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
	judgment, err := s.RecordJudgment(judgmentInput)
	if err != nil {
		return session.Judgment{}, session.JudgmentUse{}, err
	}
	if len(useInput.JudgmentRefs) == 0 && strings.TrimSpace(judgment.ID) != "" {
		useInput.JudgmentRefs = []string{session.JudgmentRef(judgment.ID)}
	}
	use, err := s.RecordUse(useInput)
	if err != nil {
		return judgment, session.JudgmentUse{}, err
	}
	return judgment, use, nil
}

func (s Service) RecordEffectAttemptWithUse(attemptInput session.EffectAttemptInput, useInput session.JudgmentUseInput) (session.EffectAttempt, session.JudgmentUse, error) {
	if s.store == nil {
		return session.EffectAttempt{}, session.JudgmentUse{}, fmt.Errorf("interpretation store unavailable")
	}
	useInput, err := validateJudgmentUseInput(useInput)
	if err != nil {
		return session.EffectAttempt{}, session.JudgmentUse{}, err
	}
	return s.store.UpsertEffectAttemptWithJudgmentUse(attemptInput, useInput)
}

func (s Service) AppendChallengeEvent(input session.JudgmentChallengeEventInput) (session.JudgmentChallengeEvent, error) {
	if s.store == nil {
		return session.JudgmentChallengeEvent{}, fmt.Errorf("interpretation store unavailable")
	}
	return s.store.AppendJudgmentChallengeEvent(input)
}

func (s Service) MarkUsesForJudgmentReconciliation(judgmentID string, status session.JudgmentUseReconciliationStatus, reason string, at time.Time) error {
	if s.store == nil {
		return fmt.Errorf("interpretation store unavailable")
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
