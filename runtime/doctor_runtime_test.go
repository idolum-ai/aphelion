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

	sender.mu.Lock()
	sent := append([]core.OutboundMessage(nil), sender.sent...)
	edits := append([]messageEdit(nil), sender.edits...)
	inlineCount := len(sender.inline)
	editInlineCount := len(sender.editInline)
	sender.mu.Unlock()

	if len(sent) != 2 {
		t.Fatalf("sent len = %d, want progress message and final report", len(sent))
	}
	if sent[0].ChatID != 1001 || !strings.Contains(sent[0].Text, "Thinking") || !strings.Contains(sent[0].Text, "Loading prompt and memory context") {
		t.Fatalf("progress message = %#v, want live doctor progress", sent[0])
	}
	if sent[0].ReplyTo == nil || *sent[0].ReplyTo != 17 {
		t.Fatalf("progress reply_to = %#v, want 17", sent[0].ReplyTo)
	}
	if sent[1].ChatID != 1001 || !strings.Contains(sent[1].Text, "State of Things") {
		t.Fatalf("report message = %#v, want doctor report to admin", sent[1])
	}
	if sent[1].ReplyTo == nil || *sent[1].ReplyTo != 17 {
		t.Fatalf("report reply_to = %#v, want 17", sent[1].ReplyTo)
	}
	if inlineCount != 0 || editInlineCount != 0 {
		t.Fatalf("inline progress = sent:%d edited:%d, want plain progress without controls", inlineCount, editInlineCount)
	}
	if len(edits) == 0 {
		t.Fatal("progress edits = 0, want live progress updates")
	}
	lastEdit := edits[len(edits)-1]
	if lastEdit.ChatID != 1001 || lastEdit.MessageID != 1 || !strings.HasPrefix(lastEdit.Text, "Done.") || !strings.Contains(lastEdit.Text, "Sending the doctor report to Telegram") {
		t.Fatalf("final progress edit = %#v, want completed doctor progress", lastEdit)
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
		"Known Issue Status Checks",
		"issue=dynamic_skills_prompt_loading status=likely_fixed",
		"allowed_statuses: active, likely_fixed, historical_resolved, residual_risk, unknown",
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
