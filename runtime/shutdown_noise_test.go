//go:build linux

package runtime

import (
	"bytes"
	"context"
	"errors"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestExpectedShutdownNoiseRequiresRuntimeShutdown(t *testing.T) {
	t.Parallel()

	rt := &Runtime{}
	err := errors.New("begin append execution events tx: sql: database is closed")
	if rt.expectedShutdownNoise(context.Background(), err) {
		t.Fatal("expectedShutdownNoise before shutdown = true, want false")
	}
	rt.BeginShutdown()
	if !rt.expectedShutdownNoise(context.Background(), err) {
		t.Fatal("expectedShutdownNoise after shutdown = false, want true")
	}
}

func TestBeginShutdownSuppressesClosedStoreParkingWarning(t *testing.T) {
	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	rt := &Runtime{store: store}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() err = %v", err)
	}

	var logs bytes.Buffer
	previousOutput := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
	}()

	rt.BeginShutdown()

	got := logs.String()
	if strings.Contains(got, "WARN restart parking failed during shutdown") {
		t.Fatalf("logs = %q, want closed-store shutdown parking noise suppressed", got)
	}
	if !strings.Contains(got, "INFO suppressing expected shutdown restart parking failure") {
		t.Fatalf("logs = %q, want explicit shutdown parking suppression evidence", got)
	}
}

func TestToolProgressReporterSuppressesShutdownEditNoise(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.BeginShutdown()
	progressSender := &fakeSender{editErr: context.Canceled}
	key := session.SessionKey{ChatID: 7711, UserID: 0, Scope: telegramDMScopeRef(7711)}
	var reported []string
	reporter := &toolProgressReporter{
		runtime:      rt,
		executionKey: key,
		sender:       progressSender,
		editor:       progressSender,
		reportIssue:  func(_ context.Context, err error) { reported = append(reported, err.Error()) },
		chatID:       7711,
		messageID:    44,
		mode:         "all",
		style:        "semantic",
		window:       4,
		seenKeys:     make(map[string]struct{}),
	}

	reporter.ToolStarted(context.Background(), "exec", []byte(`{"command":"systemctl --user restart aphelion"}`))

	if len(reported) != 0 {
		t.Fatalf("reported = %#v, want no operational issue for shutdown edit noise", reported)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if hasExecutionEvent(events, core.ExecutionEventDeliveryProgressFailed) {
		t.Fatalf("events = %#v, want no delivery.progress.failed for expected shutdown edit noise", events)
	}
}

func TestOperationalIssueSuppressesShutdownDatabaseClosedNoise(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.BeginShutdown()
	rt.reportOperationalIssue(context.Background(), "durable_wake", errors.New("list durable agents: sql: database is closed"))

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 0 {
		t.Fatalf("sent = %#v, want no operational alert for expected shutdown DB-closed noise", sender.sent)
	}
}

func TestRecoveryMemoryFlushTimeoutIsDeferredNoise(t *testing.T) {
	t.Parallel()

	if !isRecoveryMemoryFlushTimeout(context.DeadlineExceeded) {
		t.Fatal("isRecoveryMemoryFlushTimeout(context.DeadlineExceeded) = false, want true")
	}
	if isRecoveryMemoryFlushTimeout(errors.New("permission denied")) {
		t.Fatal("isRecoveryMemoryFlushTimeout(permission denied) = true, want false")
	}
}
