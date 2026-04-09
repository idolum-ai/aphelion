//go:build linux

package face

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/prompt"
)

var ErrEmptyRender = errors.New("face renderer returned empty reply")

type ProviderRendererConfig struct {
	GovernorName  string
	FaceName      string
	Channel       string
	Style         string
	WorkspaceRoot string
}

type ProviderRenderer struct {
	provider agent.Provider
	cfg      ProviderRendererConfig
}

func NewProviderRenderer(provider agent.Provider, cfg ProviderRendererConfig) (*ProviderRenderer, error) {
	if provider == nil {
		return nil, fmt.Errorf("provider is nil")
	}
	return &ProviderRenderer{
		provider: provider,
		cfg:      cfg,
	}, nil
}

func (r *ProviderRenderer) Render(ctx context.Context, req RenderRequest) (string, error) {
	workspaceRoot := firstNonEmpty(req.WorkspaceRoot, r.cfg.WorkspaceRoot)
	stableFiles, dynamicFiles, err := LoadHostPromptFiles(workspaceRoot)
	if err != nil {
		return "", err
	}

	systemPrompt := prompt.BuildFacePrompt(prompt.FaceRequest{
		GovernorName:    firstNonEmpty(req.GovernorName, r.cfg.GovernorName, prompt.DefaultGovernorName),
		FaceName:        firstNonEmpty(req.FaceName, r.cfg.FaceName, DefaultFaceName),
		Channel:         firstNonEmpty(req.Channel, r.cfg.Channel, "telegram"),
		Style:           firstNonEmpty(req.Style, r.cfg.Style),
		PrincipalRole:   req.PrincipalRole,
		CanonicalReply:  CanonicalOrFallback(req.CanonicalReply),
		LatestUserInput: req.LatestUserInput,
		StableFiles:     stableFiles,
		DynamicFiles:    dynamicFiles,
	})

	resp, err := r.provider.Complete(ctx, []agent.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: "Render the final reply for delivery. Return only the reply text."},
	}, nil)
	if err != nil {
		return "", err
	}

	rendered := strings.TrimSpace(resp.Content)
	if rendered == "" {
		return "", ErrEmptyRender
	}
	return rendered, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
