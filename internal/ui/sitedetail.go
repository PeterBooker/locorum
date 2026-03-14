package ui

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/types"
)

const (
	tabOverview  = 0
	tabDatabase  = 1
	tabUtilities = 2
)

var tabLabels = []string{"Overview", "Database", "Utilities"}

// SiteDetail is the main content panel that orchestrates sub-components
// for the currently selected site.
type SiteDetail struct {
	state *UIState
	sm    *sites.SiteManager

	list widget.List

	// Tabs
	activeTab int
	tabClicks [3]widget.Clickable

	// Overview tab interactive widgets
	openURLBtn     widget.Clickable
	openFilesDirBtn widget.Clickable
	publicDirEditor widget.Editor
	savePublicDir   widget.Clickable
	lastSiteID      string // track site changes to sync editor

	// Sub-components
	controls      *SiteControls
	dbCreds       *DBCredentials
	logViewer     *LogViewer
	wpcliPanel    *WPCLIPanel
	versionEditor *VersionEditor
	linkChecker   *LinkChecker

	// Docker unavailable
	retryInitBtn widget.Clickable
}

func NewSiteDetail(state *UIState, sm *sites.SiteManager, toasts *ToastManager) *SiteDetail {
	sd := &SiteDetail{
		state:         state,
		sm:            sm,
		controls:      NewSiteControls(state, sm),
		dbCreds:       NewDBCredentials(),
		logViewer:     NewLogViewer(state, sm),
		wpcliPanel:    NewWPCLIPanel(state, sm),
		versionEditor: NewVersionEditor(state, sm, toasts),
		linkChecker:   NewLinkChecker(state, sm),
	}
	sd.list.List.Axis = layout.Vertical
	sd.publicDirEditor.SingleLine = true
	return sd
}

func (sd *SiteDetail) Layout(gtx layout.Context, th *material.Theme) layout.Dimensions {
	// Check for Docker initialization error.
	if initError := sd.state.GetInitError(); initError != "" {
		return sd.layoutInitError(gtx, th, initError)
	}

	site := sd.state.SelectedSite()
	if site == nil {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			lbl := material.H6(th, "Select a site from the sidebar")
			lbl.Color = ColorGray400
			return lbl.Layout(gtx)
		})
	}

	// Handle tab clicks.
	for i := range sd.tabClicks {
		if sd.tabClicks[i].Clicked(gtx) {
			sd.activeTab = i
		}
	}

	return layout.Inset{
		Top: SpaceXL, Bottom: SpaceXL,
		Left: Space2XL, Right: Space2XL,
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			// Site header (always visible above tabs)
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layoutSiteHeader(gtx, th, site)
			}),
			// Controls bar (always visible above tabs)
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return sd.controls.Layout(gtx, th, site)
			}),
			// Tab bar
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				clicks := make([]*widget.Clickable, len(sd.tabClicks))
				for i := range sd.tabClicks {
					clicks[i] = &sd.tabClicks[i]
				}
				return layout.Inset{Bottom: SpaceMD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return TabBar(gtx, th, tabLabels, sd.activeTab, clicks)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return Divider(gtx, ColorBorder, 0)
						}),
					)
				})
			}),
			// Tab content (scrollable)
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return material.List(th, &sd.list).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
					return sd.layoutTabContent(gtx, th)
				})
			}),
		)
	})
}

func (sd *SiteDetail) layoutInitError(gtx layout.Context, th *material.Theme, errMsg string) layout.Dimensions {
	if sd.retryInitBtn.Clicked(gtx) {
		if retryFn := sd.state.GetRetryInit(); retryFn != nil {
			go retryFn()
		}
	}

	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.H5(th, "Docker Unavailable")
				lbl.Color = ColorRed600
				return layout.Inset{Bottom: SpaceMD}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th, errMsg)
				lbl.Color = ColorGray500
				return layout.Inset{Bottom: unit.Dp(20)}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return PrimaryButton(gtx, th, &sd.retryInitBtn, "Retry")
			}),
		)
	})
}

func (sd *SiteDetail) layoutTabContent(gtx layout.Context, th *material.Theme) layout.Dimensions {
	site := sd.state.SelectedSite()
	if site == nil {
		return layout.Dimensions{}
	}

	switch sd.activeTab {
	case tabDatabase:
		return sd.layoutDatabaseTab(gtx, th)
	case tabUtilities:
		return sd.layoutUtilitiesTab(gtx, th)
	default:
		return sd.layoutOverviewTab(gtx, th)
	}
}

// layoutOverviewTab shows site info and version settings.
func (sd *SiteDetail) layoutOverviewTab(gtx layout.Context, th *material.Theme) layout.Dimensions {
	site := sd.state.SelectedSite()
	if site == nil {
		return layout.Dimensions{}
	}

	// Sync public dir editor when site changes.
	if sd.lastSiteID != site.ID {
		sd.lastSiteID = site.ID
		sd.publicDirEditor.SetText(site.PublicDir)
	}

	sd.handleOverviewClicks(gtx, site)

	return layout.Inset{Top: SpaceMD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			// Site info section
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return sd.layoutSiteInfo(gtx, th, site)
			}),
			// Versions section
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return sd.versionEditor.Layout(gtx, th, site)
			}),
		)
	})
}

// handleOverviewClicks processes clicks for the overview tab interactive elements.
func (sd *SiteDetail) handleOverviewClicks(gtx layout.Context, site *types.Site) {
	if sd.openURLBtn.Clicked(gtx) {
		siteID := site.ID
		go func() {
			if err := sd.sm.OpenSiteURL(siteID); err != nil {
				sd.state.ShowError("Failed to open URL: " + err.Error())
			}
		}()
	}

	if sd.openFilesDirBtn.Clicked(gtx) {
		siteID := site.ID
		go func() {
			if err := sd.sm.OpenSiteFilesDir(siteID); err != nil {
				sd.state.ShowError("Failed to open files directory: " + err.Error())
			}
		}()
	}

	if sd.savePublicDir.Clicked(gtx) && !site.Started {
		siteID := site.ID
		newDir := sd.publicDirEditor.Text()
		go func() {
			if err := sd.sm.UpdatePublicDir(siteID, newDir); err != nil {
				sd.state.ShowError("Failed to update public dir: " + err.Error())
			}
		}()
	}
}

// layoutSiteInfo renders the site info section with interactive rows.
func (sd *SiteDetail) layoutSiteInfo(gtx layout.Context, th *material.Theme, site *types.Site) layout.Dimensions {
	return Section(gtx, th, "Site", func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{
			// URL row with Open button
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return sd.layoutKVWithButton(gtx, th, "URL", "https://"+site.Domain, &sd.openURLBtn, "Open")
			}),
			// Files Dir row with Open button
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return sd.layoutKVWithButton(gtx, th, "Files Dir", site.FilesDir, &sd.openFilesDirBtn, "Open")
			}),
			// Public Dir: editable when stopped, read-only when running
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return sd.layoutPublicDirRow(gtx, th, site)
			}),
			// Web Server
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: SpaceXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return KVRows(gtx, th, []KV{{"Web Server", site.WebServer}})
				})
			}),
		}

		if site.Multisite != "" {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return KVRows(gtx, th, []KV{{"Multisite", site.Multisite}})
			}))
		}

		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

// layoutKVWithButton renders a key-value row with a small action button on the right.
func (sd *SiteDetail) layoutKVWithButton(gtx layout.Context, th *material.Theme, key, value string, btn *widget.Clickable, btnLabel string) layout.Dimensions {
	return layout.Inset{Bottom: SpaceXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(LabelColWidth)
				lbl := material.Body2(th, key)
				lbl.Color = ColorGray500
				lbl.TextSize = TextBase
				return lbl.Layout(gtx)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th, value)
				lbl.TextSize = TextBase
				return lbl.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: SpaceSM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return SmallButton(gtx, th, btn, btnLabel)
				})
			}),
		)
	})
}

// layoutPublicDirRow renders the public dir as an editable field when stopped,
// or as read-only text when running.
func (sd *SiteDetail) layoutPublicDirRow(gtx layout.Context, th *material.Theme, site *types.Site) layout.Dimensions {
	if site.Started {
		// Read-only when running.
		return layout.Inset{Bottom: SpaceXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return KVRows(gtx, th, []KV{{"Public Dir", site.PublicDir}})
		})
	}

	// Editable when stopped.
	return layout.Inset{Bottom: SpaceSM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(LabelColWidth)
				lbl := material.Body2(th, "Public Dir")
				lbl.Color = ColorGray500
				lbl.TextSize = TextBase
				return lbl.Layout(gtx)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return BorderedEditor(gtx, th, &sd.publicDirEditor, "e.g. wp-content/public")
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: SpaceSM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return SmallButton(gtx, th, &sd.savePublicDir, "Save")
				})
			}),
		)
	})
}

// layoutDatabaseTab shows database credentials.
func (sd *SiteDetail) layoutDatabaseTab(gtx layout.Context, th *material.Theme) layout.Dimensions {
	site := sd.state.SelectedSite()
	if site == nil {
		return layout.Dimensions{}
	}

	return layout.Inset{Top: SpaceMD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return sd.dbCreds.Layout(gtx, th, site)
	})
}

// layoutUtilitiesTab shows logs, WP-CLI, and link checker (running-only features).
func (sd *SiteDetail) layoutUtilitiesTab(gtx layout.Context, th *material.Theme) layout.Dimensions {
	site := sd.state.SelectedSite()
	if site == nil {
		return layout.Dimensions{}
	}

	if !site.Started {
		return layout.Inset{Top: SpaceMD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body1(th, "Start the site to access utilities.")
			lbl.Color = ColorGray400
			return lbl.Layout(gtx)
		})
	}

	return layout.Inset{Top: SpaceMD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return sd.logViewer.Layout(gtx, th, site.ID)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return sd.wpcliPanel.Layout(gtx, th, site.ID)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return sd.linkChecker.Layout(gtx, th, site.ID)
			}),
		)
	})
}
