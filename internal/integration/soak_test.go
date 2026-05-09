//go:build integration && soak

package integration

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// 100 cycles surfaces slow leaks (goroutines, FDs, Docker resources)
// that a one-shot integration test never reaches.
func TestSoak_StartStopLoop(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	id := mustCreateAndStart(t, h, "soakloop")
	stop := timeoutCtx(t, h.ctx, 60*time.Second)
	if err := h.sites.StopSite(stop, id); err != nil {
		t.Fatalf("initial StopSite: %v", err)
	}

	baselineGo := runtime.NumGoroutine()

	const iterations = 100
	for i := 0; i < iterations; i++ {
		startCtx := timeoutCtx(t, h.ctx, 5*time.Minute)
		if err := h.sites.StartSite(startCtx, id); err != nil {
			t.Fatalf("iter %d StartSite: %v", i, err)
		}
		stopCtx := timeoutCtx(t, h.ctx, 60*time.Second)
		if err := h.sites.StopSite(stopCtx, id); err != nil {
			t.Fatalf("iter %d StopSite: %v", i, err)
		}
		if i%10 == 0 {
			runtime.GC()
			t.Logf("iter %d: goroutines=%d (baseline %d)", i, runtime.NumGoroutine(), baselineGo)
		}
	}

	// Tolerance allows for Docker SDK connection-pool goroutines and
	// runtime worker pre-allocation.
	final := runtime.NumGoroutine()
	if final > baselineGo+8 {
		t.Errorf("goroutine drift after %d iterations: baseline=%d final=%d",
			iterations, baselineGo, final)
	}
}

func TestSoak_ConcurrentSites(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	const N = 10
	ids := make([]string, N)
	for i := 0; i < N; i++ {
		ids[i] = mustCreateAndStart(t, h, "soakcc")
		stop := timeoutCtx(t, h.ctx, 60*time.Second)
		if err := h.sites.StopSite(stop, ids[i]); err != nil {
			t.Fatalf("init StopSite %d: %v", i, err)
		}
	}

	startCtx := timeoutCtx(t, h.ctx, 15*time.Minute)
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i, id := range ids {
		i, id := i, id
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = h.sites.StartSite(startCtx, id)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("site %d: %v", i, err)
		}
	}

	stop := timeoutCtx(t, h.ctx, 5*time.Minute)
	for _, id := range ids {
		if err := h.sites.StopSite(stop, id); err != nil {
			t.Logf("StopSite %s: %v", id, err)
		}
	}
}
