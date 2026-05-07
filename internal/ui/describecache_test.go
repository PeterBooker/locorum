package ui

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PeterBooker/locorum/internal/sites"
)

type fakeDescriber struct {
	mu       sync.Mutex
	calls    atomic.Int32
	withPort atomic.Int32
	desc     *sites.SiteDescription
	err      error
	delay    time.Duration
}

func (f *fakeDescriber) Describe(ctx context.Context, siteID string, opts sites.DescribeOptions) (*sites.SiteDescription, error) {
	f.calls.Add(1)
	if opts.IncludeHostPort {
		f.withPort.Add(1)
	}
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.desc, f.err
}

func TestDescribeCacheLoadsOnceAndCaches(t *testing.T) {
	t.Parallel()
	f := &fakeDescriber{
		desc: &sites.SiteDescription{ID: "site-1", Name: "demo", Slug: "demo"},
	}
	c := newDescribeCache(f, nil)

	// First Get returns nil and triggers a load.
	if got := c.Get("site-1", false); got != nil {
		t.Fatalf("first Get returned non-nil cached entry: %+v", got)
	}
	// Wait for the async load.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := c.Get("site-1", false); got != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := c.Get("site-1", false)
	if got == nil || got.ID != "site-1" {
		t.Fatalf("Get after load = %+v, want site-1", got)
	}
	// Repeated Gets must not trigger more loads (debounce).
	for i := 0; i < 5; i++ {
		_ = c.Get("site-1", false)
	}
	if calls := f.calls.Load(); calls != 1 {
		t.Fatalf("expected 1 load, got %d", calls)
	}
}

func TestDescribeCacheRefreshesForHostPort(t *testing.T) {
	t.Parallel()
	f := &fakeDescriber{
		desc: &sites.SiteDescription{ID: "site-1"},
	}
	c := newDescribeCache(f, nil)

	c.Get("site-1", false)
	waitFor(t, func() bool { return c.Get("site-1", false) != nil }, time.Second)
	if f.calls.Load() != 1 {
		t.Fatalf("first load count = %d, want 1", f.calls.Load())
	}

	// Now request with host port — must trigger a second load.
	c.Get("site-1", true)
	waitFor(t, func() bool { return f.withPort.Load() == 1 }, time.Second)
	if f.calls.Load() != 2 {
		t.Fatalf("after host-port request, calls = %d, want 2", f.calls.Load())
	}

	// Subsequent host-port Gets should not load again.
	for i := 0; i < 3; i++ {
		_ = c.Get("site-1", true)
	}
	if f.calls.Load() != 2 {
		t.Fatalf("repeated host-port Get = %d, want 2", f.calls.Load())
	}
}

func TestDescribeCacheInvalidateRefreshes(t *testing.T) {
	t.Parallel()
	f := &fakeDescriber{
		desc: &sites.SiteDescription{ID: "site-1"},
	}
	c := newDescribeCache(f, nil)

	c.Get("site-1", false)
	waitFor(t, func() bool { return c.Get("site-1", false) != nil }, time.Second)

	c.Invalidate("site-1")
	waitFor(t, func() bool { return f.calls.Load() == 2 }, time.Second)
	if f.calls.Load() != 2 {
		t.Fatalf("after Invalidate, calls = %d, want 2", f.calls.Load())
	}
}

func TestDescribeCacheAsJSONIncludesFields(t *testing.T) {
	t.Parallel()
	f := &fakeDescriber{
		desc: &sites.SiteDescription{ID: "abc", Name: "demo", Slug: "demo"},
	}
	c := newDescribeCache(f, nil)
	c.Get("abc", false)
	waitFor(t, func() bool { return c.Get("abc", false) != nil }, time.Second)

	body := c.AsJSON("abc")
	if body == "" {
		t.Fatalf("AsJSON returned empty after load")
	}
	if !strings.Contains(body, `"id": "abc"`) {
		t.Fatalf("AsJSON missing id field; body = %s", body)
	}
}

func TestDescribeCacheAsJSONReturnsEmptyWhenColdAndOnDrop(t *testing.T) {
	t.Parallel()
	f := &fakeDescriber{
		desc: &sites.SiteDescription{ID: "abc"},
	}
	c := newDescribeCache(f, nil)

	if got := c.AsJSON("abc"); got != "" {
		t.Fatalf("cold AsJSON = %q, want empty", got)
	}
	c.Get("abc", false)
	waitFor(t, func() bool { return c.Get("abc", false) != nil }, time.Second)

	c.Drop("abc")
	if got := c.AsJSON("abc"); got != "" {
		t.Fatalf("AsJSON after Drop = %q, want empty", got)
	}
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}
