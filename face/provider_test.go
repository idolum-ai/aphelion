//go:build linux

package face

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
)

type stubProvider struct {
	reply      string
	streamText string
	err        error
	lastCalls  int
	lastPrompt string
	lastUser   string
}

func (s *stubProvider) Complete(_ context.Context, messages []agent.Message, _ []agent.ToolDef) (*agent.Response, error) {
	s.lastCalls++
	if len(messages) > 0 && messages[0].Role == "system" {
		s.lastPrompt = messages[0].Content
	}
	if len(messages) > 1 {
		s.lastUser = messages[1].Content
	}
	if s.err != nil {
		return nil, s.err
	}
	return &agent.Response{Content: s.reply}, nil
}

func (s *stubProvider) Stream(_ context.Context, messages []agent.Message, _ []agent.ToolDef, cb agent.StreamCallback) (*agent.Response, error) {
	s.lastCalls++
	if len(messages) > 0 && messages[0].Role == "system" {
		s.lastPrompt = messages[0].Content
	}
	if len(messages) > 1 {
		s.lastUser = messages[1].Content
	}
	if s.err != nil {
		return nil, s.err
	}
	streamText := s.streamText
	if streamText == "" {
		streamText = s.reply
	}
	for _, chunk := range []string{"Rendered ", "idolum ", "reply"} {
		if streamText != "Rendered idolum reply" {
			break
		}
		if err := cb(agent.StreamChunk{Type: "text", Text: chunk}); err != nil {
			return nil, err
		}
	}
	if streamText != "Rendered idolum reply" && streamText != "" {
		if err := cb(agent.StreamChunk{Type: "text", Text: streamText}); err != nil {
			return nil, err
		}
	}
	return &agent.Response{Content: streamText, Usage: core.TokenUsage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}}, nil
}

func TestProviderRendererLoadsIdolumFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "IDOLUM.md"), []byte("idolum identity"), 0o600); err != nil {
		t.Fatalf("write IDOLUM.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "QUESTIONS-TO-IDOLUM.md"), []byte("avoid empty praise"), 0o600); err != nil {
		t.Fatalf("write QUESTIONS-TO-IDOLUM.md: %v", err)
	}

	provider := &stubProvider{reply: "Rendered idolum reply"}
	renderer, err := NewProviderRenderer(provider, ProviderRendererConfig{
		GovernorName:  "Aphelion",
		FaceName:      "Idolum",
		Channel:       "telegram",
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("NewProviderRenderer() err = %v", err)
	}

	got, err := renderer.Render(context.Background(), RenderRequest{
		FloorText:       "Canonical text",
		LatestUserInput: "How are you?",
		PrincipalRole:   "admin",
	})
	if err != nil {
		t.Fatalf("Render() err = %v", err)
	}
	if got != "Rendered idolum reply" {
		t.Fatalf("Render() = %q, want rendered idolum text", got)
	}
	if !strings.Contains(provider.lastPrompt, "### IDOLUM.md") {
		t.Fatalf("face prompt missing IDOLUM.md content: %q", provider.lastPrompt)
	}
	if !strings.Contains(provider.lastPrompt, "### QUESTIONS-TO-IDOLUM.md") {
		t.Fatalf("face prompt missing QUESTIONS-TO-IDOLUM.md content: %q", provider.lastPrompt)
	}
	if !strings.Contains(provider.lastUser, "Idolum") || !strings.Contains(provider.lastUser, "Aphelion-authored material") {
		t.Fatalf("render transport prompt = %q, want resolved face and governor names", provider.lastUser)
	}
}

func TestProviderRendererProposalLoadsIdolumFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "IDOLUM.md"), []byte("idolum identity"), 0o600); err != nil {
		t.Fatalf("write IDOLUM.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "QUESTIONS-TO-IDOLUM.md"), []byte("push for initiative"), 0o600); err != nil {
		t.Fatalf("write QUESTIONS-TO-IDOLUM.md: %v", err)
	}

	provider := &stubProvider{reply: "Tell Aphelion to lead with warmth."}
	renderer, err := NewProviderRenderer(provider, ProviderRendererConfig{
		GovernorName:  "Aphelion",
		FaceName:      "Idolum",
		Channel:       "telegram",
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("NewProviderRenderer() err = %v", err)
	}

	got, err := renderer.Propose(context.Background(), ProposalRequest{
		LatestUserInput: "I am feeling fragile today.",
		PrincipalRole:   "admin",
	})
	if err != nil {
		t.Fatalf("Propose() err = %v", err)
	}
	if got != "Tell Aphelion to lead with warmth." {
		t.Fatalf("Propose() = %q, want advisory text", got)
	}
	if !strings.Contains(provider.lastPrompt, "mode: proposal") {
		t.Fatalf("proposal prompt missing proposal mode: %q", provider.lastPrompt)
	}
	if !strings.Contains(provider.lastPrompt, "push for initiative") {
		t.Fatalf("proposal prompt missing dynamic face file: %q", provider.lastPrompt)
	}
	if !strings.Contains(provider.lastUser, "Aphelion") {
		t.Fatalf("proposal transport prompt = %q, want resolved governor name", provider.lastUser)
	}
}

func TestProviderRendererUsesResolvedNamesInTransportPrompts(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{reply: "Rendered reply"}
	renderer, err := NewProviderRenderer(provider, ProviderRendererConfig{
		GovernorName: "Host",
		FaceName:     "Mada",
		Channel:      "telegram",
	})
	if err != nil {
		t.Fatalf("NewProviderRenderer() err = %v", err)
	}

	if _, err := renderer.Render(context.Background(), RenderRequest{
		FloorText:       "Canonical text",
		LatestUserInput: "Hello",
		PrincipalRole:   "admin",
	}); err != nil {
		t.Fatalf("Render() err = %v", err)
	}
	if !strings.Contains(provider.lastUser, "Mada") || !strings.Contains(provider.lastUser, "Host-authored material") {
		t.Fatalf("render transport prompt = %q, want configured names", provider.lastUser)
	}

	if _, err := renderer.Propose(context.Background(), ProposalRequest{
		LatestUserInput: "Think this through",
		PrincipalRole:   "admin",
	}); err != nil {
		t.Fatalf("Propose() err = %v", err)
	}
	if !strings.Contains(provider.lastUser, "Host") || strings.Contains(provider.lastUser, "Aphelion") {
		t.Fatalf("proposal transport prompt = %q, want configured governor name only", provider.lastUser)
	}
}

func TestProviderRendererReturnsErrEmptyRender(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{reply: "   "}
	renderer, err := NewProviderRenderer(provider, ProviderRendererConfig{})
	if err != nil {
		t.Fatalf("NewProviderRenderer() err = %v", err)
	}

	_, err = renderer.Render(context.Background(), RenderRequest{
		FloorText: "Canonical text",
	})
	if !errors.Is(err, ErrEmptyRender) {
		t.Fatalf("Render() err = %v, want ErrEmptyRender", err)
	}
}

func TestProviderRendererRenderPromptIncludesMaterialFloor(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{reply: "Rendered reply"}
	renderer, err := NewProviderRenderer(provider, ProviderRendererConfig{
		GovernorName: "Aphelion",
		FaceName:     "Idolum",
		Channel:      "telegram",
	})
	if err != nil {
		t.Fatalf("NewProviderRenderer() err = %v", err)
	}

	if _, err := renderer.Render(context.Background(), RenderRequest{
		FloorText: "legacy canonical",
		MaterialFloor: core.MaterialPacket{
			Facts:            []string{"The codebase was inspected."},
			SceneConstraints: []string{"Keep the tone direct."},
		},
		LatestUserInput: "What should we build?",
	}); err != nil {
		t.Fatalf("Render() err = %v", err)
	}

	if !strings.Contains(provider.lastPrompt, "## Governor Material Floor") {
		t.Fatalf("render prompt missing material floor section: %q", provider.lastPrompt)
	}
	if strings.Contains(provider.lastPrompt, "## Serialized Floor Fallback") {
		t.Fatalf("render prompt should prefer material floor over serialized floor fallback: %q", provider.lastPrompt)
	}
}

func TestProviderRendererRenderStream(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "IDOLUM.md"), []byte("idolum identity"), 0o600); err != nil {
		t.Fatalf("write IDOLUM.md: %v", err)
	}

	provider := &stubProvider{streamText: "Rendered idolum reply"}
	renderer, err := NewProviderRenderer(provider, ProviderRendererConfig{
		GovernorName:  "Aphelion",
		FaceName:      "Idolum",
		Channel:       "telegram",
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("NewProviderRenderer() err = %v", err)
	}

	var chunks []string
	got, err := renderer.RenderStream(context.Background(), RenderRequest{
		FloorText:       "Canonical text",
		LatestUserInput: "How are you?",
		PrincipalRole:   "admin",
	}, func(text string) error {
		chunks = append(chunks, text)
		return nil
	})
	if err != nil {
		t.Fatalf("RenderStream() err = %v", err)
	}
	if got != "Rendered idolum reply" {
		t.Fatalf("RenderStream() = %q, want rendered idolum text", got)
	}
	if strings.Join(chunks, "") != "Rendered idolum reply" {
		t.Fatalf("chunks = %#v, want rendered idolum reply", chunks)
	}
	if usage := renderer.ConsumeLastUsage(); usage.TotalTokens != 3 {
		t.Fatalf("usage = %+v, want total 3", usage)
	}
}
