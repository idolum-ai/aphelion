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
	Reasoning     agent.ReasoningConfig
	Verbosity     agent.Verbosity
	MaxTokens     int
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

	mode := firstNonEmpty(req.Mode, "render")
	facePrompt := prompt.FaceRequest{
		GovernorName:    governorName,
		FaceName:        faceName,
		Channel:         firstNonEmpty(req.Channel, r.cfg.Channel, "telegram"),
		Mode:            mode,
		Scene:           firstNonEmpty(req.Scene),
		Style:           firstNonEmpty(req.Style, r.cfg.Style),
		PrincipalRole:   req.PrincipalRole,
		FloorText:       FloorTextOrFallback(req.FloorText),
		MaterialFloor:   req.MaterialFloor,
		LatestUserInput: req.LatestUserInput,
		CandidateReply:  strings.TrimSpace(req.CandidateReply),
		RepairNotes:     append([]string(nil), req.RepairNotes...),
		ContextNotes:    append([]string(nil), req.ContextNotes...),
		Adjudications:   core.NormalizeRuntimeAdjudications(req.Adjudications),
		StableFiles:     stableFiles,
		DynamicFiles:    dynamicFiles,
		Runtime:         req.Runtime,
	}
	systemBlocks := prompt.BuildFacePromptBlocks(facePrompt)
	systemPrompt := prompt.RenderSystemBlocks(systemBlocks)

	resp, err := r.complete(ctx, []agent.Message{
		{Role: "system", Content: systemPrompt, SystemBlocks: systemBlocks},
		{Role: "user", Content: fmt.Sprintf("Speak to the user directly as %s, from the material authorized by %s below. Return only the reply text.", faceName, governorName)},
	}, nil, mode)
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
	streamingProvider, hasStream := r.provider.(agent.StreamingProvider)
	_, hasStreamOptions := r.provider.(agent.StreamingProviderWithOptions)
	if !hasStream && !hasStreamOptions {
		return r.Render(ctx, req)
	}

	workspaceRoot := firstNonEmpty(req.WorkspaceRoot, r.cfg.WorkspaceRoot)
	stableFiles, dynamicFiles, err := LoadIdolumPromptFiles(workspaceRoot)
	if err != nil {
		return "", err
	}
	governorName := firstNonEmpty(req.GovernorName, r.cfg.GovernorName, prompt.DefaultGovernorName)
	faceName := firstNonEmpty(req.FaceName, r.cfg.FaceName, DefaultFaceName)

	mode := firstNonEmpty(req.Mode, "render")
	facePrompt := prompt.FaceRequest{
		GovernorName:    governorName,
		FaceName:        faceName,
		Channel:         firstNonEmpty(req.Channel, r.cfg.Channel, "telegram"),
		Mode:            mode,
		Scene:           firstNonEmpty(req.Scene),
		Style:           firstNonEmpty(req.Style, r.cfg.Style),
		PrincipalRole:   req.PrincipalRole,
		FloorText:       FloorTextOrFallback(req.FloorText),
		MaterialFloor:   req.MaterialFloor,
		LatestUserInput: req.LatestUserInput,
		CandidateReply:  strings.TrimSpace(req.CandidateReply),
		RepairNotes:     append([]string(nil), req.RepairNotes...),
		ContextNotes:    append([]string(nil), req.ContextNotes...),
		Adjudications:   core.NormalizeRuntimeAdjudications(req.Adjudications),
		StableFiles:     stableFiles,
		DynamicFiles:    dynamicFiles,
		Runtime:         req.Runtime,
	}
	systemBlocks := prompt.BuildFacePromptBlocks(facePrompt)
	systemPrompt := prompt.RenderSystemBlocks(systemBlocks)

	var rendered strings.Builder
	messages := []agent.Message{
		{Role: "system", Content: systemPrompt, SystemBlocks: systemBlocks},
		{Role: "user", Content: fmt.Sprintf("Speak to the user directly as %s, from the material authorized by %s below. Return only the reply text.", faceName, governorName)},
	}
	onStreamChunk := func(chunk agent.StreamChunk) error {
		if chunk.Text == "" {
			return nil
		}
		rendered.WriteString(chunk.Text)
		if onChunk != nil {
			return onChunk(chunk.Text)
		}
		return nil
	}
	var resp *agent.Response
	if withOptions, ok := r.provider.(agent.StreamingProviderWithOptions); ok {
		resp, err = withOptions.StreamWithOptions(ctx, messages, nil, r.completeOptionsForMode(mode), onStreamChunk)
	} else {
		resp, err = streamingProvider.Stream(ctx, messages, nil, onStreamChunk)
	}
	if err != nil {
		return strings.TrimSpace(rendered.String()), err
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

	mode := firstNonEmpty(req.Mode, "proposal")
	facePrompt := prompt.FaceRequest{
		GovernorName:      governorName,
		FaceName:          faceName,
		Channel:           firstNonEmpty(req.Channel, r.cfg.Channel, "telegram"),
		Style:             firstNonEmpty(req.Style, r.cfg.Style),
		PrincipalRole:     req.PrincipalRole,
		LatestUserInput:   req.LatestUserInput,
		PriorProposal:     strings.TrimSpace(req.PriorProposal),
		BrokerageFeedback: strings.TrimSpace(req.BrokerageFeedback),
		StableFiles:       stableFiles,
		DynamicFiles:      dynamicFiles,
		Mode:              mode,
		Scene:             firstNonEmpty(req.Scene),
		Runtime:           req.Runtime,
	}
	systemBlocks := prompt.BuildFacePromptBlocks(facePrompt)
	systemPrompt := prompt.RenderSystemBlocks(systemBlocks)

	resp, err := r.complete(ctx, []agent.Message{
		{Role: "system", Content: systemPrompt, SystemBlocks: systemBlocks},
		{Role: "user", Content: fmt.Sprintf("Speak to %s in one short bounded note about how this turn should move next. Return only that note, or nothing if you have no useful push.", governorName)},
	}, nil, mode)
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

func (r *ProviderRenderer) complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, mode string) (*agent.Response, error) {
	if r == nil || r.provider == nil {
		return nil, fmt.Errorf("provider is nil")
	}
	opts := r.completeOptionsForMode(mode)
	if withOptions, ok := r.provider.(agent.ProviderWithOptions); ok && faceOptionsConfigured(opts) {
		return withOptions.CompleteWithOptions(ctx, messages, tools, opts)
	}
	return r.provider.Complete(ctx, messages, tools)
}

func (r *ProviderRenderer) completeOptionsForMode(mode string) agent.CompleteOptions {
	if r == nil {
		return agent.CompleteOptions{}
	}
	return agent.CompleteOptions{
		Reasoning: r.cfg.Reasoning,
		Verbosity: r.verbosityForMode(mode),
		MaxTokens: r.cfg.MaxTokens,
	}
}

func (r *ProviderRenderer) verbosityForMode(mode string) agent.Verbosity {
	if r != nil {
		if verbosity := normalizeFaceVerbosity(r.cfg.Verbosity); verbosity != "" {
			return verbosity
		}
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "proposal", "brokerage", "repair":
		return agent.VerbosityLow
	default:
		return agent.VerbosityMedium
	}
}

func normalizeFaceVerbosity(verbosity agent.Verbosity) agent.Verbosity {
	switch agent.Verbosity(strings.ToLower(strings.TrimSpace(string(verbosity)))) {
	case agent.VerbosityLow:
		return agent.VerbosityLow
	case agent.VerbosityMedium:
		return agent.VerbosityMedium
	case agent.VerbosityHigh:
		return agent.VerbosityHigh
	default:
		return ""
	}
}

func faceOptionsConfigured(opts agent.CompleteOptions) bool {
	return opts.Reasoning.Effort != "" || opts.Reasoning.Summary != "" || opts.Verbosity != ""
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
