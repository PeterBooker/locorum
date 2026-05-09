//go:build integration

package integration

import (
	"strings"
	"testing"
	"time"
)

func TestVersionChange_PHP(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	id := mustCreateAndStart(t, h, "ver")
	stop := timeoutCtx(t, h.ctx, 60*time.Second)
	if err := h.sites.StopSite(stop, id); err != nil {
		t.Fatalf("StopSite: %v", err)
	}

	const newVer = "8.4"
	change := timeoutCtx(t, h.ctx, 5*time.Minute)
	if err := h.sites.UpdateSiteVersions(change, id, newVer, "", ""); err != nil {
		t.Fatalf("UpdateSiteVersions: %v", err)
	}

	start := timeoutCtx(t, h.ctx, 5*time.Minute)
	if err := h.sites.StartSite(start, id); err != nil {
		t.Fatalf("StartSite: %v", err)
	}

	out, err := h.sites.ExecWPCLI(h.ctx, id, []string{"eval", "echo PHP_MAJOR_VERSION . '.' . PHP_MINOR_VERSION;"})
	if err != nil {
		t.Fatalf("wp eval: %v", err)
	}
	if !strings.Contains(out, newVer) {
		t.Errorf("expected PHP %s; got %q", newVer, out)
	}

	stop2 := timeoutCtx(t, h.ctx, 60*time.Second)
	_ = h.sites.StopSite(stop2, id)
}
