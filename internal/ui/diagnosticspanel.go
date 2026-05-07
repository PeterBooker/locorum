package ui

import (
	"strconv"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/applog"
	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/utils"
)

// DiagnosticsPanel groups developer-oriented controls used to inspect or
// reset the running app: log file access, debug-mode toggle, and (added by
// later sub-tasks) the update-check banner and reset-infrastructure card.
//
// Wired from SettingsPanel; rendered as the Settings → Diagnostics card.
// Mutations go through sites.SiteManager.Config so persistence survives
// restart; the in-memory level toggle goes through applog.SetDebug so the
// next slog.Info record uses the new level without rebuilding the handler.
type DiagnosticsPanel struct {
	state  *UIState
	sm     *sites.SiteManager
	toasts *Notifications

	debugMode     widget.Bool
	openLogFolder widget.Clickable
	copyLastLines widget.Clickable
	debugSyncedTo bool

	// Optional sub-cards rendered inside the Diagnostics block. nil means
	// the section is omitted. UpdateBanner and ResetInfraCard are wired
	// from main.go after the relevant subsystems are constructed.
	updatePanel    *UpdateBannerCard
	resetInfraCard *ResetInfraCard
}

// TailLineCount is the number of lines copied to clipboard by the
// "Copy Last 200 Lines" affordance. Exposed as a constant so tests can
// reference the same value without re-declaring the magic number.
const TailLineCount = 200

func NewDiagnosticsPanel(state *UIState, sm *sites.SiteManager, toasts *Notifications) *DiagnosticsPanel {
	dp := &DiagnosticsPanel{state: state, sm: sm, toasts: toasts}
	if cfg := sm.Config(); cfg != nil {
		dp.debugMode.Value = cfg.DebugLogging()
		dp.debugSyncedTo = dp.debugMode.Value
	}
	return dp
}

// SetUpdateBanner wires (or clears) the update-check sub-card. Safe to
// call before or after Layout — the card is only rendered when set.
func (dp *DiagnosticsPanel) SetUpdateBanner(c *UpdateBannerCard) { dp.updatePanel = c }

// SetResetInfraCard wires (or clears) the reset-infrastructure sub-card.
func (dp *DiagnosticsPanel) SetResetInfraCard(c *ResetInfraCard) { dp.resetInfraCard = c }

func (dp *DiagnosticsPanel) HandleUserInteractions(gtx layout.Context) {
	if dp.debugMode.Update(gtx) && dp.debugMode.Value != dp.debugSyncedTo {
		dp.debugSyncedTo = dp.debugMode.Value
		applog.SetDebug(dp.debugMode.Value)
		if cfg := dp.sm.Config(); cfg != nil {
			if err := cfg.SetDebugLogging(dp.debugMode.Value); err != nil {
				dp.state.ShowError("Debug logging: " + err.Error())
			}
		}
	}
	if dp.openLogFolder.Clicked(gtx) {
		dir := applog.LogDir()
		if dir == "" {
			dp.toasts.ShowError("Log folder unavailable — file logging is disabled")
		} else if err := utils.OpenDirectory(dir); err != nil {
			dp.toasts.ShowError("Open log folder: " + err.Error())
		}
	}
	if dp.copyLastLines.Clicked(gtx) {
		lines, err := applog.TailLines(TailLineCount)
		switch {
		case err != nil:
			dp.toasts.ShowError("Read log: " + err.Error())
		case len(lines) == 0:
			dp.toasts.ShowInfo("Log is empty")
		default:
			CopyToClipboard(gtx, applog.FormatTail(lines))
			dp.toasts.ShowSuccess("Copied last " + strconv.Itoa(len(lines)) + " lines")
		}
	}
	if dp.updatePanel != nil {
		dp.updatePanel.HandleUserInteractions(gtx)
	}
	if dp.resetInfraCard != nil {
		dp.resetInfraCard.HandleUserInteractions(gtx)
	}
}

func (dp *DiagnosticsPanel) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if dp.updatePanel == nil {
				return layout.Dimensions{}
			}
			return dp.updatePanel.Layout(gtx, th)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return panel(gtx, th, "Diagnostics", func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th.Theme, "Inspect logs and toggle verbose output. Logs live under ~/.locorum/logs.")
						lbl.Color = th.Color.Fg2
						lbl.TextSize = th.Sizes.Body
						return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						cb := material.CheckBox(th.Theme, &dp.debugMode, "Debug Mode (verbose logging)")
						cb.Color = th.Color.Fg
						cb.IconColor = th.Color.Accent
						cb.Size = unit.Dp(20)
						cb.TextSize = th.Sizes.Body
						return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, cb.Layout)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return SecondaryButton(gtx, th, &dp.openLogFolder, "Open Log Folder")
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return SecondaryButton(gtx, th, &dp.copyLastLines, "Copy Last 200 Lines")
								})
							}),
						)
					}),
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if dp.resetInfraCard == nil {
				return layout.Dimensions{}
			}
			return dp.resetInfraCard.Layout(gtx, th)
		}),
	)
}

// UpdateBannerCard and ResetInfraCard are placeholders for the cards
// added by §7.4 and §7.6 respectively. They are declared here so the
// DiagnosticsPanel compiles standalone; the per-task files supply the
// actual implementations and the wiring fills these closures in.
type UpdateBannerCard struct {
	HandleUserInteractionsFn func(layout.Context)
	LayoutFn                 func(layout.Context, *Theme) layout.Dimensions
}

func (c *UpdateBannerCard) HandleUserInteractions(gtx layout.Context) {
	if c.HandleUserInteractionsFn != nil {
		c.HandleUserInteractionsFn(gtx)
	}
}

func (c *UpdateBannerCard) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	if c.LayoutFn == nil {
		return layout.Dimensions{}
	}
	return c.LayoutFn(gtx, th)
}

type ResetInfraCard struct {
	HandleUserInteractionsFn func(layout.Context)
	LayoutFn                 func(layout.Context, *Theme) layout.Dimensions
}

func (c *ResetInfraCard) HandleUserInteractions(gtx layout.Context) {
	if c.HandleUserInteractionsFn != nil {
		c.HandleUserInteractionsFn(gtx)
	}
}

func (c *ResetInfraCard) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	if c.LayoutFn == nil {
		return layout.Dimensions{}
	}
	return c.LayoutFn(gtx, th)
}
