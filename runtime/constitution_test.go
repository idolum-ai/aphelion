//go:build linux

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestHandleInboundRepairsVisibleGovernorLeakageBeforeDelivery(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	if err := os.WriteFile(filepath.Join(cfg.Agent.ExecRoot, "diagram.png"), []byte("png"), 0o600); err != nil {
		t.Fatalf("write diagram: %v", err)
	}
	provider.replyText = "Here are the files.\nMEDIA: diagram.png"
	provider.faceReplyText = "I deferred this to Aphelion, but here are the diagrams."
	provider.repairReplyText = "Here are the diagrams I mapped from the codebase."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	var audit TurnAudit
	rt.SetTurnAuditSink(func(got TurnAudit) {
		audit = got
	})

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     9001,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "show me a diagram",
		MessageID:  1,
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != "Here are the diagrams I mapped from the codebase." {
		t.Fatalf("final text = %q", sender.sent[0].Text)
	}
	if len(sender.sent[0].Media) != 1 {
		t.Fatalf("media len = %d, want 1", len(sender.sent[0].Media))
	}
	if !audit.FaceRepairAttempted || !audit.FaceRepairApplied {
		t.Fatalf("audit face repair = attempted:%t applied:%t, want true/true", audit.FaceRepairAttempted, audit.FaceRepairApplied)
	}
	if !containsViolationRule(audit.ConstitutionViolations, constitutionRuleFinalGovernorLeakage) {
		t.Fatalf("violations = %#v, want governor leakage rule", audit.ConstitutionViolations)
	}
}

func TestHandleInboundRepairsMediaOnlyReplyWithNarration(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	if err := os.WriteFile(filepath.Join(cfg.Agent.ExecRoot, "diagram.png"), []byte("png"), 0o600); err != nil {
		t.Fatalf("write diagram: %v", err)
	}
	provider.replyText = "MEDIA: diagram.png"
	provider.repairReplyText = "I mapped the codebase into the attached diagram."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	var audit TurnAudit
	rt.SetTurnAuditSink(func(got TurnAudit) {
		audit = got
	})

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     9002,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "show me a diagram",
		MessageID:  1,
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != "I mapped the codebase into the attached diagram." {
		t.Fatalf("final text = %q", sender.sent[0].Text)
	}
	if len(sender.sent[0].Media) != 1 {
		t.Fatalf("media len = %d, want 1", len(sender.sent[0].Media))
	}
	if !containsViolationRule(audit.ConstitutionViolations, constitutionRuleMediaNeedsNarration) {
		t.Fatalf("violations = %#v, want media narration rule", audit.ConstitutionViolations)
	}
}

func TestHandleInboundBrokerageConvergesAfterAdaptation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplyText = "INSPECT: yes\nQUESTION: no\nANSWER: yes\nPUSH:\n- Inspect first.\n- Keep it concrete."
	provider.brokerageReplyText = "INSPECT: no\nQUESTION: no\nANSWER: yes\nPUSH:\n- The repo is already sufficient.\n- Answer directly."
	provider.planningReplies = []string{
		"INSPECT: yes\nQUESTION: no\nANSWER: yes\nRATIFICATION: adapt\nPLAN:\n- Inspect the codebase before answering.",
		"INSPECT: no\nQUESTION: no\nANSWER: yes\nRATIFICATION: accept\nPLAN:\n- Answer directly from the current code context.",
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	var audit TurnAudit
	rt.SetTurnAuditSink(func(got TurnAudit) {
		audit = got
	})

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     9003,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "come up with some features for my codebase",
		MessageID:  1,
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	if len(provider.seenProposalSystem) == 0 {
		t.Fatal("expected initial proposal prompt")
	}
	if len(provider.seenBrokerageSystem) == 0 {
		t.Fatal("expected revised brokerage prompt after adaptation")
	}
	if len(audit.BrokerageRounds) != 2 {
		t.Fatalf("brokerage rounds = %d, want 2", len(audit.BrokerageRounds))
	}
	if !audit.BrokerageConverged {
		t.Fatal("brokerage should have converged")
	}
	if got := audit.BrokerageRounds[len(audit.BrokerageRounds)-1].Ratification; got != "accept" {
		t.Fatalf("final ratification = %q, want accept", got)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.lastGovernorMsgs) < 2 {
		t.Fatalf("lastGovernorMsgs len = %d, want at least 2", len(provider.lastGovernorMsgs))
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "- ratification: accept") {
		t.Fatalf("negotiated brokerage block missing accept: %q", provider.lastGovernorMsgs[1].Content)
	}
}

func TestHandleInboundBrokerageFallsBackToProposalAfterMaxRounds(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplies = []string{
		"INSPECT: yes\nQUESTION: no\nANSWER: yes\nPUSH:\n- Inspect first.",
		"Push for a grounded answer from what is already known.",
	}
	provider.brokerageReplyText = "INSPECT: yes\nQUESTION: no\nANSWER: yes\nPUSH:\n- Inspect first."
	provider.planningReplyText = "INSPECT: yes\nQUESTION: no\nANSWER: yes\nRATIFICATION: adapt\nPLAN:\n- Inspect first."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	var audit TurnAudit
	rt.SetTurnAuditSink(func(got TurnAudit) {
		audit = got
	})

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     9004,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "come up with some features for my codebase",
		MessageID:  1,
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	if audit.BrokerageConverged {
		t.Fatal("brokerage should not have converged")
	}
	if len(audit.BrokerageRounds) != maxBrokerageRounds {
		t.Fatalf("brokerage rounds = %d, want %d", len(audit.BrokerageRounds), maxBrokerageRounds)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.lastGovernorMsgs) < 2 {
		t.Fatalf("lastGovernorMsgs len = %d, want at least 2", len(provider.lastGovernorMsgs))
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "## Conversational Pressure") {
		t.Fatalf("governor input should fall back to Idolum proposal block: %q", provider.lastGovernorMsgs[1].Content)
	}
	if strings.Contains(provider.lastGovernorMsgs[1].Content, "## Execution Contract") {
		t.Fatalf("governor input should not contain negotiated brokerage after max-round fallback: %q", provider.lastGovernorMsgs[1].Content)
	}
}

func TestGroundFinalReplyWithExecutionEvidenceRewritesUngroundedSuccessClaim(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9301, UserID: 0, Scope: telegramDMScopeRef(9301)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{}`,
			CreatedAt:   now.Add(-20 * time.Second),
		},
		{
			EventType:   core.ExecutionEventTurnFailed,
			Stage:       "turn",
			Status:      "failed",
			PayloadJSON: `{"error":"tool failed"}`,
			CreatedAt:   now.Add(-10 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	rewritten, note := rt.groundFinalReplyWithExecutionEvidence(key, "Done. Everything finished cleanly.")
	if strings.TrimSpace(note) == "" {
		t.Fatalf("note = %q, want non-empty grounding note", note)
	}
	if !strings.Contains(strings.ToLower(rewritten), "completion claim is not grounded") {
		t.Fatalf("rewritten = %q, want completion-claim grounding correction", rewritten)
	}
}

func TestGroundFinalReplyWithExecutionEvidenceKeepsGroundedSuccessClaim(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9302, UserID: 0, Scope: telegramDMScopeRef(9302)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{}`,
			CreatedAt:   now.Add(-20 * time.Second),
		},
		{
			EventType:   core.ExecutionEventTurnCompleted,
			Stage:       "turn",
			Status:      "completed",
			PayloadJSON: `{"summary":"done"}`,
			CreatedAt:   now.Add(-10 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	rewritten, note := rt.groundFinalReplyWithExecutionEvidence(key, "Done. Everything finished cleanly.")
	if note != "" {
		t.Fatalf("note = %q, want empty note", note)
	}
	if rewritten != "Done. Everything finished cleanly." {
		t.Fatalf("rewritten = %q, want unchanged reply", rewritten)
	}
}

func TestGroundFinalReplyWithExecutionEvidenceRewritesUngroundedToolClaim(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9303, UserID: 0, Scope: telegramDMScopeRef(9303)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{}`,
			CreatedAt:   now.Add(-20 * time.Second),
		},
		{
			EventType:   core.ExecutionEventTurnCompleted,
			Stage:       "turn",
			Status:      "completed",
			PayloadJSON: `{"summary":"done"}`,
			CreatedAt:   now.Add(-10 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	rewritten, note := rt.groundFinalReplyWithExecutionEvidence(key, "I executed command-line checks and applied the patch.")
	if strings.TrimSpace(note) == "" {
		t.Fatalf("note = %q, want non-empty grounding note", note)
	}
	if !strings.Contains(strings.ToLower(rewritten), "tool-execution claim has no tool events") {
		t.Fatalf("rewritten = %q, want tool-claim grounding correction", rewritten)
	}
}

func TestGroundFinalReplyWithExecutionEvidenceKeepsGroundedTestClaim(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9304, UserID: 0, Scope: telegramDMScopeRef(9304)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{}`,
			CreatedAt:   now.Add(-20 * time.Second),
		},
		{
			EventType:   core.ExecutionEventToolStarted,
			Stage:       "tool",
			Status:      "started",
			PayloadJSON: `{"tool":"exec","preview":"{\"command\":\"go test ./...\"}"}`,
			CreatedAt:   now.Add(-15 * time.Second),
		},
		{
			EventType:   core.ExecutionEventToolSucceeded,
			Stage:       "tool",
			Status:      "succeeded",
			PayloadJSON: `{"tool":"exec","result_preview":"ok all tests"}`,
			CreatedAt:   now.Add(-12 * time.Second),
		},
		{
			EventType:   core.ExecutionEventTurnCompleted,
			Stage:       "turn",
			Status:      "completed",
			PayloadJSON: `{"summary":"done"}`,
			CreatedAt:   now.Add(-10 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	reply := "I ran go test and tests passed."
	rewritten, note := rt.groundFinalReplyWithExecutionEvidence(key, reply)
	if note != "" {
		t.Fatalf("note = %q, want empty note", note)
	}
	if rewritten != reply {
		t.Fatalf("rewritten = %q, want unchanged reply", rewritten)
	}
}

func containsViolationRule(violations []ConstitutionViolation, want string) bool {
	for _, violation := range violations {
		if strings.TrimSpace(violation.Rule) == strings.TrimSpace(want) {
			return true
		}
	}
	return false
}
