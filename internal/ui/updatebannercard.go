package ui

import (
	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"

	settings "github.com/PeterBooker/locorum/internal/config"
	"github.com/PeterBooker/locorum/internal/updatecheck"
	"github.com/PeterBooker/locorum/internal/utils"
)

// NewUpdateBannerCard builds the Diagnostics → "Update available" sub-
// card. The card only renders when:
//
//   - state.UpdateBannerSnapshot().Available is non-empty AND
//   - the available version is strictly newer than the dismissed one.
//
// onDismiss is called with the available version string after a click —
// it must persist via cfg.SetUpdateDismissedVersion AND call
// state.SetUpdateDismissed so the in-memory snapshot stays consistent.
// onView opens the release URL in the user's default browser.
//
// cfg is captured for the dismiss writeback. Pass nil during tests; the
// card then no-ops the dismiss button persistence step (the in-memory
// snapshot still updates).
func NewUpdateBannerCard(state *UIState, cfg *settings.Config) *UpdateBannerCard {
	u := &updateBannerImpl{state: state, cfg: cfg}
	return &UpdateBannerCard{
		HandleUserInteractionsFn: u.HandleUserInteractions,
		LayoutFn:                 u.Layout,
	}
}

type updateBannerImpl struct {
	state *UIState
	cfg   *settings.Config

	viewBtn    widget.Clickable
	dismissBtn widget.Clickable
}

func (u *updateBannerImpl) HandleUserInteractions(gtx layout.Context) {
	snap := u.state.UpdateBannerSnapshot()
	if !snap.HasUnreadUpdate(updatecheck.IsStrictlyNewer) {
		return
	}
	if u.viewBtn.Clicked(gtx) && snap.URL != "" {
		_ = utils.OpenURL(snap.URL)
	}
	if u.dismissBtn.Clicked(gtx) {
		if u.cfg != nil {
			_ = u.cfg.SetUpdateDismissedVersion(snap.Available)
		}
		u.state.SetUpdateDismissed(snap.Available)
	}
}

func (u *updateBannerImpl) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	snap := u.state.UpdateBannerSnapshot()
	if !snap.HasUnreadUpdate(updatecheck.IsStrictlyNewer) {
		return layout.Dimensions{}
	}
	return panel(gtx, th, "Update available", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, "Locorum v"+snap.Available+" is available.")
				lbl.Color = th.Color.Fg
				lbl.TextSize = th.Sizes.Body
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return PrimaryButton(gtx, th, &u.viewBtn, "View release notes")
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return SecondaryButton(gtx, th, &u.dismissBtn, "Dismiss this version")
						})
					}),
				)
			}),
		)
	})
}
