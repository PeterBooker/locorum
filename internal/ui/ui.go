package ui

import (
	"gioui.org/layout"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/types"
)

type UI struct {
	Theme *material.Theme
	State *UIState
	SM    *sites.SiteManager

	// Sub-components
	Sidebar    *Sidebar
	SiteDetail *SiteDetail
	NewSite    *NewSiteModal
}

func New(sm *sites.SiteManager) *UI {
	state := NewUIState()
	th := NewTheme()

	ui := &UI{
		Theme: th,
		State: state,
		SM:    sm,
	}

	ui.Sidebar = NewSidebar(ui)
	ui.SiteDetail = NewSiteDetail(ui)
	ui.NewSite = NewNewSiteModal(ui)

	// Wire up backend callbacks to update UI state
	sm.OnSitesUpdated = func(updatedSites []types.Site) {
		state.mu.Lock()
		state.Sites = updatedSites
		state.mu.Unlock()
		state.Invalidate()
	}

	sm.OnSiteUpdated = func(site *types.Site) {
		state.mu.Lock()
		for i, s := range state.Sites {
			if s.ID == site.ID {
				state.Sites[i] = *site
				break
			}
		}
		state.mu.Unlock()
		state.Invalidate()
	}

	return ui
}

func (ui *UI) Layout(gtx layout.Context) layout.Dimensions {
	return layout.Stack{}.Layout(gtx,
		// Base layer: sidebar + content
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return ui.Sidebar.Layout(gtx, ui.Theme)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return FillBackground(gtx, ColorWhite, func(gtx layout.Context) layout.Dimensions {
						return ui.SiteDetail.Layout(gtx, ui.Theme)
					})
				}),
			)
		}),
		// Modal overlay layer (conditional)
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			ui.State.mu.Lock()
			showModal := ui.State.ShowNewSiteModal
			ui.State.mu.Unlock()

			if showModal {
				return ui.NewSite.Layout(gtx, ui.Theme)
			}
			return layout.Dimensions{}
		}),
	)
}
