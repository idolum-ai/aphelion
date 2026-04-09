//go:build linux

package face

import (
	"context"
	"strings"
)

const (
	DefaultFaceName = "Host"
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
}

type Renderer interface {
	Render(ctx context.Context, req RenderRequest) (string, error)
}

func CanonicalOrFallback(text string) string {
	canonical := strings.TrimSpace(text)
	if canonical == "" {
		return "(no response)"
	}
	return canonical
}
