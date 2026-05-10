package ui

import (
	"context"
	"fmt"
	"image"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/orch"
	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/types"
)

type UI struct {
	Theme *Theme
	State *UIState
	SM    *sites.SiteManager

	// Sub-components
	NavRail    *NavRail
	SitesPanel *SitesPanel
	Settings   *SettingsPanel
	SiteDetail *SiteDetail
	NewSite    *NewSiteModal
	Toasts     *Notifications

	// Modals
	CloneModal    *CloneModal
	HealthBlocker *HealthBlockerModal
	Telemetry     *TelemetryModal
	PortHolders   *PortHoldersModal
	deleteDialog  ConfirmDialog
	deletePurge   widget.Bool

	// Banner action buttons. noticeBtn is the persistent info banner;
	// errorBtn is the transient error banner's optional action.
	noticeBtn widget.Clickable
	errorBtn  widget.Clickable
}

func New(sm *sites.SiteManager) *UI {
	state := NewUIState()
	th := NewTheme()

	cfg := sm.Config()
	if cfg != nil {
		th.SetMode(ParseThemeMode(cfg.ThemeMode()))
	}

	ui := &UI{
		Theme: th,
		State: state,
		SM:    sm,
	}

	ui.Toasts = NewNotifications(state)
	ui.NavRail = NewNavRail(state, sm)
	ui.SitesPanel = NewSitesPanel(state, sm, ui.Toasts)
	ui.Settings = NewSettingsPanel(state, sm, func(mode ThemeMode) {
		th.SetMode(mode)
		if cfg != nil {
			_ = cfg.SetThemeMode(mode.String())
		}
		state.Invalidate()
	})
	ui.Settings.SetDiagnosticsPanel(NewDiagnosticsPanel(state, sm, ui.Toasts))
	if cfg != nil {
		ui.Settings.SetPrivacyCard(NewPrivacyCard(state, cfg, ui.Toasts))
	}
	ui.SiteDetail = NewSiteDetail(state, sm, ui.Toasts)
	ui.NewSite = NewNewSiteModal(state, sm, ui.Toasts)
	ui.CloneModal = NewCloneModal(state, sm, ui.Toasts)
	ui.Telemetry = NewTelemetryModal(state, cfg)
	ui.PortHolders = NewPortHoldersModal(state)

	// Wire up backend callbacks to update UI state
	sm.OnSitesUpdated = func(updatedSites []types.Site) {
		state.SetSites(updatedSites)
	}
	sm.OnSiteUpdated = func(site *types.Site) {
		state.UpdateSite(*site)
		if dc := ui.SiteDetail.DescribeCache(); dc != nil {
			dc.Invalidate(site.ID)
		}
	}

	// Hook callbacks update the live-output panel.
	sm.OnHookTaskStart = state.HookTaskStarted
	sm.OnHookOutput = state.HookTaskOutput
	sm.OnHookTaskDone = state.HookTaskDone
	sm.OnHookAllDone = state.HookAllDone

	// Lifecycle plan callbacks.
	sm.OnStepStart = func(siteID string, s orch.StepResult) {
		state.LifecycleStepStarted(siteID, s)
	}
	sm.OnStepDone = func(siteID string, s orch.StepResult) {
		state.LifecycleStepDone(siteID, s)
	}
	sm.OnPlanDone = func(siteID string, r orch.Result) {
		state.LifecyclePlanDone(siteID, r)
		if dc := ui.SiteDetail.DescribeCache(); dc != nil {
			dc.Invalidate(siteID)
		}
	}
	sm.OnPullProgress = func(siteID string, p docker.PullProgress) {
		state.LifecyclePullProgress(siteID, p)
	}

	// Activity feed: prepend each freshly-persisted row to the per-site
	// caches so the overview panel and Activity tab update live.
	sm.OnActivityAppended = func(siteID string, ev storage.ActivityEvent) {
		state.AppendActivity(siteID, ev)
	}

	return ui
}

// HandleUserInteractions processes all user input for the current frame.
// Modal interactions are only processed when their modal is visible,
// preventing phantom clicks against hidden widgets.
func (ui *UI) HandleUserInteractions(gtx layout.Context) {
	ui.Toasts.HandleUserInteractions(gtx)
	ui.NavRail.HandleUserInteractions(gtx)

	switch ui.State.NavView() {
	case NavViewSettings:
		ui.Settings.HandleUserInteractions(gtx)
	default:
		ui.SitesPanel.HandleUserInteractions(gtx)
		ui.SiteDetail.HandleUserInteractions(gtx)
	}

	if ui.State.IsShowNewSiteModal() {
		ui.NewSite.HandleUserInteractions(gtx)
	}
	if showDelete, _, _ := ui.State.GetDeleteConfirmState(); showDelete {
		ui.handleDeleteConfirm(gtx)
	}
	if show, _, _ := ui.State.GetCloneModalState(); show {
		ui.CloneModal.HandleUserInteractions(gtx)
	}
	if ui.HealthBlocker != nil && ui.HealthBlocker.HasBlocker() {
		ui.HealthBlocker.HandleUserInteractions(gtx)
	}
	if ui.Telemetry != nil && ui.Telemetry.Show() {
		ui.Telemetry.HandleUserInteractions(gtx)
	}
	if show, _, _ := ui.State.GetPortHoldersModal(); show {
		ui.PortHolders.HandleUserInteractions(gtx)
	}
}

func (ui *UI) Layout(gtx layout.Context) layout.Dimensions {
	ui.HandleUserInteractions(gtx)

	errBanner := ui.State.ErrorBannerSnapshot()
	notice := ui.State.NoticeSnapshot()

	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return ui.layoutTopBar(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if notice.Message == "" {
						return layout.Dimensions{}
					}
					return ui.layoutNoticeBanner(gtx, notice)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if errBanner.Message == "" {
						return layout.Dimensions{}
					}
					return ui.layoutErrorBanner(gtx, errBanner)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return ui.layoutColumns(gtx)
				}),
			)
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			if ui.State.IsShowNewSiteModal() {
				return ui.NewSite.Layout(gtx, ui.Theme)
			}
			showDelete, deleteName, _ := ui.State.GetDeleteConfirmState()
			if showDelete {
				return ui.layoutDeleteConfirm(gtx, ui.Theme, deleteName)
			}
			if hp := ui.SiteDetail.HooksPanel(); hp != nil && hp.HasActiveModal() {
				return hp.LayoutModalLayer(gtx, ui.Theme)
			}
			return ui.CloneModal.Layout(gtx, ui.Theme)
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			// Health blocker overlays everything else when active —
			// it represents "you can't use the app right now". Render
			// after the regular modal layer so it sits on top.
			if ui.HealthBlocker != nil && ui.HealthBlocker.HasBlocker() {
				return ui.HealthBlocker.Layout(gtx, ui.Theme)
			}
			return layout.Dimensions{}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			if ui.Telemetry != nil && ui.Telemetry.Show() {
				return ui.Telemetry.Layout(gtx, ui.Theme)
			}
			return layout.Dimensions{}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			if ui.PortHolders == nil {
				return layout.Dimensions{}
			}
			if show, _, _ := ui.State.GetPortHoldersModal(); !show {
				return layout.Dimensions{}
			}
			return ui.PortHolders.Layout(gtx, ui.Theme)
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return ui.Toasts.Layout(gtx, ui.Theme)
		}),
	)
}

// layoutColumns paints the three primary columns. NavRail is fixed width;
// columns 2 and 3 vary by which root view is active.
func (ui *UI) layoutColumns(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return ui.NavRail.Layout(gtx, ui.Theme)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			switch ui.State.NavView() {
			case NavViewSettings:
				return ui.Settings.Layout(gtx, ui.Theme)
			default:
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return ui.SitesPanel.Layout(gtx, ui.Theme)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return ui.SiteDetail.Layout(gtx, ui.Theme)
					}),
				)
			}
		}),
	)
}

// handleDeleteConfirm reads the confirm/cancel clicks from the delete dialog
// and drives the delete workflow.
func (ui *UI) handleDeleteConfirm(gtx layout.Context) {
	if ui.deletePurge.Update(gtx) {
		ui.State.SetDeletePurgeVolume(ui.deletePurge.Value)
	}
	confirmed, cancelled := ui.deleteDialog.HandleUserInteractions(gtx)
	if cancelled {
		ui.State.DismissDeleteConfirm()
		ui.deletePurge.Value = false
	}
	if confirmed {
		id, purge := ui.State.ClearDeleteConfirm()
		ui.deletePurge.Value = false
		if id != "" {
			go func() {
				if err := ui.SM.DeleteSiteWithOptions(context.Background(), id, sites.DeleteOptions{PurgeVolume: purge}); err != nil {
					ui.State.ShowError("Failed to delete site: " + err.Error())
				}
			}()
		}
	}
}

func (ui *UI) layoutDeleteConfirm(gtx layout.Context, th *Theme, siteName string) layout.Dimensions {
	msg := "Delete \"" + siteName + "\"? Containers and configuration will be removed."
	return ui.deleteDialog.LayoutWithExtras(gtx, th, ConfirmDialogStyle{
		Title:        "Delete Site",
		Message:      msg,
		ConfirmLabel: "Delete",
		ConfirmColor: th.Color.Err,
	}, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			cb := material.CheckBox(th.Theme, &ui.deletePurge, "Also delete the database volume (cannot be undone)")
			cb.Color = th.Color.Err
			cb.IconColor = th.Color.Err
			cb.Size = unit.Dp(20)
			cb.TextSize = th.Sizes.SM
			return cb.Layout(gtx)
		})
	})
}

func (ui *UI) layoutErrorBanner(gtx layout.Context, b ErrorBannerSnapshot) layout.Dimensions {
	th := ui.Theme
	if ui.errorBtn.Clicked(gtx) && b.HasAction && !b.Busy {
		ui.State.TriggerErrorAction()
	}
	return FillBackground(gtx, th.Color.DangerBg, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top: unit.Dp(10), Bottom: unit.Dp(10),
			Left: th.Spacing.LG, Right: th.Spacing.LG,
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body2(th.Theme, b.Message)
					lbl.Color = th.Color.DangerFg
					lbl.TextSize = th.Sizes.Body
					return lbl.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !b.HasAction {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						label := b.ActionLabel
						if b.Busy {
							label = "Working…"
						}
						return th.SmallGated(gtx, &ui.errorBtn, label, !b.Busy)
					})
				}),
			)
		})
	})
}

// layoutTopBar paints the small "frame" bar above the three columns: app
// name on the left, optional disk-free segment in the middle, and the
// rolled-up services-health pill on the right.
func (ui *UI) layoutTopBar(gtx layout.Context) layout.Dimensions {
	th := ui.Theme
	h := ui.State.ServicesHealthSnapshot()
	statusKey, statusLabel := topBarStatusKeyLabel(h)
	diskFree := ui.State.DiskFreeBytes()

	return FillBackground(gtx, th.Color.Bg1, func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{}.Layout(gtx,
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{
					Top: unit.Dp(6), Bottom: unit.Dp(6),
					Left: th.Spacing.MD, Right: th.Spacing.MD,
				}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							lbl := material.Body2(th.Theme, "Locorum")
							lbl.Color = th.Color.Fg3
							lbl.TextSize = th.Sizes.Mono
							lbl.Font = MonoFont
							lbl.Font.Weight = font.Medium
							return lbl.Layout(gtx)
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Min.X}}
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if diskFree <= 0 {
								return layout.Dimensions{}
							}
							return layout.Inset{Right: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return ui.layoutTopBarDisk(gtx, th, diskFree)
							})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return topBarStatus(gtx, th, statusKey, statusLabel)
						}),
					)
				})
			}),
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				return EdgeLine(gtx, th.Color.Line, "bottom")
			}),
		)
	})
}

// layoutTopBarDisk renders the cached host-filesystem free-bytes reading
// as a "Disk: 12 GB" mono label. Hidden when no reading is available.
func (ui *UI) layoutTopBarDisk(gtx layout.Context, th *Theme, free int64) layout.Dimensions {
	lbl := material.Body2(th.Theme, "Disk: "+formatBytes(free))
	lbl.Color = th.Color.Fg3
	lbl.TextSize = th.Sizes.Micro
	lbl.Font = MonoFont
	lbl.Font.Weight = font.Medium
	return lbl.Layout(gtx)
}

// formatBytes renders byte counts as a short, status-bar-friendly string.
// Mirrors health.humanBytes. Decimal precision drops once values reach
// triple digits so the segment width stays roughly stable as numbers grow.
func formatBytes(n int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case n >= TB:
		return fmt.Sprintf("%.1f TB", float64(n)/float64(TB))
	case n >= GB:
		return fmt.Sprintf("%.0f GB", float64(n)/float64(GB))
	case n >= MB:
		return fmt.Sprintf("%.0f MB", float64(n)/float64(MB))
	case n >= KB:
		return fmt.Sprintf("%.0f KB", float64(n)/float64(KB))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// topBarStatusKeyLabel maps the rolled-up health snapshot onto a status
// key + uppercase mono label for the top bar.
func topBarStatusKeyLabel(h ServicesHealth) (key, label string) {
	switch h.Status {
	case ServicesHealthHealthy:
		return StatusOk, "ALL SERVICES HEALTHY"
	case ServicesHealthDegraded:
		return StatusWarn, "SERVICES DEGRADED"
	case ServicesHealthDown:
		return StatusErr, "SERVICES DOWN"
	default:
		return StatusIdle, "STARTING SERVICES…"
	}
}

func topBarStatus(gtx layout.Context, th *Theme, key, label string) layout.Dimensions {
	pal := statusPalette(th, key)
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return LiveStatusDot(gtx, th, key, key == StatusOk)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, label)
				lbl.Color = pal.fg
				lbl.TextSize = th.Sizes.Micro
				lbl.Font = MonoFont
				lbl.Font.Weight = font.Medium
				return lbl.Layout(gtx)
			})
		}),
	)
}

func (ui *UI) layoutNoticeBanner(gtx layout.Context, n NoticeSnapshot) layout.Dimensions {
	th := ui.Theme
	if ui.noticeBtn.Clicked(gtx) && n.HasAction && !n.Busy {
		ui.State.TriggerNoticeAction()
	}
	return FillBackground(gtx, th.Color.InfoBg, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top: unit.Dp(10), Bottom: unit.Dp(10),
			Left: th.Spacing.LG, Right: th.Spacing.LG,
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body2(th.Theme, n.Message)
					lbl.Color = th.Color.InfoFg
					lbl.TextSize = th.Sizes.Body
					return lbl.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !n.HasAction {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						label := n.ActionLabel
						if n.Busy {
							label = "Working…"
						}
						return th.SmallGated(gtx, &ui.noticeBtn, label, !n.Busy)
					})
				}),
			)
		})
	})
}
