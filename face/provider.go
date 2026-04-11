//go:build linux

package face

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
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
	provider  agent.Provider
	cfg       ProviderRendererConfig
	mu        sync.Mutex
	lastUsage core.TokenUsage
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
	stableFiles, dynamicFiles, err := LoadIdolumPromptFiles(workspaceRoot)
	if err != nil {
		return "", err
	}
	governorName := firstNonEmpty(req.GovernorName, r.cfg.GovernorName, prompt.DefaultGovernorName)
	faceName := firstNonEmpty(req.FaceName, r.cfg.FaceName, DefaultFaceName)

	facePrompt := prompt.FaceRequest{
		GovernorName:    governorName,
		FaceName:        faceName,
		Channel:         firstNonEmpty(req.Channel, r.cfg.Channel, "telegram"),
		Style:           firstNonEmpty(req.Style, r.cfg.Style),
		PrincipalRole:   req.PrincipalRole,
		CanonicalReply:  CanonicalOrFallback(req.CanonicalReply),
		LatestUserInput: req.LatestUserInput,
		StableFiles:     stableFiles,
		DynamicFiles:    dynamicFiles,
		Mode:            "render",
		Runtime:         req.Runtime,
	}
	systemBlocks := prompt.BuildFacePromptBlocks(facePrompt)
	systemPrompt := prompt.RenderSystemBlocks(systemBlocks)

	resp, err := r.provider.Complete(ctx, []agent.Message{
		{Role: "system", Content: systemPrompt, SystemBlocks: systemBlocks},
		{Role: "user", Content: fmt.Sprintf("Speak to the user directly as %s, within %s-approved boundaries below. Return only the reply text.", faceName, governorName)},
	}, nil)
	if err != nil {
		return "", err
	}

	rendered := strings.TrimSpace(resp.Content)
	if rendered == "" {
		return "", ErrEmptyRender
	}
	r.recordUsage(resp.Usage)
	return rendered, nil
}

func (r *ProviderRenderer) RenderStream(ctx context.Context, req RenderRequest, onChunk func(string) error) (string, error) {
	streamingProvider, ok := r.provider.(agent.StreamingProvider)
	if !ok {
		return r.Render(ctx, req)
	}

	workspaceRoot := firstNonEmpty(req.WorkspaceRoot, r.cfg.WorkspaceRoot)
	stableFiles, dynamicFiles, err := LoadIdolumPromptFiles(workspaceRoot)
	if err != nil {
		return "", err
	}
	governorName := firstNonEmpty(req.GovernorName, r.cfg.GovernorName, prompt.DefaultGovernorName)
	faceName := firstNonEmpty(req.FaceName, r.cfg.FaceName, DefaultFaceName)

	facePrompt := prompt.FaceRequest{
		GovernorName:    governorName,
		FaceName:        faceName,
		Channel:         firstNonEmpty(req.Channel, r.cfg.Channel, "telegram"),
		Style:           firstNonEmpty(req.Style, r.cfg.Style),
		PrincipalRole:   req.PrincipalRole,
		CanonicalReply:  CanonicalOrFallback(req.CanonicalReply),
		LatestUserInput: req.LatestUserInput,
		StableFiles:     stableFiles,
		DynamicFiles:    dynamicFiles,
		Mode:            "render",
		Runtime:         req.Runtime,
	}
	systemBlocks := prompt.BuildFacePromptBlocks(facePrompt)
	systemPrompt := prompt.RenderSystemBlocks(systemBlocks)

	var rendered strings.Builder
	resp, err := streamingProvider.Stream(ctx, []agent.Message{
		{Role: "system", Content: systemPrompt, SystemBlocks: systemBlocks},
		{Role: "user", Content: fmt.Sprintf("Speak to the user directly as %s, within %s-approved boundaries below. Return only the reply text.", faceName, governorName)},
	}, nil, func(chunk agent.StreamChunk) error {
		if chunk.Text == "" {
			return nil
		}
		rendered.WriteString(chunk.Text)
		if onChunk != nil {
			return onChunk(chunk.Text)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if resp != nil {
		r.recordUsage(resp.Usage)
	}

	text := strings.TrimSpace(rendered.String())
	if text == "" && resp != nil {
		text = strings.TrimSpace(resp.Content)
	}
	if text == "" {
		return "", ErrEmptyRender
	}
	return text, nil
}

func (r *ProviderRenderer) Propose(ctx context.Context, req ProposalRequest) (string, error) {
	workspaceRoot := firstNonEmpty(req.WorkspaceRoot, r.cfg.WorkspaceRoot)
	stableFiles, dynamicFiles, err := LoadIdolumPromptFiles(workspaceRoot)
	if err != nil {
		return "", err
	}
	governorName := firstNonEmpty(req.GovernorName, r.cfg.GovernorName, prompt.DefaultGovernorName)
	faceName := firstNonEmpty(req.FaceName, r.cfg.FaceName, DefaultFaceName)

	facePrompt := prompt.FaceRequest{
		GovernorName:    governorName,
		FaceName:        faceName,
		Channel:         firstNonEmpty(req.Channel, r.cfg.Channel, "telegram"),
		Style:           firstNonEmpty(req.Style, r.cfg.Style),
		PrincipalRole:   req.PrincipalRole,
		LatestUserInput: req.LatestUserInput,
		StableFiles:     stableFiles,
		DynamicFiles:    dynamicFiles,
		Mode:            firstNonEmpty(req.Mode, "proposal"),
		Runtime:         req.Runtime,
	}
	systemBlocks := prompt.BuildFacePromptBlocks(facePrompt)
	systemPrompt := prompt.RenderSystemBlocks(systemBlocks)

	resp, err := r.provider.Complete(ctx, []agent.Message{
		{Role: "system", Content: systemPrompt, SystemBlocks: systemBlocks},
		{Role: "user", Content: fmt.Sprintf("Speak to %s in one short bounded note about how this turn should move next. Return only that note, or nothing if you have no useful push.", governorName)},
	}, nil)
	if err != nil {
		return "", err
	}

	r.recordUsage(resp.Usage)
	return strings.TrimSpace(resp.Content), nil
}

func (r *ProviderRenderer) ConsumeLastUsage() core.TokenUsage {
	if r == nil {
		return core.TokenUsage{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	usage := r.lastUsage
	r.lastUsage = core.TokenUsage{}
	return usage
}

func (r *ProviderRenderer) recordUsage(usage core.TokenUsage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastUsage = usage
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
