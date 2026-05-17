//go:build linux

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/governorauth"
	"github.com/idolum-ai/aphelion/pipeline"
	providerpkg "github.com/idolum-ai/aphelion/provider"
	"github.com/idolum-ai/aphelion/session"
)

func TestFaceProviderSelection(t *testing.T) {
	origFaceRenderer := newFaceRenderer
	origResolveAuth := resolveGovernorAuth
	origCodexProvider := newCodexProvider
	defer func() {
		newFaceRenderer = origFaceRenderer
		resolveGovernorAuth = origResolveAuth
		newCodexProvider = origCodexProvider
	}()

	codexProvider := &fakeProvider{replyText: "codex"}
	newCodexProvider = func(bundle governorauth.Bundle, cfg *config.Config) (agent.Provider, error) {
		return codexProvider, nil
	}

	tests := []struct {
		name        string
		faceBackend face.Backend
		resolveAuth func(config.GovernorConfig) (governorauth.Bundle, error)
		assert      func(*testing.T, agent.Provider, agent.Provider, agent.Provider)
	}{
		{
			name:        "provider backend uses supplied provider",
			faceBackend: face.BackendProvider,
			resolveAuth: func(cfg config.GovernorConfig) (governorauth.Bundle, error) {
				return governorauth.Bundle{Backend: governorauth.BackendNative}, nil
			},
			assert: func(t *testing.T, captured, providerArg, _ agent.Provider) {
				t.Helper()
				if captured != providerArg {
					t.Fatalf("got face provider %T, want supplied provider %T", captured, providerArg)
				}
			},
		},
		{
			name:        "floor_fallback uses governor provider",
			faceBackend: face.BackendFloorFallback,
			resolveAuth: func(cfg config.GovernorConfig) (governorauth.Bundle, error) {
				return governorauth.Bundle{Backend: governorauth.BackendCodex, BaseURL: "https://codex", AccessToken: "token"}, nil
			},
			assert: func(t *testing.T, captured, _, codexAgent agent.Provider) {
				t.Helper()
				if captured == codexAgent {
					return
				}
				if _, ok := captured.(*providerpkg.FailoverChain); !ok {
					t.Fatalf("got face provider %T, want codex provider or failover chain", captured)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Helper()
			cfg, store, _, sender := buildRuntimeFixtures(t)
			cfg.Face.Backend = string(tt.faceBackend)

			var captured agent.Provider
			newFaceRenderer = func(p agent.Provider, cfg face.ProviderRendererConfig) (*face.ProviderRenderer, error) {
				captured = p
				return origFaceRenderer(p, cfg)
			}
			t.Cleanup(func() { newFaceRenderer = origFaceRenderer })

			resolveGovernorAuth = tt.resolveAuth
			t.Cleanup(func() { resolveGovernorAuth = origResolveAuth })

			providerArg := &fakeProvider{replyText: "face"}
			if _, err := New(cfg, store, providerArg, nil, sender); err != nil {
				t.Fatalf("New() err = %v", err)
			}

			tt.assert(t, captured, providerArg, codexProvider)
		})
	}
}

func TestNewRejectsNilDependencies(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	store := &session.SQLiteStore{}
	provider := &fakeProvider{}
	sender := &fakeSender{}

	if _, err := New(nil, store, provider, nil, sender); err == nil {
		t.Fatal("expected nil config error")
	}
	if _, err := New(cfg, nil, provider, nil, sender); err == nil {
		t.Fatal("expected nil store error")
	}
	if _, err := New(cfg, store, nil, nil, sender); err == nil {
		t.Fatal("expected nil provider error")
	}
	if _, err := New(cfg, store, provider, nil, nil); err == nil {
		t.Fatal("expected nil outbound error")
	}
}

func TestNewAllowsNilProviderForCodexFloorFallback(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	cfg.Governor.Backend = "codex"
	cfg.Governor.Codex.AuthSource = "codex_cli"
	cfg.Governor.Codex.CodexHome = t.TempDir()
	cfg.Face.Backend = "floor_fallback"

	authPath := filepath.Join(cfg.Governor.Codex.CodexHome, "auth.json")
	rawAuth := `{"tokens":{"access_token":"codex-access","refresh_token":"refresh-secret","account_id":"acct"}}`
	if err := os.WriteFile(authPath, []byte(rawAuth), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	origFactory := newCodexProvider
	defer func() { newCodexProvider = origFactory }()
	newCodexProvider = func(_ governorauth.Bundle, _ *config.Config) (agent.Provider, error) {
		return &fakeProvider{replyText: "codex canonical"}, nil
	}

	if _, err := New(cfg, store, nil, nil, sender); err != nil {
		t.Fatalf("New() err = %v, want nil native provider to be allowed for codex passthrough", err)
	}
}

func TestNewRejectsInvalidIdleExpiry(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Sessions.IdleExpiry = "not-a-duration"

	_, err := New(cfg, store, provider, nil, sender)
	if err == nil {
		t.Fatal("New() err = nil, want idle_expiry parse error")
	}
}

func TestNewRejectsCodexBackendWithoutCredentials(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Governor.Backend = "codex"
	cfg.Governor.Codex.AuthSource = "codex_cli"
	cfg.Governor.Codex.CodexHome = t.TempDir()

	_, err := New(cfg, store, provider, nil, sender)
	if err == nil {
		t.Fatal("New() err = nil, want codex credential failure")
	}
}

func writeTestCodexAuth(t *testing.T, authPath string) {
	t.Helper()
	rawAuth := `{"tokens":{"access_token":"codex-access","refresh_token":"refresh-secret","account_id":"acct"}}`
	if err := os.WriteFile(authPath, []byte(rawAuth), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
}

func TestNewCodexProviderUsesStoreResponsesConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Governor.Codex.StoreResponses = true

	provider, err := newCodexProvider(governorauth.Bundle{
		BaseURL:      "https://chatgpt.com/backend-api",
		AccessToken:  "codex-access",
		RefreshToken: "codex-refresh",
		AccountID:    "acct",
	}, &cfg)
	if err != nil {
		t.Fatalf("newCodexProvider(default store responses) err = %v", err)
	}
	if !codexStoreResponsesForTest(t, provider) {
		t.Fatalf("codex storeResponses = false, want true from config")
	}

	cfg.Governor.Codex.StoreResponses = false
	provider, err = newCodexProvider(governorauth.Bundle{
		BaseURL:      "https://chatgpt.com/backend-api",
		AccessToken:  "codex-access",
		RefreshToken: "codex-refresh",
		AccountID:    "acct",
	}, &cfg)
	if err != nil {
		t.Fatalf("newCodexProvider(disabled store responses) err = %v", err)
	}
	if codexStoreResponsesForTest(t, provider) {
		t.Fatalf("codex storeResponses = true, want false from config")
	}
}

func codexStoreResponsesForTest(t *testing.T, provider agent.Provider) bool {
	t.Helper()

	value := reflect.ValueOf(provider)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	field := value.FieldByName("storeResponses")
	if !field.IsValid() || field.Kind() != reflect.Bool {
		t.Fatalf("provider %T has no bool storeResponses field", provider)
	}
	return field.Bool()
}

func TestHandleInboundUsesCodexGovernorBackend(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Governor.Backend = "codex"
	cfg.Governor.Codex.AuthSource = "codex_cli"
	cfg.Governor.Codex.CodexHome = t.TempDir()

	const accessToken = "codex-access-secret"
	authPath := filepath.Join(cfg.Governor.Codex.CodexHome, "auth.json")
	rawAuth := `{"tokens":{"access_token":"` + accessToken + `","refresh_token":"refresh-secret","account_id":"acct"}}`
	if err := os.WriteFile(authPath, []byte(rawAuth), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
	cfg.Governor.Codex.BaseURL = "https://chatgpt.com/backend-api"

	origFactory := newCodexProvider
	defer func() { newCodexProvider = origFactory }()
	newCodexProvider = func(_ governorauth.Bundle, _ *config.Config) (agent.Provider, error) {
		return &fakeProvider{
			replyText: "codex canonical",
			responseUsage: core.TokenUsage{
				InputTokens:  12,
				OutputTokens: 7,
				TotalTokens:  19,
			},
		}, nil
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     404,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hi",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	callCount := provider.callCount
	provider.mu.Unlock()
	if callCount != 0 {
		t.Fatalf("native provider call count = %d, want 0", callCount)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != "codex canonical" {
		t.Fatalf("outbound text = %q, want codex canonical", sender.sent[0].Text)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 404, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if strings.Contains(sess.SystemPrompt, accessToken) {
		t.Fatalf("system prompt leaked token: %q", sess.SystemPrompt)
	}
}

func TestHandleInboundProviderFailureRecordsFailureAndAlertsAdmin(t *testing.T) {
	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &providerFailureDuringToolLoop{
		err: stubRuntimeStatusError{code: 503, msg: "503 codex overloaded"},
	}
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     404,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "trigger provider failure",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	key := session.SessionKey{ChatID: 404, UserID: 0, Scope: telegramDMScopeRef(404)}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventProviderAttemptFailed) {
		t.Fatalf("events = %#v, want provider attempt failed", events)
	}
	if hasExecutionEvent(events, core.ExecutionEventProviderAttemptSucceeded) {
		t.Fatalf("events = %#v, provider failure should not be recorded as provider success", events)
	}

	foundAlert := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		sender.mu.Lock()
		for _, msg := range sender.sent {
			if msg.ChatID == 1001 && strings.Contains(msg.Text, "System warning") && strings.Contains(msg.Text, "Component: provider") {
				foundAlert = true
				break
			}
		}
		sender.mu.Unlock()
		if foundAlert {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !foundAlert {
		sender.mu.Lock()
		defer sender.mu.Unlock()
		t.Fatalf("sent messages = %#v, want provider operational alert to admin", sender.sent)
	}
}

func TestRenderTurnReplyBypassesFaceRenderForProviderFailure(t *testing.T) {
	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &fakeProvider{}
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendProvider
	renderer := &countingFaceRenderer{text: "I haven't actually seen the repo work yet."}

	const failureText = "Inference backend is unavailable. This turn did not complete. You can /stop to cancel current work and try again."
	result, err := rt.renderTurnReply(turnRenderInput{
		Ctx:              context.Background(),
		Result:           &core.TurnResult{Text: failureText, ProviderFailure: "codex: incomplete response without stored-response continuation"},
		FacePolicy:       pipeline.FacePolicy{Render: true},
		ReplyText:        failureText,
		FloorText:        failureText,
		PromptInput:      "check the work in the repo",
		CurrentFaceModel: renderer,
	})
	if err != nil {
		t.Fatalf("renderTurnReply() err = %v", err)
	}
	if result.ReplyText != failureText {
		t.Fatalf("ReplyText = %q, want deterministic provider failure reply", result.ReplyText)
	}
	if renderer.calls != 0 {
		t.Fatalf("face render calls = %d, want 0 for provider failure recovery", renderer.calls)
	}
}

type countingFaceRenderer struct {
	text  string
	calls int
}

func (r *countingFaceRenderer) Render(context.Context, face.RenderRequest) (string, error) {
	r.calls++
	return r.text, nil
}

type providerFailureDuringToolLoop struct {
	err error
}

func (p *providerFailureDuringToolLoop) Complete(_ context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	if len(messages) > 0 && messages[0].Role == "system" && strings.Contains(messages[0].Content, "the face of") {
		return &agent.Response{Content: "ok"}, nil
	}
	if len(tools) > 0 {
		return nil, p.err
	}
	for _, msg := range messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "Before the main turn executes, ratify how this turn should proceed.") {
			return &agent.Response{Content: "INSPECT: no\nQUESTION: no\nANSWER: yes\nRATIFICATION: accept\nPLAN:\n- Answer directly."}, nil
		}
	}
	for _, msg := range messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "trigger provider failure") {
			return nil, p.err
		}
	}
	return &agent.Response{Content: "ok"}, nil
}

func (p *providerFailureDuringToolLoop) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, _ agent.CompleteOptions) (*agent.Response, error) {
	return p.Complete(ctx, messages, tools)
}

func hasExecutionEvent(events []session.ExecutionEvent, eventType string) bool {
	for _, event := range events {
		if event.EventType == eventType {
			return true
		}
	}
	return false
}

func TestNewAutoFallsBackToNativeWhenCodexCredentialsMissing(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Governor.Backend = "auto"
	cfg.Governor.Codex.AuthSource = "codex_cli"
	cfg.Governor.Codex.CodexHome = t.TempDir()

	origFactory := newCodexProvider
	defer func() { newCodexProvider = origFactory }()

	var codexFactoryCalls int32
	newCodexProvider = func(_ governorauth.Bundle, _ *config.Config) (agent.Provider, error) {
		atomic.AddInt32(&codexFactoryCalls, 1)
		return &fakeProvider{replyText: "codex"}, nil
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     405,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hi",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	if got := atomic.LoadInt32(&codexFactoryCalls); got != 0 {
		t.Fatalf("codex factory calls = %d, want 0 in native fallback", got)
	}
	provider.mu.Lock()
	callCount := provider.callCount
	provider.mu.Unlock()
	if callCount == 0 {
		t.Fatal("native provider was not used in auto fallback")
	}
}

func TestNewAutoPrefersCodexWhenCredentialsExist(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Governor.Backend = "auto"
	cfg.Governor.Codex.AuthSource = "codex_cli"
	cfg.Governor.Codex.CodexHome = t.TempDir()

	authPath := filepath.Join(cfg.Governor.Codex.CodexHome, "auth.json")
	rawAuth := `{"tokens":{"access_token":"codex-access","refresh_token":"refresh-secret","account_id":"acct"}}`
	if err := os.WriteFile(authPath, []byte(rawAuth), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	origFactory := newCodexProvider
	defer func() { newCodexProvider = origFactory }()
	newCodexProvider = func(_ governorauth.Bundle, _ *config.Config) (agent.Provider, error) {
		return &fakeProvider{replyText: "codex auto"}, nil
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     406,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hi",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	callCount := provider.callCount
	provider.mu.Unlock()
	if callCount != 0 {
		t.Fatalf("native provider call count = %d, want 0 when codex selected", callCount)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 || sender.sent[0].Text != "codex auto" {
		t.Fatalf("outbound = %#v, want codex auto", sender.sent)
	}
}

func TestCodexRuntimeFailureFallsBackToNativeProviderChain(t *testing.T) {
	cfg, store, nativeProvider, sender := buildRuntimeFixtures(t)
	cfg.Governor.Backend = "codex"
	cfg.Governor.Codex.AuthSource = "codex_cli"
	cfg.Governor.Codex.CodexHome = t.TempDir()
	writeTestCodexAuth(t, filepath.Join(cfg.Governor.Codex.CodexHome, "auth.json"))
	cfg.Face.Backend = "floor_fallback"

	origFactory := newCodexProvider
	defer func() { newCodexProvider = origFactory }()
	newCodexProvider = func(_ governorauth.Bundle, _ *config.Config) (agent.Provider, error) {
		return &fakeProvider{err: stubRuntimeStatusError{code: 503, msg: "codex unavailable"}}, nil
	}

	rt, err := New(cfg, store, nativeProvider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     407,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hi",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	nativeProvider.mu.Lock()
	callCount := nativeProvider.callCount
	nativeProvider.mu.Unlock()
	if callCount == 0 {
		t.Fatal("native provider was not used after codex runtime failure")
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 2 {
		t.Fatalf("sent len = %d, want fallback warning + final reply", len(sender.sent))
	}
	if got := sender.sent[0].Text; !strings.HasPrefix(got, "Provider fallback:") || strings.Contains(got, "\n") {
		t.Fatalf("fallback warning = %q, want one-line provider fallback warning", got)
	}
	if sender.sent[1].Text != nativeProvider.replyText {
		t.Fatalf("outbound text = %q, want %q", sender.sent[1].Text, nativeProvider.replyText)
	}
}

func TestImageTurnUsesNativeProviderWhenGovernorBackendIsCodex(t *testing.T) {
	cfg, store, nativeProvider, sender := buildRuntimeFixtures(t)
	cfg.Governor.Backend = "codex"
	cfg.Governor.Codex.AuthSource = "codex_cli"
	cfg.Governor.Codex.CodexHome = t.TempDir()
	writeTestCodexAuth(t, filepath.Join(cfg.Governor.Codex.CodexHome, "auth.json"))
	cfg.Face.Backend = "floor_fallback"

	origFactory := newCodexProvider
	defer func() { newCodexProvider = origFactory }()
	codexProvider := &fakeProvider{replyText: "codex canonical"}
	newCodexProvider = func(_ governorauth.Bundle, _ *config.Config) (agent.Provider, error) {
		return codexProvider, nil
	}

	rt, err := New(cfg, store, nativeProvider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     408,
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  1,
		Artifacts: []core.Artifact{{
			ID:         "img-408",
			Channel:    "telegram",
			SourceType: "photo",
			Kind:       "image",
			Data:       []byte("fake-image"),
			MimeType:   "image/png",
			Filename:   "photo.png",
		}},
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	codexProvider.mu.Lock()
	codexCalls := codexProvider.callCount
	codexProvider.mu.Unlock()
	if codexCalls != 0 {
		t.Fatalf("codex call count = %d, want 0 for image turn", codexCalls)
	}

	nativeProvider.mu.Lock()
	defer nativeProvider.mu.Unlock()
	if nativeProvider.callCount == 0 {
		t.Fatal("native provider was not used for image turn")
	}
	if len(nativeProvider.lastGovernorMsgs) == 0 {
		t.Fatal("native provider saw no governor messages")
	}
	last := nativeProvider.lastGovernorMsgs[len(nativeProvider.lastGovernorMsgs)-1]
	if last.Role != "user" || len(last.Media) != 1 {
		t.Fatalf("last governor message = %#v, want user message with media", last)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 408, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(sess.Messages) == 0 || !strings.Contains(sess.Messages[0].Content, "[image attached]") {
		t.Fatalf("stored user content = %#v, want image placeholder", sess.Messages)
	}
}
