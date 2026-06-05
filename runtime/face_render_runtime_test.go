//go:build linux

package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
)

func TestHandleInboundRendersViaFaceByDefault(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "governor canonical"
	provider.faceReplyText = "idolum rendered"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     901,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hello",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	finalText := sender.sent[0].Text
	if len(sender.edits) > 0 {
		finalText = sender.edits[len(sender.edits)-1].Text
	}
	if finalText != "idolum rendered" {
		t.Fatalf("outbound text = %q, want idolum rendered", finalText)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 901, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(sess.Messages) < 2 {
		t.Fatalf("session messages len = %d, want >= 2", len(sess.Messages))
	}
	if sess.Messages[1].Content != "idolum rendered" {
		t.Fatalf("session assistant text = %q, want rendered reply", sess.Messages[1].Content)
	}
	if sess.LastFloorText != "governor canonical" {
		t.Fatalf("session floor sidecar = %q, want canonical", sess.LastFloorText)
	}
}

func TestHandleInboundFaceFailureFallsBackToFloorFallback(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "governor canonical"
	provider.faceErr = errors.New("face unavailable")

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     902,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hello",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != "governor canonical" {
		t.Fatalf("outbound text = %q, want canonical fallback", sender.sent[0].Text)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 902, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if sess.LastFloorText != "governor canonical" {
		t.Fatalf("session floor sidecar = %q, want canonical", sess.LastFloorText)
	}
	if len(sess.Messages) < 2 || sess.Messages[1].Content != "governor canonical" {
		t.Fatalf("visible transcript assistant content = %q, want canonical fallback", sess.Messages[1].Content)
	}
}

func TestHandleInboundFloorFallbackBackendSkipsFaceRender(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "governor canonical"
	provider.faceReplyText = "idolum rendered"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     903,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hello",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != "governor canonical" {
		t.Fatalf("outbound text = %q, want canonical passthrough", sender.sent[0].Text)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 903, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if sess.LastFloorText != "governor canonical" {
		t.Fatalf("session floor sidecar = %q, want canonical", sess.LastFloorText)
	}
	if len(sess.Messages) < 2 || sess.Messages[1].Content != "governor canonical" {
		t.Fatalf("visible transcript assistant content = %q, want canonical passthrough", sess.Messages[1].Content)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenFaceSystem) != 0 {
		t.Fatalf("face should not be called in passthrough mode; calls=%d", len(provider.seenFaceSystem))
	}
}

func TestRenderTurnReplySkipsFaceForMaterialStatusReport(t *testing.T) {
	t.Parallel()

	cfg, store, provider, _ := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, &fakeSender{})
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendProvider
	renderer := &countingFaceRenderer{text: "unexpected face render"}
	packet := core.MaterialPacket{
		Facts:          []string{"PR #140 deployed and verified at revision 3942a132."},
		AllowedActions: []string{"Report post-deploy token/cache telemetry."},
		Refusals:       []string{"No deploy or restart was repeated."},
	}
	floorText := packet.Text()
	fallback := pipeline.SerializeFloorFallback(packet, floorText, pipeline.FallbackOptions{Channel: "telegram"})
	key := session.SessionKey{ChatID: 904, UserID: 0}

	result, err := rt.renderTurnReply(turnRenderInput{
		Ctx:              context.Background(),
		Key:              key,
		Result:           &core.TurnResult{Text: floorText},
		FacePolicy:       pipeline.FacePolicy{Render: true},
		UseMaterialFloor: true,
		ReplyText:        fallback,
		FloorText:        floorText,
		MaterialFloor:    packet,
		FallbackOpts:     pipeline.FallbackOptions{Channel: "telegram"},
		FaceAwareness:    prompt.RuntimeAwareness{ReplyModalityDefault: "text", ReplyModalityOverride: "none"},
		CurrentFaceModel: renderer,
		PromptInput:      "report deploy status",
	})
	if err != nil {
		t.Fatalf("renderTurnReply() err = %v", err)
	}
	if renderer.calls != 0 {
		t.Fatalf("face render calls = %d, want 0 for material status report", renderer.calls)
	}
	if !strings.Contains(result.ReplyText, "What matters:") || !strings.Contains(result.ReplyText, "PR #140 deployed") {
		t.Fatalf("ReplyText = %q, want serialized material fallback", result.ReplyText)
	}

	events, err := store.ExecutionEventsBySession(key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	found := false
	for _, event := range events {
		if event.EventType == core.ExecutionEventFaceRenderSkipped && strings.Contains(event.PayloadJSON, faceSkipReasonMaterialStatusReport) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("events = %#v, want face.render.skipped material_status_report", events)
	}
}

func TestRenderTurnReplyDoesNotSkipFaceForRelationalStatusShapedPacket(t *testing.T) {
	t.Parallel()

	cfg, store, provider, _ := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, &fakeSender{})
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendProvider
	renderer := &countingFaceRenderer{text: "relational face render"}
	packet := core.MaterialPacket{
		Facts: []string{"The eulogy draft passed review and merged grief into something warmer."},
	}
	floorText := packet.Text()
	fallback := pipeline.SerializeFloorFallback(packet, floorText, pipeline.FallbackOptions{Channel: "telegram"})

	result, err := rt.renderTurnReply(turnRenderInput{
		Ctx:              context.Background(),
		Key:              session.SessionKey{ChatID: 906, UserID: 0},
		Result:           &core.TurnResult{Text: floorText},
		FacePolicy:       pipeline.FacePolicy{Render: true},
		UseMaterialFloor: true,
		ReplyText:        fallback,
		FloorText:        floorText,
		MaterialFloor:    packet,
		FallbackOpts:     pipeline.FallbackOptions{Channel: "telegram"},
		FaceAwareness:    prompt.RuntimeAwareness{ReplyModalityDefault: "text", ReplyModalityOverride: "none"},
		CurrentFaceModel: renderer,
		PromptInput:      "read this carefully",
	})
	if err != nil {
		t.Fatalf("renderTurnReply() err = %v", err)
	}
	if renderer.calls != 1 {
		t.Fatalf("face render calls = %d, want 1 for relational override", renderer.calls)
	}
	if result.ReplyText != "relational face render" {
		t.Fatalf("ReplyText = %q, want face-rendered relational text", result.ReplyText)
	}
}

func TestRenderTurnReplyDoesNotSkipFaceForSceneConstraints(t *testing.T) {
	t.Parallel()

	cfg, store, provider, _ := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, &fakeSender{})
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendProvider
	renderer := &countingFaceRenderer{text: "scene-aware face render"}
	packet := core.MaterialPacket{
		Facts:            []string{"PR #140 deployed and verified."},
		SceneConstraints: []string{"Keep the visible reply warm and brief."},
	}
	floorText := packet.Text()
	fallback := pipeline.SerializeFloorFallback(packet, floorText, pipeline.FallbackOptions{Channel: "telegram"})

	result, err := rt.renderTurnReply(turnRenderInput{
		Ctx:              context.Background(),
		Key:              session.SessionKey{ChatID: 907, UserID: 0},
		Result:           &core.TurnResult{Text: floorText},
		FacePolicy:       pipeline.FacePolicy{Render: true},
		UseMaterialFloor: true,
		ReplyText:        fallback,
		FloorText:        floorText,
		MaterialFloor:    packet,
		FallbackOpts:     pipeline.FallbackOptions{Channel: "telegram"},
		FaceAwareness:    prompt.RuntimeAwareness{ReplyModalityDefault: "text", ReplyModalityOverride: "none"},
		CurrentFaceModel: renderer,
		PromptInput:      "report deploy status",
	})
	if err != nil {
		t.Fatalf("renderTurnReply() err = %v", err)
	}
	if renderer.calls != 1 {
		t.Fatalf("face render calls = %d, want 1 for scene constraints", renderer.calls)
	}
	if result.ReplyText != "scene-aware face render" {
		t.Fatalf("ReplyText = %q, want scene-aware face render", result.ReplyText)
	}
}

func TestFaceSkipPayloadContainsOnlyDecisionFields(t *testing.T) {
	t.Parallel()

	packet := core.MaterialPacket{
		Facts:          []string{"PR #140 deployed and verified."},
		AllowedActions: []string{"Report evidence."},
		Refusals:       []string{"No restart repeated."},
	}
	payload := faceSkipPayload(faceSkipReasonMaterialStatusReport, turnRenderInput{
		Result:           &core.TurnResult{},
		FacePolicy:       pipeline.FacePolicy{Render: true},
		UseMaterialFloor: true,
		MaterialFloor:    packet,
		FaceAwareness:    prompt.RuntimeAwareness{ReplyModalityDefault: "text"},
	}, "fallback")

	for _, key := range []string{"reason", "media_count", "facts", "allowed_actions", "commitments", "refusals", "notes", "fallback_chars"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("payload missing %q: %#v", key, payload)
		}
	}
	for _, key := range []string{"policy_render", "reply_with_voice", "inbound_was_voice", "reply_modality_default", "reply_modality_override", "scene_constraints"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("payload contains non-decision field %q: %#v", key, payload)
		}
	}
}

func TestShouldRecordFaceSkipEventAllowsZeroSessionKey(t *testing.T) {
	t.Parallel()

	if !shouldRecordFaceSkipEvent(session.SessionKey{}) {
		t.Fatal("shouldRecordFaceSkipEvent(zero key) = false, want true for maintenance observability")
	}
}

func TestRenderTurnReplyDoesNotSkipFaceForVoiceModality(t *testing.T) {
	t.Parallel()

	cfg, store, provider, _ := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, &fakeSender{})
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendProvider
	renderer := &countingFaceRenderer{text: "voice-shaped face render"}
	packet := core.MaterialPacket{Facts: []string{"PR #140 deployed and verified."}}
	floorText := packet.Text()
	fallback := pipeline.SerializeFloorFallback(packet, floorText, pipeline.FallbackOptions{Channel: "telegram", Voice: true})

	result, err := rt.renderTurnReply(turnRenderInput{
		Ctx:              context.Background(),
		Key:              session.SessionKey{ChatID: 905, UserID: 0},
		Result:           &core.TurnResult{Text: floorText},
		FacePolicy:       pipeline.FacePolicy{Render: true},
		UseMaterialFloor: true,
		ReplyWithVoice:   true,
		ReplyText:        fallback,
		FloorText:        floorText,
		MaterialFloor:    packet,
		FallbackOpts:     pipeline.FallbackOptions{Channel: "telegram", Voice: true},
		FaceAwareness:    prompt.RuntimeAwareness{ReplyModalityDefault: "voice", ReplyModalityOverride: "voice"},
		CurrentFaceModel: renderer,
		PromptInput:      "say the deployment status",
	})
	if err != nil {
		t.Fatalf("renderTurnReply() err = %v", err)
	}
	if renderer.calls != 1 {
		t.Fatalf("face render calls = %d, want 1 for voice modality", renderer.calls)
	}
	if result.ReplyText != "voice-shaped face render" {
		t.Fatalf("ReplyText = %q, want face-rendered voice text", result.ReplyText)
	}
}
