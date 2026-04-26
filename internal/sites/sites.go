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
	"github.com/PeterBooker/locorum/internal/router"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/types"
	"github.com/PeterBooker/locorum/internal/utils"
)

type SiteManager struct {
	st      *storage.Storage
	cli     *client.Client
	sites   map[string]types.Site
	d       *docker.Docker
	rtr     router.Router
	hooks   hooks.Runner
	homeDir string
	config  embed.FS

	// siteLocks serialises lifecycle calls per site. Created lazily on
	// first use; the outer mutex protects the map itself, the inner mutex
	// protects the lifecycle on a single site. Different sites run in
	// parallel.
	locksMu   sync.Mutex
	siteLocks map[string]*sync.Mutex

	// Callbacks invoked when sites data changes. The UI layer sets these
	// in ui.New() to trigger redraws.
	OnSitesUpdated func(sites []types.Site)
	OnSiteUpdated  func(site *types.Site)

	// Hook-stream callbacks. Set by the UI in ui.New(); fired from
	// goroutines spawned inside runHooks. Implementations are responsible
	// for thread safety + UI invalidation per the existing pattern.
	OnHookTaskStart func(siteID string, hook hooks.Hook)
	OnHookOutput    func(siteID string, line string, stderr bool)
	OnHookTaskDone  func(siteID string, result hooks.Result)
	OnHookAllDone   func(siteID string, summary hooks.Summary)
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
		st:        st,
		cli:       cli,
		d:         d,
		rtr:       rtr,
		hooks:     runner,
		config:    config,
		homeDir:   homeDir,
		sites:     make(map[string]types.Site),
		siteLocks: make(map[string]*sync.Mutex),
	}
}

// siteMutex returns the per-site lifecycle mutex, creating it on first use.
// Different sites get independent mutexes so they can run in parallel; two
// lifecycle calls on the same site queue.
func (sm *SiteManager) siteMutex(siteID string) *sync.Mutex {
	sm.locksMu.Lock()
	defer sm.locksMu.Unlock()
	m, ok := sm.siteLocks[siteID]
	if !ok {
		m = &sync.Mutex{}
		sm.siteLocks[siteID] = m
	}
	return m
}

// runHooks fires every enabled hook for ev/site, forwarding results to the
// UI callbacks. The runner returns nil in warn mode and the task error in
// strict mode; we propagate verbatim — it is the lifecycle method's
// decision whether to short-circuit the rest of its work.
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
// Used by the GUI's "Run now" buttons. Streams via the same OnHook*
// callbacks as runHooks.
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
//
// These thin wrappers let the UI layer talk to the storage's hook CRUD
// without importing storage directly. Mirrors the existing GetSetting /
// SetSetting pattern.

// ListSiteHooks returns every hook attached to siteID.
func (sm *SiteManager) ListSiteHooks(siteID string) ([]hooks.Hook, error) {
	return sm.st.ListHooks(siteID)
}

// AddSiteHook validates h and persists it. h.ID, Position, CreatedAt and
// UpdatedAt are populated on success.
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

func (sm *SiteManager) DeleteSite(ctx context.Context, id string) error {
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
	// lookup still succeeds. Tear-down operations come after.
	if err := sm.runHooks(ctx, hooks.PreDelete, site); err != nil {
		return err
	}

	exists, _ := sm.d.SiteContainersExist(site)
	if exists {
		if err := sm.d.DeleteSite(site); err != nil {
			slog.Error("Failed to remove site containers: " + err.Error())
		}
	}

	os.Remove(path.Join(sm.homeDir, ".locorum", "config", "nginx", "sites", site.Slug+".conf"))
	os.Remove(path.Join(sm.homeDir, ".locorum", "config", "apache", "sites", site.Slug+".conf"))

	if err := sm.rtr.RemoveSite(ctx, site.Slug); err != nil {
		slog.Warn("Failed to remove route on site delete: " + err.Error())
	}

	// post-delete fires AFTER container + route teardown but BEFORE the
	// SQL DELETE: the FK ON DELETE CASCADE would otherwise wipe the
	// site_hooks rows before the runner could enumerate them. Failure here
	// is informational — the destructive work has already happened, so we
	// continue to the SQL DELETE regardless.
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

	ensureWritable(site.FilesDir)

	if err := sm.ensureWordPress(site); err != nil {
		slog.Error("Failed to ensure WordPress: " + err.Error())
		return err
	}

	if site.WebServer == "apache" {
		err = sm.generateApacheSiteConfig(site, path.Join(sm.homeDir, ".locorum", "config", "apache", "sites", site.Slug+".conf"))
	} else {
		err = sm.generateSiteConfig(site, path.Join(sm.homeDir, ".locorum", "config", "nginx", "sites", site.Slug+".conf"))
	}
	if err != nil {
		slog.Error("Failed to generate site web server config: " + err.Error())
		return err
	}

	exists, err := sm.d.SiteContainersExist(site)
	if err != nil {
		slog.Error("Failed to check if site containers exist: " + err.Error())
		return err
	}

	if exists {
		err = sm.d.StartExistingSite(site)
	} else {
		err = sm.d.CreateSite(site, sm.homeDir)
	}
	if err != nil {
		slog.Error("Failed to bring up site containers: " + err.Error())
		return err
	}

	site.Started = true

	if _, err := sm.st.UpdateSite(site); err != nil {
		slog.Error("Failed to update site: " + err.Error())
		return err
	}

	if err := sm.upsertRoute(site); err != nil {
		slog.Error("Failed to register route: " + err.Error())
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

func (sm *SiteManager) upsertRoute(site *types.Site) error {
	route := router.SiteRoute{
		Slug:        site.Slug,
		PrimaryHost: site.Domain,
		Backend:     "http://locorum-" + site.Slug + "-web:80",
	}
	if site.Multisite == "subdomain" {
		route.WildcardHost = "*." + site.Domain
	}
	return sm.rtr.UpsertSite(context.Background(), route)
}

// ensureMultisiteWithHooks fires pre/post-multisite hooks around the
// existing ensureMultisite work. Public lifecycle methods that themselves
// fire pre/post-start expect this wrapper rather than ensureMultisite, so
// users can attach setup commands that run only when multisite conversion
// happens.
func (sm *SiteManager) ensureMultisiteWithHooks(ctx context.Context, site *types.Site) error {
	if err := sm.runHooks(ctx, hooks.PreMultisite, site); err != nil {
		return err
	}
	if err := sm.ensureMultisite(site); err != nil {
		return err
	}
	return sm.runHooks(ctx, hooks.PostMultisite, site)
}

// ensureMultisite converts a WordPress installation to multisite if not
// already configured.
func (sm *SiteManager) ensureMultisite(site *types.Site) error {
	containerName := "locorum-" + site.Slug + "-php"

	if _, err := sm.d.ExecInContainer(containerName, []string{"wp", "core", "is-installed", "--network"}); err == nil {
		return nil
	}

	if _, err := sm.d.ExecInContainer(containerName, []string{"wp", "core", "is-installed"}); err != nil {
		_, err = sm.d.ExecInContainer(containerName, []string{
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

	if _, err := sm.d.ExecInContainer(containerName, args); err != nil {
		return fmt.Errorf("multisite convert: %w", err)
	}

	return nil
}

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

	if err := sm.d.StopSite(site); err != nil {
		slog.Error("Failed to stop containers: " + err.Error())
		return err
	}

	site.Started = false
	if _, err := sm.st.UpdateSite(site); err != nil {
		slog.Error("Failed to update site: " + err.Error())
		return err
	}

	if err := sm.rtr.RemoveSite(ctx, site.Slug); err != nil {
		slog.Warn("Failed to remove route on site stop: " + err.Error())
	}

	if sm.OnSiteUpdated != nil {
		sm.OnSiteUpdated(site)
	}

	return sm.runHooks(ctx, hooks.PostStop, site)
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
func (sm *SiteManager) GetContainerLogs(siteID, service string, lines int) (string, error) {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return "", fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return "", fmt.Errorf("site %q not found", siteID)
	}
	containerName := "locorum-" + site.Slug + "-" + service
	return sm.d.ContainerLogs(containerName, lines)
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

	containerName := "locorum-" + site.Slug + "-php"
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

	exists, _ := sm.d.SiteContainersExist(site)
	if exists {
		if err := sm.d.DeleteSite(site); err != nil {
			slog.Error("Failed to remove old containers for version swap: " + err.Error())
		}
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
		containerName := "locorum-" + site.Slug + "-database"
		dump, err := sm.d.ExecInContainer(containerName, []string{
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

	// StartSite acquires its own per-site mutex (newSite.ID), so we don't
	// recurse on the parent site's lock.
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
			phpContainer := "locorum-" + newSlug + "-php"
			if _, err := sm.d.ExecInContainer(phpContainer, []string{
				"wp", "db", "import", "/var/www/html/locorum-clone-dump.sql",
			}); err != nil {
				slog.Warn("DB import failed during clone: " + err.Error())
			}
			_, _ = sm.d.ExecInContainer(phpContainer, []string{
				"wp", "search-replace",
				"https://" + site.Domain, "https://" + newDomain,
				"--all-tables",
			})
			os.Remove(dumpPath)
		}
	}

	sm.emitSitesUpdate()
	// post-clone fires on the SOURCE site so users can attach commands like
	// "rename the cloned files dir" or "rsync to a CI bucket". The clone
	// itself runs its own pre-/post-start chain via StartSite above.
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
func (sm *SiteManager) ExecWPCLI(siteID string, args []string) (string, error) {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return "", fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return "", fmt.Errorf("site %q not found", siteID)
	}

	containerName := "locorum-" + site.Slug + "-php"
	cmd := append([]string{"wp"}, args...)
	output, err := sm.d.ExecInContainer(containerName, cmd)
	if err != nil {
		return output, fmt.Errorf("wp-cli: %w", err)
	}
	return strings.TrimRight(output, "\n"), nil
}
