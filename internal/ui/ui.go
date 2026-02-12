package ui

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
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

	// Delete confirmation modal
	deleteConfirmBtn widget.Clickable
	deleteCancelBtn  widget.Clickable
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
	ui.State.mu.Lock()
	errMsg := ui.State.ActiveError()
	ui.State.mu.Unlock()

	// Handle delete confirmation clicks
	ui.handleDeleteConfirm(gtx)

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
			ui.State.mu.Lock()
			showNewSite := ui.State.ShowNewSiteModal
			showDelete := ui.State.ShowDeleteConfirmModal
			deleteName := ui.State.DeleteTargetName
			ui.State.mu.Unlock()

			if showNewSite {
				return ui.NewSite.Layout(gtx, ui.Theme)
			}
			if showDelete {
				return ui.layoutDeleteConfirmModal(gtx, ui.Theme, deleteName)
			}
			return layout.Dimensions{}
		}),
	)
}

func (ui *UI) handleDeleteConfirm(gtx layout.Context) {
	if ui.deleteCancelBtn.Clicked(gtx) {
		ui.State.mu.Lock()
		ui.State.ShowDeleteConfirmModal = false
		ui.State.DeleteTargetID = ""
		ui.State.DeleteTargetName = ""
		ui.State.mu.Unlock()
	}

	if ui.deleteConfirmBtn.Clicked(gtx) {
		ui.State.mu.Lock()
		id := ui.State.DeleteTargetID
		ui.State.ShowDeleteConfirmModal = false
		ui.State.DeleteTargetID = ""
		ui.State.DeleteTargetName = ""
		ui.State.mu.Unlock()

		if id != "" {
			go func() {
				if err := ui.SM.DeleteSite(id); err != nil {
					ui.State.ShowError("Failed to delete site: " + err.Error())
				}
			}()
		}
	}
}

func (ui *UI) layoutDeleteConfirmModal(gtx layout.Context, th *material.Theme, siteName string) layout.Dimensions {
	return ModalOverlay(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			// Title
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.H6(th, "Delete Site")
				return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, lbl.Layout)
			}),
			// Message
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body1(th, "Are you sure you want to delete \""+siteName+"\"? This will remove all containers and configuration for this site.")
				return layout.Inset{Bottom: unit.Dp(20)}.Layout(gtx, lbl.Layout)
			}),
			// Buttons
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceEnd}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return SecondaryButton(gtx, th, &ui.deleteCancelBtn, "Cancel")
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						b := material.Button(th, &ui.deleteConfirmBtn, "Delete")
						b.Background = ColorRed600
						b.Color = ColorWhite
						b.CornerRadius = unit.Dp(6)
						b.TextSize = unit.Sp(14)
						return b.Layout(gtx)
					}),
				)
			}),
		)
	})
}

func (ui *UI) layoutErrorBanner(gtx layout.Context, msg string) layout.Dimensions {
	return FillBackground(gtx, ColorRed700, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top:    unit.Dp(10),
			Bottom: unit.Dp(10),
			Left:   unit.Dp(16),
			Right:  unit.Dp(16),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(ui.Theme, msg)
			lbl.Color = ColorWhite
			return lbl.Layout(gtx)
		})
	})
}
