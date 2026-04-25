package ui

import (
	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
)

// LinkChecker provides a UI panel for checking broken links on a running site.
type LinkChecker struct {
	state *UIState
	sm    *sites.SiteManager

	checkBtn widget.Clickable
	output   *OutputView
}

func NewLinkChecker(state *UIState, sm *sites.SiteManager) *LinkChecker {
	return &LinkChecker{state: state, sm: sm, output: NewOutputView()}
}

func (lc *LinkChecker) Layout(gtx layout.Context, th *Theme, siteID string) layout.Dimensions {
	output, loading := lc.state.GetLinkCheckState()

	return layout.Inset{Top: th.Spacing.LG}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.H6(th.Theme, "Link Checker")
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if loading {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return Loader(gtx, th, th.Dims.LoaderSizeSM)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							lbl := material.Body2(th.Theme, "  Checking links...")
							lbl.Color = th.Color.TextSecondary
							return lbl.Layout(gtx)
						}),
					)
				}
				return SecondaryButton(gtx, th, &lc.checkBtn, "Check Links")
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return lc.output.Layout(gtx, th, output, "Click Check Links to scan for broken links", th.Dims.OutputAreaMax)
				})
			}),
		)
	})
}

// HandleUserInteractions processes the Check Links button click.
func (lc *LinkChecker) HandleUserInteractions(gtx layout.Context, siteID string) {
	if lc.checkBtn.Clicked(gtx) {
		lc.state.SetLinkCheckOutput("")
		lc.state.SetLinkCheckLoading(true)

		if err := lc.sm.CheckLinks(siteID,
			func(line string) {
				lc.state.AppendLinkCheckOutput(line)
			},
			func() {
				lc.state.SetLinkCheckLoading(false)
			},
		); err != nil {
			lc.state.ShowError("Link check failed: " + err.Error())
			lc.state.SetLinkCheckLoading(false)
		}
	}
}
