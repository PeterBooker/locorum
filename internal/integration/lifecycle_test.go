//go:build integration

package integration

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/testutil"
	"github.com/PeterBooker/locorum/internal/types"
)

func TestSiteCreate_HappyPath(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	site := types.Site{
		Name:       "happy " + uniqueSlug("hp"),
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

	cons, err := h.docker.ContainersByLabel(h.ctx, map[string]string{
		docker.LabelPlatform: docker.PlatformValue,
		docker.LabelSite:     created.Slug,
	})
	if err != nil {
		t.Fatalf("ContainersByLabel: %v", err)
	}
	if len(cons) < 4 {
		t.Errorf("expected at least 4 site containers, got %d", len(cons))
	}

	// Anything that's not 502/504 means routing is wired up — installer
	// page, WP front-end, or a 30x to the installer all qualify.
	url := "http://127.0.0.1:" + strconv.Itoa(h.httpPort)
	testutil.WaitForHTTP(t, url, &testutil.WaitOpts{
		Timeout:    3 * time.Minute,
		HostHeader: created.Domain,
		AcceptStatus: func(code int) bool {
			return code != http.StatusBadGateway && code != http.StatusGatewayTimeout
		},
	})

	stopCtx, stopCancel := context.WithTimeout(h.ctx, 60*time.Second)
	defer stopCancel()
	if err := h.sites.StopSite(stopCtx, created.ID); err != nil {
		t.Fatalf("StopSite: %v", err)
	}
}

func TestSiteStartStop_PreservesData(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	id := mustCreateAndStart(t, h, "preserve")

	sentinel := "locorum-sentinel-" + uniqueSlug("v")
	if _, err := h.sites.ExecWPCLI(h.ctx, id, []string{"option", "update", "locorum_test", sentinel}); err != nil {
		t.Fatalf("wp option update: %v", err)
	}

	stopCtx := timeoutCtx(t, h.ctx, 60*time.Second)
	if err := h.sites.StopSite(stopCtx, id); err != nil {
		t.Fatalf("StopSite: %v", err)
	}

	startCtx := timeoutCtx(t, h.ctx, 5*time.Minute)
	if err := h.sites.StartSite(startCtx, id); err != nil {
		t.Fatalf("StartSite (after stop): %v", err)
	}

	got, err := h.sites.ExecWPCLI(h.ctx, id, []string{"option", "get", "locorum_test"})
	if err != nil {
		t.Fatalf("wp option get: %v", err)
	}
	if !strings.Contains(got, sentinel) {
		t.Errorf("after stop+start, sentinel option missing. got=%q want substring %q", got, sentinel)
	}

	stop2 := timeoutCtx(t, h.ctx, 60*time.Second)
	if err := h.sites.StopSite(stop2, id); err != nil {
		t.Fatalf("StopSite (final): %v", err)
	}
}

// Asserts the CLAUDE.md "volumes kept by design" invariant.
func TestSiteDelete_KeepsVolumes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	id := mustCreateAndStart(t, h, "delkeep")

	site, err := h.storage.GetSite(id)
	if err != nil || site == nil {
		t.Fatalf("get site: %v / %v", err, site)
	}
	volumeName := "locorum-" + site.Slug + "-dbdata"

	stopCtx := timeoutCtx(t, h.ctx, 60*time.Second)
	if err := h.sites.StopSite(stopCtx, id); err != nil {
		t.Fatalf("StopSite: %v", err)
	}

	delCtx := timeoutCtx(t, h.ctx, 60*time.Second)
	if err := h.sites.DeleteSite(delCtx, id); err != nil {
		t.Fatalf("DeleteSite: %v", err)
	}

	cons, _ := h.docker.ContainersByLabel(h.ctx, map[string]string{
		docker.LabelSite: site.Slug,
	})
	if len(cons) != 0 {
		t.Errorf("after delete, %d containers still labelled site=%s", len(cons), site.Slug)
	}

	// Engine has no read-only VolumesByLabel; check the surviving volume
	// via the SDK directly.
	if _, err := h.cli.VolumeInspect(h.ctx, volumeName); err != nil {
		t.Errorf("expected volume %s to survive site delete: %v", volumeName, err)
	}
	// Volumes are only removed via the explicit purge path in production;
	// remove here so the t.Cleanup leak assertion stays clean.
	if err := h.cli.VolumeRemove(h.ctx, volumeName, true); err != nil {
		t.Logf("post-test volume cleanup: %v", err)
	}
}

func TestDockerLabelInvariant(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	id := mustCreateAndStart(t, h, "labels")
	site, _ := h.storage.GetSite(id)

	cons, err := h.docker.ContainersByLabel(h.ctx, map[string]string{
		docker.LabelSite: site.Slug,
	})
	if err != nil {
		t.Fatalf("ContainersByLabel: %v", err)
	}
	for _, c := range cons {
		if c.Labels[docker.LabelPlatform] != docker.PlatformValue {
			t.Errorf("container %v missing platform label, got %v",
				c.Names, c.Labels[docker.LabelPlatform])
		}
		if c.Labels[docker.LabelSite] == "" {
			t.Errorf("container %v missing site label", c.Names)
		}
	}

	stop := timeoutCtx(t, h.ctx, 60*time.Second)
	if err := h.sites.StopSite(stop, id); err != nil {
		t.Fatalf("StopSite: %v", err)
	}
}
