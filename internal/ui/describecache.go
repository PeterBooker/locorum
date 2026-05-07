package ui

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/PeterBooker/locorum/internal/sites"
)

// DescribeCache holds the most-recent SiteDescription per siteID. Layout
// is invoked on every FrameEvent; calling sm.Describe per frame would
// hit SQLite per frame and (with IncludeHostPort=true) Docker per frame.
// The cache is refreshed on:
//
//   - first observation of a siteID (on Layout when no cached entry)
//   - the OnSiteUpdated callback fires
//   - lifecycle plan completion (OnPlanDone)
//   - the DB-credentials card toggling open (one-shot Docker call for
//     IncludeHostPort)
//
// Refresh is async — Layout never blocks. The cached value is available
// the next frame after the goroutine returns.
//
// Wire format note: SiteDescription is the agent-facing IPC shape (see
// AGENTS-SUPPORT.md). Any field added here is additive; renames or
// drops require a coordinated bump because the JSON output of the
// "Copy as JSON" button is the same shape consumed by the CLI / MCP.
// describer is the SiteManager subset DescribeCache needs. Splitting it
// into an interface lets unit tests pass a fake without spinning up
// SQLite / Docker. *sites.SiteManager satisfies it directly.
type describer interface {
	Describe(ctx context.Context, siteID string, opts sites.DescribeOptions) (*sites.SiteDescription, error)
}

type DescribeCache struct {
	sm    describer
	state *UIState

	mu      sync.Mutex
	entries map[string]*describeEntry
}

type describeEntry struct {
	desc         *sites.SiteDescription
	loadedAt     time.Time
	withHostPort bool
	loading      bool
}

// NewDescribeCache returns an empty cache bound to a SiteManager.
func NewDescribeCache(sm *sites.SiteManager, state *UIState) *DescribeCache {
	return newDescribeCache(sm, state)
}

func newDescribeCache(d describer, state *UIState) *DescribeCache {
	return &DescribeCache{
		sm:      d,
		state:   state,
		entries: map[string]*describeEntry{},
	}
}

// Get returns the cached description for siteID, or nil if no fetch has
// completed. Triggers a background refresh if no entry exists yet.
//
// withHostPort=true forces a refresh that includes the published DB port.
// If the cached entry was loaded without it, Get triggers a fresh load
// and returns the (still-port-less) cached value for the current frame.
func (c *DescribeCache) Get(siteID string, withHostPort bool) *sites.SiteDescription {
	c.mu.Lock()
	e, ok := c.entries[siteID]
	if !ok {
		e = &describeEntry{}
		c.entries[siteID] = e
	}
	needsLoad := !e.loading && (e.desc == nil || (withHostPort && !e.withHostPort))
	if needsLoad {
		e.loading = true
	}
	cached := e.desc
	c.mu.Unlock()

	if needsLoad {
		go c.load(siteID, withHostPort)
	}
	return cached
}

// Invalidate marks the cached entry stale and triggers an async refresh.
// Call from OnSiteUpdated and OnPlanDone callbacks.
func (c *DescribeCache) Invalidate(siteID string) {
	c.mu.Lock()
	e, ok := c.entries[siteID]
	if !ok {
		c.mu.Unlock()
		return
	}
	withPort := e.withHostPort
	if e.loading {
		c.mu.Unlock()
		return
	}
	e.loading = true
	c.mu.Unlock()

	go c.load(siteID, withPort)
}

// Drop removes the cache entry — used when a site is deleted so we don't
// leak per-site state for the lifetime of the process.
func (c *DescribeCache) Drop(siteID string) {
	c.mu.Lock()
	delete(c.entries, siteID)
	c.mu.Unlock()
}

func (c *DescribeCache) load(siteID string, withHostPort bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	desc, err := c.sm.Describe(ctx, siteID, sites.DescribeOptions{
		IncludeHostPort:  withHostPort,
		IncludeSnapshots: true,
		ActivityLimit:    0,
	})

	c.mu.Lock()
	e, ok := c.entries[siteID]
	if !ok {
		e = &describeEntry{}
		c.entries[siteID] = e
	}
	e.loading = false
	if err == nil && desc != nil {
		e.desc = desc
		e.loadedAt = time.Now()
		e.withHostPort = withHostPort
	}
	c.mu.Unlock()

	if c.state != nil {
		c.state.Invalidate()
	}
}

// AsJSON returns a pretty-printed JSON serialisation of the cached
// description, or "" when nothing is cached. Used by the "Copy as JSON"
// header button.
func (c *DescribeCache) AsJSON(siteID string) string {
	c.mu.Lock()
	e, ok := c.entries[siteID]
	c.mu.Unlock()
	if !ok || e.desc == nil {
		return ""
	}
	body, err := json.MarshalIndent(e.desc, "", "  ")
	if err != nil {
		return ""
	}
	return string(body)
}
