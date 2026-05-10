package sites

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gocolly/colly/v2"

	"github.com/PeterBooker/locorum/internal/types"
)

// linkCheckClientTimeout caps a single HEAD probe. External hosts are
// expected to respond in under ~5s; longer means the host is dead or
// trying to keep us busy. Net.Resolver respects the same context.
const linkCheckClientTimeout = 10 * time.Second

func (sm *SiteManager) runLinkCheck(site *types.Site, onProgress func(string)) {
	baseURL := "https://" + site.Domain
	checked := sync.Map{}

	// The site we're walking is `*.localhost`, which always resolves to
	// loopback — so the in-bound colly transport intentionally allows
	// loopback. The OUTBOUND probe for external links uses a separate
	// dialer that refuses loopback / private / link-local destinations,
	// closing the SSRF window: a hostile (or merely-misconfigured) post
	// body can no longer cause us to GET `http://169.254.169.254/…` or
	// `http://localhost:8888/api/rawdata`.
	loopbackTransport := &http.Transport{
		// Link checker walks the user's own self-signed local sites; mkcert
		// roots may not be installed in this Go process, so verifying would
		// block the feature for every user who hasn't run `mkcert -install`.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // G402: intentional, see comment above.
	}
	externalTransport := &http.Transport{
		DialContext:     externalDialContext,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // G402: intentional, see above.
	}
	externalClient := &http.Client{
		Transport: externalTransport,
		Timeout:   linkCheckClientTimeout,
	}

	c := colly.NewCollector(
		colly.AllowedDomains(site.Domain),
		colly.MaxDepth(3),
	)
	c.WithTransport(loopbackTransport)

	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		link := e.Request.AbsoluteURL(e.Attr("href"))
		if link == "" || strings.HasPrefix(link, "mailto:") || strings.HasPrefix(link, "tel:") || strings.HasPrefix(link, "#") {
			return
		}

		// External link — check with HEAD request without following.
		if !strings.HasPrefix(link, baseURL) {
			if _, loaded := checked.LoadOrStore(link, true); !loaded {
				go func(l, source string) {
					if err := isExternalSafe(l); err != nil {
						onProgress(fmt.Sprintf("SKIP    %s (from %s) — %s", l, source, err.Error()))
						return
					}
					resp, err := externalClient.Head(l)
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

// isExternalSafe returns nil if rawURL is a public destination it's safe
// to probe, or a non-nil error explaining why it is not. The check is the
// pre-flight (parsed-URL) half: dialContext below is the dial-time half
// that catches CNAME redirects to private space.
func isExternalSafe(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("malformed url: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("empty hostname")
	}
	if isUnsafeHostname(host) {
		return fmt.Errorf("hostname %q resolves to internal infrastructure", host)
	}
	// If the hostname is a literal IP, evaluate it directly. Otherwise
	// resolve and reject if any answer is in unsafe space.
	if ip := net.ParseIP(host); ip != nil {
		if isUnsafeIP(ip) {
			return fmt.Errorf("ip %s is in unsafe address space", ip)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		// DNS failure isn't unsafe by itself; let the HEAD request
		// surface the error path. The dial-time guard catches it
		// regardless.
		return nil
	}
	for _, a := range addrs {
		if isUnsafeIP(a) {
			return fmt.Errorf("hostname %q resolves to %s (unsafe)", host, a)
		}
	}
	return nil
}

// externalDialContext rejects connections to unsafe destinations. Even if
// pre-flight DNS said the host is public, a CNAME chain or DNS rebinding
// could land us on a private address; this catches it at dial time.
func externalDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	var safe []net.IP
	for _, ip := range ips {
		if isUnsafeIP(ip) {
			continue
		}
		safe = append(safe, ip)
	}
	if len(safe) == 0 {
		return nil, fmt.Errorf("link check: refusing to dial %s (no public addresses)", host)
	}
	d := &net.Dialer{Timeout: 5 * time.Second}
	// Walk the safe list in order; first-success wins.
	var lastErr error
	for _, ip := range safe {
		conn, dialErr := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	return nil, lastErr
}

// isUnsafeHostname catches the small set of names that are unsafe by
// label even before DNS gives us an IP — covers split-horizon/internal
// resolvers that might map them to public space.
func isUnsafeHostname(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" {
		return true
	}
	for _, suf := range []string{".localhost", ".local", ".internal", ".lan", ".home"} {
		if strings.HasSuffix(h, suf) {
			return true
		}
	}
	return false
}

// isUnsafeIP returns true for any address class the link checker must
// not probe: loopback, private RFC 1918, link-local, multicast,
// unspecified, or the IPv4-mapped/embedded variants of those.
func isUnsafeIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	// Carrier-grade NAT (RFC 6598) — not "private" per Go but still
	// internal infrastructure.
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1]&0xC0 == 64 {
		return true
	}
	return false
}
