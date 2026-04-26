package ui

import (
	"context"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
)

// WPCLIPanel renders the WP-CLI command input and output panel.
type WPCLIPanel struct {
	state *UIState
	sm    *sites.SiteManager

	editor widget.Editor
	runBtn widget.Clickable
	output *OutputView
}

func NewWPCLIPanel(state *UIState, sm *sites.SiteManager) *WPCLIPanel {
	wp := &WPCLIPanel{state: state, sm: sm, output: NewOutputView()}
	wp.editor.SingleLine = true
	return wp
}

func (wp *WPCLIPanel) Layout(gtx layout.Context, th *Theme, siteID string) layout.Dimensions {
	wpcliOutput, wpcliLoading := wp.state.GetWPCLIState()

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// Section title
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.H6(th.Theme, "WP-CLI")
			return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
		}),
		// Input row: "wp " prefix + editor + run button
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th.Theme, "wp ")
						lbl.TextSize = th.Sizes.Base
						return lbl.Layout(gtx)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return BorderedMonoEditor(gtx, th, &wp.editor, "plugin list --status=active")
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							if wpcliLoading {
								return Loader(gtx, th, th.Dims.LoaderSizeSM)
							}
							return PrimaryButton(gtx, th, &wp.runBtn, "Run")
						})
					}),
				)
			})
		}),
		// Output area
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(20)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return wp.output.Layout(gtx, th, wpcliOutput, "Run a WP-CLI command to see output", unit.Dp(250))
			})
		}),
	)
}

// HandleUserInteractions processes the Run button click.
func (wp *WPCLIPanel) HandleUserInteractions(gtx layout.Context, siteID string) {
	if wp.runBtn.Clicked(gtx) {
		cmdText := wp.editor.Text()
		if cmdText == "" {
			return
		}
		args := strings.Fields(cmdText)
		wp.state.SetWPCLILoading(true)

		go func() {
			output, err := wp.sm.ExecWPCLI(context.Background(), siteID, args)
			if err != nil {
				if output != "" {
					wp.state.SetWPCLIOutput(output + "\n\nError: " + err.Error())
				} else {
					wp.state.SetWPCLIOutput("Error: " + err.Error())
				}
			} else {
				wp.state.SetWPCLIOutput(output)
			}
			wp.state.SetWPCLILoading(false)
		}()
	}
}
