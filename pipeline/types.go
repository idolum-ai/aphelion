//go:build linux

package pipeline

import "github.com/idolum-ai/aphelion/core"

// FloorArtifact is the governor-owned material artifact that a scene may be
// staged from.
type FloorArtifact struct {
	Text         string
	Material     core.MaterialPacket
	MetadataText string
	Structured   bool
}

// SceneArtifact is the face-authored visible reply.
type SceneArtifact struct {
	Text string
}

// FallbackArtifact is the degraded delivery result when ordinary scene
// authorship is absent or fails.
type FallbackArtifact struct {
	Text string
}

// ExecutionContract names the bounded execution shape negotiated during
// brokerage without forcing the turn through a fixed mode taxonomy.
type ExecutionContract struct {
	NeedsInspection bool
	NeedsQuestion   bool
	MayAnswerNow    bool
}

// RatificationDisposition records how Aphelion answered Idolum's brokerage
// push.
type RatificationDisposition string

const (
	RatificationAccept RatificationDisposition = "accept"
	RatificationAdapt  RatificationDisposition = "adapt"
	RatificationReject RatificationDisposition = "reject"
)

// SignalJudgment records how the governor treated a face-named hidden input.
type SignalJudgment string

const (
	SignalJudgmentConfirmed   SignalJudgment = "confirmed"
	SignalJudgmentOverridden  SignalJudgment = "overridden"
	SignalJudgmentNotMaterial SignalJudgment = "not_material"
)
