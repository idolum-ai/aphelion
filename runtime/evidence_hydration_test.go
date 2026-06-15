//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

func TestInteractiveAssemblyHydratesCurrentSessionEvidenceWithoutThreadLeak(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	admin, ok := rt.resolver.ResolveTelegramUser(1001)
	if !ok {
		t.Fatal("ResolveTelegramUser(1001) = false, want true")
	}
	scope, err := rt.scopeForPrincipal(admin)
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	defaultKey := session.SessionKey{ChatID: 99201, UserID: 1001, Scope: telegramDMScopeRef(99201)}
	threadKey := session.SessionKey{ChatID: 99201, UserID: 1001, Scope: session.TelegramThreadScopeRef(99201, 3)}
	if _, err := store.UpsertEvidenceObject(session.EvidenceObjectInput{
		SourceKind:      session.EvidenceSourceOperationState,
		SourceRef:       "operation_state:default-release",
		SessionID:       session.SessionIDForKey(defaultKey),
		ChatID:          defaultKey.ChatID,
		UserID:          defaultKey.UserID,
		Scope:           defaultKey.Scope,
		EpistemicStatus: session.EvidenceStatusProjection,
		SubjectKey:      "release",
		Summary:         "Current default-chat release work is about Aphelion v0.2.3.",
		PayloadJSON:     `{"topic":"aphelion release","thread":"default"}`,
		ObservedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertEvidenceObject(default) err = %v", err)
	}
	if _, err := store.UpsertEvidenceObject(session.EvidenceObjectInput{
		SourceKind:      session.EvidenceSourceOperationState,
		SourceRef:       "operation_state:thread-3-imexx",
		SessionID:       session.SessionIDForKey(threadKey),
		ChatID:          threadKey.ChatID,
		UserID:          threadKey.UserID,
		Scope:           threadKey.Scope,
		EpistemicStatus: session.EvidenceStatusProjection,
		SubjectKey:      "imexx",
		Summary:         "Thread 3 context mentions Imexx and must stay isolated.",
		PayloadJSON:     `{"topic":"imexx","thread":"3"}`,
		ObservedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertEvidenceObject(thread) err = %v", err)
	}

	msg := core.InboundMessage{
		ChatID:     defaultKey.ChatID,
		SenderID:   defaultKey.UserID,
		SenderName: "admin",
		Text:       "continue the release work",
		MessageID:  42,
	}
	assembled, err := rt.assembleInteractiveLikeTurn(context.Background(), interactiveLikeAssemblyInput{
		Scope:                scope,
		Key:                  defaultKey,
		Msg:                  msg,
		Channel:              "telegram",
		RunKind:              session.TurnRunKindInteractive,
		PrincipalRole:        string(admin.Role),
		AuditChannel:         "telegram",
		EventAwareness:       turnEventAwarenessForTest(msg),
		PromptContextErrHint: "load workspace prompt context",
		PolicyReason:         "mapped from pipeline interactive face policy",
	})
	if err != nil {
		t.Fatalf("assembleInteractiveLikeTurn() err = %v", err)
	}
	joined := strings.Join(assembled.BaseGovernorAwareness.EvidenceContext, "\n")
	if !strings.Contains(joined, "Aphelion v0.2.3") && !strings.Contains(joined, "aphelion release") {
		t.Fatalf("evidence context = %q, want default release evidence", joined)
	}
	if strings.Contains(strings.ToLower(joined), "imexx") {
		t.Fatalf("evidence context leaked thread 3 evidence: %q", joined)
	}
}

func TestInteractiveAssemblyEvidenceHydrationFavorsActiveRequestUnderPressure(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	admin, ok := rt.resolver.ResolveTelegramUser(1001)
	if !ok {
		t.Fatal("ResolveTelegramUser(1001) = false, want true")
	}
	scope, err := rt.scopeForPrincipal(admin)
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}

	key := session.SessionKey{ChatID: 99202, UserID: 1001, Scope: telegramDMScopeRef(99202)}
	sessionID := session.SessionIDForKey(key)
	if _, err := store.UpsertEvidenceObject(session.EvidenceObjectInput{
		SourceKind:      session.EvidenceSourceMessage,
		SourceRef:       "messages:stale-context",
		SessionID:       sessionID,
		ChatID:          key.ChatID,
		UserID:          key.UserID,
		Scope:           key.Scope,
		EpistemicStatus: session.EvidenceStatusClaimed,
		Summary:         "Old recovered context says focus on unrelated tailscale cleanup.",
		PayloadJSON:     `{"topic":"tailscale cleanup"}`,
		ObservedAt:      time.Now().UTC().Add(-10 * time.Minute),
	}); err != nil {
		t.Fatalf("UpsertEvidenceObject(stale) err = %v", err)
	}
	if _, err := store.UpsertEvidenceObject(session.EvidenceObjectInput{
		SourceKind:      session.EvidenceSourceOperationState,
		SourceRef:       "operation_state:active-hydration",
		SessionID:       sessionID,
		ChatID:          key.ChatID,
		UserID:          key.UserID,
		Scope:           key.Scope,
		EpistemicStatus: session.EvidenceStatusProjection,
		SubjectKey:      "hydration fidelity",
		Summary:         "Active objective: build evidence hydration fidelity tests.",
		PayloadJSON:     `{"topic":"hydration fidelity","objective":"tests"}`,
		ObservedAt:      time.Now().UTC().Add(-24 * time.Hour),
	}); err != nil {
		t.Fatalf("UpsertEvidenceObject(active) err = %v", err)
	}

	msg := core.InboundMessage{
		ChatID:     key.ChatID,
		SenderID:   key.UserID,
		SenderName: "admin",
		Text:       "continue the hydration fidelity tests under context pressure",
		MessageID:  43,
	}
	assembled, err := rt.assembleInteractiveLikeTurn(context.Background(), interactiveLikeAssemblyInput{
		Scope:                scope,
		Key:                  key,
		Msg:                  msg,
		Channel:              "telegram",
		RunKind:              session.TurnRunKindInteractive,
		PrincipalRole:        string(admin.Role),
		AuditChannel:         "telegram",
		EventAwareness:       turnEventAwarenessForTest(msg),
		PromptContextErrHint: "load workspace prompt context",
		PolicyReason:         "mapped from pipeline interactive face policy",
	})
	if err != nil {
		t.Fatalf("assembleInteractiveLikeTurn() err = %v", err)
	}
	joined := strings.Join(assembled.BaseGovernorAwareness.EvidenceContext, "\n")
	activeIdx := strings.Index(joined, "hydration fidelity")
	staleIdx := strings.Index(joined, "tailscale cleanup")
	if activeIdx < 0 {
		t.Fatalf("evidence context = %q, want active hydration evidence", joined)
	}
	if staleIdx >= 0 && staleIdx < activeIdx {
		t.Fatalf("stale context outranked active request evidence: %q", joined)
	}
}

func turnEventAwarenessForTest(msg core.InboundMessage) turn.EventAwareness {
	return turn.EventAwareness{Origin: inboundOriginLabel(msg)}
}
