package ui

import (
	"gioui.org/layout"
	"gioui.org/unit"
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
	Toasts     *ToastManager

	// Delete confirmation dialog
	deleteDialog ConfirmDialog
}

func New(sm *sites.SiteManager) *UI {
	state := NewUIState()
	th := NewTheme()

	ui := &UI{
		Theme: th,
		State: state,
		SM:    sm,
	}

	ui.Toasts = NewToastManager(state)
	ui.Sidebar = NewSidebar(state, sm, ui.Toasts)
	ui.SiteDetail = NewSiteDetail(state, sm)
	ui.NewSite = NewNewSiteModal(state, sm, ui.Toasts)

	// Wire up backend callbacks to update UI state
	sm.OnSitesUpdated = func(updatedSites []types.Site) {
		state.SetSites(updatedSites)
	}

	sm.OnSiteUpdated = func(site *types.Site) {
		state.UpdateSite(*site)
	}

	return ui
}

func (ui *UI) Layout(gtx layout.Context) layout.Dimensions {
	errMsg := ui.State.ActiveError()

	return layout.Stack{}.Layout(gtx,
		// Base layer: error banner + sidebar/content
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				// Error banner (conditional)
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if errMsg == "" {
						return layout.Dimensions{}
					}
					return ui.layoutErrorBanner(gtx, errMsg)
				}),
				// Main area: sidebar + content
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
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
			)
		}),
		// Modal overlay layer
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			if ui.State.IsShowNewSiteModal() {
				return ui.NewSite.Layout(gtx, ui.Theme)
			}

			showDelete, deleteName := ui.State.GetDeleteConfirmState()
			if showDelete {
				return ui.layoutDeleteConfirm(gtx, ui.Theme, deleteName)
			}

			return layout.Dimensions{}
		}),
		// Toast notifications layer
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return ui.Toasts.Layout(gtx, ui.Theme)
		}),
	)
}

func (ui *UI) layoutDeleteConfirm(gtx layout.Context, th *material.Theme, siteName string) layout.Dimensions {
	confirmed, cancelled, dims := ui.deleteDialog.Layout(gtx, th, ConfirmDialogStyle{
		Title:        "Delete Site",
		Message:      "Are you sure you want to delete \"" + siteName + "\"? This will remove all containers and configuration for this site.",
		ConfirmLabel: "Delete",
		ConfirmColor: ColorRed600,
	})

	if cancelled {
		ui.State.DismissDeleteConfirm()
	}

	if confirmed {
		id := ui.State.ClearDeleteConfirm()
		if id != "" {
			go func() {
				if err := ui.SM.DeleteSite(id); err != nil {
					ui.State.ShowError("Failed to delete site: " + err.Error())
				}
			}()
		}
	}

	return dims
}

func (ui *UI) layoutErrorBanner(gtx layout.Context, msg string) layout.Dimensions {
	return FillBackground(gtx, ColorRed700, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top: unit.Dp(10), Bottom: unit.Dp(10),
			Left: SpaceLG, Right: SpaceLG,
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(ui.Theme, msg)
			lbl.Color = ColorWhite
			return lbl.Layout(gtx)
		})
	})
}
