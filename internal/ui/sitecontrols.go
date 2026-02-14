package ui

import (
	"strings"

	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/sqweek/dialog"

	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/types"
)

// SiteControls renders the Start/Stop, View Files, and Export action bar.
type SiteControls struct {
	state *UIState
	sm    *sites.SiteManager

	startBtn  widget.Clickable
	stopBtn   widget.Clickable
	openFiles widget.Clickable
	exportBtn widget.Clickable
}

func NewSiteControls(state *UIState, sm *sites.SiteManager) *SiteControls {
	return &SiteControls{state: state, sm: sm}
}

func (sc *SiteControls) Layout(gtx layout.Context, th *material.Theme, site *types.Site) layout.Dimensions {
	sc.handleClicks(gtx, site)

	toggling := sc.state.IsSiteToggling(site.ID)
	exporting := sc.state.IsExportLoading()

	return layout.Inset{Bottom: SpaceXL}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceEnd}.Layout(gtx,
			// Start / Stop / Loading spinner
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if toggling {
					return layout.Inset{Right: SpaceSM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return Loader(gtx, th, LoaderSize)
					})
				}
				if site.Started {
					return layout.Inset{Right: SpaceSM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return DangerButton(gtx, th, &sc.stopBtn, "Stop")
					})
				}
				return layout.Inset{Right: SpaceSM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return SuccessButton(gtx, th, &sc.startBtn, "Start")
				})
			}),
			// View Site Files
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Right: SpaceSM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return SecondaryButton(gtx, th, &sc.openFiles, "View Site Files")
				})
			}),
			// Export (only when running)
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !site.Started {
					return layout.Dimensions{}
				}
				if exporting {
					return Loader(gtx, th, LoaderSize)
				}
				return SecondaryButton(gtx, th, &sc.exportBtn, "Export")
			}),
		)
	})
}

func (sc *SiteControls) handleClicks(gtx layout.Context, site *types.Site) {
	if sc.startBtn.Clicked(gtx) {
		siteID := site.ID
		sc.state.SetSiteToggling(siteID, true)
		go func() {
			if err := sc.sm.StartSite(siteID); err != nil {
				sc.state.ShowError("Failed to start site: " + err.Error())
			}
			sc.state.SetSiteToggling(siteID, false)
		}()
	}

	if sc.stopBtn.Clicked(gtx) {
		siteID := site.ID
		sc.state.SetSiteToggling(siteID, true)
		go func() {
			if err := sc.sm.StopSite(siteID); err != nil {
				sc.state.ShowError("Failed to stop site: " + err.Error())
			}
			sc.state.SetSiteToggling(siteID, false)
		}()
	}

	if sc.openFiles.Clicked(gtx) {
		siteID := site.ID
		go func() {
			if err := sc.sm.OpenSiteFilesDir(siteID); err != nil {
				sc.state.ShowError("Failed to open files directory: " + err.Error())
			}
		}()
	}

	if sc.exportBtn.Clicked(gtx) && site.Started {
		siteID := site.ID
		sc.state.SetExportLoading(true)
		go func() {
			defer sc.state.SetExportLoading(false)

			dest, err := dialog.File().Filter("Tar archive", "tar.gz").Title("Export site").Save()
			if err != nil {
				if err.Error() != "Cancelled" {
					sc.state.ShowError("Export cancelled: " + err.Error())
				}
				return
			}
			if !strings.HasSuffix(dest, ".tar.gz") {
				dest += ".tar.gz"
			}
			if err := sc.sm.ExportSite(siteID, dest); err != nil {
				sc.state.ShowError("Export failed: " + err.Error())
			}
		}()
	}
}

// layoutStatusBadge renders a status badge next to the site name.
func layoutSiteHeader(gtx layout.Context, th *material.Theme, site *types.Site) layout.Dimensions {
	return layout.Inset{Bottom: SpaceLG}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.H4(th, site.Name)
				return layout.Inset{Right: SpaceMD}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return StatusBadge(gtx, th, site.Started)
			}),
		)
	})
}

// layoutSiteInfoSection renders the Site info key-value section.
func layoutSiteInfoSection(gtx layout.Context, th *material.Theme, site *types.Site) layout.Dimensions {
	return Section(gtx, th, "Site", func(gtx layout.Context) layout.Dimensions {
		return KVRows(gtx, th, []KV{
			{"ID", site.ID},
			{"Slug", site.Slug},
			{"URL", "https://" + site.Domain},
			{"Files Dir", site.FilesDir},
			{"Public Dir", site.PublicDir},
			{"Created", site.CreatedAt},
			{"Updated", site.UpdatedAt},
		})
	})
}

// layoutVersionsSection renders the Versions key-value section.
func layoutVersionsSection(gtx layout.Context, th *material.Theme, site *types.Site) layout.Dimensions {
	return Section(gtx, th, "Versions", func(gtx layout.Context) layout.Dimensions {
		return KVRows(gtx, th, []KV{
			{"PHP", site.PHPVersion},
			{"MySQL", site.MySQLVersion},
			{"Redis", site.RedisVersion},
		})
	})
}
