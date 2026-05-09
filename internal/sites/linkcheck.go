package sites

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/gocolly/colly/v2"

	"github.com/PeterBooker/locorum/internal/types"
)

func (sm *SiteManager) runLinkCheck(site *types.Site, onProgress func(string)) {
	baseURL := "https://" + site.Domain
	checked := sync.Map{}

	transport := &http.Transport{
		// Link checker walks the user's own self-signed local sites; mkcert
		// roots may not be installed in this Go process, so verifying would
		// block the feature for every user who hasn't run `mkcert -install`.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // G402: intentional, see comment above
	}
	httpClient := &http.Client{Transport: transport}

	c := colly.NewCollector(
		colly.AllowedDomains(site.Domain),
		colly.MaxDepth(3),
	)
	c.WithTransport(transport)

	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		link := e.Request.AbsoluteURL(e.Attr("href"))
		if link == "" || strings.HasPrefix(link, "mailto:") || strings.HasPrefix(link, "tel:") || strings.HasPrefix(link, "#") {
			return
		}

		// External link — check with HEAD request without following.
		if !strings.HasPrefix(link, baseURL) {
			if _, loaded := checked.LoadOrStore(link, true); !loaded {
				go func(l, source string) {
					resp, err := httpClient.Head(l)
					if err != nil {
						onProgress(fmt.Sprintf("BROKEN  %s (from %s) — %s", l, source, err.Error()))
					} else {
						resp.Body.Close()
						if resp.StatusCode >= 400 {
							onProgress(fmt.Sprintf("BROKEN  [%d] %s (from %s)", resp.StatusCode, l, source))
						} else {
							onProgress(fmt.Sprintf("OK      [%d] %s", resp.StatusCode, l))
						}
					}
				}(link, e.Request.URL.String())
			}
			return
		}

		_ = e.Request.Visit(link)
	})

	c.OnResponse(func(r *colly.Response) {
		status := "OK     "
		if r.StatusCode >= 400 {
			status = "BROKEN "
		}
		onProgress(fmt.Sprintf("%s [%d] %s", status, r.StatusCode, r.Request.URL))
	})

	c.OnError(func(r *colly.Response, err error) {
		onProgress(fmt.Sprintf("ERROR   %s — %s", r.Request.URL, err.Error()))
	})

	onProgress("Starting link check for " + baseURL + " ...")
	_ = c.Visit(baseURL)
	c.Wait()
	onProgress("\nLink check complete.")
}
