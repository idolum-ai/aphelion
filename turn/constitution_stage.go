//go:build linux

package turn

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
)

// ConstitutionStageInput captures constitution-check inputs for a visible reply.
type ConstitutionStageInput struct {
	ReplyText string
	Media     []core.Media
}

// ConstitutionStageCallbacks provides validator/repair hooks for constitution
// enforcement.
type ConstitutionStageCallbacks struct {
	Validate         func(replyText string, media []core.Media) []pipeline.ConstitutionViolation
	Repair           func(ctx context.Context, replyText string, media []core.Media, violations []pipeline.ConstitutionViolation) (string, bool)
	RecordViolations func(violations []pipeline.ConstitutionViolation)
}

// RunConstitutionStage validates a candidate reply, optionally attempts repair,
// and re-validates the repaired output.
func RunConstitutionStage(ctx context.Context, input ConstitutionStageInput, callbacks ConstitutionStageCallbacks) string {
	reply := strings.TrimSpace(input.ReplyText)
	if callbacks.Validate == nil {
		return reply
	}
	violations := callbacks.Validate(reply, input.Media)
	if len(violations) == 0 {
		return reply
	}
	if callbacks.RecordViolations != nil {
		callbacks.RecordViolations(violations)
	}
	if callbacks.Repair == nil {
		return reply
	}

	repaired, ok := callbacks.Repair(ctx, reply, input.Media, violations)
	if !ok {
		return reply
	}
	repaired = strings.TrimSpace(repaired)
	if repaired == "" {
		return reply
	}

	postViolations := callbacks.Validate(repaired, input.Media)
	if len(postViolations) != 0 {
		if callbacks.RecordViolations != nil {
			callbacks.RecordViolations(postViolations)
		}
		return reply
	}
	return repaired
}
