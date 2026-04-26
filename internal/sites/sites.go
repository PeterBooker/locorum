package sites

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/client"
	"github.com/google/uuid"
	"github.com/gosimple/slug"
	"github.com/sqweek/dialog"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/orch"
	"github.com/PeterBooker/locorum/internal/router"
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
}

func NewSiteManager(st *storage.Storage, cli *client.Client, d *docker.Docker, rtr router.Router, runner hooks.Runner, config embed.FS, homeDir string) *SiteManager {
	siteTpl = template.Must(
		template.New("site.tmpl").
			Funcs(funcMap).
			ParseFS(config, "config/nginx/site.tmpl"),
	)

	apacheSiteTpl = template.Must(
		template.New("site.tmpl").
			Funcs(funcMap).
			ParseFS(config, "config/apache/site.tmpl"),
	)

	return &SiteManager{
		st:      st,
		cli:     cli,
		d:       d,
		rtr:     rtr,
		hooks:   runner,
		config:  config,
		homeDir: homeDir,
		sites:   make(map[string]types.Site),
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

// AddSiteHook validates h and persists it.
func (sm *SiteManager) AddSiteHook(h *hooks.Hook) error {
	return sm.st.AddHook(h)
}

// UpdateSiteHook persists changes to an existing hook.
func (sm *SiteManager) UpdateSiteHook(h *hooks.Hook) error {
	return sm.st.UpdateHook(h)
}

// DeleteSiteHook removes a hook by id.
func (sm *SiteManager) DeleteSiteHook(id int64) error {
	return sm.st.DeleteHook(id)
}

// ReorderSiteHooks atomically rewrites positions for an event.
func (sm *SiteManager) ReorderSiteHooks(siteID string, ev hooks.Event, ids []int64) error {
	return sm.st.ReorderHooks(siteID, ev, ids)
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

	if err := utils.EnsureDir(site.FilesDir); err != nil {
		slog.Error("Failed to create site directory: " + err.Error())
		return err
	}

	if err := sm.st.AddSite(&site); err != nil {
		return err
	}

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
			&sitesteps.FuncStep{
				Label: "ensure-wordpress",
				Do: func(_ context.Context) error {
					return sm.ensureWordPress(site)
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
			&sitesteps.RegisterRoutesStep{
				Router: sm.rtr,
				Route:  sm.routeFor(site),
			},
		},
	}

	res := sm.runPlan(ctx, site.ID, plan)
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
// web, php, database, redis.
func (sm *SiteManager) serviceSpecs(site *types.Site) []docker.ContainerSpec {
	return []docker.ContainerSpec{
		docker.WebSpec(site, sm.homeDir),
		docker.PHPSpec(site, sm.homeDir),
		docker.DatabaseSpec(site, sm.homeDir),
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
func (sm *SiteManager) runPlan(ctx context.Context, siteID string, plan orch.Plan) orch.Result {
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
	containerName := docker.SiteContainerName(site.Slug, "php")

	if _, err := sm.d.ExecInContainer(ctx, containerName, []string{"wp", "core", "is-installed", "--network"}); err == nil {
		return nil
	}

	if _, err := sm.d.ExecInContainer(ctx, containerName, []string{"wp", "core", "is-installed"}); err != nil {
		_, err = sm.d.ExecInContainer(ctx, containerName, []string{
			"wp", "core", "install",
			"--url=https://" + site.Domain,
			"--title=" + site.Name,
			"--admin_user=admin",
			"--admin_password=admin",
			"--admin_email=admin@" + site.Domain,
			"--skip-email",
		})
		if err != nil {
			return fmt.Errorf("wp core install: %w", err)
		}
	}

	args := []string{"wp", "core", "multisite-convert", "--title=" + site.Name}
	if site.Multisite == "subdomain" {
		args = append(args, "--subdomains")
	}

	if _, err := sm.d.ExecInContainer(ctx, containerName, args); err != nil {
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
	res := sm.runPlan(ctx, site.ID, plan)
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

	steps := []orch.Step{
		&sitesteps.StopContainersStep{Engine: sm.d, Containers: containers},
		&sitesteps.RemoveContainersStep{Engine: sm.d, Containers: containers},
		&sitesteps.RemoveRoutesStep{Router: sm.rtr, Slug: site.Slug},
		&sitesteps.RemoveSiteConfigsStep{HomeDir: sm.homeDir, Site: site},
		&sitesteps.RemoveNetworkStep{Engine: sm.d, Site: site},
	}
	if opts.PurgeVolume {
		steps = append(steps, &sitesteps.PurgeVolumeStep{Engine: sm.d, Site: site})
	}

	plan := orch.Plan{
		Name:  "delete-site:" + site.Slug,
		Steps: steps,
	}
	res := sm.runPlan(ctx, site.ID, plan)
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

// UpdateSiteVersions changes PHP/MySQL/Redis versions for a stopped site
// and removes old containers so they are recreated on next start with the
// new images.
func (sm *SiteManager) UpdateSiteVersions(ctx context.Context, siteID, phpVer, mysqlVer, redisVer string) error {
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
	if phpVer != "" && phpVer != site.PHPVersion {
		site.PHPVersion = phpVer
		changed = true
	}
	if mysqlVer != "" && mysqlVer != site.MySQLVersion {
		site.MySQLVersion = mysqlVer
		changed = true
	}
	if redisVer != "" && redisVer != site.RedisVersion {
		site.RedisVersion = redisVer
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
		ID:           uuid.NewString(),
		Name:         newName,
		Slug:         newSlug,
		Domain:       newDomain,
		FilesDir:     newFilesDir,
		PublicDir:    site.PublicDir,
		Started:      false,
		PHPVersion:   site.PHPVersion,
		MySQLVersion: site.MySQLVersion,
		RedisVersion: site.RedisVersion,
		WebServer:    site.WebServer,
		Multisite:    site.Multisite,
		DBPassword:   generatePassword(16),
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
		if err := os.WriteFile(dumpPath, []byte(dbDump), 0666); err != nil {
			slog.Warn("Failed to write clone dump file: " + err.Error())
		} else {
			phpContainer := docker.SiteContainerName(newSlug, "php")
			if _, err := sm.d.ExecInContainer(ctx, phpContainer, []string{
				"wp", "db", "import", "/var/www/html/locorum-clone-dump.sql",
			}); err != nil {
				slog.Warn("DB import failed during clone: " + err.Error())
			}
			_, _ = sm.d.ExecInContainer(ctx, phpContainer, []string{
				"wp", "search-replace",
				"https://" + site.Domain, "https://" + newDomain,
				"--all-tables",
			})
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
