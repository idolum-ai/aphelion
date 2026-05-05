//go:build linux

package runtime

import (
	"context"
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
	adjudication := r.adjudicateFinalReplyExecutionClaims(key, trimmedReply)
	if adjudication.HasFindings() {
		r.recordExecutionClaimAdjudication(key, adjudication, "repair_requested")
		violations := adjudication.ConstitutionViolations()
		if audit != nil {
			audit.RecordViolations(violations)
			audit.RecordExecutionClaimFindings(adjudication.Findings)
		}
		if repaired, ok := r.repairTurnReply(ctx, scope, channel, principalRole, userText, currentFaceModel, faceAwareness, materialFloor, floorText, trimmedReply, media, violations, audit); ok {
			repairedAdjudication := r.adjudicateFinalReplyExecutionClaims(key, repaired)
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
	Findings           []ExecutionClaimFinding
	LatestTurnSeq      int64
	LatestStatus       string
	LatestTerminalAt   string
	HasToolEvidence    bool
	HasTestEvidence    bool
	HasDurableEvidence bool
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
	return a
}

func (r *Runtime) adjudicateFinalReplyExecutionClaims(key session.SessionKey, reply string) executionClaimAdjudication {
	reply = strings.TrimSpace(reply)
	out := executionClaimAdjudication{}
	if r == nil || r.store == nil || reply == "" {
		return out
	}
	claims := detectExecutionClaims(reply)
	if !claims.any() {
		return out
	}
	events, err := r.store.LatestExecutionEventsBySession(key, 300)
	if err != nil || len(events) == 0 {
		return out
	}

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
	if out.LatestTurnSeq == 0 {
		return out
	}

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
	if claims.Completion && latestTerminal != "" && latestTerminal != core.ExecutionEventTurnCompleted {
		out.Findings = append(out.Findings, executionClaimFinding("completion", "completion claim is not grounded (turn="+out.LatestStatus+")", out))
	}
	missingTestEvidence := claims.Tests && !out.HasTestEvidence
	if claims.Tool && !out.HasToolEvidence && !missingTestEvidence {
		out.Findings = append(out.Findings, executionClaimFinding("tool_execution", "tool-execution claim has no tool events", out))
	}
	if missingTestEvidence {
		out.Findings = append(out.Findings, executionClaimFinding("test_execution", "test-execution claim has no test-related tool evidence", out))
	}
	if claims.Durable && !out.HasDurableEvidence {
		out.Findings = append(out.Findings, executionClaimFinding("durable_agent", "durable-agent claim has no durable lifecycle events", out))
	}
	return out
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
	}
	return ExecutionClaimFinding{
		ClaimType:        claimType,
		EvidenceStatus:   "not_observed_in_current_turn",
		Detail:           strings.TrimSpace(detail),
		LatestTurnStatus: strings.TrimSpace(adjudication.LatestStatus),
		LatestTerminalAt: strings.TrimSpace(adjudication.LatestTerminalAt),
		RequiredBehavior: required,
	}
}

func (r *Runtime) recordExecutionClaimAdjudication(key session.SessionKey, adjudication executionClaimAdjudication, visibleAction string) {
	if r == nil || !adjudication.HasFindings() {
		return
	}
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
		"claim_types":          claimTypes,
		"details":              details,
		"findings_count":       len(adjudication.Findings),
		"latest_turn_seq":      adjudication.LatestTurnSeq,
		"latest_turn_status":   strings.TrimSpace(adjudication.LatestStatus),
		"latest_terminal_at":   strings.TrimSpace(adjudication.LatestTerminalAt),
		"has_tool_evidence":    adjudication.HasToolEvidence,
		"has_test_evidence":    adjudication.HasTestEvidence,
		"has_durable_evidence": adjudication.HasDurableEvidence,
		"visible_action":       strings.TrimSpace(visibleAction),
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
	paragraphs := splitReplyParagraphs(reply)
	kept := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if paragraphHasUnsupportedExecutionClaim(paragraph, adjudication) {
			continue
		}
		kept = append(kept, paragraph)
	}
	out := strings.TrimSpace(strings.Join(kept, "\n\n"))
	if out != "" {
		return out
	}
	return "I do not have current-turn execution evidence for that claim."
}

func splitReplyParagraphs(reply string) []string {
	lines := strings.Split(strings.TrimSpace(reply), "\n")
	out := make([]string, 0)
	var current []string
	flush := func() {
		if len(current) == 0 {
			return
		}
		out = append(out, strings.TrimSpace(strings.Join(current, "\n")))
		current = nil
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()
	if len(out) == 0 && strings.TrimSpace(reply) != "" {
		return []string{strings.TrimSpace(reply)}
	}
	return out
}

func paragraphHasUnsupportedExecutionClaim(paragraph string, adjudication executionClaimAdjudication) bool {
	if strings.HasPrefix(strings.TrimSpace(paragraph), "I need to correct that:") {
		return true
	}
	claims := detectExecutionClaims(paragraph)
	for _, finding := range adjudication.Findings {
		switch strings.TrimSpace(finding.ClaimType) {
		case "completion":
			if claims.Completion {
				return true
			}
		case "tool_execution":
			if claims.Tool {
				return true
			}
		case "test_execution":
			if claims.Tests {
				return true
			}
		case "durable_agent":
			if claims.Durable {
				return true
			}
		}
	}
	return false
}

type executionClaimSet struct {
	Completion bool
	Tool       bool
	Tests      bool
	Durable    bool
}

func (c executionClaimSet) any() bool {
	return c.Completion || c.Tool || c.Tests || c.Durable
}

func detectExecutionClaims(reply string) executionClaimSet {
	lower := strings.ToLower(strings.TrimSpace(reply))
	if lower == "" {
		return executionClaimSet{}
	}
	claims := executionClaimSet{}
	if containsPositiveClaimMarker(lower,
		"done",
		"completed",
		"all set",
		"finished",
		"successfully",
	) {
		claims.Completion = true
	}
	if containsPositiveClaimMarker(lower,
		"ran ",
		"executed ",
		"used the tool",
		"called the tool",
		"executed command",
		"ran command",
		"applied the patch",
		"updated the files",
	) {
		claims.Tool = true
	}
	if containsPositiveClaimMarker(lower,
		"tests passed",
		"all tests passed",
		"validation passed",
		"ran go test",
		"go test passed",
		"go test succeeded",
		"ran pytest",
		"pytest passed",
		"pytest succeeded",
		"ran npm test",
		"npm test passed",
		"npm test succeeded",
	) {
		claims.Tests = true
		claims.Tool = true
	}
	if containsPositiveClaimMarker(lower,
		"woke durable agent",
		"woke durable child",
		"durable wake completed",
		"durable child completed",
		"child processed parent guidance",
		"processed pending parent guidance",
	) {
		claims.Durable = true
	}
	return claims
}

func containsAnyClaimMarker(text string, needles ...string) bool {
	for _, needle := range needles {
		needle = strings.TrimSpace(needle)
		if needle == "" {
			continue
		}
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func containsPositiveClaimMarker(text string, needles ...string) bool {
	for _, needle := range needles {
		needle = strings.TrimSpace(strings.ToLower(needle))
		if needle == "" {
			continue
		}
		searchFrom := 0
		for {
			idx := strings.Index(text[searchFrom:], needle)
			if idx < 0 {
				break
			}
			idx += searchFrom
			if !claimMarkerHasBoundaries(text, idx, needle) {
				searchFrom = idx + len(needle)
				if searchFrom >= len(text) {
					break
				}
				continue
			}
			start := idx - 32
			if start < 0 {
				start = 0
			}
			prefix := strings.TrimSpace(text[start:idx])
			if !containsAnyClaimMarker(prefix,
				" not ",
				"not",
				"did not",
				"didn't",
				"won't",
				"wouldn't",
				"can't",
				"cannot",
				"never",
				"no ",
				"avoid",
				"without",
				"pretend",
				"reviewed",
				"existing validation",
				"prior validation",
				"previous validation",
				"already-present validation",
				"validation record",
				"pushed fixes",
				"prior commit",
				"previous commit",
			) {
				return true
			}
			searchFrom = idx + len(needle)
			if searchFrom >= len(text) {
				break
			}
		}
	}
	return false
}

func claimMarkerHasBoundaries(text string, idx int, marker string) bool {
	if idx < 0 || marker == "" {
		return false
	}
	end := idx + len(marker)
	if end > len(text) {
		return false
	}
	if markerBoundaryRequired(marker[0]) && idx > 0 && isClaimWordByte(text[idx-1]) {
		return false
	}
	if markerBoundaryRequired(marker[len(marker)-1]) && end < len(text) && isClaimWordByte(text[end]) {
		return false
	}
	return true
}

func markerBoundaryRequired(ch byte) bool {
	return isClaimWordByte(ch)
}

func isClaimWordByte(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_'
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
		Runtime:    faceAwareness,
		MediaCount: len(media),
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
