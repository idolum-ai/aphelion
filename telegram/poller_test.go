//go:build linux

package telegram

import (
	"context"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestPollerDispatchesCallbackQueries(t *testing.T) {
	t.Parallel()

	client := NewClient("TOKEN")
	callbackSeen := make(chan CallbackQuery, 1)
	handlerCalled := make(chan struct{}, 1)

	poller := NewPoller(client, func(_ context.Context, _ core.InboundMessage) error {
		handlerCalled <- struct{}{}
		return nil
	}, WithCallbackHandler(func(_ context.Context, cb CallbackQuery) error {
		callbackSeen <- cb
		return nil
	}))

	upd := Update{
		CallbackQuery: &CallbackQuery{
			ID:   "cb-1",
			Data: "decision:1:approve",
			From: &User{ID: 7, Username: "alice"},
			Message: &Message{
				MessageID: 42,
				Chat:      &Chat{ID: 100, Type: "private"},
				Date:      time.Now().Unix(),
			},
		},
	}

	inbound, err := poller.normalizeUpdate(context.Background(), upd)
	if err != nil {
		t.Fatalf("normalizeUpdate() err = %v", err)
	}
	if inbound != nil {
		t.Fatalf("normalizeUpdate() = %#v, want nil for callback query", inbound)
	}

	if err := poller.dispatchCallback(context.Background(), *upd.CallbackQuery); err != nil {
		t.Fatalf("dispatchCallback() err = %v", err)
	}

	select {
	case cb := <-callbackSeen:
		if cb.ID != "cb-1" || cb.Data != "decision:1:approve" {
			t.Fatalf("callback = %+v, want cb-1 decision payload", cb)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("callback handler was not called")
	}

	select {
	case <-handlerCalled:
		t.Fatal("message handler should not run for callback query")
	default:
	}
}
