//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func TestKeepTelegramArtifactsPermanentlySavesOrdinaryMedia(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := session.NewSQLiteStore(filepath.Join(root, "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	rt := &Runtime{
		cfg: &config.Config{
			Agent: config.AgentConfig{
				PromptRoot:       filepath.Join(root, "prompt"),
				ExecRoot:         filepath.Join(root, "exec"),
				SharedMemoryRoot: filepath.Join(root, "shared"),
			},
			Telegram: config.TelegramConfig{
				Media: config.TelegramMediaConfig{DownloadMaxSize: "1MB"},
			},
		},
		store:    store,
		resolver: principal.NewResolver([]int64{42}, nil),
	}

	msg := core.InboundMessage{
		ChatID:    7,
		SenderID:  42,
		MessageID: 99,
		Artifacts: []core.Artifact{
			{
				ID:         "photo-1",
				Channel:    "telegram",
				Kind:       "image",
				SourceType: "photo",
				Filename:   "photo.jpg",
				MimeType:   "image/jpeg",
				Data:       []byte("image-bytes"),
			},
			{
				ID:         "doc-1",
				Channel:    "telegram",
				Kind:       "document",
				SourceType: "document",
				Subtype:    "text",
				Filename:   "notes.txt",
				MimeType:   "text/plain",
				Data:       []byte("document-bytes"),
			},
		},
	}

	if err := rt.KeepTelegramArtifactsPermanently(context.Background(), msg); err != nil {
		t.Fatalf("KeepTelegramArtifactsPermanently() err = %v", err)
	}
	sess, err := store.Load(session.SessionKey{ChatID: 7, UserID: 0, Scope: telegramDMScopeRef(7)})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	var metadata core.FloorMetadata
	if err := json.Unmarshal([]byte(sess.LastFloorMetadata), &metadata); err != nil {
		t.Fatalf("LastFloorMetadata = %q, unmarshal err = %v", sess.LastFloorMetadata, err)
	}
	if len(metadata.Artifacts) != 2 {
		t.Fatalf("metadata artifacts = %#v, want image and document refs", metadata.Artifacts)
	}
	pathsByKind := map[string]string{}
	for _, ref := range metadata.Artifacts {
		pathsByKind[ref.Kind] = ref.MaterializedPath
		if ref.Retention != "child_local" || ref.FetchState != "fetched_local" {
			t.Fatalf("ref = %#v, want child_local fetched_local", ref)
		}
		if _, err := os.Stat(ref.MaterializedPath); err != nil {
			t.Fatalf("stat materialized path %q err = %v", ref.MaterializedPath, err)
		}
	}
	if got := pathsByKind["image"]; !strings.Contains(got, filepath.Join("artifacts", "images")) {
		t.Fatalf("image path = %q, want artifacts/images", got)
	}
	if got := pathsByKind["document"]; !strings.Contains(got, filepath.Join("artifacts", "files")) {
		t.Fatalf("document path = %q, want artifacts/files", got)
	}
}
