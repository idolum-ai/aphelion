//go:build linux

package face

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/prompt"
)

const (
	DefaultFaceName   = "Idolum"
	DefaultFaceSymbol = "👁️‍🗨️"
)

type Backend string

const (
	BackendProvider            Backend = "provider"
	BackendGovernorPassthrough Backend = "governor_passthrough"
)

type RenderRequest struct {
	GovernorName    string
	FaceName        string
	Channel         string
	Style           string
	PrincipalRole   string
	WorkspaceRoot   string
	CanonicalReply  string
	LatestUserInput string
	Runtime         prompt.RuntimeAwareness
}

type Renderer interface {
	Render(ctx context.Context, req RenderRequest) (string, error)
}

type StreamRenderer interface {
	RenderStream(ctx context.Context, req RenderRequest, onChunk func(string) error) (string, error)
}

type ProposalRequest struct {
	GovernorName    string
	FaceName        string
	Channel         string
	Style           string
	PrincipalRole   string
	WorkspaceRoot   string
	LatestUserInput string
	Runtime         prompt.RuntimeAwareness
}

type Proposer interface {
	Propose(ctx context.Context, req ProposalRequest) (string, error)
}

func CanonicalOrFallback(text string) string {
	canonical := strings.TrimSpace(text)
	if canonical == "" {
		return "(no response)"
	}
	return canonical
}
