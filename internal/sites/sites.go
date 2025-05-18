package sites

import (
	"context"
	"embed"
	"html/template"
	"os"
	"path"

	"github.com/docker/docker/client"
	"github.com/gosimple/slug"
	rt "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/types"
)

type Site struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type SiteManager struct {
	st     *storage.Storage
	cli    *client.Client
	sites  map[string]Site
	ctx    context.Context
	d      *docker.Docker
	config embed.FS
}

func NewSiteManager(st *storage.Storage, cli *client.Client, d *docker.Docker, config embed.FS) *SiteManager {
	mapTpl = template.Must(
		template.New("sites.map.tmpl").
			Funcs(funcMap).
			ParseFS(config, "config/nginx/snippets/sites.map.tmpl"),
	)
	upsTpl = template.Must(
		template.New("sites.upstreams.tmpl").
			Funcs(funcMap).
			ParseFS(config, "config/nginx/snippets/sites.upstreams.tmpl"),
	)

	return &SiteManager{
		st:     st,
		cli:    cli,
		d:      d,
		config: config,
		sites:  make(map[string]Site),
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

	home, _ := os.UserHomeDir()

	err = sm.regenerateSnippets(sites, path.Join(home, ".locorum", "config", "nginx", "snippets", "sites.map.conf"), path.Join(home, ".locorum", "config", "nginx", "snippets", "sites.upstreams.conf"))
	if err != nil {
		rt.LogError(sm.ctx, "Failed to regenerate nginx snippets: "+err.Error())
		return
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
