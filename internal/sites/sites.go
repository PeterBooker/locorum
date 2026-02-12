package sites

import (
	"embed"
	"html/template"
	"log/slog"
	"os"
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

	// Only include started sites in the nginx map.
	var startedSites []types.Site
	for _, s := range sites {
		if s.Started {
			startedSites = append(startedSites, s)
		}
	}

	err = sm.generateMapConfig(startedSites, path.Join(sm.homeDir, ".locorum", "config", "nginx", "map.conf"), testConfig)
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

	sm.emitSitesUpdate()
	return nil
}

func (sm *SiteManager) DeleteSite(id string) error {
	site, err := sm.st.GetSite(id)
	if err != nil {
		slog.Error("Failed to fetch site: " + err.Error())
		return err
	}

	// Clean up Docker resources if they exist.
	if site != nil {
		exists, _ := sm.d.SiteContainersExist(site)
		if exists {
			if err := sm.d.DeleteSite(site); err != nil {
				slog.Error("Failed to remove site containers: " + err.Error())
			}
		}

		// Remove site nginx config file.
		configPath := path.Join(sm.homeDir, ".locorum", "config", "nginx", "sites", site.Slug+".conf")
		os.Remove(configPath)
	}

	if err := sm.st.DeleteSite(id); err != nil {
		return err
	}

	// Regenerate nginx map and reload to remove the deleted site's routing.
	if err := sm.RegenerateGlobalNginxMap(true); err != nil {
		slog.Error("Failed to regenerate nginx map after delete: " + err.Error())
	}

	sm.emitSitesUpdate()
	return nil
}

// emitSitesUpdate notifies the UI that the site list has changed.
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

func (sm *SiteManager) StartSite(id string) error {
	site, err := sm.st.GetSite(id)
	if err != nil {
		slog.Error("Failed to fetch site: " + err.Error())
		return err
	}

	// Generate per-site nginx config.
	err = sm.generateSiteConfig(site, path.Join(sm.homeDir, ".locorum", "config", "nginx", "sites", site.Slug+".conf"))
	if err != nil {
		slog.Error("Failed to generate site nginx config: " + err.Error())
		return err
	}

	// Check if containers already exist (e.g., from a previous start in this session).
	exists, err := sm.d.SiteContainersExist(site)
	if err != nil {
		slog.Error("Failed to check if site containers exist: " + err.Error())
		return err
	}

	if exists {
		// Containers exist, just start them.
		err = sm.d.StartExistingSite(site)
		if err != nil {
			slog.Error("Failed to start existing containers: " + err.Error())
			return err
		}
	} else {
		// First start — create containers.
		err = sm.d.CreateSite(site, sm.homeDir)
		if err != nil {
			slog.Error("Failed to create containers: " + err.Error())
			return err
		}
	}

	site.Started = true

	_, err = sm.st.UpdateSite(site)
	if err != nil {
		slog.Error("Failed to update site: " + err.Error())
		return err
	}

	// Regenerate nginx map to include this site and reload.
	if err := sm.RegenerateGlobalNginxMap(true); err != nil {
		slog.Error("Error regenerating global nginx map: " + err.Error())
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

	err = sm.d.StopSite(site)
	if err != nil {
		slog.Error("Failed to stop containers: " + err.Error())
		return err
	}

	site.Started = false

	_, err = sm.st.UpdateSite(site)
	if err != nil {
		slog.Error("Failed to update site: " + err.Error())
		return err
	}

	// Regenerate nginx map to remove this site's routing and reload.
	if err := sm.RegenerateGlobalNginxMap(true); err != nil {
		slog.Error("Error regenerating global nginx map: " + err.Error())
	}

	if sm.OnSiteUpdated != nil {
		sm.OnSiteUpdated(site)
	}

	return nil
}

// ReconcileState marks all sites as stopped in the database.
// Called on startup after Initialize() has cleaned up all containers.
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
