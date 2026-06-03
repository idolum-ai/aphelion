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

func TestHandleInboundTurnEvidenceCommandSendsLatestWorkEvidenceArtifact(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	oldPath := filepath.Join(cfg.Agent.ExecRoot, "memory", "work-evidence", "old.md")
	latestPath := filepath.Join(cfg.Agent.ExecRoot, "memory", "work-evidence", "latest.md")
	for _, item := range []struct {
		path string
		body string
	}{
		{oldPath, "old work evidence"},
		{latestPath, "latest work evidence"},
	} {
		if err := os.MkdirAll(filepath.Dir(item.path), 0o755); err != nil {
			t.Fatalf("MkdirAll() err = %v", err)
		}
		if err := os.WriteFile(item.path, []byte(item.body), 0o600); err != nil {
			t.Fatalf("WriteFile() err = %v", err)
		}
	}
	key := session.SessionKey{ChatID: 8800, UserID: 0, Scope: telegramDMScopeRef(8800)}
	if err := store.UpdateOperationState(key, session.OperationState{
		Status: session.OperationStatusCompleted,
		Artifacts: []session.OperationArtifact{
			{Label: "Work evidence", Ref: "memory/work-evidence/old.md"},
			{Label: "Work evidence", Ref: "memory/work-evidence/latest.md"},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     8800,
		ChatType:   "private",
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  43,
		Text:       "/turn-evidence",
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if result == nil || len(result.Media) != 1 {
		t.Fatalf("result = %#v, want one media artifact", result)
	}
	if provider.callCount != 0 {
		t.Fatalf("provider.callCount = %d, want direct command without model turn", provider.callCount)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want one media send", len(sender.sent))
	}
	if len(sender.sent[0].Media) != 1 || sender.sent[0].Media[0].Path != latestPath {
		t.Fatalf("sent media = %#v, want latest work evidence path %q", sender.sent[0].Media, latestPath)
	}
	if !strings.Contains(sender.sent[0].Text, "Sending Work evidence.") {
		t.Fatalf("sent text = %q, want work evidence caption", sender.sent[0].Text)
	}
}

func TestHandleInboundTurnEvidenceCommandReportsMissingArtifact(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8799, UserID: 0, Scope: telegramDMScopeRef(8799)}
	if err := store.UpdateOperationState(key, session.OperationState{Status: session.OperationStatusCompleted}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     8799,
		ChatType:   "private",
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  42,
		Text:       "/turn-evidence",
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if result == nil || strings.TrimSpace(result.Text) != "No turn evidence artifact is available for this chat." || len(result.Media) != 0 {
		t.Fatalf("result = %#v, want no-evidence text without media", result)
	}
	if provider.callCount != 0 {
		t.Fatalf("provider.callCount = %d, want direct command without model turn", provider.callCount)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 || strings.TrimSpace(sender.sent[0].Text) != "No turn evidence artifact is available for this chat." || len(sender.sent[0].Media) != 0 {
		t.Fatalf("sent = %#v, want no-evidence text without media", sender.sent)
	}
}

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

func TestHandleInboundDoesNotTreatContinuationAuthorizationAsArtifactRequest(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	expected := &core.TurnResult{Text: "continued actual work"}
	recorder := &recordingInteractiveDMTurnAssembler{result: expected}
	rt.interactiveDMAssembler = recorder

	artifactPath := filepath.Join(cfg.Agent.ExecRoot, "memory", "work-evidence", "latest.md")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() err = %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte("work evidence"), 0o600); err != nil {
		t.Fatalf("WriteFile() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8802, UserID: 0, Scope: telegramDMScopeRef(8802)}
	if err := store.UpdateOperationState(key, session.OperationState{
		Status: session.OperationStatusActive,
		Artifacts: []session.OperationArtifact{
			{Label: "Work evidence", Ref: "memory/work-evidence/latest.md"},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	text := strings.Join([]string{
		approvedContinuationEventText,
		"",
		"Approved work:",
		"Plan: custom child Telegram runner",
		"Next: Finish the repo-only custom child Telegram runner work.",
		"Scope: Prepare the no-send dry-start gate, commit it if coherent, and report evidence.",
	}, "\n")

	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:       8802,
		ChatType:     "private",
		SenderID:     1001,
		SenderName:   "admin",
		MessageID:    45,
		Text:         text,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if result != expected {
		t.Fatalf("HandleInbound() result = %#v, want assembler result", result)
	}
	if !recorder.called {
		t.Fatal("interactive assembler was not called")
	}
	if provider.callCount != 0 {
		t.Fatalf("provider.callCount = %d, want stubbed assembler boundary", provider.callCount)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 0 {
		t.Fatalf("sent = %#v, want no direct artifact send for continuation authorization", sender.sent)
	}
}

func TestHandleInboundDoesNotTreatConversationalShareLaterAsArtifactRequest(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	expected := &core.TurnResult{Text: "normal conversational reply"}
	recorder := &recordingInteractiveDMTurnAssembler{result: expected}
	rt.interactiveDMAssembler = recorder

	artifactPath := filepath.Join(cfg.Agent.ExecRoot, "memory", "work-evidence", "latest.md")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() err = %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte("work evidence"), 0o600); err != nil {
		t.Fatalf("WriteFile() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8803, UserID: 0, Scope: telegramDMScopeRef(8803)}
	if err := store.UpdateOperationState(key, session.OperationState{
		Status: session.OperationStatusActive,
		Artifacts: []session.OperationArtifact{
			{Label: "Work evidence", Ref: "memory/work-evidence/latest.md"},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	text := "I read another article called the encore of abraxas that reveals some of it, I should share it later.\n\nReply context:\nidolum_bot: Work evidence available."
	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     8803,
		ChatType:   "private",
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  46,
		Text:       text,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if result != expected {
		t.Fatalf("HandleInbound() result = %#v, want assembler result", result)
	}
	if !recorder.called {
		t.Fatal("interactive assembler was not called")
	}
	if provider.callCount != 0 {
		t.Fatalf("provider.callCount = %d, want stubbed assembler boundary", provider.callCount)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 0 {
		t.Fatalf("sent = %#v, want no direct artifact send for conversational share-later text", sender.sent)
	}
}

func TestLooksLikeOperationArtifactSendRequestIgnoresReplyContext(t *testing.T) {
	t.Parallel()

	text := "commit, push and reinstall, let's try it\n\nReply context:\nidolum_bot: Sending Proposal denial bugfix validation log."
	if looksLikeOperationArtifactSendRequest(text) {
		t.Fatalf("looksLikeOperationArtifactSendRequest(%q) = true, want false when only reply context has send/file words", text)
	}
}

func TestLooksLikeOperationArtifactSendRequestStillUsesUserText(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"send it\n\nReply context:\nidolum_bot: Proposal denial bugfix validation log.",
		"please attach the file",
		"share that report",
	} {
		if !looksLikeOperationArtifactSendRequest(text) {
			t.Fatalf("looksLikeOperationArtifactSendRequest(%q) = false, want true", text)
		}
	}
}

func TestLooksLikeOperationArtifactSendRequestRejectsWorkEvidenceText(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"can you send me the work evidence",
		"send work evidence",
		"please attach the evidence",
	} {
		if looksLikeOperationArtifactSendRequest(text) {
			t.Fatalf("looksLikeOperationArtifactSendRequest(%q) = true, want false; use /turn-evidence", text)
		}
	}
}

func TestLooksLikeOperationArtifactSendRequestRejectsNarrativeOrAmbiguousPronouns(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"I should share it later",
		"I should share that report later",
		"we can send it later",
		"send it",
		"attach that",
	} {
		if looksLikeOperationArtifactSendRequest(text) {
			t.Fatalf("looksLikeOperationArtifactSendRequest(%q) = true, want false", text)
		}
	}
}
