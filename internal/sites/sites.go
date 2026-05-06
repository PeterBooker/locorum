package sites

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/client"
	"github.com/google/uuid"
	"github.com/gosimple/slug"
	"github.com/sqweek/dialog"

	"github.com/PeterBooker/locorum/internal/config"
	"github.com/PeterBooker/locorum/internal/dbengine"
	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/genmark"
	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/orch"
	"github.com/PeterBooker/locorum/internal/router"
	"github.com/PeterBooker/locorum/internal/sites/configyaml"
	"github.com/PeterBooker/locorum/internal/sites/sitesteps"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/types"
	"github.com/PeterBooker/locorum/internal/utils"
)

// SiteManager owns site lifecycle. Lifecycle methods build orch.Plans and
// run them via orch.Run; that's where the rollback / progress / per-step
// observability live. Direct Docker calls (exec for wp-cli, container logs)
// are still used for short non-orchestrated operations.
type SiteManager struct {
	st      *storage.Storage
	cli     *client.Client
	sites   map[string]types.Site
	d       *docker.Docker
	rtr     router.Router
	hooks   hooks.Runner
	homeDir string
	config  embed.FS
	cfg     *config.Config // typed read/write of global settings

	// siteLocks serialises lifecycle calls per site. Different sites run
	// in parallel; two lifecycle calls on the same site queue.
	siteLocks sync.Map // map[string]*sync.Mutex

	// Callbacks invoked when sites data changes. The UI layer sets these
	// in ui.New() to trigger redraws.
	OnSitesUpdated func(sites []types.Site)
	OnSiteUpdated  func(site *types.Site)

	// Hook-stream callbacks. Set by the UI in ui.New(); fired from
	// goroutines spawned inside runHooks.
	OnHookTaskStart func(siteID string, hook hooks.Hook)
	OnHookOutput    func(siteID string, line string, stderr bool)
	OnHookTaskDone  func(siteID string, result hooks.Result)
	OnHookAllDone   func(siteID string, summary hooks.Summary)

	// Lifecycle progress callbacks. Fired from orch.Run during a Plan.
	// The UI subscribes to render the per-step checklist.
	OnStepStart func(siteID string, step orch.StepResult)
	OnStepDone  func(siteID string, step orch.StepResult)
	OnPlanDone  func(siteID string, result orch.Result)

	// Pull progress callback. Fired during the PullImages step. Called
	// per pull tick; aggregated across layers.
	OnPullProgress func(siteID string, progress docker.PullProgress)

	// OnActivityAppended fires after a Plan outcome is persisted to the
	// activity_events table. The UI subscribes so the overview feed +
	// Activity tab update live without polling. Fired only on a
	// successful insert; failed writes are logged and silently dropped.
	OnActivityAppended func(siteID string, ev storage.ActivityEvent)
}

func NewSiteManager(st *storage.Storage, cli *client.Client, d *docker.Docker, rtr router.Router, runner hooks.Runner, configFS embed.FS, homeDir string, cfg *config.Config) *SiteManager {
	siteTpl = template.Must(
		template.New("site.tmpl").
			Funcs(funcMap).
			ParseFS(configFS, "config/nginx/site.tmpl"),
	)

	apacheSiteTpl = template.Must(
		template.New("site.tmpl").
			Funcs(funcMap).
			ParseFS(configFS, "config/apache/site.tmpl"),
	)

	return &SiteManager{
		st:      st,
		cli:     cli,
		d:       d,
		rtr:     rtr,
		hooks:   runner,
		config:  configFS,
		homeDir: homeDir,
		cfg:     cfg,
		sites:   make(map[string]types.Site),
	}
}

// Config returns the shared global-settings facade. Nil when the
// SiteManager was constructed without one (test-only path); production
// always wires through main.New.
func (sm *SiteManager) Config() *config.Config { return sm.cfg }

// Roots returns the FilesDir of every registered site, deduped and
// sorted. Implements health.SiteRootLister so the path-shape checks
// (wsl-mnt-c, windows-longpath) can iterate over real site locations
// without depending on *sites.SiteManager directly.
//
// Errors from the underlying GetSites are swallowed (logged at debug
// only) — the health check is informational and a transient SQLite
// hiccup shouldn't trip a "we can't read your sites" finding.
func (sm *SiteManager) Roots(_ context.Context) []string {
	if sm == nil || sm.st == nil {
		return nil
	}
	rows, err := sm.st.GetSites()
	if err != nil {
		slog.Debug("sites: roots: GetSites failed", "err", err.Error())
		return nil
	}
	seen := make(map[string]struct{}, len(rows))
	out := make([]string, 0, len(rows))
	for _, s := range rows {
		if s.FilesDir == "" {
			continue
		}
		if _, ok := seen[s.FilesDir]; ok {
			continue
		}
		seen[s.FilesDir] = struct{}{}
		out = append(out, s.FilesDir)
	}
	return out
}

// writeConfigYAML projects the site row + its hooks onto
// <site.FilesDir>/.locorum/config.yaml. Errors are logged but never
// propagated — the YAML is a portability convenience, not a load-
// bearing artefact, and a transient write failure (e.g. user-deleted
// site dir mid-operation) must not abort the lifecycle method.
//
// Idempotent: WriteIfManaged short-circuits when the rendered bytes
// match disk, and respects a user-stripped marker so manual edits are
// preserved.
func (sm *SiteManager) writeConfigYAML(site *types.Site) {
	if site == nil || site.FilesDir == "" {
		return
	}
	hookList, err := sm.st.ListHooks(site.ID)
	if err != nil {
		slog.Warn("config.yaml: list hooks", "site", site.Slug, "err", err.Error())
		// Continue with no hooks — better an incomplete projection
		// than no projection at all.
		hookList = nil
	}
	body, err := configyaml.Render(configyaml.FromSite(*site, hookList))
	if err != nil {
		slog.Warn("config.yaml: render", "site", site.Slug, "err", err.Error())
		return
	}
	target := filepath.Join(site.FilesDir, configyaml.Filename)
	if err := genmark.WriteIfManaged(target, body, 0o644); err != nil && !errors.Is(err, genmark.ErrUserOwned) {
		slog.Warn("config.yaml: write", "site", site.Slug, "err", err.Error())
	}
}

// siteMutex returns the per-site lifecycle mutex, creating it on first use.
// Different sites get independent mutexes so they can run in parallel.
func (sm *SiteManager) siteMutex(siteID string) *sync.Mutex {
	if v, ok := sm.siteLocks.Load(siteID); ok {
		return v.(*sync.Mutex)
	}
	m := &sync.Mutex{}
	actual, _ := sm.siteLocks.LoadOrStore(siteID, m)
	return actual.(*sync.Mutex)
}

// runHooks fires every enabled hook for ev/site, forwarding results to the
// UI callbacks. Returns task error in fail-strict mode, nil otherwise.
func (sm *SiteManager) runHooks(ctx context.Context, ev hooks.Event, site *types.Site) error {
	if sm.hooks == nil || site == nil {
		return nil
	}
	siteID := site.ID
	opts := hooks.RunOptions{
		OnTaskStart: func(h hooks.Hook) {
			if sm.OnHookTaskStart != nil {
				sm.OnHookTaskStart(siteID, h)
			}
		},
		OnOutput: func(line string, stderr bool) {
			if sm.OnHookOutput != nil {
				sm.OnHookOutput(siteID, line, stderr)
			}
		},
		OnTaskDone: func(r hooks.Result) {
			if sm.OnHookTaskDone != nil {
				sm.OnHookTaskDone(siteID, r)
			}
		},
		OnAllDone: func(s hooks.Summary) {
			if sm.OnHookAllDone != nil {
				sm.OnHookAllDone(siteID, s)
			}
		},
	}
	if err := sm.hooks.Run(ctx, ev, site, opts); err != nil {
		slog.Error("hook run failed", "event", ev, "site", site.Slug, "err", err.Error())
		return err
	}
	slog.Info("hook run complete", "event", ev, "site", site.Slug)
	return nil
}

// RunHookNow executes a single hook for a site outside the lifecycle.
func (sm *SiteManager) RunHookNow(ctx context.Context, h hooks.Hook) (hooks.Result, error) {
	if sm.hooks == nil {
		return hooks.Result{}, fmt.Errorf("hooks runner not configured")
	}
	site, err := sm.st.GetSite(h.SiteID)
	if err != nil {
		return hooks.Result{}, fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return hooks.Result{}, fmt.Errorf("site %q not found", h.SiteID)
	}
	siteID := site.ID
	opts := hooks.RunOptions{
		OnTaskStart: func(h hooks.Hook) {
			if sm.OnHookTaskStart != nil {
				sm.OnHookTaskStart(siteID, h)
			}
		},
		OnOutput: func(line string, stderr bool) {
			if sm.OnHookOutput != nil {
				sm.OnHookOutput(siteID, line, stderr)
			}
		},
		OnTaskDone: func(r hooks.Result) {
			if sm.OnHookTaskDone != nil {
				sm.OnHookTaskDone(siteID, r)
			}
		},
	}
	return sm.hooks.RunOne(ctx, h, site, opts)
}

func (sm *SiteManager) GetSites() ([]types.Site, error) {
	return sm.st.GetSites()
}

// GetSetting returns a stored user preference, or "" if unset.
func (sm *SiteManager) GetSetting(key string) (string, error) {
	return sm.st.GetSetting(key)
}

// SetSetting upserts a user preference.
func (sm *SiteManager) SetSetting(key, value string) error {
	return sm.st.SetSetting(key, value)
}

// ─── Hook persistence pass-throughs ─────────────────────────────────────────

// ListSiteHooks returns every hook attached to siteID.
func (sm *SiteManager) ListSiteHooks(siteID string) ([]hooks.Hook, error) {
	return sm.st.ListHooks(siteID)
}

// AddSiteHook validates h and persists it. Refreshes the projected
// config.yaml so checked-in copies stay in sync.
func (sm *SiteManager) AddSiteHook(h *hooks.Hook) error {
	if err := sm.st.AddHook(h); err != nil {
		return err
	}
	sm.refreshConfigYAMLFor(h.SiteID)
	return nil
}

// UpdateSiteHook persists changes to an existing hook.
func (sm *SiteManager) UpdateSiteHook(h *hooks.Hook) error {
	if err := sm.st.UpdateHook(h); err != nil {
		return err
	}
	sm.refreshConfigYAMLFor(h.SiteID)
	return nil
}

// DeleteSiteHook removes a hook by id. Looks up the site row before
// deletion so we still have the FilesDir to write the projection to
// after the row vanishes.
func (sm *SiteManager) DeleteSiteHook(id int64) error {
	siteID := sm.siteIDForHook(id)
	if err := sm.st.DeleteHook(id); err != nil {
		return err
	}
	sm.refreshConfigYAMLFor(siteID)
	return nil
}

// ReorderSiteHooks atomically rewrites positions for an event.
func (sm *SiteManager) ReorderSiteHooks(siteID string, ev hooks.Event, ids []int64) error {
	if err := sm.st.ReorderHooks(siteID, ev, ids); err != nil {
		return err
	}
	sm.refreshConfigYAMLFor(siteID)
	return nil
}

// siteIDForHook returns the site id for a hook id by querying every
// site's hook list. Cheap because hook tables are small (single
// digits per site) and only called during DeleteSiteHook. Empty
// string when the hook is unknown — DeleteSiteHook then no-ops the
// YAML refresh.
func (sm *SiteManager) siteIDForHook(id int64) string {
	sites, err := sm.st.GetSites()
	if err != nil {
		return ""
	}
	for _, s := range sites {
		hs, err := sm.st.ListHooks(s.ID)
		if err != nil {
			continue
		}
		for _, h := range hs {
			if h.ID == id {
				return s.ID
			}
		}
	}
	return ""
}

// refreshConfigYAMLFor re-projects a site by id. Tolerates an
// unknown id (silent no-op) so hook CRUD on a deleted site doesn't
// panic mid-tear-down.
func (sm *SiteManager) refreshConfigYAMLFor(siteID string) {
	if siteID == "" {
		return
	}
	site, err := sm.st.GetSite(siteID)
	if err != nil || site == nil {
		return
	}
	sm.writeConfigYAML(site)
}

// ─── Activity feed pass-throughs ────────────────────────────────────────────

// activityRecentLimit caps the overview-panel row count. Five fits the
// design grid; anything beyond is reachable via the Activity tab.
const activityRecentLimit = 5

// GetActivity returns up to limit newest-first activity rows for siteID.
// A non-positive limit defaults to storage.ActivityRetentionDefault.
func (sm *SiteManager) GetActivity(siteID string, limit int) ([]storage.ActivityEvent, error) {
	return sm.st.GetActivity(siteID, limit)
}

// RecentActivity returns the small slice the overview panel renders. The
// Activity tab uses GetActivity directly with a higher limit.
func (sm *SiteManager) RecentActivity(siteID string) ([]storage.ActivityEvent, error) {
	return sm.st.GetActivity(siteID, activityRecentLimit)
}

// SweepActivity trims the per-site row count for every existing site to
// the retention cap. Idempotent and cheap; AppendActivity already enforces
// the cap on every insert, so this is a defensive sweep for cases where
// the cap is reduced or rows are inserted by a different process.
func (sm *SiteManager) SweepActivity() error {
	sites, err := sm.st.GetSites()
	if err != nil {
		return err
	}
	for _, s := range sites {
		if err := sm.st.TrimActivity(s.ID, storage.ActivityRetentionDefault); err != nil {
			return err
		}
	}
	return nil
}

func (sm *SiteManager) GetSite(id string) (*types.Site, error) {
	site, err := sm.st.GetSite(id)
	if err != nil {
		slog.Error("Failed to fetch site: " + err.Error())
		return nil, err
	}
	return site, nil
}

// generatePassword returns a cryptographically random hex string of n bytes.
func generatePassword(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Should never happen — crypto/rand reads from OS.
		return "password"
	}
	return hex.EncodeToString(b)
}

func (sm *SiteManager) AddSite(site types.Site) error {
	site.ID = uuid.NewString()
	site.Slug = slug.Make(site.Name)
	site.Domain = slug.Make(site.Name) + ".localhost"
	site.Started = false
	site.DBPassword = generatePassword(16)
	if site.WebServer == "" {
		site.WebServer = "nginx"
	}
	// Resolve engine + version with sensible defaults so callers (UI,
	// scripts) can leave DBEngine/DBVersion empty and still get a valid
	// site row.
	if site.DBEngine == "" {
		site.DBEngine = string(dbengine.Default)
	}
	if !dbengine.IsValid(dbengine.Kind(site.DBEngine)) {
		return fmt.Errorf("unknown database engine %q", site.DBEngine)
	}
	if site.DBVersion == "" {
		// Legacy callers still set MySQLVersion; honour it before
		// falling back to the engine's default.
		if site.MySQLVersion != "" && site.DBEngine == string(dbengine.MySQL) {
			site.DBVersion = site.MySQLVersion
		} else {
			site.DBVersion = dbengine.MustFor(dbengine.Kind(site.DBEngine)).DefaultVersion()
		}
	}

	if err := utils.EnsureDir(site.FilesDir); err != nil {
		slog.Error("Failed to create site directory: " + err.Error())
		return err
	}

	if err := sm.st.AddSite(&site); err != nil {
		return err
	}

	sm.writeConfigYAML(&site)
	sm.emitSitesUpdate()
	return nil
}

// ─── StartSite ──────────────────────────────────────────────────────────────

// StartSite brings up a site as an orchestrated Plan. Each step is small,
// idempotent, and rolled back on failure. Container readiness is verified
// (not just "running") via the WaitReady step.
func (sm *SiteManager) StartSite(ctx context.Context, id string) error {
	site, err := sm.st.GetSite(id)
	if err != nil {
		slog.Error("Failed to fetch site: " + err.Error())
		return err
	}
	if site == nil {
		return fmt.Errorf("site %q not found", id)
	}

	mu := sm.siteMutex(id)
	mu.Lock()
	defer mu.Unlock()

	if err := sm.runHooks(ctx, hooks.PreStart, site); err != nil {
		return err
	}

	specs := sm.serviceSpecs(site)

	plan := orch.Plan{
		Name: "start-site:" + site.Slug,
		Steps: []orch.Step{
			&sitesteps.FuncStep{
				Label: "ensure-files-writable",
				Do: func(_ context.Context) error {
					ensureWritable(site.FilesDir)
					return nil
				},
			},
			&sitesteps.EnsureSPXStep{Site: site, HomeDir: sm.homeDir},
			&sitesteps.FuncStep{
				Label: "ensure-wordpress",
				Do: func(_ context.Context) error {
					return sm.ensureWordPress(site)
				},
			},
			&sitesteps.FuncStep{
				Label: "ensure-wp-config",
				Do: func(_ context.Context) error {
					return sm.EnsureWPConfig(site)
				},
			},
			&sitesteps.FuncStep{
				Label: "generate-site-config",
				Do: func(_ context.Context) error {
					return sm.generateWebServerConfig(site)
				},
			},
			&sitesteps.EnsureNetworkStep{Engine: sm.d, Site: site},
			&sitesteps.EnsureVolumeStep{Engine: sm.d, Site: site},
			&sitesteps.EnsureMarkerStep{Engine: sm.d, Site: site},
			&sitesteps.PullImagesStep{
				Engine:     sm.d,
				Site:       site,
				Specs:      specs,
				OnProgress: sm.pullProgressCallback(site.ID),
			},
			&sitesteps.ChownStep{Engine: sm.d, Site: site},
			&sitesteps.CreateContainersStep{Engine: sm.d, Specs: specs},
			&sitesteps.WaitReadyStep{
				Engine:     sm.d,
				Containers: specNames(specs),
				Timeouts: map[string]time.Duration{
					docker.SiteContainerName(site.Slug, "database"): 120 * time.Second,
				},
			},
			&sitesteps.WriteMarkerStep{Execer: sm.d, Site: site},
			&sitesteps.RegisterRoutesStep{
				Router: sm.rtr,
				Route:  sm.routeFor(site),
			},
		},
	}

	res := sm.runPlan(ctx, site, plan)
	if res.FinalError != nil {
		return res.FinalError
	}

	site.Started = true
	if _, err := sm.st.UpdateSite(site); err != nil {
		slog.Error("Failed to update site: " + err.Error())
		return err
	}

	if site.Multisite != "" {
		if err := sm.ensureMultisiteWithHooks(ctx, site); err != nil {
			slog.Error("Failed to configure multisite: " + err.Error())
		}
	}

	if sm.OnSiteUpdated != nil {
		sm.OnSiteUpdated(site)
	}

	// Refresh the projected config.yaml after a successful start so
	// new sites (or sites upgraded from before this projection
	// existed) get their portable file written exactly once.
	sm.writeConfigYAML(site)

	return sm.runHooks(ctx, hooks.PostStart, site)
}

func (sm *SiteManager) routeFor(site *types.Site) router.SiteRoute {
	route := router.SiteRoute{
		Slug:        site.Slug,
		PrimaryHost: site.Domain,
		Backend:     "http://" + docker.SiteContainerName(site.Slug, "web") + ":80",
	}
	if site.Multisite == "subdomain" {
		route.WildcardHost = "*." + site.Domain
	}
	return route
}

func (sm *SiteManager) generateWebServerConfig(site *types.Site) error {
	if site.WebServer == "apache" {
		return sm.generateApacheSiteConfig(site, path.Join(sm.homeDir, ".locorum", "config", "apache", "sites", site.Slug+".conf"))
	}
	return sm.generateSiteConfig(site, path.Join(sm.homeDir, ".locorum", "config", "nginx", "sites", site.Slug+".conf"))
}

// serviceSpecs returns the four per-site container specs in the order:
// web, php, database, redis. Database routing happens through dbengine
// so MySQL and MariaDB sites resolve to engine-specific specs without
// branching here.
func (sm *SiteManager) serviceSpecs(site *types.Site) []docker.ContainerSpec {
	return []docker.ContainerSpec{
		docker.WebSpec(site, sm.homeDir),
		docker.PHPSpec(site, sm.homeDir),
		dbengine.Resolve(site).ContainerSpec(site, sm.homeDir),
		docker.RedisSpec(site),
	}
}

func specNames(specs []docker.ContainerSpec) []string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.Name
	}
	return out
}

// runPlan executes plan and forwards step / pull callbacks to the UI. It
// also emits structured slog records at start, per step, and at the plan
// boundary — so the lifecycle log on disk and the in-app logs both have
// the same view.
//
// site is captured by value into the OnPlanDone closure so message
// rendering still has the slug / version fields after a delete-site Plan
// has removed the row from storage.
func (sm *SiteManager) runPlan(ctx context.Context, site *types.Site, plan orch.Plan) orch.Result {
	siteID := ""
	if site != nil {
		siteID = site.ID
	}
	slog.Info("plan starting", "plan", plan.Name, "site", siteID, "steps", len(plan.Steps))
	cb := orch.Callbacks{
		OnStepStart: func(s orch.StepResult) {
			slog.Info("plan step start", "plan", plan.Name, "step", s.Name)
			if sm.OnStepStart != nil {
				sm.OnStepStart(siteID, s)
			}
		},
		OnStepDone: func(s orch.StepResult) {
			attrs := []any{"plan", plan.Name, "step", s.Name, "status", s.Status, "duration_ms", s.Duration.Milliseconds()}
			if s.Error != nil {
				slog.Error("plan step failed", append(attrs, "err", s.Error.Error())...)
			} else {
				slog.Info("plan step done", attrs...)
			}
			if sm.OnStepDone != nil {
				sm.OnStepDone(siteID, s)
			}
		},
		OnPlanDone: func(r orch.Result) {
			attrs := []any{"plan", r.PlanName, "duration_ms", r.Duration.Milliseconds(), "rolled_back", r.RolledBack}
			if r.FinalError != nil {
				slog.Error("plan failed", append(attrs, "err", r.FinalError.Error())...)
			} else {
				slog.Info("plan complete", attrs...)
			}
			if sm.OnPlanDone != nil {
				sm.OnPlanDone(siteID, r)
			}
			writeAuditLog(sm.homeDir, r)
			sm.recordActivity(site, plan, r)
		},
	}
	return orch.Run(ctx, plan, cb)
}

func (sm *SiteManager) pullProgressCallback(siteID string) func(docker.PullProgress) {
	if sm.OnPullProgress == nil {
		return nil
	}
	return func(p docker.PullProgress) {
		sm.OnPullProgress(siteID, p)
	}
}

// ─── ensureMultisite ────────────────────────────────────────────────────────

func (sm *SiteManager) ensureMultisiteWithHooks(ctx context.Context, site *types.Site) error {
	if err := sm.runHooks(ctx, hooks.PreMultisite, site); err != nil {
		return err
	}
	if err := sm.ensureMultisite(ctx, site); err != nil {
		return err
	}
	return sm.runHooks(ctx, hooks.PostMultisite, site)
}

func (sm *SiteManager) ensureMultisite(ctx context.Context, site *types.Site) error {
	if _, network := sm.wpIsInstalled(ctx, site); network {
		return nil
	}
	if err := sm.wpInstallDefault(ctx, site); err != nil {
		return fmt.Errorf("wp core install: %w", err)
	}
	if err := sm.wpMultisiteConvert(ctx, site, site.Multisite); err != nil {
		return fmt.Errorf("multisite convert: %w", err)
	}
	return nil
}

// ─── StopSite ───────────────────────────────────────────────────────────────

func (sm *SiteManager) StopSite(ctx context.Context, id string) error {
	site, err := sm.st.GetSite(id)
	if err != nil {
		slog.Error("Failed to fetch site: " + err.Error())
		return err
	}
	if site == nil {
		return fmt.Errorf("site %q not found", id)
	}

	mu := sm.siteMutex(id)
	mu.Lock()
	defer mu.Unlock()

	if err := sm.runHooks(ctx, hooks.PreStop, site); err != nil {
		return err
	}

	containers := specNames(sm.serviceSpecs(site))

	plan := orch.Plan{
		Name: "stop-site:" + site.Slug,
		Steps: []orch.Step{
			&sitesteps.StopContainersStep{Engine: sm.d, Containers: containers},
			&sitesteps.RemoveRoutesStep{Router: sm.rtr, Slug: site.Slug},
		},
	}
	res := sm.runPlan(ctx, site, plan)
	if res.FinalError != nil {
		return res.FinalError
	}

	site.Started = false
	if _, err := sm.st.UpdateSite(site); err != nil {
		slog.Error("Failed to update site: " + err.Error())
		return err
	}

	if sm.OnSiteUpdated != nil {
		sm.OnSiteUpdated(site)
	}

	return sm.runHooks(ctx, hooks.PostStop, site)
}

// ─── DeleteSite ─────────────────────────────────────────────────────────────

// DeleteOptions controls whether the database volume is preserved or
// purged. Mirrors the three-way confirmation modal: Stop / Delete-keep-
// volume / Purge.
type DeleteOptions struct {
	// PurgeVolume removes the database data volume too. Default false —
	// volumes survive deletion so users can recover sites or reuse data.
	PurgeVolume bool

	// SkipSnapshot disables the automatic pre-delete database snapshot.
	// Default false — every delete on a running site emits a snapshot to
	// ~/.locorum/snapshots/ first. Set true when the caller has already
	// taken a snapshot or is intentionally discarding data.
	SkipSnapshot bool
}

func (sm *SiteManager) DeleteSite(ctx context.Context, id string) error {
	return sm.DeleteSiteWithOptions(ctx, id, DeleteOptions{})
}

func (sm *SiteManager) DeleteSiteWithOptions(ctx context.Context, id string, opts DeleteOptions) error {
	site, err := sm.st.GetSite(id)
	if err != nil {
		slog.Error("Failed to fetch site: " + err.Error())
		return err
	}
	if site == nil {
		// Already gone; nothing to delete. Still emit so UI refreshes.
		sm.emitSitesUpdate()
		return nil
	}

	mu := sm.siteMutex(id)
	mu.Lock()
	defer mu.Unlock()

	// pre-delete fires BEFORE we touch storage, so the runner's hook list
	// lookup still succeeds.
	if err := sm.runHooks(ctx, hooks.PreDelete, site); err != nil {
		return err
	}

	containers := specNames(sm.serviceSpecs(site))

	var steps []orch.Step
	if !opts.SkipSnapshot && site.Started {
		// Best-effort snapshot. Captured BEFORE containers stop so the
		// DB is reachable. A snapshot failure is logged but does NOT
		// abort the delete — refusing to delete on a snapshot error
		// would strand the user with an undeletable site (e.g. if the
		// DB is corrupt). The snapshot label is "pre_delete" so it's
		// easy to find in ListSnapshots.
		steps = append(steps, &sitesteps.FuncStep{
			Label: "auto-snapshot",
			Do: func(ctx context.Context) error {
				path, err := sm.snapshotLocked(ctx, site, "pre_delete")
				if err != nil {
					slog.Warn("auto-snapshot before delete failed", "site", site.Slug, "err", err.Error())
					return nil
				}
				slog.Info("auto-snapshot before delete written", "path", path)
				return nil
			},
		})
	}
	steps = append(steps,
		&sitesteps.StopContainersStep{Engine: sm.d, Containers: containers},
		&sitesteps.RemoveContainersStep{Engine: sm.d, Containers: containers},
		&sitesteps.RemoveRoutesStep{Router: sm.rtr, Slug: site.Slug},
		&sitesteps.RemoveSiteConfigsStep{HomeDir: sm.homeDir, Site: site},
		&sitesteps.RemoveNetworkStep{Engine: sm.d, Site: site},
	)
	if opts.PurgeVolume {
		steps = append(steps, &sitesteps.PurgeVolumeStep{Engine: sm.d, Site: site})
	}

	plan := orch.Plan{
		Name:  "delete-site:" + site.Slug,
		Steps: steps,
	}
	res := sm.runPlan(ctx, site, plan)
	if res.FinalError != nil {
		// Even on error we continue to delete the SQL row so the GUI
		// doesn't show a half-deleted site forever; container cleanup is
		// retryable from a power-cycle.
		slog.Warn("delete site plan partially failed", "err", res.FinalError.Error())
	}

	// post-delete fires AFTER tear-down but BEFORE the SQL DELETE: the FK
	// ON DELETE CASCADE would otherwise wipe site_hooks rows before the
	// runner could enumerate them.
	if err := sm.runHooks(ctx, hooks.PostDelete, site); err != nil {
		slog.Warn("post-delete hook run failed", "err", err.Error())
	}

	if err := sm.st.DeleteSite(id); err != nil {
		return err
	}

	sm.emitSitesUpdate()
	return nil
}

func (sm *SiteManager) emitSitesUpdate() {
	sites, err := sm.st.GetSites()
	if err != nil {
		slog.Error("Failed to get sites: " + err.Error())
		return
	}
	if sm.OnSitesUpdated != nil {
		sm.OnSitesUpdated(sites)
	}
}

// ReconcileState marks all sites as stopped in the database. Called on
// startup after Initialize() has cleaned up all containers.
func (sm *SiteManager) ReconcileState() error {
	sites, err := sm.st.GetSites()
	if err != nil {
		return err
	}

	for i := range sites {
		if sites[i].Started {
			sites[i].Started = false
			if _, err := sm.st.UpdateSite(&sites[i]); err != nil {
				slog.Error("Failed to reconcile site state: " + err.Error())
			}
		}
	}

	sm.emitSitesUpdate()

	return nil
}

func (sm *SiteManager) OpenSiteFilesDir(id string) error {
	site, err := sm.st.GetSite(id)
	if err != nil {
		slog.Error("Failed to fetch site: " + err.Error())
		return err
	}
	if err := utils.OpenDirectory(site.FilesDir); err != nil {
		slog.Error("Failed to open site files directory: " + err.Error())
		return err
	}
	return nil
}

// PickDirectory opens a native folder-picker and returns the selected path.
func (sm *SiteManager) PickDirectory() (string, error) {
	if runtime.GOOS == "windows" && utils.HasWSL() {
		return utils.PickDirectoryInWSL()
	}
	dir, err := dialog.Directory().Title("Select a folder").Browse()
	if err != nil {
		return "", err
	}
	return dir, nil
}

// GetContainerLogs returns the last N lines of logs for a site's service
// container. Service should be one of: web, php, database, redis.
func (sm *SiteManager) GetContainerLogs(ctx context.Context, siteID, service string, lines int) (string, error) {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return "", fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return "", fmt.Errorf("site %q not found", siteID)
	}
	containerName := docker.SiteContainerName(site.Slug, service)
	return sm.d.ContainerLogs(ctx, containerName, lines)
}

// OpenAdminLogin generates a one-time auto-login URL and opens it in the browser.
func (sm *SiteManager) OpenAdminLogin(siteID string) error {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return fmt.Errorf("site %q not found", siteID)
	}
	if !site.Started {
		return fmt.Errorf("site must be running")
	}

	token := generatePassword(32)

	targetDir := site.FilesDir
	if site.PublicDir != "" && site.PublicDir != "/" {
		targetDir = filepath.Join(site.FilesDir, site.PublicDir)
	}
	muPluginsDir := filepath.Join(targetDir, "wp-content", "mu-plugins")
	if err := utils.EnsureDir(muPluginsDir); err != nil {
		return fmt.Errorf("creating mu-plugins dir: %w", err)
	}

	pluginContent := fmt.Sprintf(`<?php
// Locorum auto-login — single-use, self-deleting.
if (isset($_GET['locorum_token']) && $_GET['locorum_token'] === '%s') {
    add_action('init', function() {
        $user = get_user_by('login', 'admin');
        if (!$user) {
            $users = get_users(array('role' => 'administrator', 'number' => 1));
            $user = !empty($users) ? $users[0] : null;
        }
        if ($user) {
            wp_set_current_user($user->ID);
            wp_set_auth_cookie($user->ID, true);
        }
        @unlink(__FILE__);
        wp_redirect(admin_url());
        exit;
    });
}
`, token)

	pluginPath := filepath.Join(muPluginsDir, "locorum-autologin.php")
	if err := os.WriteFile(pluginPath, []byte(pluginContent), 0666); err != nil {
		return fmt.Errorf("writing auto-login plugin: %w", err)
	}

	loginURL := fmt.Sprintf("https://%s/wp-admin/?locorum_token=%s", site.Domain, token)
	return utils.OpenURL(loginURL)
}

// OpenSiteShell opens an interactive terminal session in the site's PHP container.
func (sm *SiteManager) OpenSiteShell(siteID string) error {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return fmt.Errorf("site %q not found", siteID)
	}
	if !site.Started {
		return fmt.Errorf("site must be running")
	}

	containerName := docker.SiteContainerName(site.Slug, "php")
	return utils.OpenTerminalWithCommand("docker", "exec", "-it", containerName, "/bin/bash")
}

// VersionsChange describes a desired change to a stopped site's runtime
// versions. The fields are tri-state: empty string = no change, a
// concrete value = set to that. ChangeEngine flips the engine kind
// itself, which always requires the migrate flow.
type VersionsChange struct {
	PHPVersion   string
	DBEngine     string
	DBVersion    string
	RedisVersion string
}

// ErrUnsafeVersionTransition is returned when a requested version change
// can't be applied in-place because the engine reports it as unsafe (e.g.
// MySQL 8 → 5.7) — the caller should route through MigrateEngine instead.
var ErrUnsafeVersionTransition = errors.New("unsafe version transition; use MigrateEngine")

// UpdateSiteVersions changes PHP/DB/Redis versions for a stopped site and
// removes old containers so they are recreated on next start with the
// new images. Same engine, same major version transitions only — engine
// swaps must go through MigrateEngine which preserves data via
// snapshot+restore.
func (sm *SiteManager) UpdateSiteVersions(ctx context.Context, siteID, phpVer, dbVer, redisVer string) error {
	return sm.UpdateSiteVersionsWithEngine(ctx, siteID, VersionsChange{
		PHPVersion:   phpVer,
		DBVersion:    dbVer,
		RedisVersion: redisVer,
	})
}

// UpdateSiteVersionsWithEngine is the engine-aware form. The caller may
// also pass DBEngine — but only the same engine is accepted here; engine
// swaps return ErrUnsafeVersionTransition pointing at MigrateEngine.
func (sm *SiteManager) UpdateSiteVersionsWithEngine(ctx context.Context, siteID string, change VersionsChange) error {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return fmt.Errorf("site %q not found", siteID)
	}
	if site.Started {
		return fmt.Errorf("site must be stopped to change versions")
	}

	mu := sm.siteMutex(siteID)
	mu.Lock()
	defer mu.Unlock()

	changed := false
	if change.PHPVersion != "" && change.PHPVersion != site.PHPVersion {
		site.PHPVersion = change.PHPVersion
		changed = true
	}
	if change.RedisVersion != "" && change.RedisVersion != site.RedisVersion {
		site.RedisVersion = change.RedisVersion
		changed = true
	}
	if change.DBEngine != "" && change.DBEngine != site.DBEngine {
		// Engine swap is not in-place — always migrate via snapshot.
		return ErrUnsafeVersionTransition
	}
	if change.DBVersion != "" && change.DBVersion != site.DBVersion {
		eng := dbengine.Resolve(site)
		if !eng.UpgradeAllowed(site.DBVersion, change.DBVersion) {
			return ErrUnsafeVersionTransition
		}
		site.DBVersion = change.DBVersion
		// Keep the legacy mirror in sync for one minor release.
		if site.DBEngine == string(dbengine.MySQL) {
			site.MySQLVersion = change.DBVersion
		}
		changed = true
	}

	if !changed {
		return nil
	}

	if err := sm.runHooks(ctx, hooks.PreVersionsChange, site); err != nil {
		return err
	}

	containers := specNames(sm.serviceSpecs(site))
	if err := (&sitesteps.RemoveContainersStep{Engine: sm.d, Containers: containers}).Apply(ctx); err != nil {
		slog.Error("Failed to remove old containers for version swap: " + err.Error())
	}

	if _, err := sm.st.UpdateSite(site); err != nil {
		return fmt.Errorf("updating site: %w", err)
	}

	if sm.OnSiteUpdated != nil {
		sm.OnSiteUpdated(site)
	}
	sm.writeConfigYAML(site)
	return sm.runHooks(ctx, hooks.PostVersionsChange, site)
}

func (sm *SiteManager) OpenSiteURL(siteID string) error {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return fmt.Errorf("site %q not found", siteID)
	}
	return utils.OpenURL("https://" + site.Domain)
}

// UpdatePublicDir changes the public directory for a stopped site.
func (sm *SiteManager) UpdatePublicDir(ctx context.Context, siteID, publicDir string) error {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return fmt.Errorf("site %q not found", siteID)
	}
	if site.Started {
		return fmt.Errorf("site must be stopped to change public directory")
	}
	if publicDir == site.PublicDir {
		return nil
	}

	site.PublicDir = publicDir
	if _, err := sm.st.UpdateSite(site); err != nil {
		return fmt.Errorf("updating site: %w", err)
	}
	if sm.OnSiteUpdated != nil {
		sm.OnSiteUpdated(site)
	}
	sm.writeConfigYAML(site)
	return nil
}

// CloneSite duplicates an existing site with a new name, copying files and database.
func (sm *SiteManager) CloneSite(ctx context.Context, siteID, newName string) error {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return fmt.Errorf("site %q not found", siteID)
	}

	mu := sm.siteMutex(siteID)
	mu.Lock()
	defer mu.Unlock()

	if err := sm.runHooks(ctx, hooks.PreClone, site); err != nil {
		return err
	}

	// Best-effort pre-clone snapshot of the source. Provides a recovery
	// point if the clone process touches the source DB unexpectedly. Not
	// fatal: clone is non-destructive of the source, so a snapshot
	// failure (e.g. DB unreachable on a flaky daemon) shouldn't block the
	// clone itself.
	if site.Started {
		if path, err := sm.snapshotLocked(ctx, site, "pre_clone"); err != nil {
			slog.Warn("clone: pre-clone snapshot failed", "site", site.Slug, "err", err.Error())
		} else {
			slog.Info("clone: pre-clone snapshot saved", "path", path)
		}
	}

	newSlug := slug.Make(newName)
	newDomain := newSlug + ".localhost"
	newFilesDir := filepath.Join(filepath.Dir(site.FilesDir), newSlug)

	if err := utils.EnsureDir(newFilesDir); err != nil {
		return fmt.Errorf("creating clone directory: %w", err)
	}
	if err := utils.CopyDir(site.FilesDir, newFilesDir); err != nil {
		return fmt.Errorf("copying site files: %w", err)
	}

	var dbDump string
	if site.Started {
		containerName := docker.SiteContainerName(site.Slug, "database")
		dump, err := sm.d.ExecInContainer(ctx, containerName, []string{
			"mysqldump", "-u", "wordpress", "-p" + site.DBPassword, "wordpress",
		})
		if err != nil {
			slog.Warn("Could not dump database during clone: " + err.Error())
		} else {
			dbDump = dump
		}
	}

	newSite := types.Site{
		ID:            uuid.NewString(),
		Name:          newName,
		Slug:          newSlug,
		Domain:        newDomain,
		FilesDir:      newFilesDir,
		PublicDir:     site.PublicDir,
		Started:       false,
		PHPVersion:    site.PHPVersion,
		DBEngine:      site.DBEngine,
		DBVersion:     site.DBVersion,
		MySQLVersion:  site.MySQLVersion,
		RedisVersion:  site.RedisVersion,
		WebServer:     site.WebServer,
		Multisite:     site.Multisite,
		PublishDBPort: site.PublishDBPort,
		DBPassword:    generatePassword(16),
	}

	if err := sm.st.AddSite(&newSite); err != nil {
		return fmt.Errorf("adding cloned site to database: %w", err)
	}

	// StartSite acquires its own per-site mutex (newSite.ID).
	if err := sm.StartSite(ctx, newSite.ID); err != nil {
		return fmt.Errorf("starting cloned site: %w", err)
	}

	if dbDump != "" {
		// MySQL needs a moment to accept connections after first start.
		time.Sleep(5 * time.Second)

		dumpPath := filepath.Join(newFilesDir, "locorum-clone-dump.sql")
		if err := os.WriteFile(dumpPath, []byte(dbDump), 0o666); err != nil {
			slog.Warn("Failed to write clone dump file: " + err.Error())
		} else {
			if _, err := sm.wpDBImport(ctx, &newSite, "/var/www/html/locorum-clone-dump.sql"); err != nil {
				slog.Warn("DB import failed during clone: " + err.Error())
			}
			if _, err := sm.wpSearchReplace(ctx, &newSite, "https://"+site.Domain, "https://"+newDomain); err != nil {
				slog.Warn("Search-replace failed during clone: " + err.Error())
			}
			os.Remove(dumpPath)
		}
	}

	sm.emitSitesUpdate()
	return sm.runHooks(ctx, hooks.PostClone, site)
}

// CheckLinks crawls a running site and reports broken links via the onProgress callback.
func (sm *SiteManager) CheckLinks(siteID string, onProgress func(string), onDone func()) error {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return fmt.Errorf("site %q not found", siteID)
	}
	if !site.Started {
		return fmt.Errorf("site must be running")
	}

	go func() {
		defer onDone()
		sm.runLinkCheck(site, onProgress)
	}()
	return nil
}

// PublishedDBHostPort returns the ephemeral TCP port the database
// container is published on, or 0 if PublishDBPort is off / the site is
// not running. Used by the DB Credentials panel to render a copyable
// connection URL.
func (sm *SiteManager) PublishedDBHostPort(ctx context.Context, siteID string) (int, error) {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return 0, fmt.Errorf("fetching site: %w", err)
	}
	if site == nil || !site.PublishDBPort || !site.Started {
		return 0, nil
	}
	eng := dbengine.Resolve(site)
	return sm.d.PublishedHostPort(ctx, docker.SiteContainerName(site.Slug, "database"), eng.DefaultPort())
}

// ConnectionURL returns the engine-formatted connection URL for the
// site's DB container at the given host port. Empty port returns a URL
// with a placeholder so the GUI can show a "click Publish to enable"
// state.
func (sm *SiteManager) ConnectionURL(siteID string, hostPort int) (string, error) {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return "", fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return "", fmt.Errorf("site %q not found", siteID)
	}
	eng := dbengine.Resolve(site)
	port := ""
	if hostPort > 0 {
		port = fmt.Sprintf("%d", hostPort)
	}
	return eng.ConnectionURL("127.0.0.1", port, site), nil
}

// SetPublishDBPort flips the per-site publish flag. Site must be
// stopped — toggling while running would change the container's spec
// hash and force an unrequested recreate.
func (sm *SiteManager) SetPublishDBPort(siteID string, on bool) error {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return fmt.Errorf("site %q not found", siteID)
	}
	if site.Started {
		return fmt.Errorf("site must be stopped to change DB host-port publish")
	}
	site.PublishDBPort = on
	if _, err := sm.st.UpdateSite(site); err != nil {
		return err
	}
	sm.writeConfigYAML(site)
	return nil
}

// spxKeyByteLen is the size of the random source for an SPX_KEY before
// base64-encoding. 32 bytes → 43 chars of RawURLEncoding, well above
// the brute-force horizon for any local-dev attacker.
const spxKeyByteLen = 32

// generateSPXKey returns a fresh base64.RawURLEncoding-encoded random
// secret suitable for use as SPX_KEY. crypto/rand failure is treated
// as fatal at the call site — site operations cannot proceed without
// a usable secret, and falling back to a constant would silently
// weaken security.
func generateSPXKey() (string, error) {
	b := make([]byte, spxKeyByteLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate spx key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// SetSPXEnabled persists the SPX-profiler toggle for siteID. Site must
// be stopped — same constraint as SetPublishDBPort, for the same
// reason: toggling SPX changes the PHP container's spec hash and
// would otherwise force an unrequested mid-flight recreate.
//
// The first time SPX is enabled for a site, a 32-byte URL-safe random
// SPX_KEY is generated and persisted. Subsequent toggles preserve the
// key so bookmarked profile URLs keep working across enable/disable
// cycles. Use RotateSPXKey to deliberately invalidate it.
//
// Emits OnSiteUpdated on success so the GUI redraws.
func (sm *SiteManager) SetSPXEnabled(siteID string, enabled bool) error {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return fmt.Errorf("site %q not found", siteID)
	}

	mu := sm.siteMutex(siteID)
	mu.Lock()
	defer mu.Unlock()

	if site.Started {
		return fmt.Errorf("site must be stopped to change SPX profiling")
	}
	if site.SPXEnabled == enabled && (!enabled || site.SPXKey != "") {
		return nil
	}

	site.SPXEnabled = enabled
	if enabled && site.SPXKey == "" {
		key, err := generateSPXKey()
		if err != nil {
			return err
		}
		site.SPXKey = key
	}

	if _, err := sm.st.UpdateSite(site); err != nil {
		return fmt.Errorf("updating site: %w", err)
	}

	sm.writeConfigYAML(site)
	if sm.OnSiteUpdated != nil {
		sm.OnSiteUpdated(site)
	}
	return nil
}

// RotateSPXKey replaces the site's SPX_KEY with a fresh random value.
// Site must be stopped (the new key only takes effect on the next
// start). Safe to call when SPX is currently disabled — the rotated
// key is persisted and used the next time SPX is enabled. Old
// bookmarked URLs stop working immediately.
func (sm *SiteManager) RotateSPXKey(siteID string) error {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return fmt.Errorf("site %q not found", siteID)
	}

	mu := sm.siteMutex(siteID)
	mu.Lock()
	defer mu.Unlock()

	if site.Started {
		return fmt.Errorf("site must be stopped to rotate SPX key")
	}

	key, err := generateSPXKey()
	if err != nil {
		return err
	}
	site.SPXKey = key

	if _, err := sm.st.UpdateSite(site); err != nil {
		return fmt.Errorf("updating site: %w", err)
	}

	sm.writeConfigYAML(site)
	if sm.OnSiteUpdated != nil {
		sm.OnSiteUpdated(site)
	}
	return nil
}

// SPXReport is one entry in the per-site Profiling-panel report list.
// Path is absolute on the host so the GUI can hand it to OpenPath.
type SPXReport struct {
	Name string
	Path string
	Size int64
	Time time.Time
}

// ListSPXReports returns the SPX profile-data files for a site,
// newest-first. Reads the per-site bind-mount source directory
// directly — no Docker exec — so the call works while the site is
// stopped and is cheap enough to call from the GUI on tab activation.
//
// Missing directory is not an error: returns an empty slice. This is
// the steady-state for a site that has never enabled SPX, or has
// enabled it and not yet captured a report.
func (sm *SiteManager) ListSPXReports(siteID string) ([]SPXReport, error) {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return nil, fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return nil, fmt.Errorf("site %q not found", siteID)
	}

	dir := filepath.Join(site.FilesDir, ".locorum", "spx")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read spx dir: %w", err)
	}

	out := make([]SPXReport, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, SPXReport{
			Name: e.Name(),
			Path: filepath.Join(dir, e.Name()),
			Size: info.Size(),
			Time: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

// ClearSPXReports removes every file in the site's SPX data directory.
// Used by the "Clear all" button on the Profiling panel. Best-effort:
// per-file remove failures are aggregated into the returned error so
// the user knows something is left, but the loop keeps going.
func (sm *SiteManager) ClearSPXReports(siteID string) error {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return fmt.Errorf("site %q not found", siteID)
	}

	dir := filepath.Join(site.FilesDir, ".locorum", "spx")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read spx dir: %w", err)
	}
	var firstErr error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if err := os.Remove(p); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("remove %s: %w", p, err)
		}
	}
	return firstErr
}

// ExecWPCLI runs a WP-CLI command inside the site's PHP container.
func (sm *SiteManager) ExecWPCLI(ctx context.Context, siteID string, args []string) (string, error) {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return "", fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return "", fmt.Errorf("site %q not found", siteID)
	}

	containerName := docker.SiteContainerName(site.Slug, "php")
	cmd := append([]string{"wp"}, args...)
	output, err := sm.d.ExecInContainer(ctx, containerName, cmd)
	if err != nil {
		return output, fmt.Errorf("wp-cli: %w", err)
	}
	return strings.TrimRight(output, "\n"), nil
}
