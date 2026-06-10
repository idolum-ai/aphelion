//go:build linux

package runtime

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

func TestTurnDeliveryPortPostCommitHooksReceiveTurnResult(t *testing.T) {
	t.Parallel()

	var got []*turn.Result
	port := &turnDeliveryPort{
		runtime: &Runtime{},
		hooks: turnCommitHooks{
			QueueReviewEvents: func(result *turn.Result) error {
				got = append(got, result)
				return nil
			},
			DeliverReviewEvents: func(result *turn.Result) error {
				got = append(got, result)
				return nil
			},
			QueueDurableArtifact: func(result *turn.Result) error {
				got = append(got, result)
				return nil
			},
			PostReplyContinuationUI: func(_ context.Context, result *turn.Result) error {
				got = append(got, result)
				return nil
			},
		},
	}

	result := &turn.Result{VisibleReply: "rendered scene"}
	_, err := port.Deliver(context.Background(), turn.DeliveryRequest{
		Message: core.OutboundMessage{ChatID: 1, Text: "rendered scene"},
		Result:  result,
	})
	if err != nil {
		t.Fatalf("Deliver() err = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("hook calls = %d, want 4", len(got))
	}
	if !reflect.DeepEqual(got, []*turn.Result{result, result, result, result}) {
		t.Fatalf("hook results = %#v, want all hooks to receive same turn result", got)
	}
}

func TestPersistTurnPreservesConsumedContinuationState(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9716, UserID: 0, Scope: telegramDMScopeRef(9716)}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	now := time.Now().UTC()
	pending := session.ContinuationState{
		Kind:              session.TurnAuthorizationKindContinuation,
		Status:            session.ContinuationStatusPending,
		DecisionID:        "phase-stale-recovery",
		DecisionMessageID: 17492,
		Objective:         "Finish approved recovery work.",
		StageSummary:      "Run approved phase",
		RemainingTurns:    1,
		UpdatedAt:         now,
		ActionProposal: session.ActionProposal{
			ID:        "aprop-phase-stale-recovery",
			Summary:   "Run approved phase",
			Status:    session.ProposalStatusPending,
			UpdatedAt: now,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-phase-stale-recovery",
			ProposalID:     "aprop-phase-stale-recovery",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			UpdatedAt:      now,
		},
	}
	sess.ContinuationState = pending

	consumed := pending
	consumed.Status = session.ContinuationStatusIdle
	consumed.DecisionID = ""
	consumed.DecisionMessageID = 0
	consumed.RemainingTurns = 0
	consumed.ActionProposal.Status = session.ProposalStatusApproved
	consumed.ActionProposal.UpdatedAt = now.Add(time.Second)
	consumed.ContinuationLease.Status = session.ContinuationLeaseStatusConsumed
	consumed.ContinuationLease.RemainingTurns = 0
	consumed.ContinuationLease.ConsumedAt = now.Add(time.Second)
	consumed.ContinuationLease.UpdatedAt = now.Add(time.Second)
	consumed.UpdatedAt = now.Add(time.Second)
	if err := store.UpdateContinuationState(key, consumed); err != nil {
		t.Fatalf("UpdateContinuationState(consumed) err = %v", err)
	}

	if _, err := rt.persistTurn(context.Background(), turnCommitInput{
		Key:             key,
		Sess:            sess,
		Msg:             core.InboundMessage{ChatID: key.ChatID, SenderID: 1001, Text: "continue"},
		OutHistory:      []agent.Message{{Role: "assistant", Content: "done"}},
		HistoryInputLen: 0,
		Result:          &core.TurnResult{Text: "done"},
		ReplyText:       "done",
	}); err != nil {
		t.Fatalf("persistTurn() err = %v", err)
	}

	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle ||
		got.DecisionID != "" ||
		got.DecisionMessageID != 0 ||
		got.RemainingTurns != 0 ||
		got.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed ||
		got.ContinuationLease.RemainingTurns != 0 {
		t.Fatalf("continuation = %#v, want consumed state preserved over stale pending session snapshot", got)
	}
}

func TestEnforceVisibleRecurrenceContractAppendsSpecificNote(t *testing.T) {
	t.Parallel()

	got := enforceVisibleRecurrenceContract("Here is the plan.", prompt.RuntimeAwareness{
		HiddenInputsActive:    true,
		HiddenInputCategories: []string{hiddenInputSemanticRecurrence},
		ProvenanceSummary:     "Prior Lighthouse thread about Proton Bridge inbox testing.",
	})

	if !strings.Contains(got, "Here is the plan.") || !strings.Contains(got, "Continuity note: This resembles Prior Lighthouse thread") {
		t.Fatalf("reply = %q, want appended continuity note", got)
	}
}

func TestEnforceVisibleRecurrenceContractSuppressesInternalOnlyProvenance(t *testing.T) {
	t.Parallel()

	got := enforceVisibleRecurrenceContract("Here is the plan.", prompt.RuntimeAwareness{
		HiddenInputsActive:    true,
		HiddenInputCategories: []string{hiddenInputSemanticRecurrence},
		ProvenanceSummary:     "related prior material in memory/decisions.md is surfacing again; an open question overlaps with this turn",
	})

	if strings.Contains(got, "memory/decisions.md") || strings.Contains(got, "related prior material") {
		t.Fatalf("reply = %q, want sanitized recurrence note without raw provenance", got)
	}
	if got != "Here is the plan." {
		t.Fatalf("reply = %q, want unchanged reply without generic continuity caveat", got)
	}
}

func TestExtractPersonaContextRequest(t *testing.T) {
	t.Parallel()

	got, ok := extractPersonaContextRequest("PERSONA_CONTEXT_REQUEST: Lighthouse idea from yesterday")
	if !ok || got != "Lighthouse idea from yesterday" {
		t.Fatalf("extractPersonaContextRequest() = %q/%t, want request", got, ok)
	}
	if _, ok := extractPersonaContextRequest("Here is the answer.\nPERSONA_CONTEXT_REQUEST: hidden"); ok {
		t.Fatal("extractPersonaContextRequest() accepted mixed visible reply")
	}
}

func TestSuppressVisiblePersonaContextRequestLeak(t *testing.T) {
	t.Parallel()

	if got := suppressVisiblePersonaContextRequestLeak("PERSONA_CONTEXT_REQUEST: PR #197 review findings text/artifacts"); got != "" {
		t.Fatalf("suppressVisiblePersonaContextRequestLeak() = %q, want empty fallback trigger", got)
	}
	if got := suppressVisiblePersonaContextRequestLeak("Here is the answer."); got != "Here is the answer." {
		t.Fatalf("suppressVisiblePersonaContextRequestLeak() = %q, want normal reply", got)
	}
	if got := suppressVisiblePersonaContextRequestLeak("Here is the answer.\nPERSONA_CONTEXT_REQUEST: hidden"); strings.Contains(got, personaContextRequestPrefix) {
		t.Fatalf("suppressVisiblePersonaContextRequestLeak() leaked marker in %q", got)
	}
	quoted := "Here is a quoted marker:\n> PERSONA_CONTEXT_REQUEST: example"
	if got := suppressVisiblePersonaContextRequestLeak(quoted); got != quoted {
		t.Fatalf("suppressVisiblePersonaContextRequestLeak() = %q, want quoted marker preserved", got)
	}
	inline := "The literal PERSONA_CONTEXT_REQUEST: marker is part of the protocol."
	if got := suppressVisiblePersonaContextRequestLeak(inline); got != inline {
		t.Fatalf("suppressVisiblePersonaContextRequestLeak() = %q, want inline marker preserved", got)
	}
}

func TestRenderTurnReplyReconcilesStreamedPersonaContextLeak(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendProvider
	rt.streamEditInterval = 0
	renderer := &streamingCountingFaceRenderer{
		text:   "Here is the answer.\nPERSONA_CONTEXT_REQUEST: hidden context request",
		chunks: []string{"Here is the answer.", "\nPERSONA_CONTEXT_REQUEST: hidden context request"},
	}
	key := session.SessionKey{ChatID: 917, UserID: 0}

	result, err := rt.renderTurnReply(turnRenderInput{
		Ctx:              context.Background(),
		Key:              key,
		Msg:              core.InboundMessage{ChatID: 917, MessageID: 44},
		Result:           &core.TurnResult{Text: "Grounded fallback text."},
		FacePolicy:       pipeline.FacePolicy{Render: true},
		UseMaterialFloor: true,
		AllowStream:      true,
		ReplyText:        "Grounded fallback text.",
		FloorText:        "Grounded fallback text.",
		CurrentFaceModel: renderer,
		PromptInput:      "continue",
	})
	if err != nil {
		t.Fatalf("renderTurnReply() err = %v", err)
	}
	if !result.StreamedReply || result.OutboundID == 0 {
		t.Fatalf("stream metadata = streamed:%v id:%d, want reconciled existing stream", result.StreamedReply, result.OutboundID)
	}
	if strings.Contains(result.ReplyText, personaContextRequestPrefix) {
		t.Fatalf("ReplyText leaked marker: %q", result.ReplyText)
	}
	sender.mu.Lock()
	if len(sender.editClear) == 0 {
		sender.mu.Unlock()
		t.Fatal("editClear empty, want streamed message reconciliation")
	}
	edited := sender.editClear[len(sender.editClear)-1].Text
	sender.mu.Unlock()
	if strings.Contains(edited, personaContextRequestPrefix) || !strings.Contains(edited, "Here is the answer.") {
		t.Fatalf("edited text = %q, want sanitized streamed reply", edited)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !testExecutionEventsContain(events, core.ExecutionEventPersonaStreamReconciled) {
		t.Fatalf("events = %#v, want persona stream reconciliation event", events)
	}
}

func TestEnforceVisibleRecurrenceContractDoesNotDuplicatePriorThreadMention(t *testing.T) {
	t.Parallel()

	reply := "This matches the prior thread about Lighthouse."
	got := enforceVisibleRecurrenceContract(reply, prompt.RuntimeAwareness{
		HiddenInputsActive:    true,
		HiddenInputCategories: []string{hiddenInputUnresolvedMemory},
		ProvenanceSummary:     "Prior Lighthouse thread.",
	})

	if got != reply {
		t.Fatalf("reply = %q, want unchanged %q", got, reply)
	}
}
