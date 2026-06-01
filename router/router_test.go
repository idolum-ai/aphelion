//go:build linux

package router

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestRouteToSession(t *testing.T) {
	t.Parallel()

	called := make(chan int64, 1)
	router := NewRouter(func(_ context.Context, session *core.SessionState, _ core.InboundMessage) (*core.TurnResult, error) {
		called <- session.ChatID
		return &core.TurnResult{}, nil
	})

	router.Route(context.Background(), core.InboundMessage{ChatID: 42})

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

	router := NewRouter(func(_ context.Context, _ *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
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
		return &core.TurnResult{}, nil
	})

	doneFirst := make(chan struct{})
	go func() {
		defer close(doneFirst)
		router.Route(context.Background(), core.InboundMessage{ChatID: 1, Text: "first"})
	}()

	select {
	case <-firstStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("first message did not start")
	}

	doneSecond := make(chan struct{})
	go func() {
		defer close(doneSecond)
		router.Route(context.Background(), core.InboundMessage{ChatID: 1, Text: "second"})
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

	router := NewRouter(func(_ context.Context, _ *core.SessionState, _ core.InboundMessage) (*core.TurnResult, error) {
		time.Sleep(150 * time.Millisecond)
		return &core.TurnResult{}, nil
	})

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		router.Route(context.Background(), core.InboundMessage{ChatID: 10})
	}()
	go func() {
		defer wg.Done()
		router.Route(context.Background(), core.InboundMessage{ChatID: 20})
	}()

	wg.Wait()
	elapsed := time.Since(start)
	if elapsed >= 280*time.Millisecond {
		t.Fatalf("expected concurrent execution under 280ms, got %s", elapsed)
	}
}

func TestConcurrentSessionsSameChatDifferentDurableAgents(t *testing.T) {
	t.Parallel()

	firstStarted := make(chan struct{}, 1)
	secondStarted := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})

	router := NewRouter(func(_ context.Context, _ *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
		switch msg.DurableAgentID {
		case "agent-a":
			firstStarted <- struct{}{}
			<-releaseFirst
		case "agent-b":
			secondStarted <- struct{}{}
		default:
			t.Fatalf("unexpected durable agent id: %q", msg.DurableAgentID)
		}
		return &core.TurnResult{}, nil
	})

	doneFirst := make(chan struct{})
	go func() {
		defer close(doneFirst)
		router.Route(context.Background(), core.InboundMessage{
			ChatID:         99,
			ChatType:       "private",
			DurableAgentID: "agent-a",
			Text:           "first",
		})
	}()

	select {
	case <-firstStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("first durable agent turn did not start")
	}

	doneSecond := make(chan struct{})
	go func() {
		defer close(doneSecond)
		router.Route(context.Background(), core.InboundMessage{
			ChatID:         99,
			ChatType:       "private",
			DurableAgentID: "agent-b",
			Text:           "second",
		})
	}()

	select {
	case <-secondStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("second durable agent turn did not start while first durable session was active")
	}

	close(releaseFirst)
	<-doneFirst
	<-doneSecond
}

func TestQueueCompaction(t *testing.T) {
	t.Parallel()

	firstStarted := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})

	var mu sync.Mutex
	processed := make([]string, 0, 2)

	router := NewRouter(func(_ context.Context, _ *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
		mu.Lock()
		processed = append(processed, msg.Text)
		mu.Unlock()

		if msg.Text == "first" {
			firstStarted <- struct{}{}
			<-releaseFirst
		}
		return &core.TurnResult{}, nil
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		router.Route(context.Background(), core.InboundMessage{ChatID: 55, Text: "first"})
	}()

	select {
	case <-firstStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("first message did not start")
	}

	router.Route(context.Background(), core.InboundMessage{ChatID: 55, Text: "second"})
	router.Route(context.Background(), core.InboundMessage{ChatID: 55, Text: "third"})

	close(releaseFirst)
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(processed) != 2 {
		t.Fatalf("expected 2 processed messages, got %d: %v", len(processed), processed)
	}
	if processed[0] != "first" {
		t.Fatalf("first processed = %q, want first", processed[0])
	}
	if got := processed[1]; !strings.Contains(got, "Merged 2 queued follow-up messages") {
		t.Fatalf("compacted message = %q, want merge header", got)
	}
	if got := processed[1]; !strings.Contains(got, "1. second") || !strings.Contains(got, "2. third") {
		t.Fatalf("compacted message = %q, want queued texts in order", got)
	}
}

func TestQueueCompactionKeepsLatestArtifactsOnly(t *testing.T) {
	t.Parallel()

	firstStarted := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})

	var (
		mu                sync.Mutex
		secondTurnText    string
		secondTurnMessage core.InboundMessage
	)

	router := NewRouter(func(_ context.Context, _ *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
		if msg.Text == "first" {
			firstStarted <- struct{}{}
			<-releaseFirst
			return &core.TurnResult{}, nil
		}
		mu.Lock()
		secondTurnText = msg.Text
		secondTurnMessage = msg
		mu.Unlock()
		return &core.TurnResult{}, nil
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		router.Route(context.Background(), core.InboundMessage{ChatID: 56, Text: "first"})
	}()

	select {
	case <-firstStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("first message did not start")
	}

	router.Route(context.Background(), core.InboundMessage{
		ChatID:    56,
		Text:      "older queued",
		Artifacts: []core.Artifact{{ID: "old-artifact", Filename: "old.txt"}},
	})
	router.Route(context.Background(), core.InboundMessage{
		ChatID:    56,
		Text:      "newest queued",
		Artifacts: []core.Artifact{{ID: "new-artifact", Filename: "new.txt"}},
	})

	close(releaseFirst)
	<-done

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(secondTurnText, "1. older queued") || !strings.Contains(secondTurnText, "2. newest queued") {
		t.Fatalf("second turn text = %q, want compacted queue lines", secondTurnText)
	}
	if len(secondTurnMessage.Artifacts) != 1 || secondTurnMessage.Artifacts[0].ID != "new-artifact" {
		t.Fatalf("second turn artifacts = %#v, want only latest queued artifacts", secondTurnMessage.Artifacts)
	}
}

func TestRouteDoesNotDequeueCompactedWorkAfterParentCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	firstStarted := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})
	var calls atomic.Int32

	router := NewRouter(func(_ context.Context, _ *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
		calls.Add(1)
		if msg.Text == "first" {
			firstStarted <- struct{}{}
			<-releaseFirst
			cancel()
		}
		return &core.TurnResult{}, nil
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		router.Route(ctx, core.InboundMessage{ChatID: 57, Text: "first"})
	}()

	select {
	case <-firstStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("first message did not start")
	}

	router.Route(context.Background(), core.InboundMessage{ChatID: 57, Text: "queued"})
	close(releaseFirst)
	<-done

	if got := calls.Load(); got != 1 {
		t.Fatalf("agent calls = %d, want only first turn before canceled parent returns", got)
	}
	status := router.Status(57)
	if status.Active || !status.Queued || status.QueueDepth != 1 {
		t.Fatalf("status after canceled parent = %+v, want queued message preserved", status)
	}
}

func TestSessionResolution(t *testing.T) {
	t.Parallel()

	seen := make(map[string]*core.SessionState)
	var mu sync.Mutex

	router := NewRouter(func(_ context.Context, session *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
		mu.Lock()
		seen[msg.Text] = session
		mu.Unlock()
		return &core.TurnResult{}, nil
	})

	router.Route(context.Background(), core.InboundMessage{ChatID: 99, Text: "a"})
	router.Route(context.Background(), core.InboundMessage{ChatID: 99, Text: "b"})
	router.Route(context.Background(), core.InboundMessage{ChatID: 100, Text: "c"})

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

	router := NewRouter(func(ctx context.Context, _ *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		return nil, ctx.Err()
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		router.Route(context.Background(), core.InboundMessage{ChatID: 7, Text: "first"})
	}()

	select {
	case <-started:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("active turn did not start")
	}

	router.Route(context.Background(), core.InboundMessage{ChatID: 7, Text: "queued"})

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

func TestSnapshotReportsActiveTurnIDsAndQueueDepth(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 1)
	release := make(chan struct{})

	router := NewRouter(func(ctx context.Context, _ *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
		if msg.Text == "first" {
			started <- struct{}{}
			<-release
		}
		return &core.TurnResult{}, nil
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		router.Route(context.Background(), core.InboundMessage{ChatID: 31, Text: "first"})
	}()

	select {
	case <-started:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("active turn did not start")
	}

	router.Route(context.Background(), core.InboundMessage{ChatID: 31, Text: "second"})
	router.Route(context.Background(), core.InboundMessage{ChatID: 31, Text: "third"})

	snapshot := router.Snapshot()
	if got := snapshot.QueueDepthByChat[31]; got != 2 {
		t.Fatalf("queue depth = %d, want 2", got)
	}
	if got := len(snapshot.ActiveTurnsByChat[31]); got != 1 {
		t.Fatalf("active turns = %d, want 1", got)
	}

	close(release)
	<-done

	final := router.Snapshot()
	if _, ok := final.QueueDepthByChat[31]; ok {
		t.Fatalf("final queue depth map = %#v, want cleared chat", final.QueueDepthByChat)
	}
	if _, ok := final.ActiveTurnsByChat[31]; ok {
		t.Fatalf("final active turns map = %#v, want cleared chat", final.ActiveTurnsByChat)
	}
}

func TestStopReturnsIdleWhenNothingRunning(t *testing.T) {
	t.Parallel()

	router := NewRouter(func(context.Context, *core.SessionState, core.InboundMessage) (*core.TurnResult, error) {
		return &core.TurnResult{}, nil
	})

	got := router.Stop(42)
	if got.ActiveCanceled || got.QueuedDropped {
		t.Fatalf("stop result = %+v, want no-op", got)
	}
}

func TestRouterIngressSequenceAndEvents(t *testing.T) {
	t.Parallel()

	firstStarted := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})

	var (
		processedMu sync.Mutex
		processed   []core.InboundMessage
		eventsMu    sync.Mutex
		events      []core.RouterEvent
	)

	router := NewRouter(func(_ context.Context, _ *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
		processedMu.Lock()
		processed = append(processed, msg)
		processedMu.Unlock()
		if msg.Text == "first" {
			firstStarted <- struct{}{}
			<-releaseFirst
		}
		return &core.TurnResult{}, nil
	})
	router.SetEventHandler(func(_ context.Context, event core.RouterEvent) {
		eventsMu.Lock()
		events = append(events, event)
		eventsMu.Unlock()
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		router.Route(context.Background(), core.InboundMessage{ChatID: 9001, Text: "first", MessageID: 11})
	}()

	select {
	case <-firstStarted:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("first message did not start")
	}

	router.Route(context.Background(), core.InboundMessage{ChatID: 9001, Text: "second", MessageID: 12})
	router.Route(context.Background(), core.InboundMessage{ChatID: 9001, Text: "third", MessageID: 13})
	close(releaseFirst)
	<-done

	processedMu.Lock()
	if len(processed) != 2 {
		t.Fatalf("processed len = %d, want 2", len(processed))
	}
	first := processed[0]
	second := processed[1]
	processedMu.Unlock()

	if first.IngressSeq != 1 {
		t.Fatalf("first ingress seq = %d, want 1", first.IngressSeq)
	}
	if second.IngressSeq != 3 {
		t.Fatalf("compacted ingress seq = %d, want latest seq 3", second.IngressSeq)
	}

	eventsMu.Lock()
	copied := append([]core.RouterEvent(nil), events...)
	eventsMu.Unlock()

	type typed struct {
		Type       string
		Seq        int64
		QueueDepth int
		Drained    int
	}
	got := make([]typed, 0, len(copied))
	for _, event := range copied {
		if event.ChatID != 9001 {
			continue
		}
		got = append(got, typed{
			Type:       event.EventType,
			Seq:        event.IngressSeq,
			QueueDepth: event.QueueDepth,
			Drained:    event.DrainedCount,
		})
	}

	if len(got) == 0 {
		t.Fatal("router events are empty")
	}

	expected := []typed{
		{Type: core.ExecutionEventIngressAccepted, Seq: 1},
		{Type: core.ExecutionEventIngressSelected, Seq: 1},
		{Type: core.ExecutionEventIngressAccepted, Seq: 2},
		{Type: core.ExecutionEventIngressQueued, Seq: 2, QueueDepth: 1},
		{Type: core.ExecutionEventIngressAccepted, Seq: 3},
		{Type: core.ExecutionEventIngressQueued, Seq: 3, QueueDepth: 2},
		{Type: core.ExecutionEventIngressCompacted, Seq: 3, Drained: 2},
		{Type: core.ExecutionEventIngressSelected, Seq: 3},
	}
	if len(got) != len(expected) {
		t.Fatalf("event len = %d, want %d\n got=%#v", len(got), len(expected), got)
	}
	for i := range expected {
		if got[i].Type != expected[i].Type || got[i].Seq != expected[i].Seq || got[i].QueueDepth != expected[i].QueueDepth || got[i].Drained != expected[i].Drained {
			t.Fatalf("event[%d] = %#v, want %#v", i, got[i], expected[i])
		}
	}
	for _, event := range copied {
		if event.ChatID != 9001 || event.EventType != core.ExecutionEventIngressSelected || event.IngressSeq != 3 {
			continue
		}
		if !event.IngressQueueWaitKnown {
			t.Fatalf("compacted selected event = %#v, want ingress queue wait", event)
		}
		if event.IngressQueueWait < 0 {
			t.Fatalf("compacted selected queue wait = %s, want non-negative", event.IngressQueueWait)
		}
		return
	}
	t.Fatal("missing compacted ingress selected event")
}

func TestRouterSnapshotIncludesPassiveQueueHealth(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	done := make(chan struct{})
	router := NewRouter(func(_ context.Context, _ *core.SessionState, _ core.InboundMessage) (*core.TurnResult, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return &core.TurnResult{}, nil
	})

	go func() {
		defer close(done)
		router.Route(context.Background(), core.InboundMessage{ChatID: 88, Text: "first"})
	}()
	<-started

	oldest := time.Now().UTC().Add(-2 * time.Minute)
	router.Route(context.Background(), core.InboundMessage{ChatID: 88, Text: "second", IngressQueuedAt: oldest})
	router.Route(context.Background(), core.InboundMessage{ChatID: 88, Text: "third", IngressQueuedAt: oldest.Add(time.Minute)})

	snapshot := router.Snapshot()
	if snapshot.TotalActiveTurns != 1 {
		t.Fatalf("TotalActiveTurns = %d, want 1", snapshot.TotalActiveTurns)
	}
	if snapshot.TotalQueuedMessages != 2 || snapshot.MaxQueueDepth != 2 || snapshot.MaxQueueDepthChatID != 88 {
		t.Fatalf("queue health = total %d max %d chat %d, want total 2 max 2 chat 88", snapshot.TotalQueuedMessages, snapshot.MaxQueueDepth, snapshot.MaxQueueDepthChatID)
	}
	if snapshot.OldestQueuedChatID != 88 || snapshot.OldestQueuedAge <= 0 || !snapshot.OldestQueuedAt.Equal(oldest) {
		t.Fatalf("oldest queue = chat %d age %s at %s, want chat 88 positive age at %s", snapshot.OldestQueuedChatID, snapshot.OldestQueuedAge, snapshot.OldestQueuedAt, oldest)
	}

	close(release)
	<-done
}
