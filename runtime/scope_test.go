//go:build linux

package runtime

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/idolum-ai/aphelion/session"
)

func TestLockSessionReleasesMapEntryWhenRefCountReachesZero(t *testing.T) {
	t.Parallel()

	rt := &Runtime{sessionLocks: make(map[string]*sessionLock)}

	const distinctKeys = 64
	for i := 0; i < distinctKeys; i++ {
		key := session.SessionKey{ChatID: int64(1000 + i), UserID: int64(i)}
		unlock := rt.lockSession(key)
		unlock()
	}

	rt.sessionMu.Lock()
	defer rt.sessionMu.Unlock()
	if got := len(rt.sessionLocks); got != 0 {
		t.Fatalf("sessionLocks size = %d after serial unlock, want 0", got)
	}
}

func TestLockSessionSerializesConcurrentAcquireOnSameKey(t *testing.T) {
	t.Parallel()

	rt := &Runtime{sessionLocks: make(map[string]*sessionLock)}
	key := session.SessionKey{ChatID: 4242, UserID: 99}

	const goroutines = 32
	var (
		held    atomic.Int32
		maxHeld atomic.Int32
		wg      sync.WaitGroup
	)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			unlock := rt.lockSession(key)
			defer unlock()
			cur := held.Add(1)
			for {
				prev := maxHeld.Load()
				if cur <= prev || maxHeld.CompareAndSwap(prev, cur) {
					break
				}
			}
			// Touch a few times so a violation would have a chance to show up.
			_ = strconv.Itoa(int(cur))
			held.Add(-1)
		}()
	}
	wg.Wait()

	if got := maxHeld.Load(); got != 1 {
		t.Fatalf("max concurrent holders for same key = %d, want 1", got)
	}

	rt.sessionMu.Lock()
	defer rt.sessionMu.Unlock()
	if got := len(rt.sessionLocks); got != 0 {
		t.Fatalf("sessionLocks size = %d after concurrent unlock, want 0", got)
	}
}

func TestLockSessionAllowsParallelAcquireOnDistinctKeys(t *testing.T) {
	t.Parallel()

	rt := &Runtime{sessionLocks: make(map[string]*sessionLock)}

	const goroutines = 32
	start := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		key := session.SessionKey{ChatID: int64(7000 + i), UserID: int64(i)}
		go func() {
			defer wg.Done()
			<-start
			unlock := rt.lockSession(key)
			defer unlock()
			<-release
		}()
	}
	close(start)

	// Spin briefly to let all goroutines acquire their distinct locks.
	for {
		rt.sessionMu.Lock()
		size := len(rt.sessionLocks)
		rt.sessionMu.Unlock()
		if size == goroutines {
			break
		}
	}

	close(release)
	wg.Wait()

	rt.sessionMu.Lock()
	defer rt.sessionMu.Unlock()
	if got := len(rt.sessionLocks); got != 0 {
		t.Fatalf("sessionLocks size = %d after parallel distinct-key unlock, want 0", got)
	}
}
