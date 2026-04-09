//go:build linux

package core

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRouteToSession(t *testing.T) {
	t.Parallel()

	called := make(chan int64, 1)
	router := NewRouter(func(_ context.Context, session *SessionState, _ InboundMessage) (*TurnResult, error) {
		called <- session.ChatID
		return &TurnResult{}, nil
	})

	router.Route(context.Background(), InboundMessage{ChatID: 42})

	select {
	case chatID := <-called:
		if chatID != 42 {
			t.Fatalf("expected ChatID 42, got %d", chatID)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("agent function was not called")
	}
}

func TestSessionMutex(t *testing.T) {
	t.Parallel()

	firstStarted := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})
	order := make(chan string, 4)

	router := NewRouter(func(_ context.Context, _ *SessionState, msg InboundMessage) (*TurnResult, error) {
		switch msg.Text {
		case "first":
			order <- "first-start"
			firstStarted <- struct{}{}
			<-releaseFirst
			order <- "first-end"
		case "second":
			order <- "second-start"
			order <- "second-end"
		default:
			t.Fatalf("unexpected message text: %s", msg.Text)
		}
		return &TurnResult{}, nil
	})

	doneFirst := make(chan struct{})
	go func() {
		defer close(doneFirst)
		router.Route(context.Background(), InboundMessage{ChatID: 1, Text: "first"})
	}()

	select {
	case <-firstStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("first message did not start")
	}

	doneSecond := make(chan struct{})
	go func() {
		defer close(doneSecond)
		router.Route(context.Background(), InboundMessage{ChatID: 1, Text: "second"})
	}()

	select {
	case got := <-order:
		if got != "first-start" {
			t.Fatalf("expected first-start first, got %s", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("missing first-start")
	}

	select {
	case got := <-order:
		t.Fatalf("expected no second execution while first is running, got %s", got)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseFirst)

	<-doneSecond
	<-doneFirst

	rest := []string{}
	for len(order) > 0 {
		rest = append(rest, <-order)
	}
	if len(rest) != 3 {
		t.Fatalf("expected 3 remaining order events, got %d: %v", len(rest), rest)
	}
	expected := []string{"first-end", "second-start", "second-end"}
	for i := range expected {
		if rest[i] != expected[i] {
			t.Fatalf("order[%d] = %s, want %s", i, rest[i], expected[i])
		}
	}
}

func TestConcurrentSessions(t *testing.T) {
	t.Parallel()

	router := NewRouter(func(_ context.Context, _ *SessionState, _ InboundMessage) (*TurnResult, error) {
		time.Sleep(150 * time.Millisecond)
		return &TurnResult{}, nil
	})

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		router.Route(context.Background(), InboundMessage{ChatID: 10})
	}()
	go func() {
		defer wg.Done()
		router.Route(context.Background(), InboundMessage{ChatID: 20})
	}()

	wg.Wait()
	elapsed := time.Since(start)
	if elapsed >= 280*time.Millisecond {
		t.Fatalf("expected concurrent execution under 280ms, got %s", elapsed)
	}
}

func TestQueueOverflow(t *testing.T) {
	t.Parallel()

	firstStarted := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})

	var mu sync.Mutex
	processed := make([]string, 0, 2)

	router := NewRouter(func(_ context.Context, _ *SessionState, msg InboundMessage) (*TurnResult, error) {
		mu.Lock()
		processed = append(processed, msg.Text)
		mu.Unlock()

		if msg.Text == "first" {
			firstStarted <- struct{}{}
			<-releaseFirst
		}
		return &TurnResult{}, nil
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		router.Route(context.Background(), InboundMessage{ChatID: 55, Text: "first"})
	}()

	select {
	case <-firstStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("first message did not start")
	}

	router.Route(context.Background(), InboundMessage{ChatID: 55, Text: "second"})
	router.Route(context.Background(), InboundMessage{ChatID: 55, Text: "third"})

	close(releaseFirst)
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(processed) != 2 {
		t.Fatalf("expected 2 processed messages, got %d: %v", len(processed), processed)
	}
	if processed[0] != "first" || processed[1] != "third" {
		t.Fatalf("expected [first third], got %v", processed)
	}
}

func TestSessionResolution(t *testing.T) {
	t.Parallel()

	seen := make(map[string]*SessionState)
	var mu sync.Mutex

	router := NewRouter(func(_ context.Context, session *SessionState, msg InboundMessage) (*TurnResult, error) {
		mu.Lock()
		seen[msg.Text] = session
		mu.Unlock()
		return &TurnResult{}, nil
	})

	router.Route(context.Background(), InboundMessage{ChatID: 99, Text: "a"})
	router.Route(context.Background(), InboundMessage{ChatID: 99, Text: "b"})
	router.Route(context.Background(), InboundMessage{ChatID: 100, Text: "c"})

	mu.Lock()
	defer mu.Unlock()

	a := seen["a"]
	b := seen["b"]
	c := seen["c"]
	if a == nil || b == nil || c == nil {
		t.Fatalf("missing captured sessions: a=%v b=%v c=%v", a, b, c)
	}
	if a != b {
		t.Fatal("expected same ChatID to resolve to same session pointer")
	}
	if a == c {
		t.Fatal("expected different ChatIDs to resolve to different session pointers")
	}
	if a.ChatID != 99 || c.ChatID != 100 {
		t.Fatalf("unexpected session ChatIDs: a=%d c=%d", a.ChatID, c.ChatID)
	}
}

func TestStopCancelsActiveTurnAndClearsQueue(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	var calls atomic.Int32

	router := NewRouter(func(ctx context.Context, _ *SessionState, msg InboundMessage) (*TurnResult, error) {
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		return nil, ctx.Err()
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		router.Route(context.Background(), InboundMessage{ChatID: 7, Text: "first"})
	}()

	select {
	case <-started:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("active turn did not start")
	}

	router.Route(context.Background(), InboundMessage{ChatID: 7, Text: "queued"})

	status := router.Status(7)
	if !status.Active || !status.Queued {
		t.Fatalf("status before stop = %+v, want active+queued", status)
	}

	stopped := router.Stop(7)
	if !stopped.ActiveCanceled || !stopped.QueuedDropped {
		t.Fatalf("stop result = %+v, want active canceled and queued dropped", stopped)
	}

	select {
	case <-canceled:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("active turn was not canceled")
	}
	<-done

	if got := calls.Load(); got != 1 {
		t.Fatalf("agent call count = %d, want 1", got)
	}

	status = router.Status(7)
	if status.Active || status.Queued {
		t.Fatalf("status after stop = %+v, want idle", status)
	}
}

func TestStopReturnsIdleWhenNothingRunning(t *testing.T) {
	t.Parallel()

	router := NewRouter(func(context.Context, *SessionState, InboundMessage) (*TurnResult, error) {
		return &TurnResult{}, nil
	})

	got := router.Stop(42)
	if got.ActiveCanceled || got.QueuedDropped {
		t.Fatalf("stop result = %+v, want no-op", got)
	}
}
