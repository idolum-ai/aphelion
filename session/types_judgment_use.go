//go:build linux

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type JudgmentUseConsequence string

const (
	JudgmentUseConsequenceAuthority             JudgmentUseConsequence = "authority"
	JudgmentUseConsequenceExecution             JudgmentUseConsequence = "execution"
	JudgmentUseConsequencePresentation          JudgmentUseConsequence = "presentation"
	JudgmentUseConsequenceModelContextAdmission JudgmentUseConsequence = "model_context_admission"
	JudgmentUseConsequenceRecoverySelection     JudgmentUseConsequence = "recovery_selection"
	JudgmentUseConsequenceCompletion            JudgmentUseConsequence = "completion"
	JudgmentUseConsequenceDurableState          JudgmentUseConsequence = "durable_state"
	JudgmentUseConsequenceDiagnostic            JudgmentUseConsequence = "diagnostic"
	JudgmentUseConsequenceControlFlow           JudgmentUseConsequence = "control_flow"
)

type JudgmentUseQualificationStatus string

const (
	JudgmentUseQualificationQualified         JudgmentUseQualificationStatus = "qualified"
	JudgmentUseQualificationBlocked           JudgmentUseQualificationStatus = "blocked"
	JudgmentUseQualificationSuspended         JudgmentUseQualificationStatus = "suspended"
	JudgmentUseQualificationRejected          JudgmentUseQualificationStatus = "rejected"
	JudgmentUseQualificationNeedsVerification JudgmentUseQualificationStatus = "needs_verification"
)

type JudgmentUseReconciliationStatus string

const (
	JudgmentUseReconciliationNotRequired JudgmentUseReconciliationStatus = "not_required"
	JudgmentUseReconciliationPending     JudgmentUseReconciliationStatus = "pending"
	JudgmentUseReconciliationReconciled  JudgmentUseReconciliationStatus = "reconciled"
	JudgmentUseReconciliationEscalated   JudgmentUseReconciliationStatus = "escalated"
)

type JudgmentDependencyRef struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Role  string `json:"role,omitempty"`
	Hash  string `json:"hash,omitempty"`
	Scope string `json:"scope,omitempty"`
}

type JudgmentUse struct {
	ID                   string
	SessionID            string
	ChatID               int64
	UserID               int64
	Scope                ScopeRef
	TurnRunID            int64
	OperationID          string
	PhaseID              string
	LeaseID              string
	ProposalID           string
	ConsumerID           string
	Consequence          JudgmentUseConsequence
	JudgmentRefs         []string
	DependencyRefs       []JudgmentDependencyRef
	PolicyRef            string
	DependencySnapshot   string
	ResultRef            string
	Irreversible         bool
	QualificationStatus  JudgmentUseQualificationStatus
	ReconciliationStatus JudgmentUseReconciliationStatus
	Reason               string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type JudgmentUseInput struct {
	ID                   string
	Key                  SessionKey
	SessionID            string
	TurnRunID            int64
	OperationID          string
	PhaseID              string
	LeaseID              string
	ProposalID           string
	ConsumerID           string
	Consequence          JudgmentUseConsequence
	JudgmentRefs         []string
	DependencyRefs       []JudgmentDependencyRef
	PolicyRef            string
	DependencySnapshot   string
	ResultRef            string
	Irreversible         bool
	QualificationStatus  JudgmentUseQualificationStatus
	ReconciliationStatus JudgmentUseReconciliationStatus
	Reason               string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func JudgmentUseRef(kind string, ref string) string {
	kind = judgmentUseToken(kind)
	ref = strings.TrimSpace(ref)
	if kind == "" || ref == "" {
		return ""
	}
	return kind + ":" + ref
}

func JudgmentUseHashRef(kind string, seed string) string {
	kind = judgmentUseToken(kind)
	seed = strings.TrimSpace(seed)
	if kind == "" || seed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(seed))
	return kind + ":" + hex.EncodeToString(sum[:])[:24]
}

func NormalizeJudgmentUseInput(input JudgmentUseInput) (JudgmentUseInput, error) {
	input.SessionID = strings.TrimSpace(input.SessionID)
	if input.SessionID == "" {
		input.SessionID = SessionIDForKey(input.Key)
	}
	input.OperationID = strings.TrimSpace(input.OperationID)
	input.PhaseID = strings.TrimSpace(input.PhaseID)
	input.LeaseID = strings.TrimSpace(input.LeaseID)
	input.ProposalID = strings.TrimSpace(input.ProposalID)
	input.ConsumerID = judgmentUseToken(input.ConsumerID)
	input.Consequence = NormalizeJudgmentUseConsequence(input.Consequence)
	input.JudgmentRefs = normalizeStringList(input.JudgmentRefs)
	input.DependencyRefs = normalizeJudgmentDependencyRefs(input.DependencyRefs)
	input.PolicyRef = strings.TrimSpace(input.PolicyRef)
	input.ResultRef = strings.TrimSpace(input.ResultRef)
	input.QualificationStatus = NormalizeJudgmentUseQualification(input.QualificationStatus)
	input.ReconciliationStatus = NormalizeJudgmentUseReconciliation(input.ReconciliationStatus)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.UpdatedAt.IsZero() {
		input.UpdatedAt = time.Now().UTC()
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = input.UpdatedAt
	}
	input.DependencySnapshot = strings.TrimSpace(input.DependencySnapshot)
	if input.DependencySnapshot == "" {
		input.DependencySnapshot = JudgmentUseDependencySnapshot(input.JudgmentRefs, input.DependencyRefs)
	}
	if input.SessionID == "" {
		return JudgmentUseInput{}, fmt.Errorf("judgment use requires session_id")
	}
	if input.ConsumerID == "" {
		return JudgmentUseInput{}, fmt.Errorf("judgment use requires consumer_id")
	}
	if input.Consequence == "" {
		return JudgmentUseInput{}, fmt.Errorf("judgment use requires consequence")
	}
	if len(input.JudgmentRefs) == 0 {
		return JudgmentUseInput{}, fmt.Errorf("judgment use requires at least one judgment ref")
	}
	if strings.TrimSpace(input.ID) == "" {
		input.ID = judgmentUseID(input)
	}
	input.ID = strings.TrimSpace(input.ID)
	return input, nil
}

func NormalizeJudgmentUseConsequence(value JudgmentUseConsequence) JudgmentUseConsequence {
	switch JudgmentUseConsequence(judgmentUseToken(string(value))) {
	case JudgmentUseConsequenceAuthority,
		JudgmentUseConsequenceExecution,
		JudgmentUseConsequencePresentation,
		JudgmentUseConsequenceModelContextAdmission,
		JudgmentUseConsequenceRecoverySelection,
		JudgmentUseConsequenceCompletion,
		JudgmentUseConsequenceDurableState,
		JudgmentUseConsequenceDiagnostic,
		JudgmentUseConsequenceControlFlow:
		return JudgmentUseConsequence(judgmentUseToken(string(value)))
	default:
		return ""
	}
}

func NormalizeJudgmentUseQualification(value JudgmentUseQualificationStatus) JudgmentUseQualificationStatus {
	switch JudgmentUseQualificationStatus(judgmentUseToken(string(value))) {
	case JudgmentUseQualificationBlocked,
		JudgmentUseQualificationSuspended,
		JudgmentUseQualificationRejected,
		JudgmentUseQualificationNeedsVerification:
		return JudgmentUseQualificationStatus(judgmentUseToken(string(value)))
	default:
		return JudgmentUseQualificationQualified
	}
}

func NormalizeJudgmentUseReconciliation(value JudgmentUseReconciliationStatus) JudgmentUseReconciliationStatus {
	switch JudgmentUseReconciliationStatus(judgmentUseToken(string(value))) {
	case JudgmentUseReconciliationPending,
		JudgmentUseReconciliationReconciled,
		JudgmentUseReconciliationEscalated:
		return JudgmentUseReconciliationStatus(judgmentUseToken(string(value)))
	default:
		return JudgmentUseReconciliationNotRequired
	}
}

func JudgmentUseDependencySnapshot(judgmentRefs []string, dependencyRefs []JudgmentDependencyRef) string {
	payload := struct {
		JudgmentRefs   []string                `json:"judgment_refs"`
		DependencyRefs []JudgmentDependencyRef `json:"dependency_refs"`
	}{
		JudgmentRefs:   normalizeStringList(judgmentRefs),
		DependencyRefs: normalizeJudgmentDependencyRefs(dependencyRefs),
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func judgmentUseID(input JudgmentUseInput) string {
	seed := strings.Join([]string{
		input.SessionID,
		fmt.Sprintf("%d", input.TurnRunID),
		input.OperationID,
		input.PhaseID,
		input.LeaseID,
		input.ProposalID,
		input.ConsumerID,
		string(input.Consequence),
		input.ResultRef,
		input.PolicyRef,
		input.DependencySnapshot,
		strings.Join(input.JudgmentRefs, ","),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "juse_" + hex.EncodeToString(sum[:16])
}

func normalizeJudgmentDependencyRefs(refs []JudgmentDependencyRef) []JudgmentDependencyRef {
	out := make([]JudgmentDependencyRef, 0, len(refs))
	seen := map[string]struct{}{}
	for _, ref := range refs {
		ref.Kind = judgmentUseToken(ref.Kind)
		ref.Ref = strings.TrimSpace(ref.Ref)
		ref.Role = judgmentUseToken(ref.Role)
		ref.Hash = strings.TrimSpace(ref.Hash)
		ref.Scope = strings.TrimSpace(ref.Scope)
		if ref.Kind == "" || ref.Ref == "" {
			continue
		}
		key := strings.Join([]string{ref.Kind, ref.Ref, ref.Role, ref.Hash, ref.Scope}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func encodeJudgmentDependencyRefs(refs []JudgmentDependencyRef) string {
	raw, err := json.Marshal(normalizeJudgmentDependencyRefs(refs))
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func decodeJudgmentDependencyRefs(raw string) []JudgmentDependencyRef {
	var refs []JudgmentDependencyRef
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &refs); err != nil {
		return nil
	}
	return normalizeJudgmentDependencyRefs(refs)
}

func judgmentUseToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-' || r == '.' || r == ':':
			b.WriteRune(r)
		case r == ' ' || r == '/' || r == '\\':
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "_")
}
