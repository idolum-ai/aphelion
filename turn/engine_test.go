//go:build linux

package turn

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

type fakeGovernor struct {
	order *[]string
	last  GovernorRequest
	resp  *GovernorResult
}

func (f *fakeGovernor) Execute(_ context.Context, req GovernorRequest) (*GovernorResult, error) {
	if f.order != nil {
		*f.order = append(*f.order, "governor.execute")
	}
	f.last = req
	if f.resp != nil {
		return f.resp, nil
	}
	return &GovernorResult{Turn: &core.TurnResult{Text: "governor text"}, FloorText: "floor text"}, nil
}

type fakeFace struct {
	order        *[]string
	proposalResp *FaceProposalResult
	renderResp   *FaceRenderResult
	lastProposal FaceProposalRequest
	lastRender   FaceRenderRequest
}

func (f *fakeFace) Propose(_ context.Context, req FaceProposalRequest) (*FaceProposalResult, error) {
	if f.order != nil {
		*f.order = append(*f.order, "face.propose")
	}
	f.lastProposal = req
	if f.proposalResp != nil {
		return f.proposalResp, nil
	}
	return &FaceProposalResult{Note: "look closer first"}, nil
}

func (f *fakeFace) Render(_ context.Context, req FaceRenderRequest) (*FaceRenderResult, error) {
	if f.order != nil {
		*f.order = append(*f.order, "face.render")
	}
	f.lastRender = req
	if f.renderResp != nil {
		return f.renderResp, nil
	}
	return &FaceRenderResult{Text: "visible scene"}, nil
}

type fakePersistence struct {
	order      *[]string
	persistErr error
}

func (f *fakePersistence) Persist(_ context.Context, req CommitRequest) (*CommitResult, error) {
	if f.order != nil {
		*f.order = append(*f.order, "persist")
	}
	if req.Plan.Mode == "" {
		panic("expected commit plan")
	}
	if f.persistErr != nil {
		return nil, f.persistErr
	}
	return &CommitResult{Persisted: true}, nil
}

type fakeDelivery struct {
	order      *[]string
	deliverErr error
}

func (f *fakeDelivery) Deliver(_ context.Context, req DeliveryRequest) (*DeliveryResult, error) {
	if f.order != nil {
		*f.order = append(*f.order, "deliver")
	}
	if req.Result == nil {
		return nil, nil
	}
	if req.Result.RenderedStream {
		return nil, nil
	}
	if f.deliverErr != nil {
		return nil, f.deliverErr
	}
	return &DeliveryResult{MessageID: 99, Kind: "text"}, nil
}

func seedRequest() Request {
	return Request{
		RunKind:    session.TurnRunKindInteractive,
		SessionKey: session.SessionKey{ChatID: 42},
		Inbound: core.InboundMessage{
			ChatID: 42,
			Text:   "help me think about the architecture",
		},
		Session: &session.Session{ChatID: 42},
	}
}

func TestMachineHandleOrdersGoldenPath(t *testing.T) {
	var order []string
	m := &Machine{
		Governor:    &fakeGovernor{order: &order, resp: &GovernorResult{Turn: &core.TurnResult{Text: "governor raw"}, FloorText: "structured floor"}},
		Face:        &fakeFace{order: &order, proposalResp: &FaceProposalResult{Note: "inspect before answering"}, renderResp: &FaceRenderResult{Text: "final visible reply"}},
		Persistence: &fakePersistence{order: &order},
		Delivery:    &fakeDelivery{order: &order},
	}

	result, err := m.Handle(context.Background(), seedRequest())
	if err != nil {
		t.Fatalf("Handle() err = %v", err)
	}
	if got, want := order, []string{"face.propose", "governor.execute", "face.render", "persist", "deliver"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %#v, want %#v", got, want)
	}
	if result.VisibleReply != "final visible reply" {
		t.Fatalf("VisibleReply = %q, want rendered scene", result.VisibleReply)
	}
	if !result.Commit.Persisted {
		t.Fatal("Commit.Persisted = false, want true")
	}
	if result.Delivery.MessageID != 99 {
		t.Fatalf("Delivery.MessageID = %d, want 99", result.Delivery.MessageID)
	}
}

func TestMachineHandleRejectsNilGovernorTurn(t *testing.T) {
	var order []string
	m := &Machine{
		Governor: &fakeGovernor{
			order: &order,
			resp:  &GovernorResult{Turn: nil, FloorText: "floor text"},
		},
		Face:        &fakeFace{order: &order, renderResp: &FaceRenderResult{Text: "visible scene", Usage: core.TokenUsage{OutputTokens: 5}}},
		Persistence: &fakePersistence{order: &order},
		Delivery:    &fakeDelivery{order: &order},
	}

	result, err := m.Handle(context.Background(), seedRequest())
	if err == nil {
		t.Fatal("Handle() err = nil, want nil governor turn rejection")
	}
	if !strings.Contains(err.Error(), "governor result turn is required") {
		t.Fatalf("Handle() err = %v, want governor result turn rejection", err)
	}
	if result != nil {
		t.Fatalf("result = %#v, want nil when governor result is structurally invalid", result)
	}
	if got, want := order, []string{"face.propose", "governor.execute"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %#v, want %#v", got, want)
	}
}

func TestMachineHandleFallsBackToFloorWhenFaceAbsent(t *testing.T) {
	m := &Machine{
		Governor: &fakeGovernor{resp: &GovernorResult{Turn: &core.TurnResult{Text: "governor raw"}, FloorText: "floor fallback text"}},
	}
	result, err := m.Handle(context.Background(), seedRequest())
	if err != nil {
		t.Fatalf("Handle() err = %v", err)
	}
	if result.VisibleReply != "floor fallback text" {
		t.Fatalf("VisibleReply = %q, want floor fallback text", result.VisibleReply)
	}
}

func TestMachineHandleSkipsDeliveryForStreamedReply(t *testing.T) {
	var order []string
	m := &Machine{
		Governor: &fakeGovernor{order: &order, resp: &GovernorResult{Turn: &core.TurnResult{Text: "governor raw"}, FloorText: "floor text"}},
		Face: &fakeFace{
			order: &order,
			renderResp: &FaceRenderResult{
				Text:         "streamed idolum reply",
				Streamed:     true,
				RenderedID:   21,
				RenderedType: "streaming",
			},
		},
		Persistence: &fakePersistence{order: &order},
		Delivery:    &fakeDelivery{order: &order},
	}
	result, err := m.Handle(context.Background(), seedRequest())
	if err != nil {
		t.Fatalf("Handle() err = %v", err)
	}
	if got, want := order, []string{"face.propose", "governor.execute", "face.render", "persist", "deliver"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %#v, want %#v", got, want)
	}
	if !result.Commit.Persisted {
		t.Fatal("result.Commit.Persisted = false, want true")
	}
	if result.Delivery.MessageID != 0 {
		t.Fatalf("Delivery.MessageID = %d, want 0 for streamed reply", result.Delivery.MessageID)
	}
}

func TestMachineHandlePersistsBeforeDeliveryWhenDeliveryFails(t *testing.T) {
	var order []string
	m := &Machine{
		Governor:    &fakeGovernor{order: &order, resp: &GovernorResult{Turn: &core.TurnResult{Text: "governor raw"}, FloorText: "structured floor"}},
		Face:        &fakeFace{order: &order, renderResp: &FaceRenderResult{Text: "final visible reply"}},
		Persistence: &fakePersistence{order: &order},
		Delivery:    &fakeDelivery{order: &order, deliverErr: errors.New("delivery failed")},
	}
	result, err := m.Handle(context.Background(), seedRequest())
	if err == nil {
		t.Fatalf("Handle() err = %v", err)
	}
	if result == nil {
		t.Fatal("result == nil, want persisted turn result")
	}
	if !result.Commit.Persisted {
		t.Fatalf("result.Commit.Persisted = %v, want true", result.Commit.Persisted)
	}
	if got, want := order, []string{"face.propose", "governor.execute", "face.render", "persist", "deliver"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %#v, want %#v", got, want)
	}
	if !strings.Contains(err.Error(), "delivery failed") {
		t.Fatalf("Handle() err = %v, want delivery failed", err)
	}
}

func TestMachineHandleSkipsDeliveryWhenPersistenceFails(t *testing.T) {
	var order []string
	m := &Machine{
		Governor:    &fakeGovernor{order: &order, resp: &GovernorResult{Turn: &core.TurnResult{Text: "governor raw"}, FloorText: "structured floor"}},
		Face:        &fakeFace{order: &order, renderResp: &FaceRenderResult{Text: "final visible reply"}},
		Persistence: &fakePersistence{order: &order, persistErr: errors.New("persist failed")},
		Delivery:    &fakeDelivery{order: &order, deliverErr: errors.New("should not be called")},
	}
	result, err := m.Handle(context.Background(), seedRequest())
	if err == nil {
		t.Fatalf("Handle() err = %v", err)
	}
	if result == nil {
		t.Fatal("result == nil, want partially returned result")
	}
	if result.Commit.Persisted {
		t.Fatalf("result.Commit.Persisted = %v, want false", result.Commit.Persisted)
	}
	if got, want := order, []string{"face.propose", "governor.execute", "face.render", "persist"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %#v, want %#v", got, want)
	}
	if !strings.Contains(err.Error(), "persist failed") {
		t.Fatalf("Handle() err = %v, want persist failed", err)
	}
}

func TestMachineHandlePassesFaceProposalIntoGovernor(t *testing.T) {
	gov := &fakeGovernor{resp: &GovernorResult{Turn: &core.TurnResult{Text: "governor raw"}, FloorText: "floor text"}}
	face := &fakeFace{proposalResp: &FaceProposalResult{Note: "ask one sharper question"}}
	m := &Machine{Governor: gov, Face: face}
	_, err := m.Handle(context.Background(), seedRequest())
	if err != nil {
		t.Fatalf("Handle() err = %v", err)
	}
	if gov.last.FaceNote != "ask one sharper question" {
		t.Fatalf("governor face note = %q, want propagated proposal", gov.last.FaceNote)
	}
	if gov.last.Policy.Reason == "" {
		t.Fatal("governor policy reason empty, want policy propagation")
	}
}

func TestMachineHandleUsesPreparedUserTextForFaceStages(t *testing.T) {
	face := &fakeFace{proposalResp: &FaceProposalResult{Note: "look closer first"}, renderResp: &FaceRenderResult{Text: "visible scene"}}
	gov := &fakeGovernor{resp: &GovernorResult{Turn: &core.TurnResult{Text: "governor raw"}, FloorText: "floor text"}}
	m := &Machine{Governor: gov, Face: face}

	_, err := m.Handle(context.Background(), Request{
		RunKind:    session.TurnRunKindInteractive,
		SessionKey: session.SessionKey{ChatID: 42},
		Inbound: core.InboundMessage{
			ChatID: 42,
			Text:   "raw inbound text",
		},
		PreparedUserText: "prepared ledger text",
		Session:          &session.Session{ChatID: 42},
	})
	if err != nil {
		t.Fatalf("Handle() err = %v", err)
	}
	if face.lastProposal.LatestUserInput != "prepared ledger text" {
		t.Fatalf("proposal LatestUserInput = %q, want prepared ledger text", face.lastProposal.LatestUserInput)
	}
	if face.lastRender.LatestUserInput != "prepared ledger text" {
		t.Fatalf("render LatestUserInput = %q, want prepared ledger text", face.lastRender.LatestUserInput)
	}
}

func TestMachineHandleMaintenanceSpeciesSkipsFaceStagesByDefaultPolicy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		runKind session.TurnRunKind
		reason  string
	}{
		{name: "heartbeat", runKind: session.TurnRunKindHeartbeat, reason: "heartbeat_default"},
		{name: "cron", runKind: session.TurnRunKindCron, reason: "cron_default"},
		{name: "recovery", runKind: session.TurnRunKindRecovery, reason: "recovery_default"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var order []string
			m := &Machine{
				Governor:    &fakeGovernor{order: &order, resp: &GovernorResult{Turn: &core.TurnResult{Text: "governor raw"}, FloorText: "structured floor"}},
				Face:        &fakeFace{order: &order, proposalResp: &FaceProposalResult{Note: "unexpected proposal"}, renderResp: &FaceRenderResult{Text: "unexpected scene"}},
				Persistence: &fakePersistence{order: &order},
				Delivery:    &fakeDelivery{order: &order},
			}

			result, err := m.Handle(context.Background(), Request{
				RunKind:    tc.runKind,
				SessionKey: session.SessionKey{ChatID: 77},
				Inbound: core.InboundMessage{
					ChatID: 77,
					Text:   "maintenance input",
				},
				Session: &session.Session{ChatID: 77},
			})
			if err != nil {
				t.Fatalf("Handle() err = %v", err)
			}
			if got, want := order, []string{"governor.execute", "persist", "deliver"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("order = %#v, want %#v", got, want)
			}
			if result.Policy.Reason != tc.reason {
				t.Fatalf("result.Policy.Reason = %q, want %q", result.Policy.Reason, tc.reason)
			}
			if result.ProposalNote != "" {
				t.Fatalf("result.ProposalNote = %q, want empty", result.ProposalNote)
			}
			if !result.Commit.Persisted {
				t.Fatal("result.Commit.Persisted = false, want true")
			}
		})
	}
}

func TestMachineHandleRejectsMismatchedChatIdentity(t *testing.T) {
	m := &Machine{Governor: &fakeGovernor{}}
	_, err := m.Handle(context.Background(), Request{
		RunKind:    session.TurnRunKindInteractive,
		SessionKey: session.SessionKey{ChatID: 7},
		Inbound:    core.InboundMessage{ChatID: 8, Text: "hi"},
		Session:    &session.Session{ChatID: 8},
	})
	if err == nil {
		t.Fatal("Handle() err = nil, want validation failure")
	}
}
