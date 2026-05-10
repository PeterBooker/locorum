package ui

import (
	"strconv"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// PortHoldersModal renders the multi-line lsof / Get-NetTCPConnection
// output the user gets when they click "Show port holders" on a
// port-conflict health finding. Single-instance because two port
// conflicts almost always share a root cause and the user only needs to
// see one — the state layer overwrites on each push.
//
// The modal is read-only: there's no Try-to-kill button. We deliberately
// don't offer a "kill PID" action because:
//   - We can't safely identify whether a foreign process is something
//     the user wants stopped (e.g. their staging Apache, OS firewall).
//   - On macOS / Windows the kill would need an elevation prompt we
//     can't service from a GUI process without a separate helper.
type PortHoldersModal struct {
	state *UIState

	closeBtn widget.Clickable
	scroll   widget.List

	keys *ModalFocus
	anim *modalShowState
}

// NewPortHoldersModal builds the modal. Wired into the root UI's
// HandleUserInteractions + Layout passes.
func NewPortHoldersModal(state *UIState) *PortHoldersModal {
	m := &PortHoldersModal{
		state: state,
		keys:  NewModalFocus(),
		anim:  NewModalAnim(),
	}
	m.scroll.Axis = layout.Vertical
	return m
}

// HandleUserInteractions processes Close clicks + Escape key. Called by
// the root UI before Layout when the modal is visible.
func (m *PortHoldersModal) HandleUserInteractions(gtx layout.Context) {
	show, _, _ := m.state.GetPortHoldersModal()
	if !show {
		return
	}
	keys := ProcessModalKeys(gtx, m.keys.Tag)
	if m.closeBtn.Clicked(gtx) || keys.Escape {
		m.state.DismissPortHoldersModal()
		m.keys.OnHide()
		m.anim.Hide()
	}
}

// Layout renders the modal. Returns zero dims when the modal is hidden
// so the caller's tree collapses cleanly.
func (m *PortHoldersModal) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	show, port, text := m.state.GetPortHoldersModal()
	if !show {
		return layout.Dimensions{}
	}

	m.anim.Show()
	return AnimatedModalOverlay(gtx, th, m.anim, func(gtx layout.Context) layout.Dimensions {
		m.keys.Layout(gtx)

		title := "Port " + strconv.Itoa(port) + " is held by:"
		if port == 0 {
			title = "Port holder details"
		}

		body := text
		if body == "" {
			body = "(no output)"
		}

		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.H6(th.Theme, title)
				lbl.Color = th.Color.TextStrong
				lbl.Font.Weight = font.SemiBold
				return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, lbl.Layout)
			}),
			// Output area. The shell-out result can be a few hundred
			// chars (a busy port can list many sockets in IPv4 + IPv6
			// pairs) — wrap it in a scrollable list of one tall label
			// so it stays bounded by the modal frame.
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return RoundedFill(gtx, th.Color.Bg2, th.Radii.R2, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						// Cap modal height so a runaway lsof output
						// doesn't push the close button off-screen.
						gtx.Constraints.Max.Y = gtx.Dp(unit.Dp(280))
						return material.List(th.Theme, &m.scroll).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
							lbl := material.Body2(th.Theme, body)
							lbl.Color = th.Color.Fg
							lbl.TextSize = th.Sizes.Mono
							lbl.Font = MonoFont
							return lbl.Layout(gtx)
						})
					})
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, "Stop the listed process and re-check from the System Health panel.")
				lbl.Color = th.Color.TextSecondary
				lbl.TextSize = th.Sizes.Body
				return layout.Inset{Top: th.Spacing.SM, Bottom: th.Spacing.MD}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceStart}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return PrimaryButton(gtx, th, &m.closeBtn, "Close")
					}),
				)
			}),
		)
	})
}
