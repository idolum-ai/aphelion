//go:build linux

package runtime

import (
	"context"
	"sync"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

type recordingInteractiveDMTurnAssembler struct {
	mu        sync.Mutex
	called    bool
	callCount int
	input     interactiveDMTurnAssemblyInput
	inputs    []interactiveDMTurnAssemblyInput
	result    *core.TurnResult
	err       error
}

func (r *recordingInteractiveDMTurnAssembler) Run(_ context.Context, input interactiveDMTurnAssemblyInput) (*core.TurnResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.called = true
	r.callCount++
	r.input = input
	r.inputs = append(r.inputs, input)
	return r.result, r.err
}

func (r *recordingInteractiveDMTurnAssembler) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.callCount
}

func TestHandleInboundUsesInteractiveDMTurnAssemblerBoundary(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	expected := &core.TurnResult{Text: "stubbed assembly result"}
	recorder := &recordingInteractiveDMTurnAssembler{result: expected}
	rt.interactiveDMAssembler = recorder

	msg := core.InboundMessage{
		ChatID:     7501,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hello from boundary test",
		MessageID:  7,
	}
	got, err := rt.HandleInbound(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if !recorder.called {
		t.Fatal("interactive DM assembler not called")
	}
	if got != expected {
		t.Fatalf("HandleInbound() result = %#v, want %#v", got, expected)
	}
	if recorder.input.Msg.ChatID != msg.ChatID || recorder.input.Msg.Text != msg.Text {
		t.Fatalf("assembler msg = %#v, want chat/text copied from inbound", recorder.input.Msg)
	}
	if recorder.input.Actor.Role != principal.RoleAdmin {
		t.Fatalf("assembler actor role = %q, want %q", recorder.input.Actor.Role, principal.RoleAdmin)
	}
	if recorder.input.Key.ChatID != msg.ChatID || recorder.input.Key.UserID != 0 {
		t.Fatalf("assembler key = %#v, want chat-scoped shared DM session key", recorder.input.Key)
	}
	if recorder.input.Key.Scope.Kind != session.ScopeKindTelegramDM {
		t.Fatalf("assembler key scope kind = %q, want %q", recorder.input.Key.Scope.Kind, session.ScopeKindTelegramDM)
	}
	if recorder.input.Scope.WorkingRoot == "" || recorder.input.Scope.SharedMemoryRoot == "" {
		t.Fatalf("assembler scope = %#v, want resolved roots", recorder.input.Scope)
	}
	if recorder.input.EventAwareness.Origin != inboundOriginLabel(msg) {
		t.Fatalf("assembler event origin = %q, want %q", recorder.input.EventAwareness.Origin, inboundOriginLabel(msg))
	}

	_, exists, err := store.ContinuationStateIfExists(session.SessionKey{
		ChatID: msg.ChatID,
		UserID: 0,
		Scope:  telegramDMScopeRef(msg.ChatID),
	})
	if err != nil {
		t.Fatalf("ContinuationStateIfExists() err = %v", err)
	}
	if exists {
		t.Fatal("ContinuationStateIfExists() exists = true, want false when assembler short-circuits turn construction")
	}
}

func TestHandleInboundFallsBackToDefaultInteractiveAssembler(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	rt.interactiveDMAssembler = nil

	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     7502,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "default assembler should run",
		MessageID:  8,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if result == nil {
		t.Fatal("HandleInbound() result = nil, want turn result")
	}

	key := session.SessionKey{ChatID: 7502, UserID: 0, Scope: telegramDMScopeRef(7502)}
	run, err := store.LatestTurnRun(key)
	if err != nil {
		t.Fatalf("LatestTurnRun() err = %v", err)
	}
	if run == nil {
		t.Fatal("LatestTurnRun() = nil, want persisted turn run from default assembler path")
	}
	if run.Kind != session.TurnRunKindInteractive {
		t.Fatalf("turn run kind = %q, want %q", run.Kind, session.TurnRunKindInteractive)
	}
}

func TestHandleInboundCarriesTurnAuthorizationEventAwarenessIntoAssembler(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{}}
	rt.interactiveDMAssembler = recorder

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:       7503,
		SenderID:     1001,
		SenderName:   "admin",
		Text:         "continue",
		MessageID:    9,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if !recorder.called {
		t.Fatal("interactive DM assembler not called")
	}
	if recorder.input.EventAwareness.Origin != string(core.InboundOriginTurnAuthorization) {
		t.Fatalf("event origin = %q, want %q", recorder.input.EventAwareness.Origin, core.InboundOriginTurnAuthorization)
	}
	if recorder.input.EventAwareness.TurnAuthorizationKind != string(session.TurnAuthorizationKindContinuation) {
		t.Fatalf("turn authorization kind = %q, want continuation", recorder.input.EventAwareness.TurnAuthorizationKind)
	}
}
