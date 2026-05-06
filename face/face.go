//go:build linux

package face

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/prompt"
)

const (
	DefaultFaceName   = "Idolum"
	DefaultFaceSymbol = "👁️‍🗨️"
)

type Backend string

const (
	BackendProvider      Backend = "provider"
	BackendFloorFallback Backend = "floor_fallback"
)

type RenderRequest struct {
	GovernorName    string
	FaceName        string
	Channel         string
	Mode            string
	Style           string
	PrincipalRole   string
	WorkspaceRoot   string
	FloorText       string
	MaterialFloor   core.MaterialPacket
	LatestUserInput string
	CandidateReply  string
	RepairNotes     []string
	ContextNotes    []string
	Adjudications   []core.RuntimeAdjudication
	Runtime         prompt.RuntimeAwareness
}

type Renderer interface {
	Render(ctx context.Context, req RenderRequest) (string, error)
}

type StreamRenderer interface {
	RenderStream(ctx context.Context, req RenderRequest, onChunk func(string) error) (string, error)
}

type ProposalRequest struct {
	GovernorName      string
	FaceName          string
	Channel           string
	Style             string
	Mode              string
	PrincipalRole     string
	WorkspaceRoot     string
	LatestUserInput   string
	PriorProposal     string
	BrokerageFeedback string
	Runtime           prompt.RuntimeAwareness
}

type Proposer interface {
	Propose(ctx context.Context, req ProposalRequest) (string, error)
}

func NormalizeBackend(raw string) Backend {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(BackendProvider):
		return BackendProvider
	case string(BackendFloorFallback):
		return BackendFloorFallback
	default:
		return Backend(strings.ToLower(strings.TrimSpace(raw)))
	}
}

func FloorTextOrFallback(text string) string {
	floor := strings.TrimSpace(text)
	if floor == "" {
		return "(no response)"
	}
	return floor
}
