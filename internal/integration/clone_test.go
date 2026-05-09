//go:build integration

package integration

import (
	"strings"
	"testing"
	"time"
)

func TestSiteClone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	srcID := mustCreateAndStart(t, h, "clonesrc")

	sentinel := "sentinel-" + uniqueSlug("c")
	if _, err := h.sites.ExecWPCLI(h.ctx, srcID, []string{"option", "update", "locorum_clone", sentinel}); err != nil {
		t.Fatalf("wp update: %v", err)
	}

	// Stop the source before cloning: starting two sites concurrently
	// would race on the shared HTTP/HTTPS ports the harness binds.
	stop := timeoutCtx(t, h.ctx, 60*time.Second)
	if err := h.sites.StopSite(stop, srcID); err != nil {
		t.Fatalf("StopSite source: %v", err)
	}

	cloneCtx := timeoutCtx(t, h.ctx, 5*time.Minute)
	if err := h.sites.CloneSite(cloneCtx, srcID, "Cloned "+uniqueSlug("dst")); err != nil {
		t.Fatalf("CloneSite: %v", err)
	}

	sites, _ := h.storage.GetSites()
	if len(sites) != 2 {
		t.Fatalf("expected 2 sites after clone, got %d", len(sites))
	}

	var dstID string
	for _, s := range sites {
		if s.ID != srcID {
			dstID = s.ID
		}
	}

	startCtx := timeoutCtx(t, h.ctx, 5*time.Minute)
	if err := h.sites.StartSite(startCtx, dstID); err != nil {
		t.Fatalf("StartSite clone: %v", err)
	}

	got, err := h.sites.ExecWPCLI(h.ctx, dstID, []string{"option", "get", "locorum_clone"})
	if err != nil {
		t.Fatalf("wp option get: %v", err)
	}
	if !strings.Contains(got, sentinel) {
		t.Errorf("clone missing sentinel: got=%q want substring %q", got, sentinel)
	}

	stop2 := timeoutCtx(t, h.ctx, 60*time.Second)
	_ = h.sites.StopSite(stop2, dstID)
}
