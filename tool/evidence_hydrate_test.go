//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestEvidenceHydrateToolReturnsExplicitPayloadWindow(t *testing.T) {
	t.Parallel()

	store := newToolEvidenceStore(t)
	key := session.SessionKey{
		ChatID: 2004,
		UserID: 42,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "2004"},
	}
	output := "HEAD important\n" + strings.Repeat("middle retained line\n", 20) + "TAIL important\n"
	payload, err := json.Marshal(map[string]any{
		"tool":   "exec",
		"output": output,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	object, err := store.UpsertEvidenceObject(session.EvidenceObjectInput{
		SourceKind:      session.EvidenceSourceToolOutput,
		SourceRef:       "tool_output:run:exec:sha",
		SessionID:       session.SessionIDForKey(key),
		ChatID:          key.ChatID,
		UserID:          key.UserID,
		Scope:           key.Scope,
		EpistemicStatus: session.EvidenceStatusAttested,
		SubjectKey:      "exec",
		Summary:         "Large exec output",
		Digest:          "sha256:abc omitted middle",
		PayloadJSON:     string(payload),
		ObservedAt:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("UpsertEvidenceObject() err = %v", err)
	}
	registry := NewRegistry(t.TempDir(), time.Second).WithSessionStore(store)
	input := fmt.Sprintf(`{"query":"inspect retained tool output","required_evidence_ids":[%q],"include_payload_ids":[%q],"payload_offset":15,"payload_limit":80}`, object.ID, object.ID)
	out, err := registry.executeWithScopeAndPrincipal(
		context.Background(),
		"evidence_hydrate",
		json.RawMessage(input),
		sandbox.Scope{WorkingRoot: t.TempDir()},
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 42},
		key,
	)
	if err != nil {
		t.Fatalf("execute evidence_hydrate err = %v", err)
	}
	for _, want := range []string{object.ID, "payload_window: offset=15", "next_offset=95", "middle retained line"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, want %q", out, want)
		}
	}
	if strings.Contains(out, "HEAD important") || strings.Contains(out, "TAIL important") {
		t.Fatalf("output = %q, want bounded payload window only", out)
	}
}

func TestEvidenceHydrateToolWithholdsCredentialBearingPayloadWindow(t *testing.T) {
	t.Parallel()

	store := newToolEvidenceStore(t)
	key := session.SessionKey{
		ChatID: 2005,
		UserID: 42,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "2005"},
	}
	payload, err := json.Marshal(map[string]any{
		"tool":   "exec",
		"output": "before\nAuthorization: Bearer bearer-secret-value\nOPENAI_API_KEY=sk-output-secret-value\nafter\n",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	object, err := store.UpsertEvidenceObject(session.EvidenceObjectInput{
		SourceKind:      session.EvidenceSourceToolOutput,
		SourceRef:       "tool_output:run:exec:redacted",
		SessionID:       session.SessionIDForKey(key),
		ChatID:          key.ChatID,
		UserID:          key.UserID,
		Scope:           key.Scope,
		EpistemicStatus: session.EvidenceStatusAttested,
		RedactionClass:  session.EvidenceRedactionRedacted,
		SubjectKey:      "exec",
		Summary:         "Large exec output",
		Digest:          "sha256:abc omitted middle",
		PayloadJSON:     string(payload),
		ObservedAt:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("UpsertEvidenceObject() err = %v", err)
	}
	registry := NewRegistry(t.TempDir(), time.Second).WithSessionStore(store)
	input := fmt.Sprintf(`{"query":"inspect retained tool output","required_evidence_ids":[%q],"include_payload_ids":[%q],"payload_limit":400}`, object.ID, object.ID)
	out, err := registry.executeWithScopeAndPrincipal(
		context.Background(),
		"evidence_hydrate",
		json.RawMessage(input),
		sandbox.Scope{WorkingRoot: t.TempDir()},
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 42},
		key,
	)
	if err != nil {
		t.Fatalf("execute evidence_hydrate err = %v", err)
	}
	for _, secret := range []string{"bearer-secret-value", "sk-output-secret-value"} {
		if strings.Contains(out, secret) {
			t.Fatalf("hydration leaked %q: %s", secret, out)
		}
	}
	for _, want := range []string{"payload_withheld: redaction_class=credential_bearing"} {
		if !strings.Contains(out, want) {
			t.Fatalf("hydration = %q, want %q", out, want)
		}
	}
}

func TestEvidenceHydrateToolWindowsAlreadyRedactedPayload(t *testing.T) {
	t.Parallel()

	store := newToolEvidenceStore(t)
	key := session.SessionKey{
		ChatID: 2007,
		UserID: 42,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "2007"},
	}
	payload, err := json.Marshal(map[string]any{
		"tool":   "exec",
		"output": "before\nAuthorization: Bearer <redacted:bearer:abcdef123456>\nafter\n",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	object, err := store.UpsertEvidenceObject(session.EvidenceObjectInput{
		SourceKind:      session.EvidenceSourceToolOutput,
		SourceRef:       "tool_output:run:exec:already-redacted",
		SessionID:       session.SessionIDForKey(key),
		ChatID:          key.ChatID,
		UserID:          key.UserID,
		Scope:           key.Scope,
		EpistemicStatus: session.EvidenceStatusAttested,
		RedactionClass:  session.EvidenceRedactionRedacted,
		SubjectKey:      "exec",
		Summary:         "Large exec output",
		Digest:          "sha256:abc omitted middle",
		PayloadJSON:     string(payload),
		ObservedAt:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("UpsertEvidenceObject() err = %v", err)
	}
	registry := NewRegistry(t.TempDir(), time.Second).WithSessionStore(store)
	input := fmt.Sprintf(`{"query":"inspect retained tool output","required_evidence_ids":[%q],"include_payload_ids":[%q],"payload_limit":400}`, object.ID, object.ID)
	out, err := registry.executeWithScopeAndPrincipal(
		context.Background(),
		"evidence_hydrate",
		json.RawMessage(input),
		sandbox.Scope{WorkingRoot: t.TempDir()},
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 42},
		key,
	)
	if err != nil {
		t.Fatalf("execute evidence_hydrate err = %v", err)
	}
	if strings.Contains(out, "payload_withheld") {
		t.Fatalf("hydration withheld already redacted payload: %s", out)
	}
	if !strings.Contains(out, "<redacted:bearer:abcdef123456>") {
		t.Fatalf("hydration = %q, want redacted marker", out)
	}
}

func TestEvidenceHydrateToolWithholdsNonHydratablePayload(t *testing.T) {
	t.Parallel()

	store := newToolEvidenceStore(t)
	key := session.SessionKey{
		ChatID: 2006,
		UserID: 42,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "2006"},
	}
	object, err := store.UpsertEvidenceObject(session.EvidenceObjectInput{
		SourceKind:      session.EvidenceSourceToolOutput,
		SourceRef:       "tool_output:run:exec:blocked",
		SessionID:       session.SessionIDForKey(key),
		ChatID:          key.ChatID,
		UserID:          key.UserID,
		Scope:           key.Scope,
		EpistemicStatus: session.EvidenceStatusAttested,
		RedactionClass:  session.EvidenceRedactionBlocked,
		SubjectKey:      "exec",
		Summary:         "Operator-only output",
		Digest:          "sha256:abc omitted middle",
		PayloadJSON:     `{"output":"operator-only-secret"}`,
		ObservedAt:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("UpsertEvidenceObject() err = %v", err)
	}
	registry := NewRegistry(t.TempDir(), time.Second).WithSessionStore(store)
	input := fmt.Sprintf(`{"query":"inspect retained tool output","required_evidence_ids":[%q],"include_payload_ids":[%q]}`, object.ID, object.ID)
	out, err := registry.executeWithScopeAndPrincipal(
		context.Background(),
		"evidence_hydrate",
		json.RawMessage(input),
		sandbox.Scope{WorkingRoot: t.TempDir()},
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 42},
		key,
	)
	if err != nil {
		t.Fatalf("execute evidence_hydrate err = %v", err)
	}
	if strings.Contains(out, "operator-only-secret") {
		t.Fatalf("hydration leaked withheld payload: %s", out)
	}
	if !strings.Contains(out, "payload_withheld: redaction_class=non_hydratable") {
		t.Fatalf("hydration = %q, want payload_withheld", out)
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
