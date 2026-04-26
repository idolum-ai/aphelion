//go:build linux

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestRunDoctorOncePersistsDeliversAndRedactsDiagnostics(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "State of Things\nRuntime is diagnosable.\n\nRecommendations\nKeep /doctor read-only."
	cfg.Agent.DynamicFiles = []string{"MEMORY.md", "SKILLS.md", "memory/knowledge.md", "memory/decisions.md"}

	root := cfg.Agent.SharedMemoryRoot
	if err := os.WriteFile(filepath.Join(root, "SKILLS.md"), []byte("# Skills\n\n- [Commit Archaeology](practices/commit-archeology.md)"), 0o600); err != nil {
		t.Fatalf("write SKILLS.md: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "practices"), 0o755); err != nil {
		t.Fatalf("mkdir practices: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "practices", "commit-archeology.md"), []byte("# Commit Archaeology\n\nDiagnose commits with evidence."), 0o600); err != nil {
		t.Fatalf("write practice: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "memory"), 0o755); err != nil {
		t.Fatalf("mkdir memory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "memory", "knowledge.md"), []byte("# knowledge\n\n- Provider timeouts must surface to Telegram."), 0o600); err != nil {
		t.Fatalf("write knowledge: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "memory", "decisions.md"), []byte("# decisions\n\n- /doctor must not run tools."), 0o600); err != nil {
		t.Fatalf("write decisions: %v", err)
	}
	logPath := filepath.Join(filepath.Dir(cfg.Sessions.DBPath), "aphelion.log")
	if err := os.WriteFile(logPath, []byte("WARN provider timeout api_key = \"sk-secret-do-not-leak\"\nAuthorization: Bearer bearer-secret\nOPENAI_API_KEY=sk-env-secret\n{\"Authorization\":\"Bearer json-secret\",\"password\":\"pw-secret\"}\n"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType:   core.ExecutionEventProviderAttemptFailed,
		Stage:       "provider",
		Status:      "failed",
		PayloadJSON: `{"error":"codex timeout"}`,
		CreatedAt:   time.Now().Add(-time.Minute),
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	err = rt.runDoctorOnce(context.Background(), core.InboundMessage{
		ChatID:     1001,
		SenderID:   1001,
		SenderName: "admin",
		ChatType:   "private",
		Text:       "/doctor",
		MessageID:  17,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("runDoctorOnce() err = %v", err)
	}

	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].ChatID != 1001 || !strings.Contains(sender.sent[0].Text, "State of Things") {
		t.Fatalf("sent = %#v, want doctor report to admin", sender.sent[0])
	}
	if sender.sent[0].ReplyTo == nil || *sender.sent[0].ReplyTo != 17 {
		t.Fatalf("reply_to = %#v, want 17", sender.sent[0].ReplyTo)
	}

	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(sess.Messages) < 2 {
		t.Fatalf("messages len = %d, want synthetic /doctor turn", len(sess.Messages))
	}
	userMsg := sess.Messages[len(sess.Messages)-2]
	assistantMsg := sess.Messages[len(sess.Messages)-1]
	if userMsg.Role != "user" || userMsg.Content != "/doctor" {
		t.Fatalf("user doctor message = %#v, want persisted /doctor request", userMsg)
	}
	if assistantMsg.Role != "assistant" || !strings.Contains(assistantMsg.Content, "Runtime is diagnosable") {
		t.Fatalf("assistant doctor message = %#v, want persisted report", assistantMsg)
	}

	var userPrompt string
	provider.mu.Lock()
	if len(provider.lastGovernorTools) != 0 {
		t.Fatalf("doctor provider tools = %#v, want none for read-only diagnostics", provider.lastGovernorTools)
	}
	for _, msg := range provider.lastGovernorMsgs {
		if msg.Role == "user" {
			userPrompt += "\n" + msg.Content
		}
	}
	provider.mu.Unlock()
	for _, want := range []string{
		doctorRequestMarker,
		"memory/knowledge.md",
		"provider.attempt.failed",
		"semantic_enabled",
		"Recent Service Log Tail",
	} {
		if !strings.Contains(userPrompt, want) {
			t.Fatalf("doctor prompt missing %q:\n%s", want, userPrompt)
		}
	}
	for _, secret := range []string{"sk-secret-do-not-leak", "bearer-secret", "sk-env-secret", "json-secret", "pw-secret"} {
		if strings.Contains(userPrompt, secret) {
			t.Fatalf("doctor prompt leaked secret %q:\n%s", secret, userPrompt)
		}
	}
}

func TestStartDoctorRejectsNonAdmin(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	err = rt.StartDoctor(context.Background(), core.InboundMessage{
		ChatID:   1002,
		SenderID: 1002,
		ChatType: "private",
		Text:     "/doctor",
	})
	if !errors.Is(err, ErrPrincipalDenied) {
		t.Fatalf("StartDoctor() err = %v, want ErrPrincipalDenied", err)
	}
}
