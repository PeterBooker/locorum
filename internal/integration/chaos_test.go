//go:build integration && chaos

package integration

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PeterBooker/locorum/internal/router"
)

// We don't self-SIGTERM (would tear down the runner) — cancelling the
// start context exercises the same orch.Run rollback path.
func TestChaos_SIGTERM_MidStart(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	id := mustCreateAndStart(t, h, "chaossig")
	stop := timeoutCtx(t, h.ctx, 60*time.Second)
	if err := h.sites.StopSite(stop, id); err != nil {
		t.Fatalf("StopSite: %v", err)
	}

	startCtx, cancel := context.WithCancel(h.ctx)
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- h.sites.StartSite(startCtx, id)
	}()
	time.Sleep(5 * time.Second)
	cancel()

	select {
	case err := <-doneCh:
		if err == nil {
			t.Logf("StartSite finished before our cancel landed; that's fine")
		} else {
			t.Logf("StartSite returned %v (expected after cancel)", err)
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("StartSite did not return 2 minutes after cancel")
	}

	startCtx2 := timeoutCtx(t, h.ctx, 5*time.Minute)
	if err := h.sites.StartSite(startCtx2, id); err != nil {
		t.Fatalf("clean restart after chaos: %v", err)
	}

	stop2 := timeoutCtx(t, h.ctx, 60*time.Second)
	_ = h.sites.StopSite(stop2, id)
}

// Regression test for genmark.WriteAtomic's tmp+rename contract.
func TestChaos_AtomicWrites_NoTornFiles(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	const concurrency = 20
	var ops atomic.Int64
	deadline := time.Now().Add(15 * time.Second)

	done := make(chan struct{})
	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			for time.Now().Before(deadline) {
				name := "chaos-svc-" + uniqueSlug("a")
				ctx := timeoutCtx(t, h.ctx, 10*time.Second)
				_ = h.router.UpsertService(ctx, router.ServiceRoute{
					Name:      name,
					Hostnames: []string{name + ".localhost"},
					Backend:   "http://127.0.0.1:9",
				})
				ops.Add(1)
			}
			if idx == 0 {
				close(done)
			}
		}(i)
	}
	<-done
	t.Logf("attempted %d upserts in 15s", ops.Load())

	// Trailing-newline + non-empty is enough to catch torn writes
	// without pulling in a YAML parser.
	dyn := filepath.Join(h.homeDir, ".locorum", "router", "dynamic")
	entries, err := os.ReadDir(dyn)
	if err != nil {
		t.Fatalf("readdir %s: %v", dyn, err)
	}
	for _, e := range entries {
		path := filepath.Join(dyn, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", e.Name(), err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("torn file (empty): %s", e.Name())
			continue
		}
		if data[len(data)-1] != '\n' {
			t.Errorf("torn file (no trailing newline): %s", e.Name())
		}
	}
}
