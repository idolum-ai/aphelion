//go:build linux

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
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
	provider.brokerageReplies = []string{
		"MODE: inspect_then_answer\nPUSH:\n- Inspect first.\n- Keep it concrete.",
		"MODE: answer_now\nPUSH:\n- The repo is already sufficient.\n- Answer directly.",
	}
	provider.planningReplies = []string{
		"MODE: inspect_then_answer\nRATIFICATION: adapt\nPLAN:\n- Inspect the codebase before answering.",
		"MODE: answer_now\nRATIFICATION: accept\nPLAN:\n- Answer directly from the current code context.",
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

	if len(provider.seenBrokerageSystem) < 2 {
		t.Fatalf("brokerage prompt calls = %d, want at least 2", len(provider.seenBrokerageSystem))
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
	provider.brokerageReplyText = "MODE: inspect_then_answer\nPUSH:\n- Inspect first."
	provider.planningReplyText = "MODE: inspect_then_answer\nRATIFICATION: adapt\nPLAN:\n- Inspect first."
	provider.proposalReplyText = "Push for a grounded answer from what is already known."

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
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "## Idolum Proposal") {
		t.Fatalf("governor input should fall back to Idolum proposal block: %q", provider.lastGovernorMsgs[1].Content)
	}
	if strings.Contains(provider.lastGovernorMsgs[1].Content, "## Negotiated Turn Brokerage") {
		t.Fatalf("governor input should not contain negotiated brokerage after max-round fallback: %q", provider.lastGovernorMsgs[1].Content)
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
