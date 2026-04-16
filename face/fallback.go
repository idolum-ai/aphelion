//go:build linux

package face

import (
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
)

type FallbackOptions = pipeline.FallbackOptions

func SerializeFloorFallback(packet core.MaterialPacket, floorText string, opts FallbackOptions) string {
	return pipeline.SerializeFloorFallback(packet, floorText, pipeline.FallbackOptions(opts))
}
