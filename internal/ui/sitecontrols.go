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

// SiteControls renders the Start/Stop, Export, and other action buttons.
type SiteControls struct {
	state *UIState
	sm    *sites.SiteManager

	startBtn      widget.Clickable
	stopBtn       widget.Clickable
	exportBtn     widget.Clickable
	openAdminBtn  widget.Clickable
	shellBtn      widget.Clickable
	cloneBtn      widget.Clickable
	liveReloadBtn widget.Clickable
}

func NewSiteControls(state *UIState, sm *sites.SiteManager) *SiteControls {
	return &SiteControls{state: state, sm: sm}
}

func (sc *SiteControls) Layout(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	toggling := sc.state.IsSiteToggling(site.ID)
	exporting := sc.state.IsExportLoading()

	return layout.Inset{Bottom: th.Spacing.XL}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			// Row 1: Start/Stop, Clone, Export
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceEnd}.Layout(gtx,
					// Start / Stop / Loading spinner
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if toggling {
							return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return Loader(gtx, th, th.Dims.LoaderSize)
							})
						}
						if site.Started {
							return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return DangerButton(gtx, th, &sc.stopBtn, "Stop")
							})
						}
						return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return SuccessButton(gtx, th, &sc.startBtn, "Start")
						})
					}),
					// Clone
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return SecondaryButton(gtx, th, &sc.cloneBtn, "Clone")
						})
					}),
					// Export (only when running)
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !site.Started {
							return layout.Dimensions{}
						}
						if exporting {
							return Loader(gtx, th, th.Dims.LoaderSize)
						}
						return SecondaryButton(gtx, th, &sc.exportBtn, "Export")
					}),
				)
			}),
			// Row 2: Running-only actions (Open Admin, Shell, Live Reload)
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !site.Started {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceEnd}.Layout(gtx,
						// Open Admin
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return PrimaryButton(gtx, th, &sc.openAdminBtn, "Open Admin")
							})
						}),
						// Shell
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return SecondaryButton(gtx, th, &sc.shellBtn, "Shell")
							})
						}),
						// Live Reload toggle
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if sc.state.IsLiveReloadEnabled(site.ID) {
								return SuccessButton(gtx, th, &sc.liveReloadBtn, "Live Reload: On")
							}
							return SecondaryButton(gtx, th, &sc.liveReloadBtn, "Live Reload: Off")
						}),
					)
				})
			}),
		)
	})
}

// HandleUserInteractions processes button clicks on the controls bar.
// Called by the parent SiteDetail before Layout each frame.
func (sc *SiteControls) HandleUserInteractions(gtx layout.Context, site *types.Site) {
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
			sc.state.SetLiveReload(siteID, false)
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

	if sc.openAdminBtn.Clicked(gtx) && site.Started {
		siteID := site.ID
		go func() {
			if err := sc.sm.OpenAdminLogin(siteID); err != nil {
				sc.state.ShowError("Failed to open admin: " + err.Error())
			}
		}()
	}

	if sc.shellBtn.Clicked(gtx) && site.Started {
		siteID := site.ID
		go func() {
			if err := sc.sm.OpenSiteShell(siteID); err != nil {
				sc.state.ShowError("Failed to open shell: " + err.Error())
			}
		}()
	}

	if sc.cloneBtn.Clicked(gtx) {
		sc.state.ShowCloneModal(site.ID, site.Name)
	}

	if sc.liveReloadBtn.Clicked(gtx) && site.Started {
		siteID := site.ID
		enabled := sc.state.IsLiveReloadEnabled(siteID)
		go func() {
			if enabled {
				if err := sc.sm.DisableLiveReload(siteID); err != nil {
					sc.state.ShowError("Live reload error: " + err.Error())
					return
				}
				sc.state.SetLiveReload(siteID, false)
			} else {
				if err := sc.sm.EnableLiveReload(siteID); err != nil {
					sc.state.ShowError("Live reload error: " + err.Error())
					return
				}
				sc.state.SetLiveReload(siteID, true)
			}
		}()
	}
}

// layoutSiteHeader renders a status badge next to the site name.
func layoutSiteHeader(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	return layout.Inset{Bottom: th.Spacing.LG}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.H4(th.Theme, TruncateWords(site.Name, 50))
				lbl.MaxLines = 1
				lbl.Truncator = "…"
				return layout.Inset{Right: th.Spacing.MD}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return StatusBadge(gtx, th, site.Started)
			}),
		)
	})
}

// layoutVersionsSection renders the Versions key-value section.
func layoutVersionsSection(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	return Section(gtx, th, "Versions", func(gtx layout.Context) layout.Dimensions {
		return KVRows(gtx, th, []KV{
			{"PHP", site.PHPVersion},
			{"MySQL", site.MySQLVersion},
			{"Redis", site.RedisVersion},
		})
	})
}
