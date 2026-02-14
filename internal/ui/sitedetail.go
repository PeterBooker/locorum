package ui

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
)

// SiteDetail is the main content panel that orchestrates sub-components
// for the currently selected site.
type SiteDetail struct {
	state *UIState
	sm    *sites.SiteManager

	list widget.List

	// Sub-components
	controls   *SiteControls
	dbCreds    *DBCredentials
	logViewer  *LogViewer
	wpcliPanel *WPCLIPanel

	// Docker unavailable
	retryInitBtn widget.Clickable
}

func NewSiteDetail(state *UIState, sm *sites.SiteManager) *SiteDetail {
	sd := &SiteDetail{
		state:      state,
		sm:         sm,
		controls:   NewSiteControls(state, sm),
		dbCreds:    NewDBCredentials(),
		logViewer:  NewLogViewer(state, sm),
		wpcliPanel: NewWPCLIPanel(state, sm),
	}
	sd.list.List.Axis = layout.Vertical
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

	return layout.Inset{
		Top: SpaceXL, Bottom: SpaceXL,
		Left: Space2XL, Right: Space2XL,
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return material.List(th, &sd.list).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
			return sd.layoutContent(gtx, th)
		})
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

func (sd *SiteDetail) layoutContent(gtx layout.Context, th *material.Theme) layout.Dimensions {
	site := sd.state.SelectedSite()
	if site == nil {
		return layout.Dimensions{}
	}

	children := []layout.FlexChild{
		// Site name + status badge
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutSiteHeader(gtx, th, site)
		}),
		// Controls bar
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sd.controls.Layout(gtx, th, site)
		}),
		// Site info section
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutSiteInfoSection(gtx, th, site)
		}),
		// Versions section
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutVersionsSection(gtx, th, site)
		}),
		// Database credentials
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sd.dbCreds.Layout(gtx, th, site)
		}),
	}

	// Log viewer (only when running)
	if site.Started {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sd.logViewer.Layout(gtx, th, site.ID)
		}))
	}

	// WP-CLI (only when running)
	if site.Started {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sd.wpcliPanel.Layout(gtx, th, site.ID)
		}))
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}
