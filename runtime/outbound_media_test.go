//go:build linux

package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestExtractOutboundReplyMediaParsesDirectives(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	user := filepath.Join(root, "user")
	for _, dir := range []string{shared, user} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) err = %v", dir, err)
		}
	}
	path := filepath.Join(root, "chart.png")
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) err = %v", path, err)
	}

	text, media := extractOutboundReplyMedia(sandbox.Scope{
		WorkingRoot:      root,
		SharedMemoryRoot: shared,
		UserMemory:       user,
	}, `Here you go.

MEDIA: {"path":"chart.png"}
`, nil)

	if text != "Here you go." {
		t.Fatalf("text = %q, want %q", text, "Here you go.")
	}
	if len(media) != 1 {
		t.Fatalf("media len = %d, want 1", len(media))
	}
	if media[0].Type != "image" {
		t.Fatalf("media type = %q, want image", media[0].Type)
	}
	if media[0].Path != path {
		t.Fatalf("media path = %q, want %q", media[0].Path, path)
	}
}

func TestExtractOutboundReplyMediaMarksAudioAsVoice(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "reply.mp3")
	if err := os.WriteFile(path, []byte("mp3"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) err = %v", path, err)
	}

	text, media := extractOutboundReplyMedia(sandbox.Scope{WorkingRoot: root}, `MEDIA: {"path":"reply.mp3","type":"voice"}`, nil)

	if text != "" {
		t.Fatalf("text = %q, want empty", text)
	}
	if len(media) != 1 {
		t.Fatalf("media len = %d, want 1", len(media))
	}
	if media[0].Type != "voice" {
		t.Fatalf("media type = %q, want voice", media[0].Type)
	}
}

func TestExtractOutboundReplyMediaDropsPathsOutsideScope(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) err = %v", outside, err)
	}

	text, media := extractOutboundReplyMedia(sandbox.Scope{WorkingRoot: root}, `Summary
MEDIA: {"path":"`+outside+`"}`, nil)

	if text != "Summary" {
		t.Fatalf("text = %q, want %q", text, "Summary")
	}
	if len(media) != 0 {
		t.Fatalf("media len = %d, want 0", len(media))
	}
}

func TestExtractOutboundReplyMediaIgnoresLegacyBareMediaText(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "chart.png")
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) err = %v", path, err)
	}

	text, media := extractOutboundReplyMedia(sandbox.Scope{WorkingRoot: root}, "Summary\nMEDIA: chart.png", nil)

	if text != "Summary\nMEDIA: chart.png" {
		t.Fatalf("text = %q, want unrecognized media line preserved as ordinary text", text)
	}
	if len(media) != 0 {
		t.Fatalf("media len = %d, want 0", len(media))
	}
}
