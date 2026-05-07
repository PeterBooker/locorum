package updatecheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

func TestParseAndCompareSemver(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"1.2.4", "1.2.3", 1},
		{"1.10.0", "1.9.9", 1},
		{"v2.0.0", "1.99.99", 1},
		{"1.0.0-beta", "1.0.0", -1},
		{"1.0.0-beta.2", "1.0.0-beta.1", 1},
		{"1.0.0", "1.0.0+build.5", 0}, // build metadata ignored
	}
	for _, c := range cases {
		a, ok := parseSemver(c.a)
		if !ok {
			t.Fatalf("parse %q failed", c.a)
		}
		b, ok := parseSemver(c.b)
		if !ok {
			t.Fatalf("parse %q failed", c.b)
		}
		if got := compareSemver(a, b); got != c.want {
			t.Errorf("compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestIsStrictlyNewerHandlesDev(t *testing.T) {
	t.Parallel()
	if IsStrictlyNewer("1.0.0", "dev") {
		t.Fatalf("dev current should not surface an upgrade")
	}
	if !IsStrictlyNewer("1.10.0", "1.9.9") {
		t.Fatalf("1.10.0 vs 1.9.9: expected newer")
	}
	if IsStrictlyNewer("1.0.0", "1.0.0") {
		t.Fatalf("equal versions should not be newer")
	}
}

func TestPickReleasePrefersHighestStable(t *testing.T) {
	t.Parallel()
	rs := []githubRelease{
		{TagName: "v1.0.0"},
		{TagName: "v1.2.3"},
		{TagName: "v1.2.4-beta", Prerelease: true},
		{TagName: "v0.9.0"},
		{TagName: "draft", Draft: true},
	}
	got := pickRelease(rs, ChannelStable)
	if got == nil || got.TagName != "v1.2.3" {
		t.Fatalf("stable pick = %+v, want v1.2.3", got)
	}

	beta := pickRelease(rs, ChannelBeta)
	if beta == nil || beta.TagName != "v1.2.4-beta" {
		t.Fatalf("beta pick = %+v, want v1.2.4-beta", beta)
	}
}

func TestCheckSurfacesNewerVersion(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"tag_name":"v2.0.0","html_url":"https://example/2.0.0","body":"notes"},
			{"tag_name":"v1.0.0","html_url":"https://example/1.0.0"}
		]`))
	}))
	t.Cleanup(srv.Close)

	res, err := Check(context.Background(), "1.0.0", Options{
		HTTPClient: &http.Client{Transport: rewriteTo(srv.URL)},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Latest != "2.0.0" {
		t.Fatalf("Latest = %q, want 2.0.0", res.Latest)
	}
	if res.URL == "" {
		t.Fatalf("URL empty")
	}
}

func TestCheckNoUpgradeWhenCurrentIsNewer(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"tag_name":"v1.0.0"}]`))
	}))
	t.Cleanup(srv.Close)

	res, err := Check(context.Background(), "1.5.0", Options{
		HTTPClient: &http.Client{Transport: rewriteTo(srv.URL)},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Latest != "" {
		t.Fatalf("Latest = %q, want empty", res.Latest)
	}
}

func TestThrottle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	state := filepath.Join(dir, "throttle")
	now := time.Now()
	if !shouldRun(state, now) {
		t.Fatalf("missing file should run")
	}
	if err := touch(state, now); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if shouldRun(state, now.Add(time.Hour)) {
		t.Fatalf("1h since touch — should NOT run")
	}
	if !shouldRun(state, now.Add(throttle+time.Minute)) {
		t.Fatalf("after throttle window — should run")
	}
}

// rewriteTo returns an http.RoundTripper that redirects every request
// to the given base URL, preserving path + query. Lets tests exercise
// Check's HTTP plumbing without exposing the URL builder.
func rewriteTo(base string) http.RoundTripper {
	bu, _ := url.Parse(base)
	return &rewriteTransport{scheme: bu.Scheme, host: bu.Host}
}

type rewriteTransport struct {
	scheme, host string
}

func (r *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = r.scheme
	u.Host = r.host
	clone.URL = &u
	return http.DefaultTransport.RoundTrip(clone)
}
