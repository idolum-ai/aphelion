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
)

type stubProvider struct {
	reply      string
	err        error
	lastCalls  int
	lastPrompt string
}

func (s *stubProvider) Complete(_ context.Context, messages []agent.Message, _ []agent.ToolDef) (*agent.Response, error) {
	s.lastCalls++
	if len(messages) > 0 && messages[0].Role == "system" {
		s.lastPrompt = messages[0].Content
	}
	if s.err != nil {
		return nil, s.err
	}
	return &agent.Response{Content: s.reply}, nil
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
		CanonicalReply:  "Canonical text",
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
}

func TestProviderRendererReturnsErrEmptyRender(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{reply: "   "}
	renderer, err := NewProviderRenderer(provider, ProviderRendererConfig{})
	if err != nil {
		t.Fatalf("NewProviderRenderer() err = %v", err)
	}

	_, err = renderer.Render(context.Background(), RenderRequest{
		CanonicalReply: "Canonical text",
	})
	if !errors.Is(err, ErrEmptyRender) {
		t.Fatalf("Render() err = %v, want ErrEmptyRender", err)
	}
}
