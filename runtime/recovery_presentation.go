//go:build linux

package runtime

import (
	"github.com/idolum-ai/aphelion/pipeline"
)

func recoveryPresentationRenderInput(input turnRenderInput) turnRenderInput {
	if input.MediaOnlyReply {
		return input
	}
	packet, floorText, ok := pipeline.ShapeInternalContinuityForPresentation(input.MaterialFloor, input.FloorText)
	if !ok {
		return input
	}
	out := input
	out.MaterialFloor = packet
	out.FloorText = floorText
	out.ReplyText = pipeline.SerializeFloorFallback(packet, floorText, input.FallbackOpts)
	return out
}
