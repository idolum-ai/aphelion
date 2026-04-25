//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestMemoryToolAdminWritesSharedKnowledge(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver)
	setFakeBubblewrapRunner(t, registry)
	_, err = registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		"memory",
		json.RawMessage(`{"action":"add","scope":"shared","store":"knowledge","content":"Prefers concise updates","source_tag":"observed","confidence":0.9}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForPrincipal(memory) err = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(tmp, "shared-memory", "memory", "knowledge.md"))
	if err != nil {
		t.Fatalf("ReadFile() err = %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "Prefers concise updates") {
		t.Fatalf("knowledge.md = %q, want content", text)
	}
	if !strings.Contains(text, "[observed, confidence: 0.90]") {
		t.Fatalf("knowledge.md = %q, want provenance tag", text)
	}
}

func TestMemoryToolAdminPrincipalScopeFallsBackToSharedMemory(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver)
	setFakeBubblewrapRunner(t, registry)
	out, err := registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		"memory",
		json.RawMessage(`{"action":"add","scope":"principal","store":"knowledge","content":"Admin global note."}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForPrincipal(memory principal scope) err = %v", err)
	}
	if !strings.Contains(out, "scope=shared") {
		t.Fatalf("output = %q, want admin principal scope normalized to shared", out)
	}

	raw, err := os.ReadFile(filepath.Join(tmp, "shared-memory", "memory", "knowledge.md"))
	if err != nil {
		t.Fatalf("ReadFile() err = %v", err)
	}
	if !strings.Contains(string(raw), "Admin global note.") {
		t.Fatalf("knowledge.md = %q, want persisted shared note", string(raw))
	}
}

func TestMemoryToolApprovedUserWritesPrincipalMemory(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver)
	setFakeBubblewrapRunner(t, registry)
	_, err = registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{TelegramUserID: 42, Role: principal.RoleApprovedUser},
		"memory",
		json.RawMessage(`{"action":"add","store":"memory","content":"Private preference retained."}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForPrincipal(memory) err = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(tmp, "users-memory", "42", "MEMORY.md"))
	if err != nil {
		t.Fatalf("ReadFile() err = %v", err)
	}
	if !strings.Contains(string(raw), "Private preference retained.") {
		t.Fatalf("MEMORY.md = %q, want content", string(raw))
	}
}

func TestMemoryToolApprovedUserCannotWriteSharedMemory(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver)
	setFakeBubblewrapRunner(t, registry)
	_, err = registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{TelegramUserID: 42, Role: principal.RoleApprovedUser},
		"memory",
		json.RawMessage(`{"action":"add","scope":"shared","store":"memory","content":"should fail"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForPrincipal(memory) err = nil, want shared memory denial")
	}
	if !strings.Contains(err.Error(), "may not write shared memory") {
		t.Fatalf("err = %v, want shared memory denial", err)
	}
}

func TestMemoryToolApprovesProposal(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	sharedRoot := filepath.Join(tmp, "shared-memory")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  sharedRoot,
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	proposal, err := memstore.CreateProposal(memstore.ProposalRequest{
		Root:       sharedRoot,
		Scope:      "shared",
		Store:      memstore.StoreDecisions,
		SourceKind: "reflection",
		Reason:     "test",
		Content:    "- Keep proposal approval reviewable.",
		Now:        time.Date(2026, 4, 25, 1, 2, 3, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateProposal() err = %v", err)
	}

	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver)
	setFakeBubblewrapRunner(t, registry)
	out, err := registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		"memory",
		json.RawMessage(`{"action":"proposal_approve","scope":"shared","proposal_id":"`+proposal.ID+`"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForPrincipal(memory proposal_approve) err = %v", err)
	}
	if !strings.Contains(out, "memory_proposal_approved") {
		t.Fatalf("output = %q, want approval confirmation", out)
	}
	raw, err := os.ReadFile(filepath.Join(sharedRoot, "memory", "decisions.md"))
	if err != nil {
		t.Fatalf("ReadFile(decisions.md) err = %v", err)
	}
	if !strings.Contains(string(raw), "aphelion-memory-entry:v1") || !strings.Contains(string(raw), "proposal approval") {
		t.Fatalf("decisions.md = %q, want instrumented approved proposal", string(raw))
	}
}

func TestSessionSearchAdminCanSearchAllSessions(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	store := newSessionSearchStore(t)
	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunner(t, registry)

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		session.SessionKey{ChatID: 1, UserID: 0},
		"session_search",
		json.RawMessage(`{"query":"alpha","scope":"all"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(session_search) err = %v", err)
	}
	if !strings.Contains(out, "[SESSION_RECALL]") || !strings.Contains(out, "[/SESSION_RECALL]") {
		t.Fatalf("output = %q, want fenced recall block", out)
	}
	if !strings.Contains(out, "chat=2") {
		t.Fatalf("output = %q, want cross-session hit for admin", out)
	}
}

func TestSessionSearchApprovedUserIsForcedToCurrentSession(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	store := newSessionSearchStore(t)
	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunner(t, registry)

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{TelegramUserID: 42, Role: principal.RoleApprovedUser},
		session.SessionKey{ChatID: 1, UserID: 0},
		"session_search",
		json.RawMessage(`{"query":"alpha","scope":"all"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(session_search) err = %v", err)
	}
	if strings.Contains(out, "chat=2") {
		t.Fatalf("output = %q, want approved user confined to current session", out)
	}
	if !strings.Contains(out, "scope: session") {
		t.Fatalf("output = %q, want forced session scope", out)
	}
}

func TestSemanticSearchAdminSearchesSharedCuratedMemory(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	sharedRoot := filepath.Join(tmp, "shared-memory")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  sharedRoot,
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sharedRoot, "memory"), 0o755); err != nil {
		t.Fatalf("MkdirAll(shared memory) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sharedRoot, "memory", "knowledge.md"), []byte("# knowledge.md\n\n- Prefers concise progress updates [observed]"), 0o600); err != nil {
		t.Fatalf("WriteFile(knowledge.md) err = %v", err)
	}

	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver).
		WithSemanticEngine(memstore.NewSemanticEngine(memstore.SemanticOptions{
			Enabled:             true,
			DBPath:              filepath.Join(tmp, "semantic.db"),
			Sources:             []string{"memory/knowledge.md"},
			InteractiveTopK:     5,
			HeartbeatTopK:       12,
			InteractiveMaxChars: 4000,
			HeartbeatMaxChars:   12000,
			DailyNotesDir:       "memory/daily",
		}))
	setFakeBubblewrapRunner(t, registry)

	out, err := registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		"semantic_search",
		json.RawMessage(`{"query":"brief progress updates","scope":"shared"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForPrincipal(semantic_search) err = %v", err)
	}
	if !strings.Contains(out, "[SEMANTIC_RECALL]") || !strings.Contains(out, "memory/knowledge.md") {
		t.Fatalf("output = %q, want fenced semantic recall hit", out)
	}
}

func TestSemanticSearchApprovedUserCannotReadSharedCuratedMemory(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver).
		WithSemanticEngine(memstore.NewSemanticEngine(memstore.SemanticOptions{
			Enabled:             true,
			DBPath:              filepath.Join(tmp, "semantic.db"),
			Sources:             []string{"memory/knowledge.md"},
			InteractiveTopK:     5,
			HeartbeatTopK:       12,
			InteractiveMaxChars: 4000,
			HeartbeatMaxChars:   12000,
			DailyNotesDir:       "memory/daily",
		}))
	setFakeBubblewrapRunner(t, registry)

	_, err = registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{TelegramUserID: 42, Role: principal.RoleApprovedUser},
		"semantic_search",
		json.RawMessage(`{"query":"brief progress updates","scope":"shared"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForPrincipal(semantic_search) err = nil, want shared-memory denial")
	}
	if !strings.Contains(err.Error(), "shared memory") {
		t.Fatalf("err = %v, want scope denial", err)
	}
}

func newSessionSearchStore(t *testing.T) *session.SQLiteStore {
	t.Helper()
	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for _, tc := range []struct {
		key      session.SessionKey
		turn     int
		userText string
		reply    string
	}{
		{session.SessionKey{ChatID: 1, UserID: 0}, 1, "alpha first", "reply one"},
		{session.SessionKey{ChatID: 2, UserID: 0}, 1, "alpha second elsewhere", "reply two"},
	} {
		sess, err := store.Load(tc.key)
		if err != nil {
			t.Fatalf("Load(%v) err = %v", tc.key, err)
		}
		sess.TurnCount = tc.turn
		if err := store.Save(sess, []session.Message{
			{Role: "user", Content: tc.userText, TurnIndex: tc.turn},
			{Role: "assistant", Content: tc.reply, FloorContent: tc.reply, TurnIndex: tc.turn},
		}, core.TokenUsage{}); err != nil {
			t.Fatalf("Save(%v) err = %v", tc.key, err)
		}
	}
	return store
}
