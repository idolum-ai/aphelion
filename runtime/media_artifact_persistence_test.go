//go:build linux

package runtime

import (
	"context"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/media"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleInboundPersistsArtifactReferencesInFloorMetadata(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "artifact-aware reply"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1301,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  1,
		Text:       "what do you make of this screenshot?",
		Artifacts: []core.Artifact{
			{ID: "img-2", Channel: "telegram", SourceType: "photo", Kind: "image", MimeType: "image/png", Filename: "screen.png", Data: []byte("image-bytes")},
		},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 1301, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if !strings.Contains(sess.LastFloorMetadata, "\"artifact_id\":\"img-2\"") {
		t.Fatalf("LastFloorMetadata = %q, want artifact reference", sess.LastFloorMetadata)
	}
	if !strings.Contains(sess.LastFloorMetadata, "\"handling\":\"attach_for_vision\"") {
		t.Fatalf("LastFloorMetadata = %q, want handling decision", sess.LastFloorMetadata)
	}
	if !strings.Contains(sess.LastFloorMetadata, "\"retention\":\"session_reference\"") {
		t.Fatalf("LastFloorMetadata = %q, want retention decision", sess.LastFloorMetadata)
	}
	if !strings.Contains(sess.LastFloorMetadata, "\"fetch_state\":\"fetched_memory\"") {
		t.Fatalf("LastFloorMetadata = %q, want fetch state", sess.LastFloorMetadata)
	}
	if !strings.Contains(sess.LastFloorMetadata, "decision_summary") {
		t.Fatalf("LastFloorMetadata = %q, want decision summary", sess.LastFloorMetadata)
	}
}

func TestPrepareInboundTurnPDFExtractionFailureFallsBackToPlaceholder(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	prepared, err := rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    409,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{{
			ID:         "pdf-409",
			Channel:    "telegram",
			SourceType: "document",
			Kind:       "document",
			Subtype:    "pdf",
			Data:       []byte("not-a-real-pdf"),
			MimeType:   "application/pdf",
			Filename:   "broken.pdf",
		}},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}
	if !strings.Contains(prepared.UserText, "PDF attached") {
		t.Fatalf("user text = %q, want PDF placeholder", prepared.UserText)
	}
	if !strings.Contains(prepared.LedgerText, "[pdf attached]") {
		t.Fatalf("ledger text = %q, want pdf attached marker", prepared.LedgerText)
	}
}

func TestPrepareInboundTurnPDFExtractionUsesMediaExtractor(t *testing.T) {
	oldExtractor := newPDFTextExtractor
	newPDFTextExtractor = func() media.PDFTextExtractor {
		return media.PDFTextExtractor{Runner: func(_ context.Context, _ string, _ ...string) ([]byte, []byte, error) {
			return []byte("Hello PDF substrate"), nil, nil
		}}
	}
	t.Cleanup(func() { newPDFTextExtractor = oldExtractor })

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	prepared, err := rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    410,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{{
			ID:         "pdf-410",
			Channel:    "telegram",
			SourceType: "document",
			Kind:       "document",
			Subtype:    "pdf",
			Data: []byte(`%PDF-1.1
1 0 obj
<< /Type /Catalog /Pages 2 0 R >>
endobj
2 0 obj
<< /Type /Pages /Kids [3 0 R] /Count 1 >>
endobj
3 0 obj
<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 144] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>
endobj
4 0 obj
<< /Length 44 >>
stream
BT /F1 24 Tf 72 72 Td (Hello PDF substrate) Tj ET
endstream
endobj
5 0 obj
<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>
endobj
trailer
<< /Root 1 0 R >>
%%EOF`),
			MimeType: "application/pdf",
			Filename: "hello.pdf",
		}},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}
	if !strings.Contains(prepared.UserText, "[DOCUMENT_TEXT]") || !strings.Contains(prepared.UserText, "Hello PDF substrate") {
		t.Fatalf("user text = %q, want extracted PDF text", prepared.UserText)
	}
}

func TestPrepareInboundTurnHydratesTelegramArtifactsAtRuntime(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "spoken transcript"}, fakeSynth{})
	fetcher := &fakeInboundFetcher{data: map[string][]byte{
		"photo-file": []byte("image-bytes"),
		"voice-file": []byte("voice-bytes"),
	}}
	rt.inbound = fetcher
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	prepared, err := rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    1400,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{
			{ID: "img-remote", Channel: "telegram", RemoteID: "photo-file", SourceType: "photo", Kind: "image", MimeType: "image/png", Filename: "screen.png"},
			{ID: "voice-remote", Channel: "telegram", RemoteID: "voice-file", SourceType: "voice", Kind: "audio", Subtype: "voice_note", MimeType: "audio/ogg", Filename: "voice.ogg"},
		},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}
	if len(fetcher.requests) != 2 {
		t.Fatalf("fetch requests = %#v, want two runtime downloads", fetcher.requests)
	}
	if len(prepared.AgentMedia) != 1 || string(prepared.AgentMedia[0].Data) != "image-bytes" {
		t.Fatalf("agent media = %#v, want hydrated image bytes", prepared.AgentMedia)
	}
	if !strings.Contains(prepared.UserText, "spoken transcript") {
		t.Fatalf("user text = %q, want hydrated voice transcript", prepared.UserText)
	}
}

func TestPrepareInboundTurnLeavesMetadataOnlyArtifactsUnfetched(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	fetcher := &fakeInboundFetcher{data: map[string][]byte{
		"video-file": []byte("video-bytes"),
	}}
	rt.inbound = fetcher
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	prepared, err := rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    1401,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{{
			ID:         "video-remote",
			Channel:    "telegram",
			RemoteID:   "video-file",
			SourceType: "video",
			Kind:       "video",
			MimeType:   "video/mp4",
			Filename:   "clip.mp4",
		}},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}
	if len(fetcher.requests) != 0 {
		t.Fatalf("fetch requests = %#v, want none for metadata-only artifact", fetcher.requests)
	}
	if !strings.Contains(prepared.UserText, "clip.mp4") {
		t.Fatalf("user text = %q, want metadata note", prepared.UserText)
	}
}

func TestPrepareInboundTurnPersistsFetchedArtifactToLocalPath(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	fetcher := &fakeInboundFetcher{data: map[string][]byte{
		"doc-file": []byte("hello world"),
	}}
	rt.inbound = fetcher
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	prepared, err := rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    1402,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{{
			ID:         "doc-remote",
			Channel:    "telegram",
			RemoteID:   "doc-file",
			SourceType: "document",
			Kind:       "document",
			Subtype:    "text",
			Filename:   "notes.txt",
			MimeType:   "text/plain",
		}},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}
	if len(prepared.ArtifactRefs) != 1 {
		t.Fatalf("artifact refs len = %d, want 1", len(prepared.ArtifactRefs))
	}
	root := filepath.Join(cfg.Agent.ExecRoot, ".aphelion", "inbound")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%q) err = %v", root, err)
	}
	if len(entries) != 1 {
		t.Fatalf("inbound materialized files = %d, want 1", len(entries))
	}
	path := filepath.Join(root, entries[0].Name())
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) err = %v", path, err)
	}
	if string(data) != "hello world" {
		t.Fatalf("materialized file = %q, want hello world", string(data))
	}
}

func TestPrepareInboundTurnDoesNotPersistUnfetchedArtifact(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	fetcher := &fakeInboundFetcher{data: map[string][]byte{
		"video-file": []byte("video-bytes"),
	}}
	rt.inbound = fetcher
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	_, err = rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    1403,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{{
			ID:         "video-remote-2",
			Channel:    "telegram",
			RemoteID:   "video-file",
			SourceType: "video",
			Kind:       "video",
			Filename:   "clip.mp4",
			MimeType:   "video/mp4",
		}},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}
	root := filepath.Join(cfg.Agent.ExecRoot, ".aphelion", "inbound")
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("inbound root stat err = %v, want not-exist", err)
	}
}

func TestPrepareInboundTurnSkipsMediaWhenOperatorChoseSkip(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "should not appear"}, fakeSynth{})
	fetcher := &fakeInboundFetcher{data: map[string][]byte{
		"voice-skip": []byte("voice-bytes"),
	}}
	rt.inbound = fetcher
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	prepared, err := rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    1402,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{{
			ID:         "voice-skip",
			Channel:    "telegram",
			RemoteID:   "voice-skip",
			SourceType: "voice",
			Kind:       "audio",
			Subtype:    "voice_note",
			Filename:   "voice.ogg",
			MimeType:   "audio/ogg",
			Metadata: map[string]string{
				core.ArtifactMetadataMediaProcessingChoice: "skip",
			},
		}},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}
	if len(fetcher.requests) != 0 {
		t.Fatalf("fetch requests = %#v, want none after skip", fetcher.requests)
	}
	if strings.Contains(prepared.UserText, "should not appear") || !strings.Contains(prepared.UserText, "skipped") {
		t.Fatalf("user text = %q, want skipped note without transcript", prepared.UserText)
	}
	if got := prepared.ArtifactRefs[0].Handling; got != "inspect_metadata" {
		t.Fatalf("handling = %q, want inspect_metadata", got)
	}
}

func TestPrepareInboundTurnAnalyzesVideoWithNativeMedia(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	fetcher := &fakeInboundFetcher{data: map[string][]byte{
		"video-analyze": []byte("video-bytes"),
	}}
	rt.inbound = fetcher
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	prepared, err := rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    1403,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{{
			ID:         "video-analyze",
			Channel:    "telegram",
			RemoteID:   "video-analyze",
			SourceType: "video",
			Kind:       "video",
			Filename:   "clip.mp4",
			MimeType:   "video/mp4",
			Metadata: map[string]string{
				core.ArtifactMetadataMediaProcessingChoice: "analyze",
			},
		}},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}
	if len(fetcher.requests) != 1 || fetcher.requests[0] != "video-analyze" {
		t.Fatalf("fetch requests = %#v, want video analysis download", fetcher.requests)
	}
	if prepared.MediaMode != "video_analysis" || len(prepared.AgentMedia) != 1 {
		t.Fatalf("prepared media mode=%q agent_media=%#v, want video analysis media", prepared.MediaMode, prepared.AgentMedia)
	}
	if prepared.AgentMedia[0].Type != "video" || string(prepared.AgentMedia[0].Data) != "video-bytes" {
		t.Fatalf("agent media = %#v, want video bytes", prepared.AgentMedia[0])
	}
	if got := prepared.ArtifactRefs[0].Handling; got != "attach_for_media_analysis" {
		t.Fatalf("handling = %q, want attach_for_media_analysis", got)
	}
}

func TestHandleInboundPersistsArtifactDecisionHiddenInput(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "artifact decision reply"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	fetcher := &fakeInboundFetcher{data: map[string][]byte{
		"doc-file-hidden": []byte("hello world"),
	}}
	rt.inbound = fetcher

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1302,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  1,
		Text:       "hold onto this text file",
		Artifacts: []core.Artifact{{
			ID:         "doc-hidden",
			Channel:    "telegram",
			RemoteID:   "doc-file-hidden",
			SourceType: "document",
			Kind:       "document",
			Subtype:    "text",
			Filename:   "notes.txt",
			MimeType:   "text/plain",
		}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 1302, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if !strings.Contains(sess.LastFloorMetadata, "artifact_retention_decision") {
		t.Fatalf("LastFloorMetadata = %q, want artifact retention decision hidden input", sess.LastFloorMetadata)
	}
	if !strings.Contains(sess.LastFloorMetadata, "fetched_local") {
		t.Fatalf("LastFloorMetadata = %q, want fetched_local decision trail", sess.LastFloorMetadata)
	}
}

func TestPrepareInboundTurnExplicitTurnRetentionSkipsLocalPersistence(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	fetcher := &fakeInboundFetcher{data: map[string][]byte{
		"doc-turn": []byte("hello turn"),
	}}
	rt.inbound = fetcher
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	prepared, err := rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    1501,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{{
			ID:         "doc-turn",
			Channel:    "telegram",
			RemoteID:   "doc-turn",
			SourceType: "document",
			Kind:       "document",
			Subtype:    "text",
			Filename:   "notes.txt",
			MimeType:   "text/plain",
			Metadata: map[string]string{
				"aphelion_retention_choice": "turn",
				"aphelion_materialize":      "memory_only",
			},
			DefaultRetention: "ephemeral",
		}},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}
	if len(prepared.ArtifactRefs) != 1 {
		t.Fatalf("artifact refs len = %d, want 1", len(prepared.ArtifactRefs))
	}
	if got := prepared.ArtifactRefs[0].FetchState; got != "fetched_memory" {
		t.Fatalf("FetchState = %q, want fetched_memory", got)
	}
	root := filepath.Join(cfg.Agent.ExecRoot, ".aphelion", "inbound")
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("inbound root stat err = %v, want not-exist", err)
	}
}

func TestPrepareInboundTurnExplicitLocalRetentionForcesFetchAndPersistence(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	fetcher := &fakeInboundFetcher{data: map[string][]byte{
		"video-local": []byte("video-bytes"),
	}}
	rt.inbound = fetcher
	scope, err := rt.scopeForPrincipal(principal.Principal{TelegramUserID: 1001, Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	prepared, err := rt.prepareInboundTurn(context.Background(), scope, core.InboundMessage{
		ChatID:    1502,
		SenderID:  1001,
		MessageID: 1,
		Artifacts: []core.Artifact{{
			ID:         "video-local",
			Channel:    "telegram",
			RemoteID:   "video-local",
			SourceType: "video",
			Kind:       "video",
			Filename:   "clip.mp4",
			MimeType:   "video/mp4",
			Metadata: map[string]string{
				"aphelion_retention_choice": "local",
				"aphelion_materialize":      "local",
			},
			DefaultRetention: "child_local",
			RetentionCeiling: "child_local",
		}},
	})
	if err != nil {
		t.Fatalf("prepareInboundTurn() err = %v", err)
	}
	if len(prepared.ArtifactRefs) != 1 {
		t.Fatalf("artifact refs len = %d, want 1", len(prepared.ArtifactRefs))
	}
	if got := prepared.ArtifactRefs[0].FetchState; got != "fetched_local" {
		t.Fatalf("FetchState = %q, want fetched_local", got)
	}
	if got := prepared.ArtifactRefs[0].Retention; got != "child_local" {
		t.Fatalf("Retention = %q, want child_local", got)
	}
	root := filepath.Join(cfg.Agent.ExecRoot, ".aphelion", "inbound")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%q) err = %v", root, err)
	}
	if len(entries) != 1 {
		t.Fatalf("inbound materialized files = %d, want 1", len(entries))
	}
}

func TestHandleInboundIndexesRetainedArtifacts(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Indexed."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.inbound = &fakeInboundFetcher{data: map[string][]byte{
		"telegram-file-keep": []byte("keep these notes"),
	}}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1303,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  21,
		Text:       "keep this locally",
		Artifacts: []core.Artifact{{
			ID:               "doc-keep",
			Channel:          "telegram",
			RemoteID:         "telegram-file-keep",
			SourceType:       "document",
			Kind:             "document",
			Subtype:          "text",
			MimeType:         "text/plain",
			Filename:         "notes.txt",
			DefaultRetention: "child_local",
			Metadata: map[string]string{
				"aphelion_retention_choice": "local",
			},
		}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	hits, err := store.SearchArtifacts("notes.txt", 10, nil)
	if err != nil {
		t.Fatalf("SearchArtifacts() err = %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("artifact hits len = %d, want 1", len(hits))
	}
	if hits[0].ArtifactID != "doc-keep" {
		t.Fatalf("ArtifactID = %q, want doc-keep", hits[0].ArtifactID)
	}
	if hits[0].Retention != "child_local" {
		t.Fatalf("Retention = %q, want child_local", hits[0].Retention)
	}
	if hits[0].FetchState != "fetched_local" {
		t.Fatalf("FetchState = %q, want fetched_local", hits[0].FetchState)
	}
	if !strings.Contains(hits[0].MaterializedPath, "notes.txt") {
		t.Fatalf("MaterializedPath = %q, want notes.txt suffix", hits[0].MaterializedPath)
	}
}

func TestHandleInboundVoiceInputCanChooseTextReplyModality(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.faceReplyText = "REPLY_MODALITY: text\nUse this exact command: go test ./..."
	var synthesized string
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "please give exact commands"}, fakeSynth{
		media:    core.Media{Type: "voice", Data: []byte("mp3"), MimeType: "audio/mpeg", Filename: "reply.mp3"},
		lastText: &synthesized,
	})

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1210,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  90,
		Artifacts:  []core.Artifact{{ID: "voice-text-choice", Channel: "telegram", SourceType: "voice", Kind: "audio", Subtype: "voice_note", Data: []byte("ogg"), MimeType: "audio/ogg", Filename: "voice.ogg"}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.voice) != 0 {
		t.Fatalf("voice sends = %d, want 0 after text modality directive", len(sender.voice))
	}
	if synthesized != "" {
		t.Fatalf("synthesized = %q, want empty", synthesized)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("text sends = %d, want 1", len(sender.sent))
	}
	if strings.Contains(sender.sent[0].Text, "REPLY_MODALITY") || !strings.Contains(sender.sent[0].Text, "go test ./...") {
		t.Fatalf("text reply = %q, want stripped directive and exact command", sender.sent[0].Text)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenFaceSystem) == 0 || !strings.Contains(provider.seenFaceSystem[len(provider.seenFaceSystem)-1], "reply_modality_default: voice") || !strings.Contains(provider.seenFaceSystem[len(provider.seenFaceSystem)-1], "REPLY_MODALITY: text") {
		t.Fatalf("face prompt = %q, want voice modality awareness and directive contract", provider.seenFaceSystem)
	}
}
