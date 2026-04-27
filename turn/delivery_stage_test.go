//go:build linux

package turn

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/idolum-ai/aphelion/core"
)

func TestRunDeliveryStageSkipsSendWhenDeliveryDisabled(t *testing.T) {
	t.Parallel()

	var order []string
	got, err := RunDeliveryStage(context.Background(), DeliveryStageInput{
		Request: DeliveryRequest{
			Message: core.OutboundMessage{Text: "reply"},
			Result: &Result{
				RenderedID:   42,
				RenderedType: "streaming",
			},
		},
		Deliver:        false,
		RecordOutbound: true,
	}, DeliveryStageCallbacks{
		Send: func(context.Context, core.OutboundMessage, bool) (int64, string, error) {
			order = append(order, "send")
			return 0, "", nil
		},
		RecordFinal: func(string, []core.Media, string) {
			order = append(order, "final")
		},
		RecordOutbound: func(context.Context, int64, string) error {
			order = append(order, "record")
			return nil
		},
		PostCommit: func(context.Context) error {
			order = append(order, "post")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunDeliveryStage() err = %v", err)
	}
	if got == nil {
		t.Fatal("RunDeliveryStage() = nil result, want delivery result")
	}
	if got.MessageID != 42 || got.Kind != "streaming" {
		t.Fatalf("delivery result = %#v, want id/type from rendered stream", got)
	}
	if want := []string{"final", "record", "post"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %#v, want %#v", order, want)
	}
}

func TestRunDeliveryStageSkipsRecordWhenNoRenderedIDAndNotSending(t *testing.T) {
	t.Parallel()

	var order []string
	_, err := RunDeliveryStage(context.Background(), DeliveryStageInput{
		Request: DeliveryRequest{
			Message: core.OutboundMessage{Text: "reply"},
			Result: &Result{
				RenderedID:   0,
				RenderedType: "streaming",
			},
		},
		Deliver:        false,
		RecordOutbound: true,
	}, DeliveryStageCallbacks{
		RecordFinal: func(string, []core.Media, string) {
			order = append(order, "final")
		},
		RecordOutbound: func(context.Context, int64, string) error {
			order = append(order, "record")
			return nil
		},
		PostCommit: func(context.Context) error {
			order = append(order, "post")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunDeliveryStage() err = %v", err)
	}
	if want := []string{"final", "post"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %#v, want %#v", order, want)
	}
}

func TestRunDeliveryStageSendPathOrdersSendThenRecordThenPost(t *testing.T) {
	t.Parallel()

	var order []string
	got, err := RunDeliveryStage(context.Background(), DeliveryStageInput{
		Request: DeliveryRequest{
			Message: core.OutboundMessage{Text: "reply"},
			Result:  &Result{},
		},
		Deliver:        true,
		RecordOutbound: true,
	}, DeliveryStageCallbacks{
		Send: func(context.Context, core.OutboundMessage, bool) (int64, string, error) {
			order = append(order, "send")
			return 77, "text", nil
		},
		RecordFinal: func(string, []core.Media, string) {
			order = append(order, "final")
		},
		RecordOutbound: func(context.Context, int64, string) error {
			order = append(order, "record")
			return nil
		},
		PostCommit: func(context.Context) error {
			order = append(order, "post")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunDeliveryStage() err = %v", err)
	}
	if got == nil {
		t.Fatal("RunDeliveryStage() = nil result, want delivery result")
	}
	if got.MessageID != 77 || got.Kind != "text" {
		t.Fatalf("delivery result = %#v, want send result", got)
	}
	if want := []string{"send", "final", "record", "post"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %#v, want %#v", order, want)
	}
}

func TestRunDeliveryStageSendsWhenStreamHandledWithoutMessageID(t *testing.T) {
	t.Parallel()

	var order []string
	got, err := RunDeliveryStage(context.Background(), DeliveryStageInput{
		Request: DeliveryRequest{
			Message: core.OutboundMessage{Text: "reply after stream fallback"},
			Result: &Result{
				RenderedStream: true,
				RenderedID:     0,
				RenderedType:   "",
			},
		},
		Deliver:        true,
		RecordOutbound: true,
	}, DeliveryStageCallbacks{
		Send: func(context.Context, core.OutboundMessage, bool) (int64, string, error) {
			order = append(order, "send")
			return 88, "text", nil
		},
		RecordFinal: func(string, []core.Media, string) {
			order = append(order, "final")
		},
		RecordOutbound: func(context.Context, int64, string) error {
			order = append(order, "record")
			return nil
		},
		PostCommit: func(context.Context) error {
			order = append(order, "post")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunDeliveryStage() err = %v", err)
	}
	if got == nil || got.MessageID != 88 || got.Kind != "text" {
		t.Fatalf("delivery result = %#v, want sent text id", got)
	}
	if want := []string{"send", "final", "record", "post"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %#v, want %#v", order, want)
	}
}

func TestRunDeliveryStageReturnsSendError(t *testing.T) {
	t.Parallel()

	var order []string
	wantErr := errors.New("send failed")
	_, err := RunDeliveryStage(context.Background(), DeliveryStageInput{
		Request: DeliveryRequest{
			Message: core.OutboundMessage{Text: "reply"},
			Result:  &Result{},
		},
		Deliver:        true,
		RecordOutbound: true,
	}, DeliveryStageCallbacks{
		Send: func(context.Context, core.OutboundMessage, bool) (int64, string, error) {
			order = append(order, "send")
			return 0, "", wantErr
		},
		RecordFinal: func(string, []core.Media, string) {
			order = append(order, "final")
		},
		RecordOutbound: func(context.Context, int64, string) error {
			order = append(order, "record")
			return nil
		},
		PostCommit: func(context.Context) error {
			order = append(order, "post")
			return nil
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunDeliveryStage() err = %v, want %v", err, wantErr)
	}
	if want := []string{"send"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %#v, want %#v", order, want)
	}
}

func TestRunDeliveryStageReturnsRecordErrorBeforePostCommit(t *testing.T) {
	t.Parallel()

	var order []string
	wantErr := errors.New("record failed")
	_, err := RunDeliveryStage(context.Background(), DeliveryStageInput{
		Request: DeliveryRequest{
			Message: core.OutboundMessage{Text: "reply"},
			Result:  &Result{},
		},
		Deliver:        true,
		RecordOutbound: true,
	}, DeliveryStageCallbacks{
		Send: func(context.Context, core.OutboundMessage, bool) (int64, string, error) {
			order = append(order, "send")
			return 77, "text", nil
		},
		RecordFinal: func(string, []core.Media, string) {
			order = append(order, "final")
		},
		RecordOutbound: func(context.Context, int64, string) error {
			order = append(order, "record")
			return wantErr
		},
		PostCommit: func(context.Context) error {
			order = append(order, "post")
			return nil
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunDeliveryStage() err = %v, want %v", err, wantErr)
	}
	if want := []string{"send", "final", "record"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %#v, want %#v", order, want)
	}
}

func TestRunDeliveryStageReturnsPostCommitError(t *testing.T) {
	t.Parallel()

	var order []string
	wantErr := errors.New("post failed")
	_, err := RunDeliveryStage(context.Background(), DeliveryStageInput{
		Request: DeliveryRequest{
			Message: core.OutboundMessage{Text: "reply"},
			Result: &Result{
				RenderedID:   88,
				RenderedType: "streaming",
			},
		},
		Deliver:        false,
		RecordOutbound: true,
	}, DeliveryStageCallbacks{
		RecordFinal: func(string, []core.Media, string) {
			order = append(order, "final")
		},
		RecordOutbound: func(context.Context, int64, string) error {
			order = append(order, "record")
			return nil
		},
		PostCommit: func(context.Context) error {
			order = append(order, "post")
			return wantErr
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunDeliveryStage() err = %v, want %v", err, wantErr)
	}
	if want := []string{"final", "record", "post"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %#v, want %#v", order, want)
	}
}

func TestRunDeliveryStageNilResultNoOps(t *testing.T) {
	t.Parallel()

	called := false
	got, err := RunDeliveryStage(context.Background(), DeliveryStageInput{
		Request: DeliveryRequest{
			Message: core.OutboundMessage{Text: "reply"},
			Result:  nil,
		},
		Deliver:        true,
		RecordOutbound: true,
	}, DeliveryStageCallbacks{
		Send: func(context.Context, core.OutboundMessage, bool) (int64, string, error) {
			called = true
			return 1, "text", nil
		},
	})
	if err != nil {
		t.Fatalf("RunDeliveryStage() err = %v", err)
	}
	if got != nil {
		t.Fatalf("RunDeliveryStage() = %#v, want nil", got)
	}
	if called {
		t.Fatal("callbacks called with nil result")
	}
}

func TestRunDeliveryStagePassesLiveContextToPostCommit(t *testing.T) {
	t.Parallel()

	key := struct{}{}
	ctx := context.WithValue(context.Background(), key, "live")
	var seen any
	_, err := RunDeliveryStage(ctx, DeliveryStageInput{
		Request: DeliveryRequest{
			Message: core.OutboundMessage{Text: "reply"},
			Result:  &Result{},
		},
		Deliver: true,
	}, DeliveryStageCallbacks{
		Send: func(context.Context, core.OutboundMessage, bool) (int64, string, error) {
			return 1, "text", nil
		},
		PostCommit: func(postCtx context.Context) error {
			seen = postCtx.Value(key)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunDeliveryStage() err = %v", err)
	}
	if seen != "live" {
		t.Fatalf("post commit context value = %#v, want live", seen)
	}
}
