//go:build linux

package runtime

import (
	"context"
	"fmt"
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
	trimmedReply, groundingNote := r.groundFinalReplyWithExecutionEvidence(key, trimmedReply)
	if groundingNote != "" && audit != nil {
		audit.RecordViolations([]ConstitutionViolation{{
			Rule:    "execution_claim_ungrounded",
			Surface: "final_reply",
			Detail:  groundingNote,
		}})
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

func (r *Runtime) groundFinalReplyWithExecutionEvidence(key session.SessionKey, reply string) (string, string) {
	reply = strings.TrimSpace(reply)
	if r == nil || r.store == nil || reply == "" {
		return reply, ""
	}
	claims := detectExecutionClaims(reply)
	if !claims.any() {
		return reply, ""
	}
	events, err := r.store.LatestExecutionEventsBySession(key, 300)
	if err != nil || len(events) == 0 {
		return reply, ""
	}

	latestTurnStart := int64(0)
	latestTerminal := ""
	latestTerminalAt := ""
	hasToolEvidence := false
	hasTestEvidence := false
	hasDurableEvidence := false
	for _, event := range events {
		eventType := strings.TrimSpace(event.EventType)
		switch eventType {
		case core.ExecutionEventTurnStarted:
			if event.Seq > latestTurnStart {
				latestTurnStart = event.Seq
				latestTerminal = ""
				latestTerminalAt = ""
				hasToolEvidence = false
				hasTestEvidence = false
				hasDurableEvidence = false
			}
		case core.ExecutionEventTurnCompleted, core.ExecutionEventTurnFailed, core.ExecutionEventTurnInterrupted:
			if latestTurnStart == 0 || event.Seq < latestTurnStart {
				continue
			}
			latestTerminal = eventType
			latestTerminalAt = event.CreatedAt.UTC().Format(time.RFC3339)
		case core.ExecutionEventToolStarted, core.ExecutionEventToolSucceeded, core.ExecutionEventToolFailed:
			if latestTurnStart == 0 || event.Seq < latestTurnStart {
				continue
			}
			hasToolEvidence = true
			payload := executionEventPayload(event.PayloadJSON)
			preview := strings.ToLower(strings.TrimSpace(payloadString(payload, "preview")))
			resultPreview := strings.ToLower(strings.TrimSpace(payloadString(payload, "result_preview")))
			if strings.Contains(preview, "go test") ||
				strings.Contains(preview, "pytest") ||
				strings.Contains(preview, "npm test") ||
				strings.Contains(resultPreview, "go test") ||
				strings.Contains(resultPreview, "pytest") ||
				strings.Contains(resultPreview, "npm test") {
				hasTestEvidence = true
			}
		case core.ExecutionEventDurableWakeStarted,
			core.ExecutionEventDurableWakeCompleted,
			core.ExecutionEventDurableWakeFailed,
			core.ExecutionEventDurableStateAwake,
			core.ExecutionEventDurableStateDormant,
			core.ExecutionEventDurablePolicyApplied,
			core.ExecutionEventDurablePolicyApplyFailed,
			core.ExecutionEventDurableParentAck:
			if latestTurnStart == 0 || event.Seq < latestTurnStart {
				continue
			}
			hasDurableEvidence = true
		}
	}
	if latestTurnStart == 0 {
		return reply, ""
	}

	status := "in_progress"
	switch latestTerminal {
	case core.ExecutionEventTurnFailed:
		status = "failed"
	case core.ExecutionEventTurnInterrupted:
		status = "interrupted"
	case "":
		status = "in_progress"
	}

	reasons := make([]string, 0, 4)
	if claims.Completion && latestTerminal != "" && latestTerminal != core.ExecutionEventTurnCompleted {
		reasons = append(reasons, fmt.Sprintf("completion claim is not grounded (turn=%s)", status))
	}
	if claims.Tool && !hasToolEvidence {
		reasons = append(reasons, "tool-execution claim has no tool events")
	}
	if claims.Tests && !hasTestEvidence {
		reasons = append(reasons, "test-execution claim has no test-related tool evidence")
	}
	if claims.Durable && !hasDurableEvidence {
		reasons = append(reasons, "durable-agent claim has no durable lifecycle events")
	}
	if len(reasons) == 0 {
		return reply, ""
	}
	note := "execution claims are not grounded by TES: " + strings.Join(reasons, "; ")
	prefix := fmt.Sprintf(
		"I need to correct that: %s",
		strings.Join(reasons, "; "),
	)
	if latestTerminalAt != "" {
		prefix += " (as of " + latestTerminalAt + " UTC)"
	}
	prefix += "."
	return prefix + "\n" + reply, note
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
		"go test",
		"pytest",
		"npm test",
	) {
		claims.Tests = true
		claims.Tool = true
	}
	if containsPositiveClaimMarker(lower,
		"durable agent",
		"durable child",
		"parent-child",
		"review artifact",
		"wake loop",
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
		GovernorName:    prompt.DefaultGovernorName,
		FaceName:        face.DefaultFaceName,
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
