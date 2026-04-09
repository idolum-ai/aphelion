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

func TestProviderRendererLoadsHostFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "HOST.md"), []byte("host identity"), 0o600); err != nil {
		t.Fatalf("write HOST.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "QUESTIONS-TO-HOST.md"), []byte("avoid empty praise"), 0o600); err != nil {
		t.Fatalf("write QUESTIONS-TO-HOST.md: %v", err)
	}

	provider := &stubProvider{reply: "Rendered host reply"}
	renderer, err := NewProviderRenderer(provider, ProviderRendererConfig{
		GovernorName:  "Aphelion",
		FaceName:      "Host",
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
	if got != "Rendered host reply" {
		t.Fatalf("Render() = %q, want rendered host text", got)
	}
	if !strings.Contains(provider.lastPrompt, "### HOST.md") {
		t.Fatalf("face prompt missing HOST.md content: %q", provider.lastPrompt)
	}
	if !strings.Contains(provider.lastPrompt, "### QUESTIONS-TO-HOST.md") {
		t.Fatalf("face prompt missing QUESTIONS-TO-HOST.md content: %q", provider.lastPrompt)
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
