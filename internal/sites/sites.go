package sites

import (
	"context"
	"embed"
	"html/template"
	"path"

	"github.com/docker/docker/client"
	"github.com/gosimple/slug"
	rt "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/types"
	"github.com/PeterBooker/locorum/internal/utils"
)

type Site struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type SiteManager struct {
	st      *storage.Storage
	cli     *client.Client
	sites   map[string]Site
	ctx     context.Context
	d       *docker.Docker
	homeDir string
	config  embed.FS
}

func NewSiteManager(st *storage.Storage, cli *client.Client, d *docker.Docker, config embed.FS, homeDir string) *SiteManager {
	siteTpl = template.Must(
		template.New("site.tmpl").
			Funcs(funcMap).
			ParseFS(config, "config/nginx/site.tmpl"),
	)

	return &SiteManager{
		st:      st,
		cli:     cli,
		d:       d,
		config:  config,
		homeDir: homeDir,
		sites:   make(map[string]Site),
	}
}

func (sm *SiteManager) SetContext(ctx context.Context) {
	sm.ctx = ctx
}

func (sm *SiteManager) GetSites() ([]types.Site, error) {
	return sm.st.GetSites()
}

func (sm *SiteManager) AddSite(site types.Site) error {
	site.Slug = slug.Make(site.Name)
	site.Domain = slug.Make(site.Name) + ".local"

	err := utils.EnsureDir(path.Join(sm.homeDir, "locorum", "sites", site.Slug))
	if err != nil {
		rt.LogError(sm.ctx, "Failed to create site directory: "+err.Error())
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
		rt.LogError(sm.ctx, "Failed to get sites: "+err.Error())
		return
	}

	for _, site := range sites {
		err = sm.regenerateSiteConfig(site, path.Join(sm.homeDir, ".locorum", "config", "nginx", "sites-enabled", site.Slug+".conf"))
		if err != nil {
			rt.LogError(sm.ctx, "Failed to regenerate nginx snippets: "+err.Error())
			return
		}
	}

	err = sm.d.TestGlobalNginxConfig()
	if err != nil {
		rt.LogError(sm.ctx, "Failed to test nginx config: "+err.Error())
		return
	}

	err = sm.d.ReloadGlobalNginx()
	if err != nil {
		rt.LogError(sm.ctx, "Failed to reload nginx config: "+err.Error())
		return
	}

	rt.EventsEmit(sm.ctx, "sitesUpdated", sites)
}

func (sm *SiteManager) StartSite(id string) error {
	site, err := sm.st.GetSite(id)
	if err != nil {
		return err
	}

	sm.d.CreateSite(site.Slug)

	return nil
}
