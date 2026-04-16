//go:build linux

package runtime

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/pipeline"
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
