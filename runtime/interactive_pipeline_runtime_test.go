//go:build linux

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
)

func TestHandleInboundApprovedUserDoesNotLoadGlobalDynamicMemory(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	if err := os.WriteFile(filepath.Join(cfg.Agent.ExecRoot, "MEMORY.md"), []byte("GLOBAL-MEMORY-SECRET"), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     71,
		SenderID:   1002,
		SenderName: "approved",
		Text:       "hello",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenGovernorSystem) == 0 {
		t.Fatal("seenGovernorSystem empty, want at least one prompt")
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "agent rules") {
		t.Fatalf("approved user prompt missing shared bootstrap: %q", provider.seenGovernorSystem[0])
	}
	if strings.Contains(provider.seenGovernorSystem[0], "GLOBAL-MEMORY-SECRET") {
		t.Fatalf("approved user prompt leaked global dynamic memory: %q", provider.seenGovernorSystem[0])
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "principal_role: approved_user") {
		t.Fatalf("approved user prompt missing principal role: %q", provider.seenGovernorSystem[0])
	}
}

func TestHandleInboundThreadsRuntimeAwarenessToGovernorAndIdolum(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "canonical"
	provider.faceReplyText = "rendered"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     711,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "I feel overwhelmed and need help thinking this through",
		MessageID:  1,
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenGovernorSystem) == 0 {
		t.Fatal("seenGovernorSystem empty, want governor prompt")
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "## Runtime Awareness") {
		t.Fatalf("governor prompt missing runtime awareness: %q", provider.seenGovernorSystem[0])
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "- run_kind: interactive") {
		t.Fatalf("governor prompt missing run kind: %q", provider.seenGovernorSystem[0])
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "- event_origin: user") {
		t.Fatalf("governor prompt missing event origin: %q", provider.seenGovernorSystem[0])
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "- prompt_root: "+cfg.Agent.PromptRoot) {
		t.Fatalf("governor prompt missing prompt root: %q", provider.seenGovernorSystem[0])
	}
	if len(provider.seenFaceSystem) == 0 {
		t.Fatal("seenFaceSystem empty, want face prompt")
	}
	if !strings.Contains(provider.seenFaceSystem[0], "## Delivery Awareness") {
		t.Fatalf("face prompt missing delivery awareness: %q", provider.seenFaceSystem[0])
	}
	if strings.Contains(provider.seenFaceSystem[0], "exec_root") {
		t.Fatalf("face prompt leaked exec root: %q", provider.seenFaceSystem[0])
	}
}

func TestHandleInboundIncludesIdolumProposalInGovernorInput(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplyText = "Push for a warmer reply and consider inspecting the repo before answering."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     72,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "I'm feeling uncertain about how to answer this well",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenProposalSystem) == 0 {
		t.Fatal("seenProposalSystem empty, want Idolum proposal prompt call")
	}
	if !strings.Contains(provider.seenProposalSystem[0], "mode: proposal") {
		t.Fatalf("proposal prompt missing proposal mode: %q", provider.seenProposalSystem[0])
	}
	if len(provider.seenGovernorSystem) == 0 {
		t.Fatal("seenGovernorSystem empty, want governor prompt")
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "## Conversational Pressure") {
		t.Fatalf("governor input missing Idolum proposal block: %q", provider.seenGovernorSystem[0])
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "Push for a warmer reply") {
		t.Fatalf("governor input missing concrete Idolum push: %q", provider.seenGovernorSystem[0])
	}
}

func TestHandleInboundFaceStagesUsePreparedLedgerText(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplyText = "Keep it steady and concrete."
	provider.replyText = "The current time is 12:00 UTC."
	provider.faceReplyText = "It's 12:00 UTC."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.ConfigureVoice(config.VoiceConfig{Mode: "auto"}, fakeTranscriber{text: "transcribed voice text"}, fakeSynth{})

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     7301,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  88,
		Artifacts:  []core.Artifact{{ID: "voice-issue3", Channel: "telegram", SourceType: "voice", Kind: "audio", Subtype: "voice_note", Data: []byte("ogg"), MimeType: "audio/ogg", Filename: "voice.ogg"}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenProposalSystem) == 0 {
		t.Fatal("seenProposalSystem empty, want proposal prompt call")
	}
	if len(provider.seenFaceSystem) == 0 {
		t.Fatal("seenFaceSystem empty, want face render prompt")
	}
	if !strings.Contains(provider.seenProposalSystem[0], "transcribed voice text") {
		t.Fatalf("proposal prompt = %q, want prepared ledger text", provider.seenProposalSystem[0])
	}
	if !strings.Contains(provider.seenProposalSystem[0], "[voice attached]") {
		t.Fatalf("proposal prompt = %q, want prepared ledger marker alongside transcript", provider.seenProposalSystem[0])
	}
	if !strings.Contains(provider.seenFaceSystem[0], "transcribed voice text") {
		t.Fatalf("face prompt = %q, want prepared ledger text", provider.seenFaceSystem[0])
	}
	_ = store
	_ = sender
}

func TestHandleInboundUsesBrokerageForStrategicTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplyText = "INSPECT: yes\nQUESTION: no\nANSWER: yes\nWHY: Ground the feature ideas in the repo.\nPUSH:\n- Inspect first.\n- Keep the answer concrete."
	provider.brokerageReplyText = "INSPECT: no\nQUESTION: no\nANSWER: yes\nWHY: The repo context already supports a direct answer.\nPUSH:\n- Answer directly.\n- Keep it concrete."
	provider.planningReplies = []string{
		"INSPECT: yes\nQUESTION: no\nANSWER: yes\nRATIFICATION: adapt\nPLAN:\n- Inspect the codebase before proposing features.\n- Then reply with prioritized ideas.",
		"INSPECT: no\nQUESTION: no\nANSWER: yes\nRATIFICATION: accept\nPLAN:\n- Reply with prioritized ideas grounded in the current repo context.",
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     720,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "come up with some features for my codebase",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenProposalSystem) == 0 {
		t.Fatal("seenProposalSystem empty, want proposal prompt call")
	}
	if !strings.Contains(provider.seenProposalSystem[0], "mode: proposal") {
		t.Fatalf("proposal prompt missing proposal mode: %q", provider.seenProposalSystem[0])
	}
	if len(provider.seenBrokerageSystem) == 0 {
		t.Fatal("seenBrokerageSystem empty, want revised brokerage prompt after adaptation")
	}
	if len(provider.seenPlanningSystem) == 0 {
		t.Fatal("seenPlanningSystem empty, want planning ratification call")
	}
	if len(provider.seenGovernorSystem) == 0 {
		t.Fatal("seenGovernorSystem empty, want governor prompt")
	}
	if !strings.Contains(provider.seenGovernorSystem[len(provider.seenGovernorSystem)-1], "- brokerage_active: true") {
		t.Fatalf("governor prompt missing brokerage awareness: %q", provider.seenGovernorSystem[len(provider.seenGovernorSystem)-1])
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "## Execution Contract") {
		t.Fatalf("governor input missing negotiated brokerage block: %#v", provider.lastGovernorMsgs)
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "### Conversational Pressure") {
		t.Fatalf("negotiated brokerage block missing idolum position: %q", provider.lastGovernorMsgs[1].Content)
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "### Approved Steps") {
		t.Fatalf("negotiated brokerage block missing approved steps: %q", provider.lastGovernorMsgs[1].Content)
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "### Ratification Record") {
		t.Fatalf("negotiated brokerage block missing ratification record: %q", provider.lastGovernorMsgs[1].Content)
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "inspect=no, question=no, answer=yes") {
		t.Fatalf("negotiated brokerage block missing execution contract summary: %q", provider.lastGovernorMsgs[1].Content)
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "- ratification: accept") {
		t.Fatalf("negotiated brokerage block missing ratification disposition: %q", provider.lastGovernorMsgs[1].Content)
	}
	if !strings.Contains(provider.seenGovernorSystem[len(provider.seenGovernorSystem)-1], "- brokerage_ratification: accept") {
		t.Fatalf("governor awareness missing brokerage ratification: %q", provider.seenGovernorSystem[len(provider.seenGovernorSystem)-1])
	}
}

func TestHandleInboundPersistsHiddenInputProvenanceForBrokerageTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Memory.Semantic.Enabled = true
	cfg.Memory.Semantic.Sources = []string{"memory/knowledge.md"}
	cfg.Memory.Semantic.InteractiveTopK = 5
	cfg.Memory.Semantic.InteractiveMaxChars = 4000
	provider.proposalReplyText = "INSPECT: yes\nQUESTION: no\nANSWER: yes\nWHY: There is a recurring semantic layer decision hiding under the feature request.\nPUSH:\n- Inspect first.\n- Name the buried blocker."
	provider.brokerageReplyText = "INSPECT: no\nQUESTION: no\nANSWER: yes\nWHY: The recurring blocker is already visible enough to name directly.\nPUSH:\n- Name the buried blocker plainly.\n- Then answer."
	provider.planningReplies = []string{
		"INSPECT: yes\nQUESTION: no\nANSWER: yes\nRATIFICATION: adapt\nSIGNAL_JUDGMENT: confirmed\nPLAN:\n- Inspect the codebase before proposing features.\n- Then answer with prioritized ideas.",
		"INSPECT: no\nQUESTION: no\nANSWER: yes\nRATIFICATION: accept\nSIGNAL_JUDGMENT: confirmed\nPLAN:\n- Name the buried blocker.\n- Then answer with prioritized ideas.",
	}

	if err := os.MkdirAll(filepath.Join(cfg.Agent.SharedMemoryRoot, "memory"), 0o755); err != nil {
		t.Fatalf("MkdirAll(memory) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Agent.SharedMemoryRoot, "memory", "knowledge.md"), []byte("# knowledge.md\n\n- The semantic layer is the recurring architectural tension."), 0o600); err != nil {
		t.Fatalf("write knowledge.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Agent.SharedMemoryRoot, "memory", "questions.md"), []byte("# questions.md\n\n- Should the semantic layer stay lexical-first or become vector-ranked?"), 0o600); err != nil {
		t.Fatalf("write questions.md: %v", err)
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     721,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "come up with some features for my semantic layer work",
		MessageID:  1,
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	if len(provider.seenProposalSystem) == 0 {
		t.Fatal("seenProposalSystem empty, want proposal prompt call")
	}
	if !strings.Contains(provider.seenProposalSystem[0], "- hidden_inputs_active: true") {
		t.Fatalf("proposal prompt missing hidden-input awareness: %q", provider.seenProposalSystem[0])
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "signal_judgment: confirmed") {
		t.Fatalf("negotiated brokerage block missing signal judgment: %q", provider.lastGovernorMsgs[1].Content)
	}
	provider.mu.Unlock()

	sess, err := store.Load(session.SessionKey{ChatID: 721, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if strings.TrimSpace(sess.LastFloorMetadata) == "" {
		t.Fatal("LastFloorMetadata empty, want hidden-input provenance")
	}
	if !strings.Contains(sess.LastFloorMetadata, "semantic_recurrence") {
		t.Fatalf("LastFloorMetadata = %q, want semantic recurrence", sess.LastFloorMetadata)
	}
	if !strings.Contains(sess.LastFloorMetadata, "unresolved_memory_state") {
		t.Fatalf("LastFloorMetadata = %q, want unresolved memory state", sess.LastFloorMetadata)
	}
	if len(sess.Messages) < 2 || strings.TrimSpace(sess.Messages[len(sess.Messages)-1].FloorMetadata) == "" {
		t.Fatalf("assistant floor metadata missing from messages: %#v", sess.Messages)
	}
}

func TestHandleInboundRendersFromStructuredMaterialFloor(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplyText = "Push for a clear, grounded answer."
	provider.replyText = strings.Join([]string{
		"FACTS:",
		"- The user is asking for help thinking through the situation.",
		"ALLOWED_ACTIONS:",
		"- Offer grounded next steps.",
		"SCENE_CONSTRAINTS:",
		"- Keep the tone steady and direct.",
		"NOTES:",
		"- Do not sound like a report.",
	}, "\n")
	provider.faceReplyText = "Rendered Idolum scene."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     7201,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "I feel overwhelmed and need help thinking this through",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenGovernorSystem) == 0 {
		t.Fatal("seenGovernorSystem empty, want governor prompt")
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "## Output Contract") {
		t.Fatalf("governor prompt missing material floor output contract: %q", provider.seenGovernorSystem[0])
	}
	if len(provider.seenFaceSystem) == 0 {
		t.Fatal("seenFaceSystem empty, want face render prompt")
	}
	if !strings.Contains(provider.seenFaceSystem[0], "## Execution Facts") {
		t.Fatalf("face prompt missing material floor section: %q", provider.seenFaceSystem[0])
	}
	if strings.Contains(provider.seenFaceSystem[0], "## Execution Facts Fallback") {
		t.Fatalf("face prompt should not use serialized floor fallback when structured material is available: %q", provider.seenFaceSystem[0])
	}

	sess, err := store.Load(session.SessionKey{ChatID: 7201, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if !strings.Contains(sess.LastFloorText, "FACTS:") {
		t.Fatalf("LastFloorText = %q, want text-shaped material floor", sess.LastFloorText)
	}
	if len(sess.Messages) < 2 {
		t.Fatalf("messages len = %d, want at least 2", len(sess.Messages))
	}
	last := sess.Messages[len(sess.Messages)-1]
	if got := strings.TrimSpace(last.Content); got != provider.faceReplyText {
		t.Fatalf("assistant scene content = %q, want rendered scene %q", got, provider.faceReplyText)
	}
	if !strings.Contains(last.FloorContent, "FACTS:") {
		t.Fatalf("assistant floor content = %q, want structured floor sidecar", last.FloorContent)
	}
	if strings.Contains(last.Content, "FACTS:") {
		t.Fatalf("assistant scene content leaked floor payload: %q", last.Content)
	}
}

func TestConvergeTurnBrokeragePropagatesLiveContextToFaceNote(t *testing.T) {
	t.Parallel()

	cfg, _, provider, _ := buildRuntimeFixtures(t)
	rt := &Runtime{cfg: cfg}
	exec := pipeline.TurnExecutionContract{Provider: provider}

	provider.planningReplyText = "INSPECT: yes\nQUESTION: no\nANSWER: yes\nRATIFICATION: adapt\nPLAN:\n- revise it"

	ctxKey := struct{}{}
	ctx := context.WithValue(context.Background(), ctxKey, "live-turn")

	called := false
	gotMode := ""
	gotValue := any(nil)

	updated, _ := rt.convergeTurnBrokerage(
		ctx,
		exec,
		prompt.RuntimeAwareness{},
		nil,
		nil,
		"user text",
		turnBrokerage{
			Active:             true,
			Phase:              "brokerage",
			IdolumNote:         "INSPECT: yes\nQUESTION: no\nANSWER: yes",
			Ratification:       "adapt",
			RatificationRecord: "needs revision",
		},
		func(reqCtx context.Context, mode string, awareness prompt.RuntimeAwareness, _ string, _ string) (string, core.TokenUsage, error) {
			called = true
			gotMode = mode
			gotValue = reqCtx.Value(ctxKey)
			_ = awareness
			return "Push the revised plan.", core.TokenUsage{}, nil
		},
		nil,
		nil,
	)

	if !called {
		t.Fatal("requestFaceNote was not called")
	}
	if gotValue != "live-turn" {
		t.Fatalf("context value = %#v, want live-turn", gotValue)
	}
	if gotMode != "brokerage" && gotMode != "proposal" {
		t.Fatalf("mode = %q, want brokerage or proposal callback path", gotMode)
	}
	if updated.IdolumNote != "Push the revised plan." {
		t.Fatalf("updated.IdolumNote = %q, want revised proposal", updated.IdolumNote)
	}
	if updated.Phase != "proposal" {
		t.Fatalf("updated.Phase = %q, want proposal", updated.Phase)
	}
	if updated.Ratification != "" {
		t.Fatalf("updated.Ratification = %q, want cleared ratification", updated.Ratification)
	}
	if updated.RatificationRecord != "" {
		t.Fatalf("updated.RatificationRecord = %q, want cleared record", updated.RatificationRecord)
	}
}

func TestHandleInboundFallsBackToPlainProposalWhenBrokerageRatificationFails(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplies = []string{
		"INSPECT: yes\nQUESTION: no\nANSWER: yes\nWHY: Ground the answer.\nPUSH:\n- Inspect first.",
		"Push for a concrete answer grounded in what is already known.",
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.provider = planningErrorProvider{Provider: rt.provider, err: errors.New("planning failed")}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     721,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "come up with some features for my codebase",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenProposalSystem) == 0 {
		t.Fatal("seenProposalSystem empty, want initial proposal plus proposal rerun after planning failure")
	}
	if len(provider.lastGovernorMsgs) < 2 {
		t.Fatalf("lastGovernorMsgs len = %d, want at least 2", len(provider.lastGovernorMsgs))
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "## Conversational Pressure") {
		t.Fatalf("governor input should fall back to Idolum proposal block: %q", provider.lastGovernorMsgs[1].Content)
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "Push for a concrete answer grounded in what is already known.") {
		t.Fatalf("governor input should use rerun proposal text: %q", provider.lastGovernorMsgs[1].Content)
	}
	if strings.Contains(provider.lastGovernorMsgs[1].Content, "## Execution Contract") {
		t.Fatalf("governor input should not contain negotiated brokerage after planning failure: %q", provider.lastGovernorMsgs[1].Content)
	}
	if !strings.Contains(provider.seenGovernorSystem[len(provider.seenGovernorSystem)-1], "- brokerage_phase: proposal") {
		t.Fatalf("governor awareness should fall back to proposal mode: %q", provider.seenGovernorSystem[len(provider.seenGovernorSystem)-1])
	}
}

func TestHandleInboundFallsBackToPlainProposalWhenBrokerageRatificationIsInvalid(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplies = []string{
		"INSPECT: yes\nQUESTION: no\nANSWER: yes\nPUSH:\n- Inspect first.",
		"Push for a concrete answer grounded in what is already known.",
	}
	provider.planningReplyText = "INSPECT: yes\nQUESTION: no\nANSWER: yes\nPLAN:\n- Inspect first."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     7211,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "come up with some features for my codebase",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenProposalSystem) == 0 {
		t.Fatal("seenProposalSystem empty, want proposal rerun after invalid planning response")
	}
	if len(provider.lastGovernorMsgs) < 2 {
		t.Fatalf("lastGovernorMsgs len = %d, want at least 2", len(provider.lastGovernorMsgs))
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "## Conversational Pressure") {
		t.Fatalf("governor input should fall back to Idolum proposal block: %q", provider.lastGovernorMsgs[1].Content)
	}
	if strings.Contains(provider.lastGovernorMsgs[1].Content, "## Execution Contract") {
		t.Fatalf("governor input should not contain negotiated brokerage after invalid planning response: %q", provider.lastGovernorMsgs[1].Content)
	}
}

func TestHandleInboundPreservesBrokerageWhenProposalRerunAlsoFails(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplyText = "INSPECT: yes\nQUESTION: no\nANSWER: yes\nPUSH:\n- Inspect first.\n- Keep the user moving."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.provider = planningErrorProvider{Provider: rt.provider, err: errors.New("planning failed")}
	provider.proposalErr = errors.New("proposal rerun failed")
	provider.proposalErrAfter = 2

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     722,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "come up with some features for my codebase",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.lastGovernorMsgs) < 2 {
		t.Fatalf("lastGovernorMsgs len = %d, want at least 2", len(provider.lastGovernorMsgs))
	}
	if !strings.Contains(provider.lastGovernorMsgs[1].Content, "## Conversational Pressure") {
		t.Fatalf("governor input should fail closed to proposal framing when rerun fails: %q", provider.lastGovernorMsgs[1].Content)
	}
	if strings.Contains(provider.lastGovernorMsgs[1].Content, "## Execution Contract") {
		t.Fatalf("governor input should not retain negotiated brokerage after failed convergence: %q", provider.lastGovernorMsgs[1].Content)
	}
	if !strings.Contains(provider.seenGovernorSystem[len(provider.seenGovernorSystem)-1], "- brokerage_phase: proposal") {
		t.Fatalf("governor awareness should fall back to proposal mode when rerun fails: %q", provider.seenGovernorSystem[len(provider.seenGovernorSystem)-1])
	}
}

func TestHandleInboundFaceFailureUsesSerializedFallbackAfterMaterialFloor(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplyText = "Push for a clear, grounded answer."
	provider.replyText = strings.Join([]string{
		"FACTS:",
		"- The repo was inspected.",
		"ALLOWED_ACTIONS:",
		"- Propose the strongest next steps.",
		"SCENE_CONSTRAINTS:",
		"- Keep the tone practical.",
	}, "\n")
	provider.faceErr = errors.New("render failed")

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     7202,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "I feel overwhelmed and need help thinking this through",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 7202, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	want := strings.Join([]string{
		"What matters:",
		"- The repo was inspected.",
		"",
		"Next:",
		"- Propose the strongest next steps.",
	}, "\n")
	if sender.sent[0].Text != want {
		t.Fatalf("sent text = %q, want serialized floor fallback %q", sender.sent[0].Text, want)
	}
	if !strings.Contains(sess.LastFloorText, "FACTS:") {
		t.Fatalf("session floor sidecar = %q, want structured floor sidecar", sess.LastFloorText)
	}
	if len(sess.Messages) < 2 {
		t.Fatalf("messages len = %d, want at least 2", len(sess.Messages))
	}
	if !strings.Contains(sess.Messages[1].FloorContent, "FACTS:") {
		t.Fatalf("assistant floor content = %q, want structured floor sidecar", sess.Messages[1].FloorContent)
	}
}

func TestHandleInboundFloorFallbackBackendSerializesStructuredFloor(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.proposalReplyText = "Stay grounded and concrete."
	provider.replyText = strings.Join([]string{
		"FACTS:",
		"- The repo was inspected.",
		"COMMITMENTS:",
		"- Keep the answer focused on the next move.",
		"SCENE_CONSTRAINTS:",
		"- Do not sound theatrical.",
	}, "\n")

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     7203,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "help me think this through",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	want := strings.Join([]string{
		"What matters:",
		"- The repo was inspected.",
		"",
		"Committed:",
		"- Keep the answer focused on the next move.",
	}, "\n")

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != want {
		t.Fatalf("outbound text = %q, want serialized floor fallback %q", sender.sent[0].Text, want)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 7203, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if !strings.Contains(sess.LastFloorText, "FACTS:") {
		t.Fatalf("session floor sidecar = %q, want structured floor sidecar", sess.LastFloorText)
	}
	if len(sess.Messages) < 2 || sess.Messages[1].Content != want {
		t.Fatalf("visible transcript assistant content = %q, want serialized floor fallback", sess.Messages[1].Content)
	}
	if len(sess.Messages) >= 2 && !strings.Contains(sess.Messages[1].FloorContent, "FACTS:") {
		t.Fatalf("assistant floor content = %q, want structured floor sidecar", sess.Messages[1].FloorContent)
	}
}

func TestHandleInboundSurfacesPriorRetainedArtifactsAsHiddenInput(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = strings.TrimSpace(`FACTS:
- I can still see the retained artifact context.
SCENE_CONSTRAINTS:
- Keep the reply simple.`)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	artifactMeta := `{"artifacts":[{"artifact_id":"doc-1","summary":"notes.txt","retention":"child_local","materialized_path":"/tmp/notes.txt"}]}`
	seed := &session.Session{
		ChatID:            1001,
		UserID:            0,
		LastFloorMetadata: artifactMeta,
		Messages:          []session.Message{{Role: "assistant", Content: "prior reply", FloorMetadata: artifactMeta, TurnIndex: 1}},
		TurnCount:         1,
	}
	if err := store.Save(seed, nil, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(seed) err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1001,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  2,
		Text:       "what do we still have from before?",
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	reloaded, err := store.Load(session.SessionKey{ChatID: 1001, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if !strings.Contains(reloaded.LastFloorMetadata, "retained_artifact_context") {
		t.Fatalf("LastFloorMetadata = %q, want retained artifact hidden input", reloaded.LastFloorMetadata)
	}
	if !strings.Contains(reloaded.LastFloorMetadata, "notes.txt") {
		t.Fatalf("LastFloorMetadata = %q, want retained artifact summary", reloaded.LastFloorMetadata)
	}
}
