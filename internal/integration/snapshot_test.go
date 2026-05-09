//go:build integration

package integration

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PeterBooker/locorum/internal/sites"
)

func TestSnapshot_RestoreVerifies(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	id := mustCreateAndStart(t, h, "snap")

	pre := "pre-" + uniqueSlug("s")
	if _, err := h.sites.ExecWPCLI(h.ctx, id, []string{"option", "update", "locorum_snap", pre}); err != nil {
		t.Fatalf("wp update pre: %v", err)
	}

	snapPath, err := h.sites.Snapshot(h.ctx, id, "test-baseline")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if _, err := os.Stat(snapPath); err != nil {
		t.Fatalf("snapshot file missing: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(snapPath) })

	post := "post-" + uniqueSlug("s")
	if _, err := h.sites.ExecWPCLI(h.ctx, id, []string{"option", "update", "locorum_snap", post}); err != nil {
		t.Fatalf("wp update post: %v", err)
	}

	restCtx := timeoutCtx(t, h.ctx, 5*time.Minute)
	if err := h.sites.RestoreSnapshot(restCtx, id, snapPath, sites.RestoreSnapshotOptions{}); err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}

	got, err := h.sites.ExecWPCLI(h.ctx, id, []string{"option", "get", "locorum_snap"})
	if err != nil {
		t.Fatalf("wp option get: %v", err)
	}
	if !strings.Contains(got, pre) {
		t.Errorf("after restore, expected %q substring; got %q", pre, got)
	}
	if strings.Contains(got, post) {
		t.Errorf("after restore, post-mutation value %q still present", post)
	}

	stop := timeoutCtx(t, h.ctx, 60*time.Second)
	_ = h.sites.StopSite(stop, id)
}
