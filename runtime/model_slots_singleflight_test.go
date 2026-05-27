//go:build linux

package runtime

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
)

type fakeStampedeProvider struct{}

func (*fakeStampedeProvider) Complete(context.Context, []agent.Message, []agent.ToolDef) (*agent.Response, error) {
	return &agent.Response{}, nil
}

func TestCachedProviderForModelSlotDeduplicatesConcurrentBuilds(t *testing.T) {
	t.Parallel()

	const goroutines = 32
	var buildCount atomic.Int32
	provider := &fakeStampedeProvider{}
	released := make(chan struct{})

	rt := &Runtime{
		cfg: &config.Config{},
		buildProviderHook: func(*config.Config, core.ModelSlotConfig) (agent.Provider, error) {
			buildCount.Add(1)
			// Hold the build path open long enough for the rest of the
			// goroutines to pile up at the singleflight gate. Without
			// dedup they would each fall through here.
			<-released
			return provider, nil
		},
	}

	slot := core.ModelSlotConfig{Provider: "anthropic", Model: "claude-fake"}

	var (
		wg      sync.WaitGroup
		got     [goroutines]agent.Provider
		errs    [goroutines]error
		barrier sync.WaitGroup
	)
	barrier.Add(goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			barrier.Done()
			barrier.Wait()
			got[i], errs[i] = rt.cachedProviderForModelSlot(slot)
		}()
	}
	// Give all goroutines time to reach the singleflight gate before we let
	// the build path return.
	time.Sleep(50 * time.Millisecond)
	close(released)
	wg.Wait()

	if c := buildCount.Load(); c != 1 {
		t.Fatalf("build called %d times under stampede, want exactly 1", c)
	}
	for i := 0; i < goroutines; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: err = %v, want nil", i, errs[i])
		}
		if got[i] != agent.Provider(provider) {
			t.Fatalf("goroutine %d: provider = %v, want shared instance", i, got[i])
		}
	}

	// Subsequent acquisition with cache populated must not call build again.
	if _, err := rt.cachedProviderForModelSlot(slot); err != nil {
		t.Fatalf("cached acquire err = %v", err)
	}
	if c := buildCount.Load(); c != 1 {
		t.Fatalf("build called %d times after cache populated, want still 1", c)
	}
}
