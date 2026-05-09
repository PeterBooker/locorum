//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/PeterBooker/locorum/internal/types"
)

func mustCreateAndStart(t *testing.T, h *harness, base string) string {
	t.Helper()
	site := types.Site{
		Name:       base + "-" + uniqueSlug(base),
		FilesDir:   t.TempDir(),
		PublicDir:  "/",
		PHPVersion: "8.3",
		WebServer:  "nginx",
		DBEngine:   "mysql",
		DBVersion:  "8.4",
	}
	if err := h.sites.AddSite(site); err != nil {
		t.Fatalf("AddSite: %v", err)
	}
	created := getSingleSite(t, h)
	startCtx, cancel := context.WithTimeout(h.ctx, 5*time.Minute)
	defer cancel()
	if err := h.sites.StartSite(startCtx, created.ID); err != nil {
		t.Fatalf("StartSite: %v", err)
	}
	return created.ID
}

// getSingleSite trips on count != 1 — each harness has its own home,
// so a well-formed test owns at most one site at a time.
func getSingleSite(t *testing.T, h *harness) types.Site {
	t.Helper()
	sites, err := h.storage.GetSites()
	if err != nil {
		t.Fatalf("GetSites: %v", err)
	}
	if len(sites) != 1 {
		t.Fatalf("expected 1 site, got %d", len(sites))
	}
	return sites[0]
}
