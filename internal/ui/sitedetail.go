package ui

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/types"
)

type SiteDetail struct {
	ui        *UI
	startBtn  widget.Clickable
	stopBtn   widget.Clickable
	openFiles widget.Clickable
	list      widget.List
}

func NewSiteDetail(ui *UI) *SiteDetail {
	sd := &SiteDetail{ui: ui}
	sd.list.List.Axis = layout.Vertical
	return sd
}

func (sd *SiteDetail) Layout(gtx layout.Context, th *material.Theme) layout.Dimensions {
	sd.ui.State.mu.Lock()
	site := sd.ui.State.SelectedSite()
	sd.ui.State.mu.Unlock()

	if site == nil {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			lbl := material.H6(th, "Select a site from the sidebar")
			lbl.Color = ColorGray400
			return lbl.Layout(gtx)
		})
	}

	// Handle button clicks
	sd.handleClicks(gtx, site)

	return layout.Inset{
		Top:    unit.Dp(24),
		Bottom: unit.Dp(24),
		Left:   unit.Dp(32),
		Right:  unit.Dp(32),
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return material.List(th, &sd.list).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
			return sd.layoutContent(gtx, th, site)
		})
	})
}

func (sd *SiteDetail) handleClicks(gtx layout.Context, site *types.Site) {
	if sd.startBtn.Clicked(gtx) {
		siteID := site.ID
		sd.ui.State.mu.Lock()
		sd.ui.State.SiteToggling[siteID] = true
		sd.ui.State.mu.Unlock()
		sd.ui.State.Invalidate()

		go func() {
			if err := sd.ui.SM.StartSite(siteID); err != nil {
				sd.ui.State.ShowError("Failed to start site: " + err.Error())
			}
			sd.ui.State.mu.Lock()
			sd.ui.State.SiteToggling[siteID] = false
			sd.ui.State.mu.Unlock()
			sd.ui.State.Invalidate()
		}()
	}

	if sd.stopBtn.Clicked(gtx) {
		siteID := site.ID
		sd.ui.State.mu.Lock()
		sd.ui.State.SiteToggling[siteID] = true
		sd.ui.State.mu.Unlock()
		sd.ui.State.Invalidate()

		go func() {
			if err := sd.ui.SM.StopSite(siteID); err != nil {
				sd.ui.State.ShowError("Failed to stop site: " + err.Error())
			}
			sd.ui.State.mu.Lock()
			sd.ui.State.SiteToggling[siteID] = false
			sd.ui.State.mu.Unlock()
			sd.ui.State.Invalidate()
		}()
	}

	if sd.openFiles.Clicked(gtx) {
		siteID := site.ID
		go func() {
			if err := sd.ui.SM.OpenSiteFilesDir(siteID); err != nil {
				sd.ui.State.ShowError("Failed to open files directory: " + err.Error())
			}
		}()
	}
}

func (sd *SiteDetail) layoutContent(gtx layout.Context, th *material.Theme, site *types.Site) layout.Dimensions {
	sd.ui.State.mu.Lock()
	toggling := sd.ui.State.SiteToggling[site.ID]
	sd.ui.State.mu.Unlock()

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// Site name heading
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.H4(th, site.Name)
			return layout.Inset{Bottom: unit.Dp(16)}.Layout(gtx, lbl.Layout)
		}),
		// Controls row: start/stop + open files
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(24)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return sd.layoutControls(gtx, th, site, toggling)
			})
		}),
		// Site section
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sd.layoutSection(gtx, th, "Site", []kv{
				{"ID", site.ID},
				{"Slug", site.Slug},
				{"URL", "https://" + site.Domain},
				{"Files Dir", site.FilesDir},
				{"Public Dir", site.PublicDir},
				{"Created", site.CreatedAt},
				{"Updated", site.UpdatedAt},
			})
		}),
		// Versions section
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sd.layoutSection(gtx, th, "Versions", []kv{
				{"PHP", site.PHPVersion},
				{"MySQL", site.MySQLVersion},
				{"Redis", site.RedisVersion},
			})
		}),
		// Database section
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sd.layoutSection(gtx, th, "Database", []kv{
				{"Hostname", "database"},
				{"Database", "wordpress"},
				{"User", "wordpress"},
				{"Password", "password"},
			})
		}),
	)
}

func (sd *SiteDetail) layoutControls(gtx layout.Context, th *material.Theme, site *types.Site, toggling bool) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceEnd}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if toggling {
				return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					loader := material.Loader(th)
					gtx.Constraints.Max.X = gtx.Dp(unit.Dp(36))
					gtx.Constraints.Max.Y = gtx.Dp(unit.Dp(36))
					return loader.Layout(gtx)
				})
			}
			if site.Started {
				b := material.Button(th, &sd.stopBtn, "Stop")
				b.Background = ColorRed600
				b.Color = ColorWhite
				b.CornerRadius = unit.Dp(6)
				b.TextSize = unit.Sp(14)
				return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, b.Layout)
			}
			b := material.Button(th, &sd.startBtn, "Start")
			b.Background = ColorGreen600
			b.Color = ColorWhite
			b.CornerRadius = unit.Dp(6)
			b.TextSize = unit.Sp(14)
			return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, b.Layout)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return SecondaryButton(gtx, th, &sd.openFiles, "View Site Files")
		}),
	)
}

type kv struct {
	Key   string
	Value string
}

func (sd *SiteDetail) layoutSection(gtx layout.Context, th *material.Theme, title string, items []kv) layout.Dimensions {
	children := make([]layout.FlexChild, 0, len(items)+1)

	// Section title
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		lbl := material.H6(th, title)
		return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, lbl.Layout)
	}))

	// Key-value rows
	for _, item := range items {
		item := item
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Dp(unit.Dp(100))
						lbl := material.Body2(th, item.Key)
						lbl.Color = ColorGray500
						lbl.TextSize = unit.Sp(14)
						return lbl.Layout(gtx)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th, item.Value)
						lbl.TextSize = unit.Sp(14)
						return lbl.Layout(gtx)
					}),
				)
			})
		}))
	}

	// Bottom margin for section
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Spacer{Height: unit.Dp(20)}.Layout(gtx)
	}))

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}
