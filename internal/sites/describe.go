package sites

import (
	"context"
	"fmt"
	"time"

	"github.com/PeterBooker/locorum/internal/dbengine"
	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/types"
)

// SiteDescription is the canonical, side-effect-free snapshot of a site
// for any client that needs to read its state without touching SiteManager
// internals: the GUI's overview panel, the CLI's `site describe`, the MCP
// server's describe_site tool. JSON tags pin the wire format for IPC and
// MCP — rename fields here only with a coordinated client bump.
type SiteDescription struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Domain    string `json:"domain"`
	URL       string `json:"url"`
	FilesDir  string `json:"filesDir"`
	PublicDir string `json:"publicDir"`

	Started bool `json:"started"`

	WebServer string `json:"webServer"`
	Multisite string `json:"multisite,omitempty"`

	PHP      VersionInfo `json:"php"`
	Database DBInfo      `json:"database"`
	Redis    VersionInfo `json:"redis"`

	Containers []ContainerInfo `json:"containers,omitempty"`

	Hooks    HooksSummary    `json:"hooks"`
	Activity []ActivityEntry `json:"recentActivity,omitempty"`

	SnapshotsCount int `json:"snapshotsCount"`

	Profiling SPXInfo `json:"profiling,omitempty"`

	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// VersionInfo captures the runtime version of one of the site's services.
type VersionInfo struct {
	Version string `json:"version"`
}

// DBInfo describes the database service. Credentials are user-static
// (per LEARNINGS.md §4.5) so exposing the username + db name is safe; the
// password lives behind a separate request gate to keep it out of every
// describe payload.
type DBInfo struct {
	Engine    string `json:"engine"`
	Version   string `json:"version"`
	Username  string `json:"username"`
	Database  string `json:"database"`
	Container string `json:"container,omitempty"`
	// HostPort is the published TCP port on 127.0.0.1, or 0 when the
	// site does not publish its DB port. Resolving the port requires a
	// live Docker call, so callers opt in via DescribeOptions.
	HostPort int `json:"hostPort,omitempty"`
}

// ContainerInfo names the per-service containers that back a site, so
// clients can reference them in `exec` / `logs` calls without reverse-
// engineering the SiteContainerName scheme.
type ContainerInfo struct {
	Service string `json:"service"`
	Name    string `json:"name"`
}

// HooksSummary reports the configured hook count broken down by event so
// agents can spot a site that will run a 30-second pre-start hook before
// they hit "start."
type HooksSummary struct {
	Total  int            `json:"total"`
	ByKind map[string]int `json:"byKind,omitempty"`
}

// ActivityEntry is a flattened ActivityEvent for the wire. Storage's
// ActivityEvent uses unexported helpers (json.RawMessage, time.Time) that
// don't survive JSON-RPC well; this type pins the wire shape.
type ActivityEntry struct {
	ID         int64     `json:"id"`
	Time       time.Time `json:"time"`
	Plan       string    `json:"plan"`
	Kind       string    `json:"kind"`
	Status     string    `json:"status"`
	DurationMS int64     `json:"durationMs"`
	Message    string    `json:"message,omitempty"`
}

// SPXInfo reports the profiler state for the site. Empty struct when the
// profiler is not enabled — fields are intentionally narrow so the SPX
// secret never leaks into a describe payload.
type SPXInfo struct {
	Enabled bool `json:"enabled"`
}

// DescribeOptions controls which optional sections require live Docker /
// disk lookups. The plain Describe() takes the cheap path; richer clients
// (CLI / MCP) can opt in.
type DescribeOptions struct {
	// IncludeActivity attaches the most recent activity rows to the
	// description. Cheap (one indexed SQLite query); off by default so
	// list_sites stays compact.
	IncludeActivity bool

	// ActivityLimit caps how many rows IncludeActivity returns. <= 0
	// uses the same limit as RecentActivity (5). The CLI passes a
	// larger value for `site describe`.
	ActivityLimit int

	// IncludeSnapshots counts the on-disk snapshots for the site by
	// listing the snapshots dir. ~O(N) where N is files for that site;
	// negligible for typical use.
	IncludeSnapshots bool

	// IncludeHostPort resolves the published DB port via Docker. Skip
	// for batch list_sites calls — the lookup is one Docker round-trip
	// per site.
	IncludeHostPort bool
}

// Describe returns a SiteDescription for siteID. Pure with respect to
// site state when opts.IncludeHostPort is false — safe to call from
// render loops, MCP read-only profiles, and JSON-RPC handlers without
// triggering side effects on the daemon.
//
// A non-existent site returns (nil, nil) so callers can distinguish
// "not found" (cheap) from "lookup failed" (wrap).
func (sm *SiteManager) Describe(ctx context.Context, siteID string, opts DescribeOptions) (*SiteDescription, error) {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return nil, fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return nil, nil
	}
	return sm.describeFromSite(ctx, site, opts)
}

// DescribeAll returns a SiteDescription for every site, ordered as
// GetSites returns them. Used by `locorum site list --json` and the MCP
// list_sites tool.
//
// Uses the cheap-path defaults (no host-port lookup, no activity, no
// snapshot count) regardless of the per-call opts unless the caller
// explicitly flips them on. The CLI takes the slow path on a single
// site, never on the list.
func (sm *SiteManager) DescribeAll(ctx context.Context, opts DescribeOptions) ([]SiteDescription, error) {
	rows, err := sm.st.GetSites()
	if err != nil {
		return nil, fmt.Errorf("listing sites: %w", err)
	}
	out := make([]SiteDescription, 0, len(rows))
	for i := range rows {
		desc, err := sm.describeFromSite(ctx, &rows[i], opts)
		if err != nil {
			return nil, err
		}
		if desc != nil {
			out = append(out, *desc)
		}
	}
	return out, nil
}

// describeFromSite is the shared body of Describe / DescribeAll. Callers
// guarantee site is non-nil.
func (sm *SiteManager) describeFromSite(ctx context.Context, site *types.Site, opts DescribeOptions) (*SiteDescription, error) {
	eng := dbengine.Resolve(site)

	desc := &SiteDescription{
		ID:        site.ID,
		Name:      site.Name,
		Slug:      site.Slug,
		Domain:    site.Domain,
		URL:       "https://" + site.Domain,
		FilesDir:  site.FilesDir,
		PublicDir: site.PublicDir,
		Started:   site.Started,
		WebServer: site.WebServer,
		Multisite: site.Multisite,
		PHP:       VersionInfo{Version: site.PHPVersion},
		Database: DBInfo{
			Engine:    site.DBEngine,
			Version:   site.DBVersion,
			Username:  "wordpress",
			Database:  "wordpress",
			Container: docker.SiteContainerName(site.Slug, "database"),
		},
		Redis: VersionInfo{Version: site.RedisVersion},
		Containers: []ContainerInfo{
			{Service: "web", Name: docker.SiteContainerName(site.Slug, "web")},
			{Service: "php", Name: docker.SiteContainerName(site.Slug, "php")},
			{Service: "database", Name: docker.SiteContainerName(site.Slug, "database")},
			{Service: "redis", Name: docker.SiteContainerName(site.Slug, "redis")},
		},
		Profiling: SPXInfo{Enabled: site.SPXEnabled},
		CreatedAt: site.CreatedAt,
		UpdatedAt: site.UpdatedAt,
	}
	// Engine resolver fills in DBEngine when the column was NULL on
	// legacy rows; mirror its choice into the description so clients
	// see the same value the rest of the system uses.
	if desc.Database.Engine == "" {
		desc.Database.Engine = string(eng.Kind())
	}

	desc.Hooks = sm.summariseHooks(site.ID)

	if opts.IncludeActivity {
		limit := opts.ActivityLimit
		if limit <= 0 {
			limit = activityRecentLimit
		}
		rows, err := sm.st.GetActivity(site.ID, limit)
		if err != nil {
			return nil, fmt.Errorf("loading activity: %w", err)
		}
		desc.Activity = flattenActivity(rows)
	}

	if opts.IncludeSnapshots {
		snaps, err := sm.ListSnapshots(site.Slug)
		if err == nil {
			desc.SnapshotsCount = len(snaps)
		}
	}

	if opts.IncludeHostPort && site.PublishDBPort && site.Started {
		port, err := sm.d.PublishedHostPort(ctx, desc.Database.Container, eng.DefaultPort())
		if err == nil {
			desc.Database.HostPort = port
		}
	}

	return desc, nil
}

// summariseHooks counts hooks per event for the site. Returns an empty
// summary on lookup failure rather than propagating — the description is
// useful even when one section is missing, and the GUI's overview panel
// already tolerates a zero summary.
func (sm *SiteManager) summariseHooks(siteID string) HooksSummary {
	out := HooksSummary{}
	list, err := sm.st.ListHooks(siteID)
	if err != nil || len(list) == 0 {
		return out
	}
	out.Total = len(list)
	out.ByKind = make(map[string]int, 4)
	for _, h := range list {
		ev := string(h.Event)
		if !hooks.Event(ev).Valid() {
			ev = "other"
		}
		out.ByKind[ev]++
	}
	return out
}

// flattenActivity turns []storage.ActivityEvent into the wire-friendly
// []ActivityEntry. Drops the JSON details blob — clients that need it
// can call get_activity directly.
func flattenActivity(rows []storage.ActivityEvent) []ActivityEntry {
	if len(rows) == 0 {
		return nil
	}
	out := make([]ActivityEntry, len(rows))
	for i, r := range rows {
		out[i] = ActivityEntry{
			ID:         r.ID,
			Time:       r.Time,
			Plan:       r.Plan,
			Kind:       string(r.Kind),
			Status:     string(r.Status),
			DurationMS: r.DurationMS,
			Message:    r.Message,
		}
	}
	return out
}
