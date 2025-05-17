package sites

import (
	"context"

	"github.com/google/uuid"
	rt "github.com/wailsapp/wails/v2/pkg/runtime"
)

type Site struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type SiteManager struct {
	sites map[string]Site
	ctx   context.Context
}

func NewSiteManager() *SiteManager {
	return &SiteManager{
		sites: make(map[string]Site),
	}
}

func (sm *SiteManager) SetContext(ctx context.Context) {
	sm.ctx = ctx
}

func (sm *SiteManager) GetSites() []Site {
	sites := make([]Site, 0, len(sm.sites))
	for _, site := range sm.sites {
		sites = append(sites, site)
	}
	return sites
}

func (sm *SiteManager) AddSite(site Site) {
	site.ID = uuid.NewString()
	sm.sites[site.ID] = site
	sm.emitUpdate()
}

func (sm *SiteManager) DeleteSite(id string) {
	delete(sm.sites, id)
	sm.emitUpdate()
}

func (sm *SiteManager) emitUpdate() {
	rt.EventsEmit(sm.ctx, "sitesUpdated", sm.GetSites())
}
