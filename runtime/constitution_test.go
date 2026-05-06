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
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/turn"
)

func TestHandleInboundRepairsVisibleGovernorLeakageBeforeDelivery(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	if err := os.WriteFile(filepath.Join(cfg.Agent.ExecRoot, "diagram.png"), []byte("png"), 0o600); err != nil {
		t.Fatalf("write diagram: %v", err)
	}
	provider.replyText = `Here are the files.
MEDIA: {"path":"diagram.png"}`
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
	provider.replyText = `MEDIA: {"path":"diagram.png"}`
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

func TestHandleInboundRepairsMediaContradictionBeforeDelivery(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	if err := os.WriteFile(filepath.Join(cfg.Agent.ExecRoot, "diagram.png"), []byte("png"), 0o600); err != nil {
		t.Fatalf("write diagram: %v", err)
	}
	provider.replyText = `Here are the files.
MEDIA: {"path":"diagram.png"}`
	provider.faceReplyText = "I can't generate diagrams, but here are the images."
	provider.repairReplyText = "I mapped the codebase into the attached diagrams."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	var audit TurnAudit
	rt.SetTurnAuditSink(func(got TurnAudit) {
		audit = got
	})

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     9005,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "show me diagrams",
		MessageID:  1,
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != "I mapped the codebase into the attached diagrams." {
		t.Fatalf("final text = %q", sender.sent[0].Text)
	}
	if len(sender.sent[0].Media) != 1 {
		t.Fatalf("media len = %d, want 1", len(sender.sent[0].Media))
	}
	if !audit.FaceRepairAttempted || !audit.FaceRepairApplied {
		t.Fatalf("audit face repair = attempted:%t applied:%t, want true/true", audit.FaceRepairAttempted, audit.FaceRepairApplied)
	}
	if !containsViolationRule(audit.ConstitutionViolations, constitutionRuleMediaReplyContradiction) {
		t.Fatalf("violations = %#v, want media contradiction rule", audit.ConstitutionViolations)
	}
}

func TestApplyTurnConstitutionRepairsUngroundedExecutionClaimWithoutBanner(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.repairReplyText = "I reviewed the existing validation record for the pushed fixes."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9004, UserID: 0, Scope: telegramDMScopeRef(9004)}
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
			PayloadJSON: `{"summary":"reviewed prior work"}`,
			CreatedAt:   now.Add(-10 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	auditRecorder := newTurnAuditRecorder(key, "telegram", "admin", "review the pushed fixes")
	scope := sandbox.Scope{
		Principal:        principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		GlobalRoot:       cfg.Agent.PromptRoot,
		SharedMemoryRoot: cfg.Agent.SharedMemoryRoot,
		WorkingRoot:      cfg.Agent.ExecRoot,
	}
	finalText := rt.applyTurnConstitution(
		context.Background(),
		key,
		scope,
		"telegram",
		"admin",
		"review the pushed fixes",
		rt.currentFaceRenderer(),
		prompt.RuntimeAwareness{},
		core.MaterialPacket{},
		"",
		"Validation passed: go test ./...",
		nil,
		auditRecorder,
	)
	if finalText != "I reviewed the existing validation record for the pushed fixes." {
		t.Fatalf("final text = %q", finalText)
	}
	if strings.Contains(finalText, "I need to correct that") {
		t.Fatalf("final text = %q, want no deterministic correction banner", finalText)
	}
	audit := auditRecorder.Snapshot()
	if !audit.FaceRepairAttempted || !audit.FaceRepairApplied {
		t.Fatalf("audit face repair = attempted:%t applied:%t, want true/true", audit.FaceRepairAttempted, audit.FaceRepairApplied)
	}
	if !containsViolationRule(audit.ConstitutionViolations, constitutionRuleExecutionClaimUngrounded) {
		t.Fatalf("violations = %#v, want execution claim grounding rule", audit.ConstitutionViolations)
	}
	if len(audit.ExecutionClaimFindings) != 1 || audit.ExecutionClaimFindings[0].ClaimType != "test_execution" {
		t.Fatalf("execution findings = %#v, want one test_execution finding", audit.ExecutionClaimFindings)
	}
	provider.mu.Lock()
	seenFaceSystem := append([]string(nil), provider.seenFaceSystem...)
	provider.mu.Unlock()
	if len(seenFaceSystem) == 0 {
		t.Fatal("expected repair face prompt to be recorded")
	}
	repairPrompt := seenFaceSystem[len(seenFaceSystem)-1]
	for _, want := range []string{"## Runtime Facts", "execution_claim", "test_execution", "not required prose"} {
		if !strings.Contains(repairPrompt, want) {
			t.Fatalf("repair prompt missing %q:\n%s", want, repairPrompt)
		}
	}
	if strings.Contains(repairPrompt, "I need to correct that") {
		t.Fatalf("repair prompt leaked deterministic correction banner:\n%s", repairPrompt)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	assertHasEventType(t, events, core.ExecutionEventReplyClaimAdjudicated)
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
	cfg.Governor.Brokerage.MinRounds = 1
	cfg.Governor.Brokerage.MaxRounds = 4
	cfg.Governor.Brokerage.AbsoluteMaxRounds = 6
	cfg.Governor.Brokerage.MaxElapsed = "20s"
	cfg.Governor.Brokerage.StableContractRounds = 2
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
	if len(audit.BrokerageRounds) != cfg.Governor.Brokerage.MaxRounds {
		t.Fatalf("brokerage rounds = %d, want %d", len(audit.BrokerageRounds), cfg.Governor.Brokerage.MaxRounds)
	}
	if audit.BrokerageStopReason != turn.BrokerageStopMaxRounds || audit.BrokerageStopRound != cfg.Governor.Brokerage.MaxRounds {
		t.Fatalf("brokerage stop = reason:%q round:%d, want max_rounds at %d", audit.BrokerageStopReason, audit.BrokerageStopRound, cfg.Governor.Brokerage.MaxRounds)
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

func TestHandleInboundBrokerageFallsBackWhenContractStabilizes(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplies = []string{
		"INSPECT: yes\nQUESTION: no\nANSWER: yes\nPUSH:\n- Inspect first.",
		"INSPECT: yes\nQUESTION: no\nANSWER: yes\nPUSH:\n- Inspect the repo before answering.",
	}
	provider.brokerageReplyText = "INSPECT: yes\nQUESTION: no\nANSWER: yes\nPUSH:\n- Inspect the repo before answering."
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
		ChatID:     9006,
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
	if len(audit.BrokerageRounds) != 2 {
		t.Fatalf("brokerage rounds = %d, want 2 after stable contract", len(audit.BrokerageRounds))
	}
	if audit.BrokerageStopReason != turn.BrokerageStopStableContract || audit.BrokerageStopRound != 2 {
		t.Fatalf("brokerage stop = reason:%q round:%d, want stable_contract at 2", audit.BrokerageStopReason, audit.BrokerageStopRound)
	}
}

func TestGroundFinalReplyWithExecutionEvidenceAdjudicatesUngroundedSuccessClaimWithoutVisibleBanner(t *testing.T) {
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
	if rewritten != "Done. Everything finished cleanly." {
		t.Fatalf("rewritten = %q, want unchanged reply for persona repair path", rewritten)
	}
	if strings.Contains(rewritten, "I need to correct that") {
		t.Fatalf("rewritten = %q, want no deterministic correction banner", rewritten)
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

func TestGroundFinalReplyWithExecutionEvidenceDoesNotRewriteRunningTurnCompletion(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9305, UserID: 0, Scope: telegramDMScopeRef(9305)}
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{EventType: core.ExecutionEventTurnStarted, Stage: "turn", Status: "running", PayloadJSON: `{}`},
		{EventType: core.ExecutionEventToolSucceeded, Stage: "tool", Status: "succeeded", PayloadJSON: `{"tool":"exec","result_preview":"ok"}`},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	reply := "Done. I updated the files."
	rewritten, note := rt.groundFinalReplyWithExecutionEvidence(key, reply)
	if note != "" {
		t.Fatalf("note = %q, want empty note for still-running final render path", note)
	}
	if rewritten != reply {
		t.Fatalf("rewritten = %q, want unchanged reply", rewritten)
	}
}

func TestGroundFinalReplyWithExecutionEvidenceUsesLatestEventWindow(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9306, UserID: 0, Scope: telegramDMScopeRef(9306)}
	old := make([]session.ExecutionEventInput, 0, 310)
	for i := 0; i < 310; i++ {
		old = append(old, session.ExecutionEventInput{EventType: core.ExecutionEventToolFailed, Stage: "tool", Status: "failed", PayloadJSON: `{}`})
	}
	if _, err := store.AppendExecutionEvents(key, old); err != nil {
		t.Fatalf("AppendExecutionEvents(old) err = %v", err)
	}
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{EventType: core.ExecutionEventTurnStarted, Stage: "turn", Status: "running", PayloadJSON: `{}`},
		{EventType: core.ExecutionEventToolSucceeded, Stage: "tool", Status: "succeeded", PayloadJSON: `{"tool":"exec","preview":"{\"command\":\"go test ./...\"}","result_preview":"ok"}`},
		{EventType: core.ExecutionEventTurnCompleted, Stage: "turn", Status: "completed", PayloadJSON: `{}`},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(latest) err = %v", err)
	}

	reply := "Done. I ran go test and tests passed."
	rewritten, note := rt.groundFinalReplyWithExecutionEvidence(key, reply)
	if note != "" {
		t.Fatalf("note = %q, want empty note from latest event window", note)
	}
	if rewritten != reply {
		t.Fatalf("rewritten = %q, want unchanged reply", rewritten)
	}
}

func TestGroundFinalReplyWithExecutionEvidenceAdjudicatesUngroundedToolClaimWithoutVisibleBanner(t *testing.T) {
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

	reply := "I executed command-line checks and applied the patch."
	rewritten, note := rt.groundFinalReplyWithExecutionEvidence(key, reply)
	if strings.TrimSpace(note) == "" {
		t.Fatalf("note = %q, want non-empty grounding note", note)
	}
	if !strings.Contains(strings.ToLower(note), "tool-execution claim has no tool events") {
		t.Fatalf("note = %q, want structured grounding detail", note)
	}
	if rewritten != reply {
		t.Fatalf("rewritten = %q, want unchanged reply for persona repair path", rewritten)
	}
	if strings.Contains(rewritten, "I need to correct that") {
		t.Fatalf("rewritten = %q, want no deterministic correction banner", rewritten)
	}
}

func TestGroundFinalReplyWithExecutionEvidenceKeepsConceptualFeatureDiscussion(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9307, UserID: 0, Scope: telegramDMScopeRef(9307)}
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
			PayloadJSON: `{"summary":"answered conceptually"}`,
			CreatedAt:   now.Add(-10 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	reply := "Yes. I would frame document ingestion as a quarantine layer for the durable email agent, not as the bot blindly reading attachments."
	rewritten, note := rt.groundFinalReplyWithExecutionEvidence(key, reply)
	if note != "" {
		t.Fatalf("note = %q, want no grounding note for conceptual discussion", note)
	}
	if rewritten != reply {
		t.Fatalf("rewritten = %q, want unchanged reply", rewritten)
	}
}

func TestGroundFinalReplyWithExecutionEvidenceKeepsAttributedPriorValidation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9308, UserID: 0, Scope: telegramDMScopeRef(9308)}
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
			PayloadJSON: `{"summary":"reviewed prior validation"}`,
			CreatedAt:   now.Add(-10 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	reply := "I reviewed the existing validation record: go test ./... passed in the prior commit."
	rewritten, note := rt.groundFinalReplyWithExecutionEvidence(key, reply)
	if note != "" {
		t.Fatalf("note = %q, want no grounding note for attributed prior validation", note)
	}
	if rewritten != reply {
		t.Fatalf("rewritten = %q, want unchanged reply", rewritten)
	}
}

func TestGroundFinalReplyWithExecutionEvidenceKeepsCommandSuggestion(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9309, UserID: 0, Scope: telegramDMScopeRef(9309)}
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
			PayloadJSON: `{"summary":"answered with suggested command"}`,
			CreatedAt:   now.Add(-10 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	reply := "Use this exact command: go test ./..."
	rewritten, note := rt.groundFinalReplyWithExecutionEvidence(key, reply)
	if note != "" {
		t.Fatalf("note = %q, want no grounding note for command suggestion", note)
	}
	if rewritten != reply {
		t.Fatalf("rewritten = %q, want unchanged reply", rewritten)
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
