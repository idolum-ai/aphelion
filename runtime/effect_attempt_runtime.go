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

const (
	workCommandEvidenceSourceEffectAttempt     = "effect_attempt"
	workCommandEvidenceSourceExecutionEvent    = "execution_event"
	workCommandEvidenceSourceWorkResultCommand = "work_result_command"
	workCommandEvidenceSourceCodexEvent        = "codex_event"
)

type workEffectAttemptIndex struct {
	byID              map[string]session.EffectAttempt
	turnByCommandHash map[string][]string
	workByCommandHash map[string][]string
}

type workEffectAttemptUpdate struct {
	attemptID    string
	command      string
	effectKind   string
	effectReason string
	boundaryKind string
	subjectJSON  string
	evidenceRefs []string
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
	index := r.workEffectAttemptIndex(key, req, result.TurnRunID)
	phaseID := effectAttemptPhaseID(req)
	commandEvidence := workResultCommandEvidence(result)
	updatesByID := map[string]*workEffectAttemptUpdate{}
	var updateOrder []string
	matchedByHash := map[string]string{}
	consumedIDs := map[string]struct{}{}
	var attempts []session.EffectAttempt
	var writeErr error
	for _, evidence := range commandEvidence {
		evidence, ok := normalizeWorkCommandEvidence(evidence)
		if !ok {
			continue
		}
		rawCommand := strings.TrimSpace(evidence.Command)
		var existing session.EffectAttempt
		existingID := ""
		if id := strings.TrimSpace(evidence.EffectAttemptID); id != "" {
			if attempt, ok := index.byID[id]; ok {
				existing = attempt
				existingID = attempt.AttemptID
			}
		}
		if rawCommand == "" && strings.TrimSpace(existing.Command) != "" {
			rawCommand = existing.Command
		}
		command := redactRuntimeEvidenceText(commandeffect.NormalizeCommand(rawCommand))
		if command == "" {
			continue
		}
		commandHash := strings.TrimSpace(evidence.CommandHash)
		if commandHash == "" {
			commandHash = session.EffectAttemptCommandHash(command)
		}
		effect := commandeffect.Classify(rawCommand)
		if existingID == "" {
			if attempt, ok := consumeWorkEffectAttemptByHash(&index, commandHash, consumedIDs); ok {
				existing = attempt
				existingID = attempt.AttemptID
			}
		} else {
			consumedIDs[existingID] = struct{}{}
		}
		if existingID == "" {
			if priorID := matchedByHash[commandHash]; priorID != "" {
				if update := updatesByID[priorID]; update != nil {
					update.evidenceRefs = appendUniqueRuntimeWorkStrings(update.evidenceRefs, workEffectAttemptEvidenceRefs(result, evidence)...)
				}
				continue
			}
			if !effect.SideEffects {
				continue
			}
			err := fmt.Errorf("missing pre-dispatch effect attempt for command_hash=%s", commandHash)
			log.Printf("WARN record work effect attempt refused first-write-after-result chat_id=%d command_hash=%s", key.ChatID, commandHash)
			writeErr = errors.Join(writeErr, err)
			continue
		}
		if !effect.SideEffects && !session.EffectAttemptHasSideEffects(existing) {
			continue
		}
		if strings.TrimSpace(existing.Command) != "" {
			rawCommand = existing.Command
			command = existing.Command
		}
		if strings.TrimSpace(existing.CommandHash) != "" {
			commandHash = strings.TrimSpace(existing.CommandHash)
		}
		matchedByHash[commandHash] = existingID
		if update := updatesByID[existingID]; update != nil {
			update.evidenceRefs = appendUniqueRuntimeWorkStrings(update.evidenceRefs, workEffectAttemptEvidenceRefs(result, evidence)...)
			continue
		}
		effectKind := string(effect.Kind)
		effectReason := effect.Reason
		boundaryKind := ""
		if boundary, ok := commandeffect.BoundaryForCommand(rawCommand); ok {
			boundaryKind = string(boundary.Kind)
		}
		if strings.TrimSpace(existing.EffectKind) != "" {
			effectKind = strings.TrimSpace(existing.EffectKind)
			effectReason = strings.TrimSpace(existing.EffectReason)
			boundaryKind = strings.TrimSpace(existing.BoundaryKind)
		}
		subjectJSON := effectAttemptSubjectJSON(rawCommand)
		if existingSubject := strings.TrimSpace(existing.SubjectJSON); existingSubject != "" && existingSubject != "{}" {
			subjectJSON = existing.SubjectJSON
		}
		updatesByID[existingID] = &workEffectAttemptUpdate{
			attemptID:    existingID,
			command:      command,
			effectKind:   effectKind,
			effectReason: effectReason,
			boundaryKind: boundaryKind,
			subjectJSON:  subjectJSON,
			evidenceRefs: workEffectAttemptEvidenceRefs(result, evidence),
		}
		updateOrder = append(updateOrder, existingID)
	}
	for _, attemptID := range updateOrder {
		update := updatesByID[attemptID]
		if update == nil {
			continue
		}
		status := session.EffectAttemptStatusExecuted
		errorText := ""
		if cause != nil {
			status = session.EffectAttemptStatusUncertain
			errorText = redactRuntimeEvidenceText(trimError(cause.Error()))
		}
		attempt, err := r.store.UpsertEffectAttempt(session.EffectAttemptInput{
			AttemptID:    update.attemptID,
			Key:          key,
			TurnRunID:    result.TurnRunID,
			OperationID:  firstNonEmptyContinuation(req.OperationID, req.Operation.ID),
			PhaseID:      phaseID,
			LeaseID:      req.LeaseID,
			ProposalID:   req.State.ActionProposal.ID,
			WorkMode:     string(req.Mode),
			Executor:     firstRuntimeWorkNonEmpty(result.ExecutorName, "work"),
			Tool:         "work_executor",
			Command:      update.command,
			EffectKind:   update.effectKind,
			EffectReason: update.effectReason,
			BoundaryKind: update.boundaryKind,
			SubjectJSON:  update.subjectJSON,
			Status:       status,
			ErrorText:    errorText,
			EvidenceRefs: update.evidenceRefs,
			StartedAt:    startedAt,
			CompletedAt:  completedAt,
			UpdatedAt:    completedAt,
		})
		if err != nil {
			log.Printf("WARN record work effect attempt failed chat_id=%d command_hash=%s err=%v", key.ChatID, session.EffectAttemptCommandHash(update.command), err)
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

func workResultCommandEvidence(result WorkResult) []WorkCommandEvidence {
	if len(result.CommandEvidence) > 0 {
		return append([]WorkCommandEvidence(nil), result.CommandEvidence...)
	}
	var evidence []WorkCommandEvidence
	seenHashes := map[string]struct{}{}
	for _, command := range result.Commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		hash := runtimeWorkCommandHash(command)
		evidence = append(evidence, WorkCommandEvidence{
			Command:     command,
			CommandHash: hash,
			Source:      workCommandEvidenceSourceWorkResultCommand,
		})
		if hash != "" {
			seenHashes[hash] = struct{}{}
		}
	}
	for _, event := range result.CodexEvents {
		command := strings.TrimSpace(event.Command)
		if command == "" && event.Kind == "command" {
			command = strings.TrimSpace(event.Subject)
		}
		if command == "" {
			continue
		}
		hash := runtimeWorkCommandHash(command)
		if _, ok := seenHashes[hash]; ok {
			continue
		}
		evidence = append(evidence, WorkCommandEvidence{
			Command:     command,
			CommandHash: hash,
			Source:      workCommandEvidenceSourceCodexEvent,
		})
		if hash != "" {
			seenHashes[hash] = struct{}{}
		}
	}
	return evidence
}

func normalizeWorkCommandEvidence(evidence WorkCommandEvidence) (WorkCommandEvidence, bool) {
	evidence.Command = strings.TrimSpace(evidence.Command)
	evidence.CommandHash = strings.TrimSpace(evidence.CommandHash)
	if evidence.CommandHash == "" && evidence.Command != "" {
		evidence.CommandHash = runtimeWorkCommandHash(evidence.Command)
	}
	evidence.EffectAttemptID = strings.TrimSpace(evidence.EffectAttemptID)
	evidence.Source = strings.TrimSpace(evidence.Source)
	evidence.EvidenceRefs = appendUniqueRuntimeWorkStrings(nil, evidence.EvidenceRefs...)
	return evidence, evidence.Command != "" || evidence.CommandHash != "" || evidence.EffectAttemptID != ""
}

func runtimeWorkCommandHash(command string) string {
	// Fuzzy evidence-correlation key only. This inherits NormalizeCommand's
	// whitespace compaction and must not stand in for exact shell identity,
	// authority, or proof that two command strings are equivalent.
	command = redactRuntimeEvidenceText(commandeffect.NormalizeCommand(strings.TrimSpace(command)))
	if command == "" {
		return ""
	}
	return session.EffectAttemptCommandHash(command)
}

func appendWorkCommandEvidence(result *WorkResult, evidence WorkCommandEvidence) {
	if result == nil {
		return
	}
	evidence, ok := normalizeWorkCommandEvidence(evidence)
	if !ok {
		return
	}
	result.CommandEvidence = append(result.CommandEvidence, evidence)
	appendWorkResultCommand(result, evidence.Command)
}

func appendWorkResultCommand(result *WorkResult, command string) {
	if result == nil {
		return
	}
	command = strings.TrimSpace(command)
	hash := runtimeWorkCommandHash(command)
	if hash == "" {
		return
	}
	for _, existing := range result.Commands {
		if runtimeWorkCommandHash(existing) == hash {
			return
		}
	}
	result.Commands = append(result.Commands, command)
}

func appendUniqueRuntimeWorkStrings(dst []string, src ...string) []string {
	for _, item := range src {
		dst = appendUniqueRuntimeWorkString(dst, item)
	}
	return dst
}

func (r *Runtime) workEffectAttemptIndex(key session.SessionKey, req WorkRequest, turnRunID int64) workEffectAttemptIndex {
	index := workEffectAttemptIndex{
		byID:              map[string]session.EffectAttempt{},
		turnByCommandHash: map[string][]string{},
		workByCommandHash: map[string][]string{},
	}
	if r == nil || r.store == nil {
		return index
	}
	if turnRunID > 0 {
		existing, err := r.store.EffectAttemptsByTurnRun(key, turnRunID)
		if err != nil {
			log.Printf("WARN read turn effect attempts failed chat_id=%d turn_run_id=%d err=%v", key.ChatID, turnRunID, err)
		} else {
			addWorkEffectAttemptsToIndex(&index, existing, index.turnByCommandHash)
		}
	}
	existing, err := r.store.EffectAttemptsForWork(key, firstNonEmptyContinuation(req.OperationID, req.Operation.ID), effectAttemptPhaseID(req), req.LeaseID, req.State.ActionProposal.ID)
	if err != nil {
		log.Printf("WARN read work effect attempts failed chat_id=%d err=%v", key.ChatID, err)
		return index
	}
	addWorkEffectAttemptsToIndex(&index, existing, index.workByCommandHash)
	return index
}

func addWorkEffectAttemptsToIndex(index *workEffectAttemptIndex, attempts []session.EffectAttempt, byHash map[string][]string) {
	if index == nil {
		return
	}
	for _, attempt := range attempts {
		if strings.TrimSpace(attempt.AttemptID) == "" || !session.EffectAttemptHasSideEffects(attempt) {
			continue
		}
		index.byID[attempt.AttemptID] = attempt
		hash := strings.TrimSpace(attempt.CommandHash)
		if hash == "" {
			hash = runtimeWorkCommandHash(attempt.Command)
		}
		if hash != "" && byHash != nil {
			byHash[hash] = append(byHash[hash], attempt.AttemptID)
		}
	}
}

func workEffectAttemptIDQueuesByHash(attempts []session.EffectAttempt) map[string][]string {
	queues := map[string][]string{}
	addWorkEffectAttemptsToIndex(&workEffectAttemptIndex{byID: map[string]session.EffectAttempt{}}, attempts, queues)
	return queues
}

func consumeWorkEffectAttemptByHash(index *workEffectAttemptIndex, commandHash string, consumed map[string]struct{}) (session.EffectAttempt, bool) {
	if index == nil {
		return session.EffectAttempt{}, false
	}
	id := consumeWorkEffectAttemptIDByHash(index.turnByCommandHash, commandHash, consumed)
	if id == "" {
		id = consumeWorkEffectAttemptIDByHash(index.workByCommandHash, commandHash, consumed)
	}
	if id == "" {
		return session.EffectAttempt{}, false
	}
	attempt, ok := index.byID[id]
	return attempt, ok
}

func consumeWorkEffectAttemptIDByHash(byHash map[string][]string, commandHash string, consumed map[string]struct{}) string {
	commandHash = strings.TrimSpace(commandHash)
	if commandHash == "" || len(byHash) == 0 {
		return ""
	}
	for {
		queue := byHash[commandHash]
		if len(queue) == 0 {
			return ""
		}
		id := strings.TrimSpace(queue[0])
		byHash[commandHash] = queue[1:]
		if id == "" {
			continue
		}
		if consumed != nil {
			if _, ok := consumed[id]; ok {
				continue
			}
			consumed[id] = struct{}{}
		}
		return id
	}
}

func (r *Runtime) attachEffectAttemptsToWorkResult(key session.SessionKey, req WorkRequest, result *WorkResult) []session.EffectAttempt {
	if r == nil || r.store == nil || result == nil {
		return nil
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
		return nil
	}
	for _, attempt := range attempts {
		if strings.TrimSpace(attempt.Command) != "" {
			appendWorkCommandEvidence(result, WorkCommandEvidence{
				Command:         attempt.Command,
				CommandHash:     attempt.CommandHash,
				EffectAttemptID: attempt.AttemptID,
				Source:          workCommandEvidenceSourceEffectAttempt,
				EvidenceRefs:    attempt.EvidenceRefs,
			})
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
	return attempts
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
	errorText = redactRuntimeEvidenceText(trimError(errorText))
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

func workEffectAttemptEvidenceRefs(result WorkResult, evidence WorkCommandEvidence) []string {
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
	if strings.TrimSpace(evidence.Source) != "" {
		refs = append(refs, "work_command_evidence:"+strings.TrimSpace(evidence.Source))
	}
	refs = append(refs, evidence.EvidenceRefs...)
	return appendUniqueRuntimeWorkStrings(nil, refs...)
}
