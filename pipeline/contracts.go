//go:build linux

package pipeline

import (
	"context"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/prompt"
)

// BrokerageProposal is Idolum's bounded pre-turn push about how a turn should
// move.
type BrokerageProposal struct {
	RawText       string
	SuggestedMode TurnMode
	Usage         core.TokenUsage
}

// BrokerageRatification is Aphelion's bounded answer to a brokerage proposal.
type BrokerageRatification struct {
	RawText        string
	RatifiedMode   TurnMode
	Disposition    RatificationDisposition
	SignalJudgment SignalJudgment
	RatifiedSteps  []string
	Usage          core.TokenUsage
}

// BrokerageArtifact preserves both sides of the negotiated brokerage surface.
type BrokerageArtifact struct {
	Proposal     BrokerageProposal
	Ratification BrokerageRatification
}

// ProposalRequest names the face-side input for a pre-turn proposal or
// brokerage pass.
type ProposalRequest struct {
	GovernorName    string
	FaceName        string
	Channel         string
	Style           string
	Mode            string
	PrincipalRole   string
	WorkspaceRoot   string
	LatestUserInput string
	Runtime         prompt.RuntimeAwareness
}

// RenderRequest names the face-side input for ordinary scene authorship.
type RenderRequest struct {
	GovernorName    string
	FaceName        string
	Channel         string
	Style           string
	PrincipalRole   string
	WorkspaceRoot   string
	LatestUserInput string
	Runtime         prompt.RuntimeAwareness
	Floor           FloorArtifact
}

// FallbackRequest names the degraded delivery input when a floor must surface
// without ordinary scene authorship.
type FallbackRequest struct {
	Channel string
	Voice   bool
	Floor   FloorArtifact
}

// ProposalPort is the face-side proposal surface.
type ProposalPort interface {
	Propose(ctx context.Context, req ProposalRequest) (*BrokerageProposal, error)
}

// ScenePort is the ordinary face-scene authorship surface.
type ScenePort interface {
	RenderScene(ctx context.Context, req RenderRequest) (*SceneArtifact, error)
}

// FallbackPort is the degraded floor-to-user delivery surface.
type FallbackPort interface {
	RenderFallback(ctx context.Context, req FallbackRequest) (*FallbackArtifact, error)
}
