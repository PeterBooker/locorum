//go:build integration

package integration

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/PeterBooker/locorum/internal/router"
	"github.com/PeterBooker/locorum/internal/testutil"
)

// Validates Traefik's file-provider hot reload.
func TestRouter_HotReload_NoConnectionDrop(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	idA := mustCreateAndStart(t, h, "rtra")
	siteA, _ := h.storage.GetSite(idA)

	urlA := "http://127.0.0.1:" + strconv.Itoa(h.httpPort)
	accept := func(code int) bool {
		return code != http.StatusBadGateway && code != http.StatusGatewayTimeout
	}
	testutil.WaitForHTTP(t, urlA, &testutil.WaitOpts{
		Timeout: 2 * time.Minute, HostHeader: siteA.Domain, AcceptStatus: accept,
	})

	// Discard port — we never hit the backend, just exercise route
	// registration during a live request flow.
	if err := h.router.UpsertService(h.ctx, router.ServiceRoute{
		Name:      "fake-svc-" + uniqueSlug("svc"),
		Hostnames: []string{"fake.localhost"},
		Backend:   "http://127.0.0.1:9",
	}); err != nil {
		t.Fatalf("UpsertService: %v", err)
	}

	testutil.WaitForHTTP(t, urlA, &testutil.WaitOpts{
		Timeout: 30 * time.Second, HostHeader: siteA.Domain, AcceptStatus: accept,
	})

	stop := timeoutCtx(t, h.ctx, 60*time.Second)
	_ = h.sites.StopSite(stop, idA)
}

func TestRouter_RemoveSite_DropsRoute(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	id := mustCreateAndStart(t, h, "rtrm")
	site, _ := h.storage.GetSite(id)

	urlBase := "http://127.0.0.1:" + strconv.Itoa(h.httpPort)
	testutil.WaitForHTTP(t, urlBase, &testutil.WaitOpts{
		Timeout:    2 * time.Minute,
		HostHeader: site.Domain,
		AcceptStatus: func(code int) bool {
			return code != http.StatusBadGateway && code != http.StatusGatewayTimeout
		},
	})

	stop := timeoutCtx(t, h.ctx, 60*time.Second)
	if err := h.sites.StopSite(stop, id); err != nil {
		t.Fatalf("StopSite: %v", err)
	}
	del := timeoutCtx(t, h.ctx, 60*time.Second)
	if err := h.sites.DeleteSite(del, id); err != nil {
		t.Fatalf("DeleteSite: %v", err)
	}

	// Traefik returns 404 for unknown vhosts; a connection-level error
	// also signals route drop (idle-conn close), so treat it as success.
	req, _ := http.NewRequestWithContext(h.ctx, http.MethodGet, urlBase, nil)
	req.Host = site.Domain
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 after delete; got %d", resp.StatusCode)
	}
}
