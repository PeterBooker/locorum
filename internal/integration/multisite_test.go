//go:build integration

package integration

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/PeterBooker/locorum/internal/testutil"
	"github.com/PeterBooker/locorum/internal/types"
)

func TestMultisite_SubdomainRouting(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	site := types.Site{
		Name:       "multi-" + uniqueSlug("ms"),
		FilesDir:   t.TempDir(),
		PublicDir:  "/",
		PHPVersion: "8.3",
		WebServer:  "nginx",
		DBEngine:   "mysql",
		DBVersion:  "8.4",
		Multisite:  "subdomain",
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

	url := "http://127.0.0.1:" + strconv.Itoa(h.httpPort)
	for _, host := range []string{
		created.Domain,
		"sub1." + created.Slug + ".localhost",
	} {
		host := host
		t.Run(host, func(t *testing.T) {
			testutil.WaitForHTTP(t, url, &testutil.WaitOpts{
				Timeout:    3 * time.Minute,
				HostHeader: host,
				AcceptStatus: func(code int) bool {
					return code != http.StatusBadGateway && code != http.StatusGatewayTimeout
				},
			})
		})
	}

	stop := timeoutCtx(t, h.ctx, 60*time.Second)
	_ = h.sites.StopSite(stop, created.ID)
}
