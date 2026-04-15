//go:build linux

package runtime

import (
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/pipeline"
)

func shouldUseMaterialFloorContract(backend face.Backend, policy faceTurnPolicy) bool {
	return pipeline.ShouldUseMaterialFloorContract(string(backend), pipeline.FacePolicy(policy))
}

func governorMaterialArtifact(text string, useContract bool) (core.MaterialPacket, string, bool) {
	return pipeline.BuildFloorFromGovernor(text, useContract)
}

func materialFloorHeuristicText(packet core.MaterialPacket, fallback string) string {
	return pipeline.FormatFloorTextForRender(packet, fallback)
}

func parseMaterialPacket(text string) (core.MaterialPacket, error) {
	return pipeline.ParseMaterialPacket(text)
}

func normalizeMaterialHeading(line string) string {
	return pipeline.NormalizeMaterialHeading(line)
}

func parseMaterialItem(line string) string {
	return pipeline.ParseMaterialItem(line)
}
