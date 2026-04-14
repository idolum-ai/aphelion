//go:build linux

package runtime

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func (r *Runtime) applyTurnConstitution(
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
	audit *turnAuditRecorder,
) string {
	if audit != nil {
		audit.RecordFinalReply(replyText, media, "")
	}
	if r == nil || r.constitutionGate == nil {
		return strings.TrimSpace(replyText)
	}

	snapshot := TurnAudit{
		Channel:         strings.TrimSpace(channel),
		PrincipalRole:   strings.TrimSpace(principalRole),
		UserText:        strings.TrimSpace(userText),
		FinalReplyText:  strings.TrimSpace(replyText),
		FinalReplyMedia: cloneAuditMedia(media),
	}
	if audit != nil {
		base := audit.Snapshot()
		snapshot = base
		snapshot.FinalReplyText = strings.TrimSpace(replyText)
		snapshot.FinalReplyMedia = cloneAuditMedia(media)
	}

	violations := r.constitutionGate.ValidateFinal(snapshot)
	if len(violations) == 0 {
		return strings.TrimSpace(replyText)
	}
	if audit != nil {
		audit.RecordViolations(violations)
	}

	repaired, repairedOK := r.repairTurnReply(ctx, scope, channel, principalRole, userText, currentFaceModel, faceAwareness, materialFloor, floorText, replyText, media, violations, audit)
	if repairedOK {
		candidate := snapshot
		candidate.FinalReplyText = repaired
		postViolations := r.constitutionGate.ValidateFinal(candidate)
		if audit != nil {
			audit.RecordViolations(postViolations)
		}
		if len(postViolations) == 0 {
			return repaired
		}
	}
	return strings.TrimSpace(replyText)
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
	repairNotes := make([]string, 0, len(violations))
	for _, violation := range violations {
		detail := strings.TrimSpace(violation.Detail)
		if detail == "" {
			detail = strings.TrimSpace(violation.Rule)
		}
		if detail == "" {
			continue
		}
		repairNotes = append(repairNotes, detail)
	}
	repairAwareness := faceAwareness
	repairAwareness.DeliveryMode = "constitution_repair"
	repairAwareness.StreamReply = false
	repairAwareness.MediaAttached = len(media) > 0
	if len(media) > 0 {
		repairAwareness.MediaMode = "attachments"
	}
	repaired, err := currentFaceModel.Render(ctx, face.RenderRequest{
		GovernorName:    prompt.DefaultGovernorName,
		FaceName:        face.DefaultFaceName,
		Channel:         channel,
		Mode:            "repair",
		PrincipalRole:   principalRole,
		WorkspaceRoot:   faceWorkspaceRoot(scope),
		FloorText:       floorText,
		MaterialFloor:   materialFloor,
		LatestUserInput: userText,
		CandidateReply:  strings.TrimSpace(replyText),
		RepairNotes:     repairNotes,
		Runtime:         repairAwareness,
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
