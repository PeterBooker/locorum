package ui

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
)

// LogViewer renders the container log viewer panel with service selector and output area.
type LogViewer struct {
	state *UIState
	sm    *sites.SiteManager

	serviceDropdown *Dropdown
	refreshBtn      widget.Clickable
	output          *OutputView
}

func NewLogViewer(state *UIState, sm *sites.SiteManager) *LogViewer {
	return &LogViewer{
		state:           state,
		sm:              sm,
		serviceDropdown: NewDropdown([]string{"web", "php", "database", "redis"}),
		output:          NewOutputView(),
	}
}

func (lv *LogViewer) Layout(gtx layout.Context, th *Theme, siteID string) layout.Dimensions {
	logOutput, _, logLoading := lv.state.GetLogState()

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// Section title
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.H6(th.Theme, "Container Logs")
			return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
		}),
		// Controls: dropdown + refresh
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Max.X = gtx.Dp(unit.Dp(160))
						return lv.serviceDropdown.Layout(gtx, th, "Service")
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							if logLoading {
								return Loader(gtx, th, th.Dims.LoaderSizeSM)
							}
							return SecondaryButton(gtx, th, &lv.refreshBtn, "Refresh")
						})
					}),
				)
			})
		}),
		// Log output area
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(20)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return lv.output.Layout(gtx, th, logOutput, "Click Refresh to load logs", th.Dims.OutputAreaMax)
			})
		}),
	)
}

// HandleUserInteractions processes the Refresh button click.
func (lv *LogViewer) HandleUserInteractions(gtx layout.Context, siteID string) {
	if lv.refreshBtn.Clicked(gtx) {
		service := lv.serviceDropdown.Options[lv.serviceDropdown.Selected]
		lv.state.SetLogLoading(true)

		go func() {
			output, err := lv.sm.GetContainerLogs(siteID, service, 100)
			if err != nil {
				lv.state.SetLogOutput(service, "Error: "+err.Error())
			} else {
				lv.state.SetLogOutput(service, output)
			}
			lv.state.SetLogLoading(false)
		}()
	}
}
