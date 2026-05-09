package testutil

import (
	"context"
	"crypto/tls"
	"net/http"
	"testing"
	"time"
)

// WaitOpts tunes WaitForHTTP. Zero values use 60s/200ms and `< 500`
// status acceptance.
type WaitOpts struct {
	Timeout       time.Duration
	Interval      time.Duration
	AcceptStatus  func(int) bool
	SkipTLSVerify bool
	HostHeader    string
}

func (o *WaitOpts) timeout() time.Duration {
	if o == nil || o.Timeout == 0 {
		return 60 * time.Second
	}
	return o.Timeout
}

func (o *WaitOpts) interval() time.Duration {
	if o == nil || o.Interval == 0 {
		return 200 * time.Millisecond
	}
	return o.Interval
}

func (o *WaitOpts) accept(code int) bool {
	if o != nil && o.AcceptStatus != nil {
		return o.AcceptStatus(code)
	}
	return code > 0 && code < 500
}

// WaitForHTTP polls url until a status passes opts.AcceptStatus (default
// 1xx–4xx) or opts.Timeout elapses. Reports the last error/status on
// timeout — silent waits are the LEARNINGS.md §1.5 footgun.
func WaitForHTTP(t testing.TB, url string, opts *WaitOpts) {
	t.Helper()

	client := &http.Client{Timeout: 5 * time.Second}
	if opts != nil && opts.SkipTLSVerify {
		client.Transport = &http.Transport{
			//nolint:gosec // local self-signed cert; see WaitOpts docs.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	deadline := time.Now().Add(opts.timeout())
	var lastErr error
	var lastStatus int

	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
		if err != nil {
			t.Fatalf("WaitForHTTP: invalid url %q: %v", url, err)
		}
		if opts != nil && opts.HostHeader != "" {
			req.Host = opts.HostHeader
		}
		resp, err := client.Do(req)
		if err == nil {
			lastStatus = resp.StatusCode
			_ = resp.Body.Close()
			if opts.accept(lastStatus) {
				return
			}
		} else {
			lastErr = err
		}
		time.Sleep(opts.interval())
	}

	if lastErr != nil {
		t.Fatalf("WaitForHTTP %s: timed out after %s; last error: %v", url, opts.timeout(), lastErr)
	}
	t.Fatalf("WaitForHTTP %s: timed out after %s; last status %d", url, opts.timeout(), lastStatus)
}
