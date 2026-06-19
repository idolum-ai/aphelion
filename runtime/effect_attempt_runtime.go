//go:build linux

package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/session"
)

type effectAttemptRecordError struct {
	Cause error
}

func (e effectAttemptRecordError) Error() string {
	if e.Cause == nil {
		return "effect attempt ledger write failed"
	}
	return "effect attempt ledger write failed: " + e.Cause.Error()
}

func (e effectAttemptRecordError) Unwrap() error {
	return e.Cause
}

func (r *Runtime) recordWorkResultEffectAttempts(key session.SessionKey, req WorkRequest, result WorkResult, cause error, startedAt time.Time, completedAt time.Time) ([]session.EffectAttempt, error) {
	if r == nil || r.store == nil {
		return nil, nil
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	existingByCommandHash := r.effectAttemptIDsByTurnCommandHash(key, result.TurnRunID)
	phaseID := effectAttemptPhaseID(req)
	commands := append([]string(nil), result.Commands...)
	for _, event := range result.CodexEvents {
		if strings.TrimSpace(event.Command) != "" {
			commands = appendUniqueRuntimeString(commands, event.Command)
		} else if event.Kind == "command" && strings.TrimSpace(event.Subject) != "" {
			commands = appendUniqueRuntimeString(commands, event.Subject)
		}
	}
	var attempts []session.EffectAttempt
	var writeErr error
	for _, command := range commands {
		rawCommand := strings.TrimSpace(command)
		command = commandeffect.NormalizeCommand(rawCommand)
		if rawCommand == "" {
			continue
		}
		effect := commandeffect.Classify(rawCommand)
		if !effect.SideEffects {
			continue
		}
		status := session.EffectAttemptStatusExecuted
		errorText := ""
		if cause != nil {
			status = session.EffectAttemptStatusUncertain
			errorText = trimError(cause.Error())
		}
		boundaryKind := ""
		if boundary, ok := commandeffect.BoundaryForCommand(rawCommand); ok {
			boundaryKind = string(boundary.Kind)
		}
		attemptID := workEffectAttemptID(key, req, command)
		if result.TurnRunID > 0 {
			if existingID := strings.TrimSpace(existingByCommandHash[session.EffectAttemptCommandHash(command)]); existingID != "" {
				attemptID = existingID
			}
		}
		attempt, err := r.store.UpsertEffectAttempt(session.EffectAttemptInput{
			AttemptID:    attemptID,
			Key:          key,
			TurnRunID:    result.TurnRunID,
			OperationID:  firstNonEmptyContinuation(req.OperationID, req.Operation.ID),
			PhaseID:      phaseID,
			LeaseID:      req.LeaseID,
			ProposalID:   req.State.ActionProposal.ID,
			WorkMode:     string(req.Mode),
			Executor:     firstRuntimeWorkNonEmpty(result.ExecutorName, "work"),
			Tool:         "work_executor",
			Command:      command,
			EffectKind:   string(effect.Kind),
			EffectReason: effect.Reason,
			BoundaryKind: boundaryKind,
			SubjectJSON:  effectAttemptSubjectJSON(rawCommand),
			Status:       status,
			ErrorText:    errorText,
			EvidenceRefs: workEffectAttemptEvidenceRefs(result),
			StartedAt:    startedAt,
			CompletedAt:  completedAt,
			UpdatedAt:    completedAt,
		})
		if err != nil {
			log.Printf("WARN record work effect attempt failed chat_id=%d command=%q err=%v", key.ChatID, command, err)
			writeErr = errors.Join(writeErr, err)
			continue
		}
		attempts = append(attempts, attempt)
	}
	if writeErr != nil {
		return attempts, effectAttemptRecordError{Cause: writeErr}
	}
	return attempts, nil
}

func (r *Runtime) effectAttemptIDsByTurnCommandHash(key session.SessionKey, turnRunID int64) map[string]string {
	if r == nil || r.store == nil || turnRunID <= 0 {
		return nil
	}
	existing, err := r.store.EffectAttemptsByTurnRun(key, turnRunID)
	if err != nil {
		log.Printf("WARN read turn effect attempts failed chat_id=%d turn_run_id=%d err=%v", key.ChatID, turnRunID, err)
		return nil
	}
	out := make(map[string]string, len(existing))
	for _, attempt := range existing {
		if strings.TrimSpace(attempt.AttemptID) == "" || !session.EffectAttemptHasSideEffects(attempt) {
			continue
		}
		hash := strings.TrimSpace(attempt.CommandHash)
		if hash == "" {
			hash = session.EffectAttemptCommandHash(attempt.Command)
		}
		if hash != "" {
			out[hash] = attempt.AttemptID
		}
	}
	return out
}

func (r *Runtime) attachEffectAttemptsToWorkResult(key session.SessionKey, req WorkRequest, result *WorkResult) {
	if r == nil || r.store == nil || result == nil {
		return
	}
	var attempts []session.EffectAttempt
	var err error
	if result.TurnRunID > 0 {
		attempts, err = r.store.EffectAttemptsByTurnRun(key, result.TurnRunID)
	} else {
		attempts, err = r.store.EffectAttemptsForWork(key, firstNonEmptyContinuation(req.OperationID, req.Operation.ID), effectAttemptPhaseID(req), req.LeaseID, req.State.ActionProposal.ID)
	}
	if err != nil {
		log.Printf("WARN read effect attempts for work result failed chat_id=%d err=%v", key.ChatID, err)
		return
	}
	for _, attempt := range attempts {
		if strings.TrimSpace(attempt.Command) != "" {
			result.Commands = appendUniqueRuntimeWorkString(result.Commands, attempt.Command)
		}
		if session.EffectAttemptHasSideEffects(attempt) {
			result.SideEffects = true
		}
		if attempt.Status == session.EffectAttemptStatusFailed || attempt.Status == session.EffectAttemptStatusRejected {
			result.ToolFailures++
			if attempt.ErrorText != "" {
				result.ToolFailureTexts = appendUniqueRuntimeWorkString(result.ToolFailureTexts, attempt.ErrorText)
			}
		}
	}
}

func (r *Runtime) unresolvedEffectAttemptsForRequest(key session.SessionKey, req WorkRequest) []session.EffectAttempt {
	if r == nil || r.store == nil {
		return nil
	}
	attempts, err := r.store.UnresolvedSideEffectAttemptsForWork(key, firstNonEmptyContinuation(req.OperationID, req.Operation.ID), effectAttemptPhaseID(req), req.LeaseID, req.State.ActionProposal.ID)
	if err != nil {
		log.Printf("WARN read unresolved effect attempts failed chat_id=%d err=%v", key.ChatID, err)
		return nil
	}
	return attempts
}

func (r *Runtime) markEffectAttemptsForRequest(key session.SessionKey, req WorkRequest, status session.EffectAttemptStatus, errorText string, now time.Time) {
	if r == nil || r.store == nil {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	attempts, err := r.store.EffectAttemptsForWork(key, firstNonEmptyContinuation(req.OperationID, req.Operation.ID), effectAttemptPhaseID(req), req.LeaseID, req.State.ActionProposal.ID)
	if err != nil {
		log.Printf("WARN read effect attempts for mark failed chat_id=%d err=%v", key.ChatID, err)
		return
	}
	for _, attempt := range attempts {
		if !session.EffectAttemptHasSideEffects(attempt) {
			continue
		}
		if _, err := r.store.UpsertEffectAttempt(session.EffectAttemptInput{
			AttemptID:    attempt.AttemptID,
			Key:          key,
			TurnRunID:    attempt.TurnRunID,
			OperationID:  attempt.OperationID,
			PhaseID:      attempt.PhaseID,
			LeaseID:      attempt.LeaseID,
			ProposalID:   attempt.ProposalID,
			WorkMode:     attempt.WorkMode,
			Executor:     attempt.Executor,
			Tool:         attempt.Tool,
			Command:      attempt.Command,
			EffectKind:   attempt.EffectKind,
			EffectReason: attempt.EffectReason,
			BoundaryKind: attempt.BoundaryKind,
			SubjectJSON:  attempt.SubjectJSON,
			Status:       status,
			ErrorText:    errorText,
			EvidenceRefs: append(attempt.EvidenceRefs, "work_outcome_resolution"),
			StartedAt:    attempt.StartedAt,
			CompletedAt:  now,
			UpdatedAt:    now,
		}); err != nil {
			log.Printf("WARN mark effect attempt failed chat_id=%d attempt=%s err=%v", key.ChatID, attempt.AttemptID, err)
		}
	}
}

func effectAttemptPhaseID(req WorkRequest) string {
	op := session.NormalizeOperationState(req.Operation)
	leaseID := strings.TrimSpace(req.LeaseID)
	if leaseID != "" {
		for _, phase := range op.PhasePlan.Phases {
			if strings.TrimSpace(phase.LeaseID) == leaseID {
				return strings.TrimSpace(phase.ID)
			}
		}
	}
	state := session.NormalizeContinuationState(req.State)
	if phase, ok := session.CurrentContinuationApprovalBundlePhase(state.ApprovalBundle); ok {
		return firstNonEmptyContinuation(phase.OperationPhaseID, phase.ID)
	}
	return ""
}

func workEffectAttemptID(key session.SessionKey, req WorkRequest, command string) string {
	seed := strings.Join([]string{
		session.SessionIDForKey(key),
		firstNonEmptyContinuation(req.OperationID, req.Operation.ID),
		effectAttemptPhaseID(req),
		strings.TrimSpace(req.LeaseID),
		strings.TrimSpace(req.State.ActionProposal.ID),
		string(req.Mode),
		session.EffectAttemptCommandHash(command),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "eff_" + hex.EncodeToString(sum[:16])
}

func workEffectAttemptEvidenceRefs(result WorkResult) []string {
	var refs []string
	if result.TurnRunID > 0 {
		refs = append(refs, fmt.Sprintf("turn_run:%d", result.TurnRunID))
	}
	if strings.TrimSpace(result.ThreadID) != "" {
		refs = append(refs, "codex_thread:"+strings.TrimSpace(result.ThreadID))
	}
	if strings.TrimSpace(result.TurnID) != "" {
		refs = append(refs, "codex_turn:"+strings.TrimSpace(result.TurnID))
	}
	return refs
}
