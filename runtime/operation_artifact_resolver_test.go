//go:build linux

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestHandleInboundSendsLatestOperationPDFArtifactDirectly(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	pdfPath := filepath.Join(cfg.Agent.ExecRoot, "reports", "semantic-review.pdf")
	if err := os.MkdirAll(filepath.Dir(pdfPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() err = %v", err)
	}
	if err := os.WriteFile(pdfPath, []byte("%PDF-1.7"), 0o600); err != nil {
		t.Fatalf("WriteFile() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8801, UserID: 0, Scope: telegramDMScopeRef(8801)}
	if err := store.UpdateOperationState(key, session.OperationState{
		Status: session.OperationStatusCompleted,
		Artifacts: []session.OperationArtifact{
			{Label: "notes", Ref: "notes.txt"},
			{Label: "semantic review PDF", Ref: "reports/semantic-review.pdf"},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     8801,
		ChatType:   "private",
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  44,
		Text:       "send me the pdf",
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if result == nil || len(result.Media) != 1 {
		t.Fatalf("result = %#v, want one media artifact", result)
	}
	if provider.callCount != 0 {
		t.Fatalf("provider.callCount = %d, want direct artifact send without model turn", provider.callCount)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want one media send", len(sender.sent))
	}
	if len(sender.sent[0].Media) != 1 || sender.sent[0].Media[0].Path != pdfPath {
		t.Fatalf("sent media = %#v, want pdf path %q", sender.sent[0].Media, pdfPath)
	}
	if !strings.Contains(strings.ToLower(sender.sent[0].Text), "pdf") {
		t.Fatalf("sent text = %q, want pdf label", sender.sent[0].Text)
	}
}
