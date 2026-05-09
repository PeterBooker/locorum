//go:build integration

package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/PeterBooker/locorum/internal/types"
)

// Per-site mutex serialises within a site but not across sites. No
// strict wall-time bound — CI variance — just both reach Started.
func TestPerSiteMutex_AllowsConcurrency(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	siteA := types.Site{
		Name:       "concA-" + uniqueSlug("a"),
		FilesDir:   t.TempDir(),
		PublicDir:  "/",
		PHPVersion: "8.3",
		WebServer:  "nginx",
		DBEngine:   "mysql",
		DBVersion:  "8.4",
	}
	siteB := siteA
	siteB.Name = "concB-" + uniqueSlug("b")
	siteB.FilesDir = t.TempDir()

	if err := h.sites.AddSite(siteA); err != nil {
		t.Fatalf("AddSite A: %v", err)
	}
	if err := h.sites.AddSite(siteB); err != nil {
		t.Fatalf("AddSite B: %v", err)
	}

	all, _ := h.storage.GetSites()
	if len(all) != 2 {
		t.Fatalf("expected 2 sites; got %d", len(all))
	}

	startCtx, cancel := context.WithTimeout(h.ctx, 8*time.Minute)
	defer cancel()

	var wg sync.WaitGroup
	errs := make([]error, 2)
	t0 := time.Now()
	for i, s := range all {
		i, id := i, s.ID
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = h.sites.StartSite(startCtx, id)
		}()
	}
	wg.Wait()
	dur := time.Since(t0)
	t.Logf("both sites started in %s", dur)

	for i, e := range errs {
		if e != nil {
			t.Errorf("site %d StartSite: %v", i, e)
		}
	}

	stop := timeoutCtx(t, h.ctx, 2*time.Minute)
	for _, s := range all {
		_ = h.sites.StopSite(stop, s.ID)
	}
}
