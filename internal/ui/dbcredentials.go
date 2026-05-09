package ui

import (
	"context"
	"fmt"
	"sync"

	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/types"
)

// dbCredField holds the state for a single copyable database credential row.
type dbCredField struct {
	sel  widget.Selectable
	copy widget.Clickable
}

// DBCredentials renders the Database section with selectable values, copy
// buttons, and an opt-in host-port publish toggle so the user can connect
// from desktop DB clients.
type DBCredentials struct {
	creds      [7]dbCredField
	publishBtn widget.Clickable
	sm         *sites.SiteManager
	state      *UIState
	toasts     *Notifications

	mu             sync.Mutex
	cachedSiteID   string
	cachedPort     int
	publishLoading bool
}

func NewDBCredentials() *DBCredentials {
	return &DBCredentials{}
}

// Bind stores the SiteManager + UI bridges DBCredentials needs for the
// publish-port flow. Called from NewSiteDetail after construction so the
// existing zero-arg NewDBCredentials still compiles.
func (dc *DBCredentials) Bind(sm *sites.SiteManager, state *UIState, toasts *Notifications) {
	dc.sm = sm
	dc.state = state
	dc.toasts = toasts
}

// credItems returns the list of credential rows shown in the Database
// section.
func (dc *DBCredentials) credItems(site *types.Site) []KV {
	rows := []KV{
		{"Hostname", "database"},
		{"Adminer Host", "locorum-" + site.Slug + "-database"},
		{"Database", "wordpress"},
		{"User", "wordpress"},
		{"Password", site.DBPassword},
	}
	if site.PublishDBPort {
		port := dc.cachedHostPort(site.ID)
		portStr := "—"
		urlStr := ""
		if port > 0 {
			portStr = fmt.Sprintf("127.0.0.1:%d", port)
			if dc.sm != nil {
				if u, err := dc.sm.ConnectionURL(site.ID, port); err == nil {
					urlStr = u
				}
			}
		} else if site.Started {
			portStr = "(starting…)"
		}
		rows = append(rows, KV{"Host Port", portStr})
		if urlStr != "" {
			rows = append(rows, KV{"Connection URL", urlStr})
		}
	}
	return rows
}

func (dc *DBCredentials) cachedHostPort(siteID string) int {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if dc.cachedSiteID == siteID {
		return dc.cachedPort
	}
	return 0
}

// HandleUserInteractions processes per-row Copy button clicks + the
// publish-port toggle.
func (dc *DBCredentials) HandleUserInteractions(gtx layout.Context, site *types.Site) {
	items := dc.credItems(site)
	for i, item := range items {
		if i >= len(dc.creds) {
			break
		}
		if dc.creds[i].copy.Clicked(gtx) {
			CopyToClipboard(gtx, item.Value)
		}
	}

	// Refresh the cached host port lazily — once per site, plus whenever
	// the panel becomes visible for a different site.
	if dc.sm != nil && site.PublishDBPort && site.Started {
		dc.mu.Lock()
		needFetch := dc.cachedSiteID != site.ID
		dc.mu.Unlock()
		if needFetch {
			go dc.refreshHostPort(site.ID)
		}
	}

	if dc.publishBtn.Clicked(gtx) {
		dc.togglePublish(site)
	}
}

func (dc *DBCredentials) togglePublish(site *types.Site) {
	if dc.sm == nil {
		return
	}
	dc.mu.Lock()
	if dc.publishLoading {
		dc.mu.Unlock()
		return
	}
	dc.publishLoading = true
	dc.mu.Unlock()

	want := !site.PublishDBPort
	if site.Started {
		if dc.toasts != nil {
			dc.toasts.ShowError("Stop the site before changing host-port publishing.")
		}
		dc.mu.Lock()
		dc.publishLoading = false
		dc.mu.Unlock()
		return
	}
	siteID := site.ID
	go func() {
		defer func() {
			dc.mu.Lock()
			dc.publishLoading = false
			dc.mu.Unlock()
		}()
		if err := dc.sm.SetPublishDBPort(siteID, want); err != nil {
			if dc.state != nil {
				dc.state.ShowError("Failed to change publish flag: " + err.Error())
			}
			return
		}
		if dc.toasts != nil {
			dc.toasts.ShowSuccess("Saved. Start the site to apply.")
		}
	}()
}

func (dc *DBCredentials) refreshHostPort(siteID string) {
	if dc.sm == nil {
		return
	}
	port, err := dc.sm.PublishedDBHostPort(context.Background(), siteID)
	if err != nil {
		// Container probably hasn't started yet; silently retry next
		// frame via the cache miss path.
		return
	}
	dc.mu.Lock()
	dc.cachedSiteID = siteID
	dc.cachedPort = port
	dc.mu.Unlock()
	if dc.state != nil {
		dc.state.Invalidate()
	}
}

func (dc *DBCredentials) Layout(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	items := dc.credItems(site)

	return Section(gtx, th, "Database", func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, 0, len(items)+1)
		for i, item := range items {
			idx := i
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						// Key label
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.X = gtx.Dp(th.Dims.LabelColWidth)
							lbl := material.Body2(th.Theme, item.Key)
							lbl.Color = th.Color.TextSecondary
							lbl.TextSize = th.Sizes.Base
							return lbl.Layout(gtx)
						}),
						// Selectable value
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return SelectableLabel(gtx, th, &dc.creds[idx].sel, item.Value, th.Sizes.Base, th.Fg, MonoFont)
						}),
						// Copy button
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return SmallButton(gtx, th, &dc.creds[idx].copy, "Copy")
							})
						}),
					)
				})
			}))
		}

		// Publish toggle row.
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			label := "Publish DB port to host (off)"
			if site.PublishDBPort {
				label = "Publish DB port to host (on)"
			}
			return layout.Inset{Top: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return SecondaryButton(gtx, th, &dc.publishBtn, label)
			})
		}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}
