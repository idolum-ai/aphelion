//go:build linux

package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestStartIdleExpiryLoopRunsAndStopsWithContext(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	var calls int32
	rt.expireIdle = func(_ time.Duration) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	rt.startIdleExpiryLoop(ctx, 20*time.Millisecond, func(string, ...any) {})

	time.Sleep(75 * time.Millisecond)
	beforeCancel := atomic.LoadInt32(&calls)
	if beforeCancel < 2 {
		t.Fatalf("expire calls before cancel = %d, want >= 2", beforeCancel)
	}

	cancel()
	time.Sleep(60 * time.Millisecond)
	afterCancel := atomic.LoadInt32(&calls)
	if afterCancel != beforeCancel {
		t.Fatalf("expire calls changed after cancel: before=%d after=%d", beforeCancel, afterCancel)
	}
}

func TestStartIdleExpiryLoopLogsErrorsAndContinues(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	var calls int32
	rt.expireIdle = func(_ time.Duration) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, errors.New("boom")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt.startIdleExpiryLoop(ctx, 20*time.Millisecond, func(string, ...any) {})
	time.Sleep(70 * time.Millisecond)

	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Fatalf("expire calls = %d, want >= 2 despite errors", got)
	}
}

func TestIdleExpirySweepCadence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		idleExpiry time.Duration
		want       time.Duration
	}{
		{name: "negative defaults to minute", idleExpiry: -time.Second, want: time.Minute},
		{name: "tiny floors at minute", idleExpiry: 30 * time.Second, want: time.Minute},
		{name: "quarter duration", idleExpiry: 4 * time.Hour, want: time.Hour},
		{name: "caps at hour", idleExpiry: 24 * time.Hour, want: time.Hour},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := idleExpirySweepCadence(tc.idleExpiry)
			if got != tc.want {
				t.Fatalf("idleExpirySweepCadence(%s) = %s, want %s", tc.idleExpiry, got, tc.want)
			}
		})
	}
}
