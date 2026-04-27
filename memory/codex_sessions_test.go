//go:build linux

package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSemanticEngineImportCodexSessionsQuarantinesAndDedupes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	codexHome := filepath.Join(root, "codex")
	now := time.Date(2026, time.April, 26, 12, 0, 0, 0, time.UTC)
	sessionPath := writeCodexSessionTestFixture(t, codexHome, now.Add(-2*time.Hour), []map[string]any{
		codexSessionTestEvent("session_meta", now.Add(-2*time.Hour), map[string]any{
			"id":             "sess-1",
			"source":         "codex_cli",
			"model_provider": "openai",
			"cwd":            "/workspace/aphelion",
		}),
		codexSessionTestEvent("response_item", now.Add(-2*time.Hour), map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]string{{
				"type": "input_text",
				"text": "Please recover Aphelion memory. OPENAI_API_KEY=secret-value contact me at operator@example.com",
			}},
		}),
		codexSessionTestEvent("response_item", now.Add(-90*time.Minute), map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []map[string]string{{
				"type": "output_text",
				"text": "Recovered the semantic quarantine notes and left the imported archive pending review.",
			}},
		}),
		codexSessionTestEvent("response_item", now.Add(-80*time.Minute), map[string]any{
			"type": "function_call",
			"name": "exec_command",
		}),
		codexSessionTestEvent("event_msg", now.Add(-75*time.Minute), map[string]any{
			"type":      "exec_command_end",
			"exit_code": 1,
			"command":   []string{"go", "test", "./..."},
		}),
	})

	engine := NewSemanticEngine(SemanticOptions{
		Enabled:             true,
		DBPath:              filepath.Join(root, "semantic.db"),
		Sources:             []string{"memory/knowledge.md"},
		InteractiveTopK:     5,
		HeartbeatTopK:       12,
		InteractiveMaxChars: 4000,
		HeartbeatMaxChars:   12000,
		DailyNotesDir:       "memory/daily",
	})
	defer engine.Close()

	result, err := engine.ImportCodexSessions(context.Background(), CodexSessionImportOptions{
		CodexHome:   codexHome,
		Lookback:    48 * time.Hour,
		ActiveGrace: time.Minute,
		MaxSessions: 10,
		Scope:       "shared",
		Now:         now,
	})
	if err != nil {
		t.Fatalf("ImportCodexSessions() err = %v", err)
	}
	if result.Imported != 1 || result.SkippedAlreadyImported != 0 || result.Failed != 0 {
		t.Fatalf("ImportCodexSessions() = %#v, want one imported without failures", result)
	}
	if result.SessionsDir != filepath.Join(codexHome, "sessions") {
		t.Fatalf("SessionsDir = %q, want codex sessions dir", result.SessionsDir)
	}

	docs, err := engine.ListImportAudit(context.Background(), SemanticAuditFilter{
		State: SemanticImportStateQuarantine,
		Scope: "shared",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListImportAudit() err = %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("ListImportAudit() len = %d, want 1", len(docs))
	}
	if docs[0].ProvenanceSource != CodexSessionImportProvenance || docs[0].SourceKind != "codex_session" {
		t.Fatalf("doc = %#v, want codex session import", docs[0])
	}
	if docs[0].SourcePath != filepath.ToSlash(filepath.Join("codex_sessions", "2026", "04", "25", filepath.Base(sessionPath))) {
		t.Fatalf("SourcePath = %q, want codex_sessions relative path", docs[0].SourcePath)
	}

	review, err := engine.ReviewImportDocument(context.Background(), docs[0].ID, 8, 4000)
	if err != nil {
		t.Fatalf("ReviewImportDocument() err = %v", err)
	}
	joined := strings.Join(review.Excerpts, "\n")
	for _, needle := range []string{"User Goals", "Assistant Outcomes", "Tool Activity", "exec_command: 1", "exit_code=1 command=go test ./..."} {
		if !strings.Contains(joined, needle) {
			t.Fatalf("review excerpts = %q, want substring %q", joined, needle)
		}
	}
	for _, leaked := range []string{"secret-value", "operator@example.com"} {
		if strings.Contains(joined, leaked) {
			t.Fatalf("review excerpts leaked %q: %q", leaked, joined)
		}
	}

	hits, err := engine.Search(context.Background(), SemanticSearchRequest{
		Root:  root,
		Scope: "shared",
		Query: "semantic quarantine notes",
		Mode:  SemanticModeInteractive,
		Now:   now,
	})
	if err != nil {
		t.Fatalf("Search() err = %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("Search() hits = %#v, want quarantined import excluded", hits)
	}

	again, err := engine.ImportCodexSessions(context.Background(), CodexSessionImportOptions{
		CodexHome:   codexHome,
		Lookback:    48 * time.Hour,
		ActiveGrace: time.Minute,
		MaxSessions: 10,
		Scope:       "shared",
		Now:         now,
	})
	if err != nil {
		t.Fatalf("ImportCodexSessions(second) err = %v", err)
	}
	if again.Imported != 0 || again.SkippedAlreadyImported != 1 {
		t.Fatalf("ImportCodexSessions(second) = %#v, want dedupe skip", again)
	}
}

func TestSemanticEngineImportCodexSessionsUpdatesChangedExistingSession(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	codexHome := filepath.Join(root, "codex")
	now := time.Date(2026, time.April, 26, 12, 0, 0, 0, time.UTC)
	sessionPath := writeCodexSessionTestFixture(t, codexHome, now.Add(-3*time.Hour), []map[string]any{
		codexSessionTestEvent("response_item", now.Add(-3*time.Hour), map[string]any{
			"type":    "message",
			"role":    "user",
			"content": []map[string]string{{"type": "input_text", "text": "initial imported codex session"}},
		}),
	})

	engine := NewSemanticEngine(SemanticOptions{
		Enabled: true,
		DBPath:  filepath.Join(root, "semantic.db"),
	})
	defer engine.Close()

	result, err := engine.ImportCodexSessions(context.Background(), CodexSessionImportOptions{
		CodexHome:   codexHome,
		Lookback:    48 * time.Hour,
		ActiveGrace: time.Minute,
		MaxSessions: 10,
		Scope:       "shared",
		Now:         now,
	})
	if err != nil {
		t.Fatalf("ImportCodexSessions() err = %v", err)
	}
	if result.Imported != 1 || result.Updated != 0 || result.SkippedAlreadyImported != 0 {
		t.Fatalf("ImportCodexSessions() = %#v, want one new import", result)
	}

	appendCodexSessionTestFixture(t, sessionPath, now.Add(-90*time.Minute), []map[string]any{
		codexSessionTestEvent("response_item", now.Add(-90*time.Minute), map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []map[string]string{{
				"type": "output_text",
				"text": "appended codex session update should refresh the semantic import",
			}},
		}),
	})

	updated, err := engine.ImportCodexSessions(context.Background(), CodexSessionImportOptions{
		CodexHome:   codexHome,
		Lookback:    48 * time.Hour,
		ActiveGrace: time.Minute,
		MaxSessions: 10,
		Scope:       "shared",
		Now:         now,
	})
	if err != nil {
		t.Fatalf("ImportCodexSessions(updated) err = %v", err)
	}
	if updated.Imported != 0 || updated.Updated != 1 || updated.SkippedAlreadyImported != 0 {
		t.Fatalf("ImportCodexSessions(updated) = %#v, want one refreshed import", updated)
	}

	docs, err := engine.ListImportAudit(context.Background(), SemanticAuditFilter{
		State: SemanticImportStateQuarantine,
		Scope: "shared",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListImportAudit() err = %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("ListImportAudit() len = %d, want 1 refreshed document", len(docs))
	}
	review, err := engine.ReviewImportDocument(context.Background(), docs[0].ID, 8, 4000)
	if err != nil {
		t.Fatalf("ReviewImportDocument() err = %v", err)
	}
	if joined := strings.Join(review.Excerpts, "\n"); !strings.Contains(joined, "appended codex session update should refresh the semantic import") {
		t.Fatalf("review excerpts = %q, want appended session update", joined)
	}

	again, err := engine.ImportCodexSessions(context.Background(), CodexSessionImportOptions{
		CodexHome:   codexHome,
		Lookback:    48 * time.Hour,
		ActiveGrace: time.Minute,
		MaxSessions: 10,
		Scope:       "shared",
		Now:         now,
	})
	if err != nil {
		t.Fatalf("ImportCodexSessions(third) err = %v", err)
	}
	if again.Imported != 0 || again.Updated != 0 || again.SkippedAlreadyImported != 1 {
		t.Fatalf("ImportCodexSessions(third) = %#v, want unchanged import skip", again)
	}
}

func TestSemanticEngineImportCodexSessionsSkipsOldAndActive(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	codexHome := filepath.Join(root, "codex")
	now := time.Date(2026, time.April, 26, 12, 0, 0, 0, time.UTC)
	writeCodexSessionTestFixture(t, codexHome, now.Add(-72*time.Hour), []map[string]any{
		codexSessionTestEvent("response_item", now.Add(-72*time.Hour), map[string]any{
			"type":    "message",
			"role":    "user",
			"content": []map[string]string{{"type": "input_text", "text": "old session"}},
		}),
	})
	writeCodexSessionTestFixture(t, codexHome, now.Add(-30*time.Second), []map[string]any{
		codexSessionTestEvent("response_item", now.Add(-30*time.Second), map[string]any{
			"type":    "message",
			"role":    "user",
			"content": []map[string]string{{"type": "input_text", "text": "active session"}},
		}),
	})

	engine := NewSemanticEngine(SemanticOptions{
		Enabled: true,
		DBPath:  filepath.Join(root, "semantic.db"),
	})
	defer engine.Close()

	result, err := engine.ImportCodexSessions(context.Background(), CodexSessionImportOptions{
		CodexHome:   codexHome,
		Lookback:    24 * time.Hour,
		ActiveGrace: time.Minute,
		MaxSessions: 10,
		Scope:       "shared",
		Now:         now,
	})
	if err != nil {
		t.Fatalf("ImportCodexSessions() err = %v", err)
	}
	if result.Scanned != 2 || result.Imported != 0 || result.SkippedOld != 1 || result.SkippedActive != 1 {
		t.Fatalf("ImportCodexSessions() = %#v, want old and active skips", result)
	}
}

func writeCodexSessionTestFixture(t *testing.T, codexHome string, modTime time.Time, events []map[string]any) string {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2026", "04", "25")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(codex sessions) err = %v", err)
	}
	path := filepath.Join(dir, "rollout-"+modTime.UTC().Format("20060102T150405.000000000")+".jsonl")
	lines := make([]string, 0, len(events))
	for _, event := range events {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("Marshal(codex event) err = %v", err)
		}
		lines = append(lines, string(raw))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(codex session) err = %v", err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("Chtimes(codex session) err = %v", err)
	}
	return path
}

func appendCodexSessionTestFixture(t *testing.T, path string, modTime time.Time, events []map[string]any) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(codex session append) err = %v", err)
	}
	defer file.Close()
	for _, event := range events {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("Marshal(codex appended event) err = %v", err)
		}
		if _, err := file.Write(append(raw, '\n')); err != nil {
			t.Fatalf("Write(codex appended event) err = %v", err)
		}
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("Chtimes(codex session append) err = %v", err)
	}
}

func codexSessionTestEvent(kind string, ts time.Time, payload map[string]any) map[string]any {
	return map[string]any{
		"type":      kind,
		"timestamp": ts.UTC().Format(time.RFC3339Nano),
		"payload":   payload,
	}
}
