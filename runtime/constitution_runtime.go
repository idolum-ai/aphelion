//go:build linux

package runtime

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/turn"
)

func (r *Runtime) applyTurnConstitution(
	ctx context.Context,
	key session.SessionKey,
	scope sandbox.Scope,
	channel string,
	principalRole string,
	userText string,
	currentFaceModel face.Renderer,
	faceAwareness prompt.RuntimeAwareness,
	materialFloor core.MaterialPacket,
	floorText string,
	replyText string,
	media []core.Media,
	audit *turnAuditRecorder,
) string {
	if audit != nil {
		audit.RecordFinalReply(replyText, media, "")
	}
	trimmedReply := strings.TrimSpace(replyText)
	adjudication := r.adjudicateFinalReplyExecutionClaimsWithContext(ctx, key, trimmedReply)
	if adjudication.HasFindings() {
		r.recordExecutionClaimAdjudication(key, adjudication, "repair_requested")
		violations := adjudication.ConstitutionViolations()
		if audit != nil {
			audit.RecordViolations(violations)
			audit.RecordExecutionClaimFindings(adjudication.Findings)
		}
		if repaired, ok := r.repairTurnReply(ctx, scope, channel, principalRole, userText, currentFaceModel, faceAwareness, materialFloor, floorText, trimmedReply, media, violations, []core.RuntimeAdjudication{adjudication.RuntimeAdjudication("repair_requested")}, audit); ok {
			repairedAdjudication := r.adjudicateFinalReplyExecutionClaimsWithContext(ctx, key, repaired)
			if !repairedAdjudication.HasFindings() {
				trimmedReply = strings.TrimSpace(repaired)
				r.recordExecutionClaimAdjudication(key, repairedAdjudication.WithPrior(adjudication), "persona_repaired")
			} else {
				trimmedReply = neutralizeUnsupportedExecutionClaims(repaired, repairedAdjudication)
				r.recordExecutionClaimAdjudication(key, repairedAdjudication, "fallback_neutralized")
			}
		} else {
			trimmedReply = neutralizeUnsupportedExecutionClaims(trimmedReply, adjudication)
			r.recordExecutionClaimAdjudication(key, adjudication, "fallback_neutralized")
		}
	}
	if r == nil || r.constitutionGate == nil {
		return trimmedReply
	}

	baseSnapshot := TurnAudit{
		Channel:         strings.TrimSpace(channel),
		PrincipalRole:   strings.TrimSpace(principalRole),
		UserText:        strings.TrimSpace(userText),
		FinalReplyText:  trimmedReply,
		FinalReplyMedia: cloneAuditMedia(media),
	}
	if audit != nil {
		baseSnapshot = audit.Snapshot()
		baseSnapshot.FinalReplyText = trimmedReply
		baseSnapshot.FinalReplyMedia = cloneAuditMedia(media)
	}

	validateCandidate := func(candidateText string, candidateMedia []core.Media) []ConstitutionViolation {
		candidate := baseSnapshot
		candidate.FinalReplyText = strings.TrimSpace(candidateText)
		candidate.FinalReplyMedia = cloneAuditMedia(candidateMedia)
		return r.constitutionGate.ValidateFinal(candidate)
	}

	return turn.RunConstitutionStage(ctx, turn.ConstitutionStageInput{
		ReplyText: trimmedReply,
		Media:     media,
	}, turn.ConstitutionStageCallbacks{
		Validate: validateCandidate,
		Repair: func(ctx context.Context, candidateText string, candidateMedia []core.Media, violations []ConstitutionViolation) (string, bool) {
			return r.repairTurnReply(
				ctx,
				scope,
				channel,
				principalRole,
				userText,
				currentFaceModel,
				faceAwareness,
				materialFloor,
				floorText,
				candidateText,
				candidateMedia,
				violations,
				nil,
				audit,
			)
		},
		RecordViolations: func(violations []ConstitutionViolation) {
			if audit != nil {
				audit.RecordViolations(violations)
			}
		},
	})
}

type executionClaimAdjudication struct {
	Findings                         []ExecutionClaimFinding
	Interpretation                   []core.InterpretationClaim
	LatestTurnSeq                    int64
	LatestStatus                     string
	LatestTerminalAt                 string
	HasToolEvidence                  bool
	HasTestEvidence                  bool
	HasDurableEvidence               bool
	HasActiveContinuationAuthority   bool
	HasPendingContinuationApproval   bool
	HasMaterializableOperationFollow bool
}

func (a executionClaimAdjudication) HasFindings() bool {
	return len(a.Findings) > 0
}

func (a executionClaimAdjudication) Note() string {
	if len(a.Findings) == 0 {
		return ""
	}
	parts := make([]string, 0, len(a.Findings))
	for _, finding := range a.Findings {
		if detail := strings.TrimSpace(finding.Detail); detail != "" {
			parts = append(parts, detail)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "execution claims are not grounded by TES: " + strings.Join(parts, "; ")
}

func (a executionClaimAdjudication) ConstitutionViolations() []ConstitutionViolation {
	if len(a.Findings) == 0 {
		return nil
	}
	violations := make([]ConstitutionViolation, 0, len(a.Findings))
	for _, finding := range a.Findings {
		detail := strings.TrimSpace(finding.RequiredBehavior)
		if detail == "" {
			detail = strings.TrimSpace(finding.Detail)
		}
		if detail == "" {
			continue
		}
		violations = append(violations, ConstitutionViolation{
			Rule:    constitutionRuleExecutionClaimUngrounded,
			Surface: "final_reply",
			Detail:  detail,
		})
	}
	return violations
}

func (a executionClaimAdjudication) WithPrior(prior executionClaimAdjudication) executionClaimAdjudication {
	if len(a.Findings) == 0 {
		a.Findings = append([]ExecutionClaimFinding(nil), prior.Findings...)
	}
	if len(a.Interpretation) == 0 {
		a.Interpretation = append([]core.InterpretationClaim(nil), prior.Interpretation...)
	}
	if a.LatestTurnSeq == 0 {
		a.LatestTurnSeq = prior.LatestTurnSeq
	}
	if a.LatestStatus == "" {
		a.LatestStatus = prior.LatestStatus
	}
	if a.LatestTerminalAt == "" {
		a.LatestTerminalAt = prior.LatestTerminalAt
	}
	a.HasToolEvidence = a.HasToolEvidence || prior.HasToolEvidence
	a.HasTestEvidence = a.HasTestEvidence || prior.HasTestEvidence
	a.HasDurableEvidence = a.HasDurableEvidence || prior.HasDurableEvidence
	a.HasActiveContinuationAuthority = a.HasActiveContinuationAuthority || prior.HasActiveContinuationAuthority
	a.HasPendingContinuationApproval = a.HasPendingContinuationApproval || prior.HasPendingContinuationApproval
	a.HasMaterializableOperationFollow = a.HasMaterializableOperationFollow || prior.HasMaterializableOperationFollow
	return a
}

func (a executionClaimAdjudication) RuntimeAdjudication(visibleAction string) core.RuntimeAdjudication {
	a = a.WithPrior(executionClaimAdjudication{})
	evidenceRefs := make([]string, 0, 1)
	if a.LatestTurnSeq > 0 {
		evidenceRefs = append(evidenceRefs, "tes:turn_seq:"+strconv.FormatInt(a.LatestTurnSeq, 10))
	}
	return core.NormalizeRuntimeAdjudication(core.RuntimeAdjudication{
		Kind:          "execution_claim",
		Surface:       "final_reply",
		SubjectID:     "latest_turn",
		OperatorLabel: executionClaimOperatorLabel(visibleAction),
		Findings:      append([]core.RuntimeFinding(nil), a.Findings...),
		EvidenceRefs:  evidenceRefs,
		VisibleAction: visibleAction,
		CreatedAt:     time.Now().UTC(),
	})
}

func (r *Runtime) adjudicateFinalReplyExecutionClaims(key session.SessionKey, reply string) executionClaimAdjudication {
	return r.adjudicateFinalReplyExecutionClaimsWithContext(context.Background(), key, reply)
}

func (r *Runtime) adjudicateFinalReplyExecutionClaimsWithContext(ctx context.Context, key session.SessionKey, reply string) executionClaimAdjudication {
	reply = strings.TrimSpace(reply)
	out := executionClaimAdjudication{}
	if r == nil || r.store == nil || reply == "" {
		return out
	}
	claims := r.interpretFinalReplyExecutionClaims(ctx, reply)
	if len(claims) == 0 {
		return out
	}
	out.Interpretation = claims
	events, err := r.store.LatestExecutionEventsBySession(key, 300)
	if err == nil && len(events) > 0 {
		latestTerminal := ""
		for _, event := range events {
			eventType := strings.TrimSpace(event.EventType)
			switch eventType {
			case core.ExecutionEventTurnStarted:
				if event.Seq > out.LatestTurnSeq {
					out.LatestTurnSeq = event.Seq
					latestTerminal = ""
					out.LatestTerminalAt = ""
					out.HasToolEvidence = false
					out.HasTestEvidence = false
					out.HasDurableEvidence = false
				}
			case core.ExecutionEventTurnCompleted, core.ExecutionEventTurnFailed, core.ExecutionEventTurnInterrupted:
				if out.LatestTurnSeq == 0 || event.Seq < out.LatestTurnSeq {
					continue
				}
				latestTerminal = eventType
				out.LatestTerminalAt = event.CreatedAt.UTC().Format(time.RFC3339)
			case core.ExecutionEventToolStarted, core.ExecutionEventToolSucceeded, core.ExecutionEventToolFailed:
				if out.LatestTurnSeq == 0 || event.Seq < out.LatestTurnSeq {
					continue
				}
				out.HasToolEvidence = true
				payload := executionEventPayload(event.PayloadJSON)
				preview := strings.ToLower(strings.TrimSpace(payloadString(payload, "preview")))
				resultPreview := strings.ToLower(strings.TrimSpace(payloadString(payload, "result_preview")))
				if strings.Contains(preview, "go test") ||
					strings.Contains(preview, "pytest") ||
					strings.Contains(preview, "npm test") ||
					strings.Contains(resultPreview, "go test") ||
					strings.Contains(resultPreview, "pytest") ||
					strings.Contains(resultPreview, "npm test") {
					out.HasTestEvidence = true
				}
			case core.ExecutionEventDurableWakeStarted,
				core.ExecutionEventDurableWakeCompleted,
				core.ExecutionEventDurableWakeFailed,
				core.ExecutionEventDurableStateAwake,
				core.ExecutionEventDurableStateDormant,
				core.ExecutionEventDurablePolicyApplied,
				core.ExecutionEventDurablePolicyApplyFailed,
				core.ExecutionEventDurableParentAck:
				if out.LatestTurnSeq == 0 || event.Seq < out.LatestTurnSeq {
					continue
				}
				out.HasDurableEvidence = true
			}
		}
		if out.LatestTurnSeq != 0 {
			out.LatestStatus = "in_progress"
			switch latestTerminal {
			case core.ExecutionEventTurnCompleted:
				out.LatestStatus = "completed"
			case core.ExecutionEventTurnFailed:
				out.LatestStatus = "failed"
			case core.ExecutionEventTurnInterrupted:
				out.LatestStatus = "interrupted"
			case "":
				out.LatestStatus = "in_progress"
			}
			if executionClaimsInclude(claims, "completion") && latestTerminal != "" && latestTerminal != core.ExecutionEventTurnCompleted {
				out.Findings = append(out.Findings, executionClaimFinding("completion", "completion claim is not grounded (turn="+out.LatestStatus+")", out))
			}
			missingTestEvidence := executionClaimsInclude(claims, "test_execution") && !out.HasTestEvidence
			if executionClaimsInclude(claims, "tool_execution") && !out.HasToolEvidence && !missingTestEvidence {
				out.Findings = append(out.Findings, executionClaimFinding("tool_execution", "tool-execution claim has no tool events", out))
			}
			if missingTestEvidence {
				out.Findings = append(out.Findings, executionClaimFinding("test_execution", "test-execution claim has no test-related tool evidence", out))
			}
			if executionClaimsInclude(claims, "durable_agent") && !out.HasDurableEvidence {
				out.Findings = append(out.Findings, executionClaimFinding("durable_agent", "durable-agent claim has no durable lifecycle events", out))
			}
		}
	}
	out = r.adjudicationWithContinuationSurfaceState(key, claims, out, time.Now().UTC())
	if executionClaimsInclude(claims, "continuation_execution") && !out.HasActiveContinuationAuthority {
		out.Findings = append(out.Findings, executionClaimFinding("continuation_execution", "continuation claim has no active approved continuation lease", out))
	}
	if executionClaimsInclude(claims, "approval_granted") && !out.HasActiveContinuationAuthority {
		out.Findings = append(out.Findings, executionClaimFinding("approval_granted", "approval claim has no active approved continuation lease", out))
	}
	if executionClaimsInclude(claims, "approval_request") &&
		!out.HasPendingContinuationApproval &&
		!out.HasMaterializableOperationFollow &&
		!out.HasActiveContinuationAuthority {
		out.Findings = append(out.Findings, executionClaimFinding("approval_request", "approval-request claim has no pending approval state", out))
	}
	return out
}

func (r *Runtime) adjudicationWithContinuationSurfaceState(key session.SessionKey, claims []core.InterpretationClaim, out executionClaimAdjudication, now time.Time) executionClaimAdjudication {
	if !executionClaimsInclude(claims, "continuation_execution") &&
		!executionClaimsInclude(claims, "approval_granted") &&
		!executionClaimsInclude(claims, "approval_request") {
		return out
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if cont, exists, err := r.store.ContinuationStateIfExists(key); err == nil && exists {
		cont = session.NormalizeContinuationState(cont)
		if cont.Status == session.ContinuationStatusApproved && cont.ContinuationLease.ActiveAt(now) && cont.RemainingTurns > 0 {
			out.HasActiveContinuationAuthority = true
		}
		if continuationStateHasFreshPendingLease(cont, now) {
			out.HasPendingContinuationApproval = true
		}
	}
	if opState, err := r.store.OperationState(key); err == nil {
		opState = session.NormalizeOperationState(opState)
		if pendingOperationProposalNeedsButton(opState.Proposal) ||
			pendingOperationPlanLeaseNeedsButton(opState.PlanLease) ||
			operationHasMaterializablePhaseApproval(opState) {
			out.HasMaterializableOperationFollow = true
		}
	}
	return out
}

func operationHasMaterializablePhaseApproval(opState session.OperationState) bool {
	if _, ok := nextOperationPhaseForApproval(opState); ok {
		return true
	}
	if _, ok := nextOperationPhaseBundleForApproval(opState); ok {
		return true
	}
	return false
}

func executionClaimFinding(claimType string, detail string, adjudication executionClaimAdjudication) ExecutionClaimFinding {
	required := "Remove or qualify this unsupported execution claim in your own voice. Do not prepend a correction banner. If the claim is about prior work, explicitly attribute it as prior evidence rather than current-turn execution."
	switch claimType {
	case "test_execution":
		required = "Do not claim tests ran or passed in this turn without current-turn test tool evidence. If useful, say you reviewed prior validation instead. Do not prepend a correction banner."
	case "tool_execution":
		required = "Do not claim commands, tools, patches, or file edits happened in this turn without tool events. Remove or qualify the claim. Do not prepend a correction banner."
	case "durable_agent":
		required = "Do not claim durable-agent wake or lifecycle work happened without durable lifecycle events. Remove or qualify the claim. Do not prepend a correction banner."
	case "completion":
		required = "Do not claim completion when the latest turn is not completed. State only the observable state if it matters. Do not prepend a correction banner."
	case "continuation_execution":
		required = "Do not claim you will continue or begin a next phase unless an active approved continuation lease exists. Ask for a fresh bounded approval instead. Do not prepend a correction banner."
	case "approval_granted":
		required = "Do not claim approval or authority is already granted unless an active approved continuation lease exists. Ask for a fresh bounded approval instead. Do not prepend a correction banner."
	case "approval_request":
		required = "Do not claim an approval request exists unless pending approval state exists. Ask for a fresh bounded approval or park explicitly. Do not prepend a correction banner."
	}
	return ExecutionClaimFinding{
		Kind:             claimType,
		ClaimType:        claimType,
		EvidenceStatus:   "not_observed_in_current_turn",
		Detail:           strings.TrimSpace(detail),
		LatestTurnStatus: strings.TrimSpace(adjudication.LatestStatus),
		LatestTerminalAt: strings.TrimSpace(adjudication.LatestTerminalAt),
		RequiredBehavior: required,
	}
}

func executionClaimOperatorLabel(visibleAction string) string {
	switch strings.TrimSpace(visibleAction) {
	case "repair_requested":
		return "Reply claim needs repair"
	case "persona_repaired":
		return "Reply claim repaired"
	case "fallback_neutralized":
		return "Reply claim neutralized"
	default:
		return "Reply claim adjudicated"
	}
}

func (r *Runtime) recordExecutionClaimAdjudication(key session.SessionKey, adjudication executionClaimAdjudication, visibleAction string) {
	if r == nil || !adjudication.HasFindings() {
		return
	}
	runtimeAdjudication := adjudication.RuntimeAdjudication(visibleAction)
	claimTypes := make([]string, 0, len(adjudication.Findings))
	details := make([]string, 0, len(adjudication.Findings))
	for _, finding := range adjudication.Findings {
		if value := strings.TrimSpace(finding.ClaimType); value != "" {
			claimTypes = append(claimTypes, value)
		}
		if value := strings.TrimSpace(finding.Detail); value != "" {
			details = append(details, value)
		}
	}
	r.recordExecutionEvent(key, core.ExecutionEventReplyClaimAdjudicated, "reply", "adjudicated", map[string]any{
		"adjudication_kind":                   runtimeAdjudication.Kind,
		"surface":                             runtimeAdjudication.Surface,
		"subject_id":                          runtimeAdjudication.SubjectID,
		"operator_label":                      runtimeAdjudication.OperatorLabel,
		"findings":                            runtimeAdjudication.Findings,
		"evidence_refs":                       runtimeAdjudication.EvidenceRefs,
		"claim_types":                         claimTypes,
		"interpretation_claims":               adjudication.Interpretation,
		"details":                             details,
		"findings_count":                      len(adjudication.Findings),
		"latest_turn_seq":                     adjudication.LatestTurnSeq,
		"latest_turn_status":                  strings.TrimSpace(adjudication.LatestStatus),
		"latest_terminal_at":                  strings.TrimSpace(adjudication.LatestTerminalAt),
		"has_tool_evidence":                   adjudication.HasToolEvidence,
		"has_test_evidence":                   adjudication.HasTestEvidence,
		"has_durable_evidence":                adjudication.HasDurableEvidence,
		"has_active_continuation_authority":   adjudication.HasActiveContinuationAuthority,
		"has_pending_continuation_approval":   adjudication.HasPendingContinuationApproval,
		"has_materializable_operation_follow": adjudication.HasMaterializableOperationFollow,
		"visible_action":                      strings.TrimSpace(visibleAction),
	}, time.Now().UTC())
}

func (r *Runtime) groundFinalReplyWithExecutionEvidence(key session.SessionKey, reply string) (string, string) {
	reply = strings.TrimSpace(reply)
	adjudication := r.adjudicateFinalReplyExecutionClaims(key, reply)
	return reply, adjudication.Note()
}

func neutralizeUnsupportedExecutionClaims(reply string, adjudication executionClaimAdjudication) string {
	reply = strings.TrimSpace(reply)
	if reply == "" || !adjudication.HasFindings() {
		return reply
	}
	if adjudicationHasContinuationSurfaceFinding(adjudication) {
		return "I need a fresh bounded approval before continuing."
	}
	return "I do not have current-turn execution evidence for that claim."
}

func adjudicationHasContinuationSurfaceFinding(adjudication executionClaimAdjudication) bool {
	for _, finding := range adjudication.Findings {
		switch strings.TrimSpace(finding.ClaimType) {
		case "continuation_execution", "approval_granted", "approval_request":
			return true
		}
	}
	return false
}

func (r *Runtime) interpretFinalReplyExecutionClaims(ctx context.Context, reply string) []core.InterpretationClaim {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return nil
	}
	claims := r.interpretCurrentTurnClaims(ctx, interpretationRequest{
		Surface: "final_reply",
		Text:    reply,
	})
	out := make([]core.InterpretationClaim, 0, len(claims))
	for _, claim := range interpretationClaimsWithIntent(claims, "reply_execution_claim") {
		if claim.Scope != "" && claim.Scope != "final_reply" {
			continue
		}
		filteredRisk := executionClaimRisks(claim.Risk)
		if len(filteredRisk) == 0 {
			continue
		}
		claim.Scope = "final_reply"
		claim.Risk = filteredRisk
		claim.ProposedNextAction = firstNonEmpty(claim.ProposedNextAction, "validate_against_tes")
		out = append(out, core.NormalizeInterpretationClaim(claim))
	}
	return out
}

func executionClaimRisks(risks []string) []string {
	out := make([]string, 0, len(risks))
	seen := map[string]struct{}{}
	for _, risk := range risks {
		risk = strings.TrimSpace(risk)
		switch risk {
		case "completion", "tool_execution", "test_execution", "durable_agent", "continuation_execution", "approval_granted", "approval_request":
			if _, ok := seen[risk]; ok {
				continue
			}
			seen[risk] = struct{}{}
			out = append(out, risk)
		}
	}
	return out
}

func executionClaimsInclude(claims []core.InterpretationClaim, risk string) bool {
	risk = strings.TrimSpace(risk)
	if risk == "" {
		return false
	}
	for _, claim := range claims {
		claim = core.NormalizeInterpretationClaim(claim)
		if claim.Intent != "reply_execution_claim" {
			continue
		}
		for _, candidate := range claim.Risk {
			if candidate == risk {
				return true
			}
		}
	}
	return false
}

func (r *Runtime) repairTurnReply(
	ctx context.Context,
	scope sandbox.Scope,
	channel string,
	principalRole string,
	userText string,
	currentFaceModel face.Renderer,
	faceAwareness prompt.RuntimeAwareness,
	materialFloor core.MaterialPacket,
	floorText string,
	replyText string,
	media []core.Media,
	violations []ConstitutionViolation,
	adjudications []core.RuntimeAdjudication,
	audit *turnAuditRecorder,
) (string, bool) {
	if r == nil || r.faceBackend == face.BackendFloorFallback || currentFaceModel == nil {
		return "", false
	}
	if audit != nil {
		audit.MarkFaceRepairAttempted()
	}
	contract, ok := pipeline.BuildRepairContract(pipeline.RepairContract{
		Channel:       channel,
		PrincipalRole: principalRole,
		UserText:      userText,
		Candidate:     replyText,
		FloorText:     floorText,
		Material: pipeline.FloorMaterial{
			Packet: materialFloor,
			Text:   floorText,
		},
		Runtime:       faceAwareness,
		Adjudications: adjudications,
		MediaCount:    len(media),
	}, violations)
	if !ok {
		return "", false
	}
	repaired, err := currentFaceModel.Render(ctx, face.RenderRequest{
		GovernorName:    r.governorName(),
		FaceName:        r.faceName(),
		Channel:         contract.Channel,
		Mode:            "repair",
		PrincipalRole:   contract.PrincipalRole,
		WorkspaceRoot:   faceWorkspaceRoot(scope),
		FloorText:       contract.FloorText,
		MaterialFloor:   contract.Material.Packet,
		LatestUserInput: contract.UserText,
		CandidateReply:  contract.Candidate,
		RepairNotes:     contract.Violations,
		Adjudications:   contract.Adjudications,
		Runtime:         contract.Runtime,
	})
	if err != nil {
		return "", false
	}
	repaired = strings.TrimSpace(repaired)
	if repaired == "" {
		return "", false
	}
	if audit != nil {
		audit.MarkFaceRepairApplied()
	}
	return repaired, true
}

func (r *Runtime) emitTurnAudit(audit *turnAuditRecorder) {
	if r == nil || audit == nil || r.turnAuditSink == nil {
		return
	}
	r.turnAuditSink(audit.Snapshot())
}
