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
	"github.com/idolum-ai/aphelion/turn"
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
	trimmedReply := strings.TrimSpace(replyText)
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
