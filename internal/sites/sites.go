package sites

import (
	"embed"
	"html/template"
	"log/slog"
	"path"

	"github.com/docker/docker/client"
	"github.com/google/uuid"
	"github.com/gosimple/slug"
	"github.com/sqweek/dialog"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/types"
	"github.com/PeterBooker/locorum/internal/utils"
)

type SiteManager struct {
	st      *storage.Storage
	cli     *client.Client
	sites   map[string]types.Site
	d       *docker.Docker
	homeDir string
	config  embed.FS

	// Callbacks invoked when sites data changes.
	// The UI layer sets these to trigger redraws.
	OnSitesUpdated func(sites []types.Site)
	OnSiteUpdated  func(site *types.Site)
}

func NewSiteManager(st *storage.Storage, cli *client.Client, d *docker.Docker, config embed.FS, homeDir string) *SiteManager {
	siteTpl = template.Must(
		template.New("site.tmpl").
			Funcs(funcMap).
			ParseFS(config, "config/nginx/site.tmpl"),
	)

	mapTpl = template.Must(
		template.New("map.tmpl").
			Funcs(funcMap).
			ParseFS(config, "config/nginx/map.tmpl"),
	)

	return &SiteManager{
		st:      st,
		cli:     cli,
		d:       d,
		config:  config,
		homeDir: homeDir,
		sites:   make(map[string]types.Site),
	}
}

func (sm *SiteManager) RegenerateGlobalNginxMap(testConfig bool) error {
	sites, err := sm.GetSites()
	if err != nil {
		slog.Error("Failed to get sites: " + err.Error())
		return err
	}

	err = sm.generateMapConfig(sites, path.Join(sm.homeDir, ".locorum", "config", "nginx", "map.conf"), testConfig)
	if err != nil {
		slog.Error("Failed to create global nginx map: " + err.Error())
		return err
	}

	return nil
}

func (sm *SiteManager) GetSites() ([]types.Site, error) {
	return sm.st.GetSites()
}

func (sm *SiteManager) GetSite(id string) (*types.Site, error) {
	site, err := sm.st.GetSite(id)
	if err != nil {
		slog.Error("Failed to fetch site: " + err.Error())
		return nil, err
	}

	return site, nil
}

func (sm *SiteManager) AddSite(site types.Site) error {
	site.ID = uuid.NewString()
	site.Slug = slug.Make(site.Name)
	site.Domain = slug.Make(site.Name) + ".localhost"
	site.Started = false

	err := utils.EnsureDir(site.FilesDir)
	if err != nil {
		slog.Error("Failed to create site directory: " + err.Error())
		return err
	}

	if err := sm.st.AddSite(&site); err != nil {
		return err
	}

	sm.emitUpdate()
	return nil
}

func (sm *SiteManager) DeleteSite(id string) error {
	if err := sm.st.DeleteSite(id); err != nil {
		return err
	}

	sm.emitUpdate()
	return nil
}

func (sm *SiteManager) emitUpdate() {
	sites, err := sm.st.GetSites()
	if err != nil {
		slog.Error("Failed to get sites: " + err.Error())
		return
	}

	err = sm.d.TestGlobalNginxConfig()
	if err != nil {
		slog.Error("Failed to test nginx config: " + err.Error())
		return
	}

	err = sm.d.ReloadGlobalNginx()
	if err != nil {
		slog.Error("Failed to reload nginx config: " + err.Error())
		return
	}

	if sm.OnSitesUpdated != nil {
		sm.OnSitesUpdated(sites)
	}
}

func (sm *SiteManager) StartSite(id string) error {
	site, err := sm.st.GetSite(id)
	if err != nil {
		slog.Error("Failed to fetch site: " + err.Error())
		return err
	}

	err = sm.RegenerateGlobalNginxMap(true)
	if err != nil {
		slog.Error("Error regenerating global nginx map: " + err.Error())
		return err
	}

	err = sm.generateSiteConfig(site, path.Join(sm.homeDir, ".locorum", "config", "nginx", "sites", site.Slug+".conf"))
	if err != nil {
		slog.Error("Failed to add new sites nginx config: " + err.Error())
		return err
	}

	err = sm.d.CreateSite(site, sm.homeDir)
	if err != nil {
		slog.Error("Failed to create containers: " + err.Error())
		return err
	}

	site.Started = true

	_, err = sm.st.UpdateSite(site)
	if err != nil {
		slog.Error("Failed to update site: " + err.Error())
		return err
	}

	if sm.OnSiteUpdated != nil {
		sm.OnSiteUpdated(site)
	}

	return nil
}

func (sm *SiteManager) StopSite(id string) error {
	site, err := sm.st.GetSite(id)
	if err != nil {
		slog.Error("Failed to fetch site: " + err.Error())
		return err
	}

	err = sm.d.RemoveSite(site)
	if err != nil {
		slog.Error("Failed to remove containers: " + err.Error())
		return err
	}

	site.Started = false

	_, err = sm.st.UpdateSite(site)
	if err != nil {
		slog.Error("Failed to update site: " + err.Error())
		return err
	}

	if sm.OnSiteUpdated != nil {
		sm.OnSiteUpdated(site)
	}

	return nil
}

// OpenSiteFilesDir opens the directory for the specified site.
func (sm *SiteManager) OpenSiteFilesDir(id string) error {
	site, err := sm.st.GetSite(id)
	if err != nil {
		slog.Error("Failed to fetch site: " + err.Error())
		return err
	}

	err = utils.OpenDirectory(site.FilesDir)
	if err != nil {
		slog.Error("Failed to open site files directory: " + err.Error())
		return err
	}

	return nil
}

// PickDirectory opens a native folder-picker and returns the selected path.
func (sm *SiteManager) PickDirectory() (string, error) {
	dir, err := dialog.Directory().Title("Select a folder").Browse()
	if err != nil {
		return "", err
	}

	return dir, nil
}
