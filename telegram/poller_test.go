//go:build linux

package telegram

import (
	"context"
	"errors"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
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

func TestPollerContinuesAfterCallbackHandlerError(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	updates := []Update{
		{
			UpdateID: 10,
			CallbackQuery: &CallbackQuery{
				ID:   "cb-fails",
				Data: "continuation:decision:approve",
				From: &User{ID: 7, Username: "alice"},
				Message: &Message{
					MessageID: 42,
					Chat:      &Chat{ID: 100, Type: "private"},
					Date:      now,
				},
			},
		},
		{
			UpdateID: 11,
			Message: &Message{
				MessageID: 43,
				Chat:      &Chat{ID: 100, Type: "private"},
				From:      &User{ID: 7, Username: "alice"},
				Text:      "after callback",
				Date:      now + 1,
			},
		},
	}
	call := 0
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/getUpdates" {
				t.Fatalf("unexpected path %s", req.URL.Path)
			}
			call++
			resp := getUpdatesResponse{Ok: true}
			if call == 1 {
				resp.Result = updates
			}
			return encodeJSONResponse(t, resp), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	callbackCalls := 0
	handled := make([]core.InboundMessage, 0, 1)
	checkpoint := &testPollerCheckpoint{}
	poller := NewPoller(client, func(_ context.Context, msg core.InboundMessage) error {
		handled = append(handled, msg)
		cancel()
		return nil
	}, WithCallbackHandler(func(_ context.Context, cb CallbackQuery) error {
		callbackCalls++
		if cb.ID != "cb-fails" {
			t.Fatalf("callback ID = %q, want cb-fails", cb.ID)
		}
		return errors.New("callback handler failed")
	}), WithPollerTimeout(1), WithCheckpoint(checkpoint))
	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Poller.Run() err = %v, want nil after contained callback error", err)
	}
	if callbackCalls != 1 {
		t.Fatalf("callbackCalls = %d, want 1", callbackCalls)
	}
	if len(handled) != 1 || handled[0].Text != "after callback" {
		t.Fatalf("handled = %#v, want message after failed callback", handled)
	}
	if len(checkpoint.failures) != 1 || checkpoint.failures[0].UpdateID != 10 || checkpoint.failures[0].UpdateKind != "callback_query" {
		t.Fatalf("failures = %#v, want callback failure for update 10", checkpoint.failures)
	}
	if len(checkpoint.ops) < 2 || checkpoint.ops[0] != "failure" || checkpoint.ops[1] != "save" {
		t.Fatalf("checkpoint ops = %#v, want callback failure ledgered before offset save", checkpoint.ops)
	}
}

func TestPollerRetriesTransientGetUpdatesTransportError(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	call := 0
	var delays []time.Duration
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/getUpdates" {
				t.Fatalf("unexpected path %s", req.URL.Path)
			}
			call++
			if call == 1 {
				return nil, &url.Error{Op: "Post", URL: req.URL.String(), Err: syscall.ECONNRESET}
			}
			return encodeJSONResponse(t, getUpdatesResponse{
				Ok: true,
				Result: []Update{{
					UpdateID: 20,
					Message: &Message{
						MessageID: 44,
						Chat:      &Chat{ID: 100, Type: "private"},
						From:      &User{ID: 7, Username: "alice"},
						Text:      "after retry",
						Date:      now,
					},
				}},
			}), nil
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var handled []core.InboundMessage
	poller := NewPoller(client, func(_ context.Context, msg core.InboundMessage) error {
		handled = append(handled, msg)
		cancel()
		return nil
	},
		WithPollerTimeout(1),
		withPollerRetryBackoff(time.Second, 30*time.Second),
		withPollerRetrySleep(func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		}),
	)

	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Poller.Run() err = %v, want nil after retry", err)
	}
	if call != 2 {
		t.Fatalf("getUpdates calls = %d, want 2", call)
	}
	if len(delays) != 1 || delays[0] != time.Second {
		t.Fatalf("retry delays = %#v, want [1s]", delays)
	}
	if len(handled) != 1 || handled[0].Text != "after retry" {
		t.Fatalf("handled = %#v, want successful update after retry", handled)
	}
}

func TestPollerRetriesGetUpdatesServerError(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	call := 0
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			call++
			if call == 1 {
				return encodeHTTPJSONResponse(t, http.StatusInternalServerError, getUpdatesResponse{
					Ok:          false,
					Description: "Internal Server Error",
				}), nil
			}
			return encodeJSONResponse(t, getUpdatesResponse{
				Ok: true,
				Result: []Update{{
					UpdateID: 21,
					Message: &Message{
						MessageID: 45,
						Chat:      &Chat{ID: 100, Type: "private"},
						From:      &User{ID: 7, Username: "alice"},
						Text:      "after server retry",
						Date:      now,
					},
				}},
			}), nil
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var handled []core.InboundMessage
	poller := NewPoller(client, func(_ context.Context, msg core.InboundMessage) error {
		handled = append(handled, msg)
		cancel()
		return nil
	}, withPollerRetryBackoff(time.Second, 30*time.Second), withPollerRetrySleep(func(context.Context, time.Duration) error {
		return nil
	}))

	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Poller.Run() err = %v, want nil after retry", err)
	}
	if call != 2 {
		t.Fatalf("getUpdates calls = %d, want 2", call)
	}
	if len(handled) != 1 || handled[0].Text != "after server retry" {
		t.Fatalf("handled = %#v, want successful update after server retry", handled)
	}
}

func TestPollerHonorsGetUpdatesRetryAfter(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	call := 0
	var delays []time.Duration
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			call++
			if call == 1 {
				return encodeHTTPJSONResponse(t, http.StatusTooManyRequests, getUpdatesResponse{
					Ok:          false,
					Description: "Too Many Requests: retry after 7",
					Parameters:  telegramResponseParameters{RetryAfter: 7},
				}), nil
			}
			return encodeJSONResponse(t, getUpdatesResponse{
				Ok: true,
				Result: []Update{{
					UpdateID: 22,
					Message: &Message{
						MessageID: 46,
						Chat:      &Chat{ID: 100, Type: "private"},
						From:      &User{ID: 7, Username: "alice"},
						Text:      "after retry-after",
						Date:      now,
					},
				}},
			}), nil
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var handled []core.InboundMessage
	poller := NewPoller(client, func(_ context.Context, msg core.InboundMessage) error {
		handled = append(handled, msg)
		cancel()
		return nil
	},
		withPollerRetryBackoff(time.Second, 30*time.Second),
		withPollerRetrySleep(func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		}),
	)

	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Poller.Run() err = %v, want nil after retry-after", err)
	}
	if call != 2 {
		t.Fatalf("getUpdates calls = %d, want 2", call)
	}
	if len(delays) != 1 || delays[0] != 7*time.Second {
		t.Fatalf("retry delays = %#v, want [7s]", delays)
	}
	if len(handled) != 1 || handled[0].Text != "after retry-after" {
		t.Fatalf("handled = %#v, want successful update after retry-after", handled)
	}
}

func TestPollerCapsGetUpdatesRetryAfterAtMaxBackoff(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	call := 0
	var delays []time.Duration
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			call++
			if call == 1 {
				return encodeHTTPJSONResponse(t, http.StatusTooManyRequests, getUpdatesResponse{
					Ok:          false,
					Description: "Too Many Requests: retry after 60",
					Parameters:  telegramResponseParameters{RetryAfter: 60},
				}), nil
			}
			return encodeJSONResponse(t, getUpdatesResponse{
				Ok: true,
				Result: []Update{{
					UpdateID: 23,
					Message: &Message{
						MessageID: 47,
						Chat:      &Chat{ID: 100, Type: "private"},
						From:      &User{ID: 7, Username: "alice"},
						Text:      "after capped retry-after",
						Date:      now,
					},
				}},
			}), nil
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var handled []core.InboundMessage
	poller := NewPoller(client, func(_ context.Context, msg core.InboundMessage) error {
		handled = append(handled, msg)
		cancel()
		return nil
	},
		withPollerRetryBackoff(time.Second, 3*time.Second),
		withPollerRetrySleep(func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		}),
	)

	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Poller.Run() err = %v, want nil after capped retry-after", err)
	}
	if call != 2 {
		t.Fatalf("getUpdates calls = %d, want 2", call)
	}
	if len(delays) != 1 || delays[0] != 3*time.Second {
		t.Fatalf("retry delays = %#v, want [3s]", delays)
	}
	if len(handled) != 1 || handled[0].Text != "after capped retry-after" {
		t.Fatalf("handled = %#v, want successful update after capped retry-after", handled)
	}
}

func TestPollerRetryBackoffIsBoundedAndResetsAfterSuccess(t *testing.T) {
	t.Parallel()

	call := 0
	var delays []time.Duration
	var cancel context.CancelFunc
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			call++
			if call == 3 {
				return encodeJSONResponse(t, getUpdatesResponse{Ok: true}), nil
			}
			return nil, &url.Error{Op: "Post", URL: req.URL.String(), Err: syscall.ECONNRESET}
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	ctx, cancelFn := context.WithCancel(context.Background())
	cancel = cancelFn
	defer cancel()

	poller := NewPoller(client, func(_ context.Context, _ core.InboundMessage) error {
		t.Fatal("handler should not run for empty polls")
		return nil
	},
		WithPollerTimeout(1),
		withPollerRetryBackoff(time.Second, 2*time.Second),
		withPollerRetrySleep(func(ctx context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			if len(delays) == 4 {
				cancel()
				return ctx.Err()
			}
			return nil
		}),
	)

	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Poller.Run() err = %v, want nil after cancellation", err)
	}
	want := []time.Duration{time.Second, 2 * time.Second, time.Second, 2 * time.Second}
	if len(delays) != len(want) {
		t.Fatalf("retry delays = %#v, want %#v", delays, want)
	}
	for i := range want {
		if delays[i] != want[i] {
			t.Fatalf("retry delays = %#v, want %#v", delays, want)
		}
	}
}

func TestPollerCancelDuringTransientGetUpdatesBackoffStopsCleanly(t *testing.T) {
	t.Parallel()

	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			return nil, &url.Error{Op: "Post", URL: req.URL.String(), Err: syscall.ECONNRESET}
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	poller := NewPoller(client, func(_ context.Context, _ core.InboundMessage) error {
		t.Fatal("handler should not run when polling never succeeds")
		return nil
	}, withPollerRetrySleep(func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}))

	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Poller.Run() err = %v, want nil for cancellation during backoff", err)
	}
}

func TestPollerDoesNotRetryNonRetryableGetUpdatesAPIError(t *testing.T) {
	t.Parallel()

	call := 0
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			call++
			return encodeHTTPJSONResponse(t, http.StatusUnauthorized, getUpdatesResponse{
				Ok:          false,
				Description: "Unauthorized",
			}), nil
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	poller := NewPoller(client, func(_ context.Context, _ core.InboundMessage) error {
		t.Fatal("handler should not run for unauthorized polling")
		return nil
	}, withPollerRetrySleep(func(context.Context, time.Duration) error {
		t.Fatal("sleep should not run for non-retryable polling errors")
		return nil
	}))

	err := poller.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("Poller.Run() err = %v, want Unauthorized", err)
	}
	if call != 1 {
		t.Fatalf("getUpdates calls = %d, want 1", call)
	}
}

func TestPollerRetryAfterDoesNotOverrideNonRetryableMarker(t *testing.T) {
	t.Parallel()

	err := telegramGetUpdatesError{
		description: "Unauthorized",
		retryAfter:  7 * time.Second,
	}
	if retryableTelegramPollError(err) {
		t.Fatal("retryableTelegramPollError() = true for Unauthorized with retry_after, want false")
	}
}

func TestPollerRecordsTerminalCallbackSuccessBeforeOffset(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			resp := getUpdatesResponse{Ok: true}
			resp.Result = []Update{{
				UpdateID: 50,
				CallbackQuery: &CallbackQuery{
					ID:   "cb-ok",
					Data: "progress:details",
					From: &User{ID: 7, Username: "alice"},
					Message: &Message{
						MessageID: 42,
						Chat:      &Chat{ID: 100, Type: "private"},
						Date:      now,
					},
				},
			}}
			return encodeJSONResponse(t, resp), nil
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	checkpoint := &testPollerCheckpoint{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	poller := NewPoller(client, func(_ context.Context, _ core.InboundMessage) error {
		t.Fatal("message handler should not run for callback query")
		return nil
	}, WithCallbackHandler(func(_ context.Context, cb CallbackQuery) error {
		if cb.ID != "cb-ok" {
			t.Fatalf("callback id = %q, want cb-ok", cb.ID)
		}
		if cb.UpdateID != 50 {
			t.Fatalf("callback update id = %d, want 50", cb.UpdateID)
		}
		cancel()
		return nil
	}), WithPollerTimeout(1), WithCheckpoint(checkpoint))
	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Poller.Run() err = %v", err)
	}
	if len(checkpoint.terminals) != 1 {
		t.Fatalf("terminals = %#v, want one callback terminal", checkpoint.terminals)
	}
	terminal := checkpoint.terminals[0]
	if terminal.UpdateID != 50 || terminal.UpdateKind != "callback_query" || terminal.Status != PollerTerminalCompleted || terminal.Reason != "callback_handled" || terminal.ChatID != 100 || terminal.MessageID != 42 {
		t.Fatalf("terminal = %#v, want completed callback refs", terminal)
	}
	if len(checkpoint.saved) != 1 || checkpoint.saved[0] != 51 {
		t.Fatalf("saved offsets = %#v, want [51]", checkpoint.saved)
	}
	if len(checkpoint.ops) != 2 || checkpoint.ops[0] != "terminal" || checkpoint.ops[1] != "save" {
		t.Fatalf("checkpoint ops = %#v, want terminal before save", checkpoint.ops)
	}
}

func TestPollerUsesCheckpointOffsetAndRecordsPoisonMessage(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	var requestedOffsets []int64
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/getUpdates" {
				t.Fatalf("unexpected path %s", req.URL.Path)
			}
			var payload struct {
				Offset int64 `json:"offset"`
			}
			decodeJSONRequest(t, req, &payload)
			requestedOffsets = append(requestedOffsets, payload.Offset)
			resp := getUpdatesResponse{Ok: true}
			if len(requestedOffsets) == 1 {
				resp.Result = []Update{
					{
						UpdateID: 42,
						Message: &Message{
							MessageID: 100,
							Chat:      &Chat{ID: 7001, Type: "private"},
							From:      &User{ID: 9, Username: "alice"},
							Text:      "poison",
							Date:      now,
						},
					},
					{
						UpdateID: 43,
						Message: &Message{
							MessageID: 101,
							Chat:      &Chat{ID: 7001, Type: "private"},
							From:      &User{ID: 9, Username: "alice"},
							Text:      "after",
							Date:      now + 1,
						},
					},
				}
			}
			return encodeJSONResponse(t, resp), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	checkpoint := &testPollerCheckpoint{next: 42}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var handled []string
	poller := NewPoller(client, func(_ context.Context, msg core.InboundMessage) error {
		handled = append(handled, msg.Text)
		if msg.Text == "poison" {
			return errors.New("handler failed")
		}
		cancel()
		return nil
	}, WithPollerTimeout(1), WithCheckpoint(checkpoint))
	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Poller.Run() err = %v", err)
	}
	if len(requestedOffsets) != 1 || requestedOffsets[0] != 42 {
		t.Fatalf("requestedOffsets = %#v, want [42]", requestedOffsets)
	}
	if len(handled) != 2 || handled[0] != "poison" || handled[1] != "after" {
		t.Fatalf("handled = %#v, want poison then after", handled)
	}
	if len(checkpoint.failures) != 1 {
		t.Fatalf("failures = %#v, want one poison failure", checkpoint.failures)
	}
	failure := checkpoint.failures[0]
	if failure.UpdateID != 42 || failure.UpdateKind != "message" || failure.ChatID != 7001 || failure.MessageID != 100 {
		t.Fatalf("failure = %#v, want update/message refs", failure)
	}
	if got := checkpoint.saved[len(checkpoint.saved)-1]; got != 44 {
		t.Fatalf("last saved offset = %d, want 44", got)
	}
}

func TestPollerRecordsAcceptedBeforeHandlerAndOffset(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			resp := getUpdatesResponse{Ok: true}
			resp.Result = []Update{{
				UpdateID: 77,
				Message: &Message{
					MessageID: 200,
					Chat:      &Chat{ID: 7001, Type: "private"},
					From:      &User{ID: 9, Username: "alice"},
					Text:      "durable please",
					Date:      now,
				},
			}}
			return encodeJSONResponse(t, resp), nil
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	checkpoint := &testPollerCheckpoint{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	poller := NewPoller(client, func(_ context.Context, msg core.InboundMessage) error {
		if msg.IngressSurface != "telegram:primary" || msg.IngressUpdateID != 77 {
			t.Fatalf("ingress identity = %q/%d, want telegram:primary/77", msg.IngressSurface, msg.IngressUpdateID)
		}
		if len(checkpoint.accepted) != 1 {
			t.Fatalf("accepted count during handler = %d, want 1", len(checkpoint.accepted))
		}
		if len(checkpoint.saved) != 0 {
			t.Fatalf("saved offsets during handler = %#v, want none before handler returns", checkpoint.saved)
		}
		cancel()
		return nil
	}, WithPollerTimeout(1), WithCheckpoint(checkpoint), WithIngressSurface("telegram:primary"))
	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Poller.Run() err = %v", err)
	}
	if len(checkpoint.accepted) != 1 || checkpoint.accepted[0].UpdateID != 77 || checkpoint.accepted[0].SessionID == "" {
		t.Fatalf("accepted = %#v, want update 77 with session id", checkpoint.accepted)
	}
	if len(checkpoint.handled) != 1 || checkpoint.handled[0] != 77 {
		t.Fatalf("handled = %#v, want [77]", checkpoint.handled)
	}
	if len(checkpoint.saved) != 1 || checkpoint.saved[0] != 78 {
		t.Fatalf("saved offsets = %#v, want [78]", checkpoint.saved)
	}
}

func TestPollerDoesNotHandleOrAdvanceOffsetWhenAcceptedRecordFails(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			resp := getUpdatesResponse{Ok: true}
			resp.Result = []Update{{
				UpdateID: 88,
				Message: &Message{
					MessageID: 201,
					Chat:      &Chat{ID: 7002, Type: "private"},
					From:      &User{ID: 10, Username: "bob"},
					Text:      "must not enter memory only",
					Date:      now,
				},
			}}
			return encodeJSONResponse(t, resp), nil
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	checkpoint := &testPollerCheckpoint{acceptErr: errors.New("sqlite unavailable")}
	var handled bool
	poller := NewPoller(client, func(_ context.Context, _ core.InboundMessage) error {
		handled = true
		return nil
	}, WithPollerTimeout(1), WithCheckpoint(checkpoint), WithIngressSurface("telegram:primary"))

	err := poller.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "sqlite unavailable") {
		t.Fatalf("Poller.Run() err = %v, want accept failure", err)
	}
	if handled {
		t.Fatal("handler ran after accepted record failure")
	}
	if len(checkpoint.saved) != 0 {
		t.Fatalf("saved offsets = %#v, want none", checkpoint.saved)
	}
}

func TestPollerRetryDoesNotSwallowAcceptedRecordFailure(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	call := 0
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			call++
			if call == 1 {
				return nil, &url.Error{Op: "Post", URL: req.URL.String(), Err: syscall.ECONNRESET}
			}
			return encodeJSONResponse(t, getUpdatesResponse{
				Ok: true,
				Result: []Update{{
					UpdateID: 89,
					Message: &Message{
						MessageID: 202,
						Chat:      &Chat{ID: 7002, Type: "private"},
						From:      &User{ID: 10, Username: "bob"},
						Text:      "must not enter memory only",
						Date:      now,
					},
				}},
			}), nil
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	checkpoint := &testPollerCheckpoint{acceptErr: errors.New("sqlite unavailable")}
	poller := NewPoller(client, func(_ context.Context, _ core.InboundMessage) error {
		t.Fatal("handler should not run after accepted record failure")
		return nil
	}, WithCheckpoint(checkpoint), withPollerRetrySleep(func(context.Context, time.Duration) error {
		return nil
	}))

	err := poller.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "sqlite unavailable") {
		t.Fatalf("Poller.Run() err = %v, want accepted record failure", err)
	}
	if call != 2 {
		t.Fatalf("getUpdates calls = %d, want transient retry then successful poll", call)
	}
	if len(checkpoint.saved) != 0 {
		t.Fatalf("saved offsets = %#v, want none", checkpoint.saved)
	}
}

func TestPollerSkipsTerminalRedeliveryAndAdvancesOffset(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	var cancel context.CancelFunc
	call := 0
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			call++
			resp := getUpdatesResponse{Ok: true}
			if call == 1 {
				resp.Result = []Update{{
					UpdateID: 101,
					Message: &Message{
						MessageID: 301,
						Chat:      &Chat{ID: 7003, Type: "private"},
						From:      &User{ID: 10, Username: "bob"},
						Text:      "already terminal",
						Date:      now,
					},
				}}
			} else if cancel != nil {
				cancel()
			}
			return encodeJSONResponse(t, resp), nil
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	checkpoint := &testPollerCheckpoint{states: map[int64]PollerUpdateState{
		101: {Found: true, Terminal: true, Status: PollerTerminalCompleted},
	}}
	ctx, cancelFn := context.WithCancel(context.Background())
	cancel = cancelFn
	defer cancel()

	var handled bool
	poller := NewPoller(client, func(_ context.Context, _ core.InboundMessage) error {
		handled = true
		return nil
	}, WithPollerTimeout(1), WithCheckpoint(checkpoint))
	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Poller.Run() err = %v", err)
	}
	if handled {
		t.Fatal("handler ran for terminal redelivery")
	}
	if len(checkpoint.accepted) != 0 || len(checkpoint.handled) != 0 || len(checkpoint.terminals) != 0 {
		t.Fatalf("checkpoint accepted=%#v handled=%#v terminals=%#v, want no dispatch records", checkpoint.accepted, checkpoint.handled, checkpoint.terminals)
	}
	if len(checkpoint.saved) != 1 || checkpoint.saved[0] != 102 {
		t.Fatalf("saved offsets = %#v, want [102]", checkpoint.saved)
	}
	if len(checkpoint.ops) != 2 || checkpoint.ops[0] != "state" || checkpoint.ops[1] != "save" {
		t.Fatalf("checkpoint ops = %#v, want state then save", checkpoint.ops)
	}
}

func TestPollerDoesNotDispatchWhenAcceptedLedgerIsNotDispatchable(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	var cancel context.CancelFunc
	call := 0
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			call++
			resp := getUpdatesResponse{Ok: true}
			if call == 1 {
				resp.Result = []Update{{
					UpdateID: 102,
					Message: &Message{
						MessageID: 302,
						Chat:      &Chat{ID: 7004, Type: "private"},
						From:      &User{ID: 10, Username: "bob"},
						Text:      "already running",
						Date:      now,
					},
				}}
			} else if cancel != nil {
				cancel()
			}
			return encodeJSONResponse(t, resp), nil
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	checkpoint := &testPollerCheckpoint{acceptResult: PollerAcceptResult{Dispatch: false, Status: "running"}}
	ctx, cancelFn := context.WithCancel(context.Background())
	cancel = cancelFn
	defer cancel()

	var handled bool
	poller := NewPoller(client, func(_ context.Context, _ core.InboundMessage) error {
		handled = true
		return nil
	}, WithPollerTimeout(1), WithCheckpoint(checkpoint))
	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Poller.Run() err = %v", err)
	}
	if handled {
		t.Fatal("handler ran for non-dispatchable accepted ledger")
	}
	if len(checkpoint.accepted) != 1 || checkpoint.accepted[0].UpdateID != 102 {
		t.Fatalf("accepted = %#v, want update 102 recorded/idempotency checked", checkpoint.accepted)
	}
	if len(checkpoint.handled) != 0 {
		t.Fatalf("handled = %#v, want none", checkpoint.handled)
	}
	if len(checkpoint.saved) != 1 || checkpoint.saved[0] != 103 {
		t.Fatalf("saved offsets = %#v, want [103]", checkpoint.saved)
	}
}

func TestPollerRecordsSkippedUnresolvedMessageBeforeOffset(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	var cancel context.CancelFunc
	call := 0
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			call++
			resp := getUpdatesResponse{Ok: true}
			if call == 1 {
				resp.Result = []Update{{
					UpdateID: 90,
					Message: &Message{
						MessageID: 300,
						Chat:      &Chat{ID: 7003, Type: "private"},
						From:      &User{ID: 999, Username: "unknown"},
						Text:      "unauthorized",
						Date:      now,
					},
				}}
			} else if cancel != nil {
				cancel()
			}
			return encodeJSONResponse(t, resp), nil
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	checkpoint := &testPollerCheckpoint{}
	ctx, cancelFn := context.WithCancel(context.Background())
	cancel = cancelFn
	defer cancel()

	var handled bool
	poller := NewPoller(client, func(_ context.Context, _ core.InboundMessage) error {
		handled = true
		return nil
	}, WithPollerTimeout(1), WithCheckpoint(checkpoint), WithPrincipalResolver(principal.NewResolver([]int64{1001}, nil)))
	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Poller.Run() err = %v", err)
	}
	if handled {
		t.Fatal("handler ran for unresolved message principal")
	}
	if len(checkpoint.terminals) != 1 {
		t.Fatalf("terminals = %#v, want one skipped terminal", checkpoint.terminals)
	}
	terminal := checkpoint.terminals[0]
	if terminal.UpdateID != 90 || terminal.Status != PollerTerminalSkipped || terminal.Reason != "unresolved_message_principal" || terminal.ChatID != 7003 || terminal.SenderID != 999 || terminal.MessageID != 300 {
		t.Fatalf("terminal = %#v, want skipped unresolved message refs", terminal)
	}
	if len(checkpoint.saved) != 1 || checkpoint.saved[0] != 91 {
		t.Fatalf("saved offsets = %#v, want [91]", checkpoint.saved)
	}
	if len(checkpoint.ops) != 2 || checkpoint.ops[0] != "terminal" || checkpoint.ops[1] != "save" {
		t.Fatalf("checkpoint ops = %#v, want skipped terminal before save", checkpoint.ops)
	}
}

func TestPollerRecordsSkippedIgnoredMessageBeforeOffset(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	var cancel context.CancelFunc
	call := 0
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			call++
			resp := getUpdatesResponse{Ok: true}
			if call == 1 {
				resp.Result = []Update{{
					UpdateID: 91,
					Message: &Message{
						MessageID: 301,
						Chat:      &Chat{ID: -1007004, Type: "group"},
						From:      &User{ID: 7, Username: "alice"},
						Text:      "ordinary group noise",
						Date:      now,
					},
				}}
			} else if cancel != nil {
				cancel()
			}
			return encodeJSONResponse(t, resp), nil
		},
	}
	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	checkpoint := &testPollerCheckpoint{}
	ctx, cancelFn := context.WithCancel(context.Background())
	cancel = cancelFn
	defer cancel()

	var handled bool
	poller := NewPoller(client, func(_ context.Context, _ core.InboundMessage) error {
		handled = true
		return nil
	}, WithPollerTimeout(1), WithCheckpoint(checkpoint))
	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Poller.Run() err = %v", err)
	}
	if handled {
		t.Fatal("handler ran for ignored message")
	}
	if len(checkpoint.terminals) != 1 {
		t.Fatalf("terminals = %#v, want one ignored-message terminal", checkpoint.terminals)
	}
	terminal := checkpoint.terminals[0]
	if terminal.UpdateID != 91 || terminal.Status != PollerTerminalSkipped || terminal.Reason != "ignored_message" || terminal.ChatID != -1007004 || terminal.SenderID != 7 || terminal.MessageID != 301 {
		t.Fatalf("terminal = %#v, want skipped ignored message refs", terminal)
	}
	if len(checkpoint.saved) != 1 || checkpoint.saved[0] != 92 {
		t.Fatalf("saved offsets = %#v, want [92]", checkpoint.saved)
	}
	if len(checkpoint.ops) != 2 || checkpoint.ops[0] != "terminal" || checkpoint.ops[1] != "save" {
		t.Fatalf("checkpoint ops = %#v, want skipped terminal before save", checkpoint.ops)
	}
}

func TestPollerDispatchesMessageReactions(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	updates := []Update{
		{
			UpdateID: 10,
			MessageReaction: &MessageReactionUpdated{
				Chat:      &Chat{ID: 7, Type: "private"},
				User:      &User{ID: 3, Username: "alice"},
				MessageID: 42,
				Date:      now,
				NewReaction: []ReactionType{
					{Type: "emoji", Emoji: "👍"},
				},
			},
		},
	}
	call := 0
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/botTOKEN/getUpdates" {
				t.Fatalf("unexpected path %s", req.URL.Path)
			}
			call++
			resp := getUpdatesResponse{Ok: true}
			if call == 1 {
				resp.Result = updates
			}
			return encodeJSONResponse(t, resp), nil
		},
	}

	client := NewClient("TOKEN",
		WithBaseURL("https://api.telegram.org/botTOKEN/"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handled := make([]core.InboundMessage, 0, 1)
	poller := NewPoller(client, func(_ context.Context, msg core.InboundMessage) error {
		handled = append(handled, msg)
		cancel()
		return nil
	}, WithPollerTimeout(1))
	if err := poller.Run(ctx); err != nil {
		t.Fatalf("poller failed: %v", err)
	}

	if len(handled) != 1 {
		t.Fatalf("handled len = %d, want 1", len(handled))
	}
	if handled[0].Reaction == nil || handled[0].Reaction.MessageID != 42 || len(handled[0].Reaction.New) != 1 || handled[0].Reaction.New[0] != "👍" {
		t.Fatalf("handled reaction = %#v, want message 42 thumbs up", handled[0].Reaction)
	}
}

func TestPollerAllowUnresolvedPrivateMessagePredicate(t *testing.T) {
	t.Parallel()

	poller := NewPoller(
		NewClient("TOKEN"),
		func(_ context.Context, _ core.InboundMessage) error { return nil },
		WithUnresolvedPrivatePredicate(func(msg *Message) bool {
			return strings.HasPrefix(strings.TrimSpace(msg.Text), "agent:")
		}),
	)

	if !poller.allowUnresolvedPrivateMessage(&Message{
		Chat: &Chat{Type: "private"},
		Text: "agent:family-group hello",
	}) {
		t.Fatal("allowUnresolvedPrivateMessage() = false, want true for durable relay prefix")
	}
	if poller.allowUnresolvedPrivateMessage(&Message{
		Chat: &Chat{Type: "private"},
		Text: "hello",
	}) {
		t.Fatal("allowUnresolvedPrivateMessage() = true, want false for ordinary private message")
	}
	if poller.allowUnresolvedPrivateMessage(&Message{
		Chat: &Chat{Type: "group"},
		Text: "agent:family-group hello",
	}) {
		t.Fatal("allowUnresolvedPrivateMessage() = true, want false for non-private chat")
	}
}
