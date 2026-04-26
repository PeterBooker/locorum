package ui

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/types"
)

type UI struct {
	Theme *Theme
	State *UIState
	SM    *sites.SiteManager

	// Sub-components
	Sidebar    *Sidebar
	SiteDetail *SiteDetail
	NewSite    *NewSiteModal
	Toasts     *Notifications

	// Modals
	CloneModal   *CloneModal
	deleteDialog ConfirmDialog
}

// SettingKeyThemeMode persists the user's theme preference ("dark", "light",
// or "system"). Read/written via SiteManager.GetSetting / SetSetting.
const SettingKeyThemeMode = "theme_mode"

func New(sm *sites.SiteManager) *UI {
	state := NewUIState()
	th := NewTheme()

	if stored, err := sm.GetSetting(SettingKeyThemeMode); err == nil && stored != "" {
		th.SetMode(ParseThemeMode(stored))
	}

	ui := &UI{
		Theme: th,
		State: state,
		SM:    sm,
	}

	ui.Toasts = NewNotifications(state)
	ui.Sidebar = NewSidebar(state, sm, ui.Toasts)
	ui.SiteDetail = NewSiteDetail(state, sm, ui.Toasts)
	ui.NewSite = NewNewSiteModal(state, sm, ui.Toasts)
	ui.CloneModal = NewCloneModal(state, sm, ui.Toasts)

	// Wire up backend callbacks to update UI state
	sm.OnSitesUpdated = func(updatedSites []types.Site) {
		state.SetSites(updatedSites)
	}

	sm.OnSiteUpdated = func(site *types.Site) {
		state.UpdateSite(*site)
	}

	return ui
}

// HandleUserInteractions processes all user input for the current frame: button
// clicks, text-editor changes, keyboard events. Called before Layout each frame.
// Modal interactions are only processed when their modal is visible, preventing
// phantom clicks against hidden widgets.
func (ui *UI) HandleUserInteractions(gtx layout.Context) {
	ui.Toasts.HandleUserInteractions(gtx)
	ui.Sidebar.HandleUserInteractions(gtx)
	ui.SiteDetail.HandleUserInteractions(gtx)

	if ui.State.IsShowNewSiteModal() {
		ui.NewSite.HandleUserInteractions(gtx)
	}

	if showDelete, _ := ui.State.GetDeleteConfirmState(); showDelete {
		ui.handleDeleteConfirm(gtx)
	}

	if show, _, _ := ui.State.GetCloneModalState(); show {
		ui.CloneModal.HandleUserInteractions(gtx)
	}
}

func (ui *UI) Layout(gtx layout.Context) layout.Dimensions {
	ui.HandleUserInteractions(gtx)

	errMsg := ui.State.ActiveError()
	notice := ui.State.GetNotice()

	return layout.Stack{}.Layout(gtx,
		// Base layer: notice banner + error banner + sidebar/content
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				// Notice banner (persistent info, e.g. mkcert prompt)
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if notice == "" {
						return layout.Dimensions{}
					}
					return ui.layoutNoticeBanner(gtx, notice)
				}),
				// Error banner (conditional, transient)
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
							return FillBackground(gtx, ui.Theme.Color.ContentBg, func(gtx layout.Context) layout.Dimensions {
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

			// Clone modal
			return ui.CloneModal.Layout(gtx, ui.Theme)
		}),
		// Toast notifications layer
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return ui.Toasts.Layout(gtx, ui.Theme)
		}),
	)
}

// handleDeleteConfirm reads the confirm/cancel clicks from the delete dialog and
// drives the delete workflow. Called from HandleUserInteractions when the
// delete-confirm modal is visible.
func (ui *UI) handleDeleteConfirm(gtx layout.Context) {
	confirmed, cancelled := ui.deleteDialog.HandleUserInteractions(gtx)

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
}

func (ui *UI) layoutDeleteConfirm(gtx layout.Context, th *Theme, siteName string) layout.Dimensions {
	return ui.deleteDialog.Layout(gtx, th, ConfirmDialogStyle{
		Title:        "Delete Site",
		Message:      "Are you sure you want to delete \"" + siteName + "\"? This will remove all containers and configuration for this site.",
		ConfirmLabel: "Delete",
		ConfirmColor: th.Color.Danger,
	})
}

func (ui *UI) layoutErrorBanner(gtx layout.Context, msg string) layout.Dimensions {
	th := ui.Theme
	return FillBackground(gtx, th.Color.DangerDeep, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top: unit.Dp(10), Bottom: unit.Dp(10),
			Left: th.Spacing.LG, Right: th.Spacing.LG,
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, msg)
			lbl.Color = th.Color.White
			return lbl.Layout(gtx)
		})
	})
}

func (ui *UI) layoutNoticeBanner(gtx layout.Context, msg string) layout.Dimensions {
	th := ui.Theme
	return FillBackground(gtx, th.Color.InfoBg, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top: unit.Dp(10), Bottom: unit.Dp(10),
			Left: th.Spacing.LG, Right: th.Spacing.LG,
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, msg)
			lbl.Color = th.Color.InfoFg
			return lbl.Layout(gtx)
		})
	})
}
