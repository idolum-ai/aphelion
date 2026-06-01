//go:build linux

package telegramcontrol

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/router"
)

func TestIngressSequencerStopDropsQueuedGenerationAndAllowsNewWork(t *testing.T) {
	started := make(chan string, 4)
	release := make(chan struct{})
	var mu sync.Mutex
	handled := []string{}
	var dropped []core.InboundMessage
	router := router.NewRouter(func(ctx context.Context, _ *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
		started <- msg.Text
		if msg.Text == "first" {
			<-ctx.Done()
			<-release
		}
		mu.Lock()
		handled = append(handled, msg.Text)
		mu.Unlock()
		return nil, nil
	})
	ingress := NewIngressSequencer(router, time.Minute)
	defer ingress.Close()
	ingress.SetDropHandler(func(messages []core.InboundMessage) {
		mu.Lock()
		defer mu.Unlock()
		dropped = append(dropped, messages...)
	})

	msg := func(text string) core.InboundMessage {
		return core.InboundMessage{ChatID: 42, ChatType: "private", SenderID: 7, MessageID: int64(len(text)), Text: text, IngressSurface: "telegram:primary", IngressUpdateID: int64(len(text))}
	}
	if err := ingress.Enqueue(context.Background(), msg("first")); err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	select {
	case got := <-started:
		if got != "first" {
			t.Fatalf("started = %q, want first", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first message did not start")
	}
	if err := ingress.Enqueue(context.Background(), msg("second")); err != nil {
		t.Fatalf("enqueue second: %v", err)
	}
	stop := ingress.Stop(42)
	if !stop.ActiveCanceled || !stop.QueuedDropped {
		t.Fatalf("stop = %#v, want active canceled and queued dropped", stop)
	}
	mu.Lock()
	sawDroppedSecond := containsIngressIdentity(dropped, "telegram:primary", 6)
	mu.Unlock()
	if !sawDroppedSecond {
		t.Fatalf("dropped messages = %#v, want queued second ingress reported", dropped)
	}
	close(release)

	deadline := time.After(time.Second)
	for {
		mu.Lock()
		sawFirst := containsString(handled, "first")
		sawSecond := containsString(handled, "second")
		mu.Unlock()
		if sawFirst {
			if sawSecond {
				t.Fatalf("handled = %#v, queued message should have been dropped", handled)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("handled = %#v, first did not finish", handled)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if err := ingress.Enqueue(context.Background(), msg("third")); err != nil {
		t.Fatalf("enqueue third: %v", err)
	}
	select {
	case got := <-started:
		if got != "third" {
			t.Fatalf("post-stop started = %q, want third", got)
		}
	case <-time.After(time.Second):
		t.Fatal("post-stop message did not start")
	}
}

func TestIngressSequencerSuppressesDuplicateIngressIdentity(t *testing.T) {
	started := make(chan string, 4)
	release := make(chan struct{})
	router := router.NewRouter(func(ctx context.Context, _ *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
		started <- msg.Text
		if msg.Text == "first" {
			select {
			case <-ctx.Done():
			case <-release:
			}
		}
		return nil, nil
	})
	ingress := NewIngressSequencer(router, time.Minute)
	defer ingress.Close()

	msg := func(updateID int64, text string) core.InboundMessage {
		return core.InboundMessage{
			ChatID:          43,
			ChatType:        "private",
			SenderID:        7,
			MessageID:       updateID,
			Text:            text,
			IngressSurface:  "telegram:callback-work:thread-summary",
			IngressUpdateID: updateID,
		}
	}
	if err := ingress.Enqueue(context.Background(), msg(1001, "first")); err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	select {
	case got := <-started:
		if got != "first" {
			t.Fatalf("started = %q, want first", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first message did not start")
	}
	if err := ingress.Enqueue(context.Background(), msg(1001, "first duplicate")); err != nil {
		t.Fatalf("enqueue active duplicate: %v", err)
	}
	if err := ingress.Enqueue(context.Background(), msg(1002, "second")); err != nil {
		t.Fatalf("enqueue second: %v", err)
	}
	if err := ingress.Enqueue(context.Background(), msg(1002, "second duplicate")); err != nil {
		t.Fatalf("enqueue queued duplicate: %v", err)
	}
	waitFor(t, time.Second, func() bool {
		return ingress.Snapshot().QueueDepthByChat[43] == 1
	}, "single queued duplicate-suppressed ingress")

	close(release)
	select {
	case got := <-started:
		if got != "second" {
			t.Fatalf("next started = %q, want second", got)
		}
	case <-time.After(time.Second):
		t.Fatal("second message did not start")
	}
	select {
	case got := <-started:
		t.Fatalf("unexpected duplicate start %q", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestIngressSequencerSnapshotAndIdleRetirement(t *testing.T) {
	block := make(chan struct{})
	router := router.NewRouter(func(ctx context.Context, _ *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
		if msg.Text == "first" {
			select {
			case <-ctx.Done():
			case <-block:
			}
		}
		return nil, nil
	})
	ingress := NewIngressSequencer(router, time.Minute)
	ingress.idleTTL = 20 * time.Millisecond
	defer ingress.Close()

	base := core.InboundMessage{ChatID: 99, ChatType: "private", SenderID: 7}
	if err := ingress.Enqueue(context.Background(), withIngressText(base, 1, "first")); err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	if err := ingress.Enqueue(context.Background(), withIngressText(base, 2, "second")); err != nil {
		t.Fatalf("enqueue second: %v", err)
	}
	waitFor(t, time.Second, func() bool {
		return ingress.Snapshot().QueueDepthByChat[99] == 1
	}, "ingress snapshot queue depth")

	close(block)
	waitFor(t, time.Second, func() bool {
		ingress.mu.Lock()
		defer ingress.mu.Unlock()
		return len(ingress.workers) == 0
	}, "ingress worker retirement")
}

func withIngressText(msg core.InboundMessage, messageID int64, text string) core.InboundMessage {
	msg.MessageID = messageID
	msg.Text = text
	return msg
}

func waitFor(t *testing.T, timeout time.Duration, fn func() bool, label string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", label)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsIngressIdentity(values []core.InboundMessage, surface string, updateID int64) bool {
	for _, value := range values {
		if value.IngressSurface == surface && value.IngressUpdateID == updateID {
			return true
		}
	}
	return false
}
