//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestEvidenceHydrateToolReturnsCurrentSessionEvidence(t *testing.T) {
	t.Parallel()

	store := newToolEvidenceStore(t)
	key := session.SessionKey{
		ChatID: 2001,
		UserID: 42,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "2001"},
	}
	object, err := store.UpsertEvidenceObject(session.EvidenceObjectInput{
		SourceKind:      session.EvidenceSourceOperationState,
		SourceRef:       "operation_state:op-tool:source",
		SessionID:       session.SessionIDForKey(key),
		ChatID:          key.ChatID,
		UserID:          key.UserID,
		Scope:           key.Scope,
		EpistemicStatus: session.EvidenceStatusProjection,
		SubjectKey:      "op-tool",
		Summary:         "Canonical source says inspect release.yml before proposing continuation.",
		PayloadJSON:     `{"operation_id":"op-tool","target":"release.yml"}`,
		ObservedAt:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("UpsertEvidenceObject() err = %v", err)
	}
	registry := NewRegistry(t.TempDir(), time.Second).WithSessionStore(store)
	out, err := registry.executeWithScopeAndPrincipal(
		context.Background(),
		"evidence_hydrate",
		json.RawMessage(`{"query":"continue op-tool from canonical source","operation_id":"op-tool","limit":4}`),
		sandbox.Scope{WorkingRoot: t.TempDir()},
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 42},
		key,
	)
	if err != nil {
		t.Fatalf("execute evidence_hydrate err = %v", err)
	}
	for _, want := range []string{"[EVIDENCE_HYDRATION]", object.ID, "operation_state", "release.yml", "[/EVIDENCE_HYDRATION]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, want %q", out, want)
		}
	}
	runs, err := store.EvidenceHydrationRunsBySession(key, 1)
	if err != nil {
		t.Fatalf("EvidenceHydrationRunsBySession() err = %v", err)
	}
	if len(runs) != 1 || runs[0].Status != "completed" {
		t.Fatalf("hydration runs = %#v, want one completed run", runs)
	}
}

func TestEvidenceHydrateToolReportsCrossSessionRequiredIDMissing(t *testing.T) {
	t.Parallel()

	store := newToolEvidenceStore(t)
	key := session.SessionKey{ChatID: 2002, UserID: 42, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "2002"}}
	otherKey := session.SessionKey{ChatID: 2003, UserID: 42, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "2003"}}
	other, err := store.UpsertEvidenceObject(session.EvidenceObjectInput{
		SourceKind:      session.EvidenceSourceMessage,
		SourceRef:       "messages:other-thread",
		SessionID:       session.SessionIDForKey(otherKey),
		ChatID:          otherKey.ChatID,
		UserID:          otherKey.UserID,
		Scope:           otherKey.Scope,
		EpistemicStatus: session.EvidenceStatusClaimed,
		Summary:         "Other thread should not hydrate.",
		PayloadJSON:     `{"content":"other thread"}`,
	})
	if err != nil {
		t.Fatalf("UpsertEvidenceObject(other) err = %v", err)
	}
	registry := NewRegistry(t.TempDir(), time.Second).WithSessionStore(store)
	out, err := registry.executeWithScopeAndPrincipal(
		context.Background(),
		"evidence_hydrate",
		json.RawMessage(`{"query":"stay in this thread","required_evidence_ids":["`+other.ID+`"]}`),
		sandbox.Scope{WorkingRoot: t.TempDir()},
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 42},
		key,
	)
	if err != nil {
		t.Fatalf("execute evidence_hydrate err = %v", err)
	}
	if !strings.Contains(out, "missing_required: "+other.ID) {
		t.Fatalf("output = %q, want cross-session id reported missing", out)
	}
	if strings.Contains(out, "Other thread should not hydrate") {
		t.Fatalf("output = %q, leaked cross-session evidence summary", out)
	}
}

func newToolEvidenceStore(t *testing.T) *session.SQLiteStore {
	t.Helper()
	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
