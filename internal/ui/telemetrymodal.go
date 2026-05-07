package ui

import (
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"

	settings "github.com/PeterBooker/locorum/internal/config"
	"github.com/PeterBooker/locorum/internal/utils"
)

// telemetryPrivacyURL is the placeholder link surfaced by the modal's
// "Read privacy doc" button. The doc itself is gated on UX.md §13 — until
// it lands, the URL points at the repo so users at least see the canonical
// project page.
const telemetryPrivacyURL = "https://github.com/PeterBooker/locorum#telemetry"

// TelemetryModal is the first-launch opt-in dialog. Shown once per
// install (gated on KeyTelemetryDecided). The user has three choices:
// Opt in, Decline, or Read privacy doc. Any of the first two flips
// KeyTelemetryDecided=true and persists the chosen TelemetryOptIn; the
// privacy doc opens an external URL without dismissing the modal so the
// user can read and then choose.
type TelemetryModal struct {
	state *UIState
	cfg   *settings.Config

	optIn   widget.Clickable
	decline widget.Clickable
	docs    widget.Clickable

	anim *modalShowState
}

// NewTelemetryModal binds the modal to the persistent settings store
// and the UI state. Pass nil cfg in tests; the buttons then update only
// the in-memory state and never touch storage.
func NewTelemetryModal(state *UIState, cfg *settings.Config) *TelemetryModal {
	return &TelemetryModal{state: state, cfg: cfg, anim: NewModalAnim()}
}

// Show reports whether the modal should render this frame. True only on
// first launch (KeyTelemetryDecided still false). When cfg is nil the
// modal is permanently hidden — a defensive default for tests.
func (m *TelemetryModal) Show() bool {
	if m.cfg == nil {
		return false
	}
	return !m.cfg.TelemetryDecided()
}

// HandleUserInteractions reads the three button clicks. Returns true
// when the modal should be torn down (the user clicked Opt-in or
// Decline). The caller can then trigger a redraw / unmount.
func (m *TelemetryModal) HandleUserInteractions(gtx layout.Context) bool {
	if !m.Show() {
		return false
	}
	if m.docs.Clicked(gtx) {
		_ = utils.OpenURL(telemetryPrivacyURL)
		// Don't dismiss — the user is reading the policy.
	}
	if m.optIn.Clicked(gtx) {
		m.persistDecision(true)
		return true
	}
	if m.decline.Clicked(gtx) {
		m.persistDecision(false)
		return true
	}
	return false
}

func (m *TelemetryModal) persistDecision(optIn bool) {
	if m.cfg == nil {
		return
	}
	if err := m.cfg.SetTelemetryOptIn(optIn); err != nil {
		m.state.ShowError("Save telemetry choice: " + err.Error())
		return
	}
	if err := m.cfg.SetTelemetryDecided(true); err != nil {
		m.state.ShowError("Save telemetry choice: " + err.Error())
		return
	}
	m.state.Invalidate()
}

// Layout renders the modal over the supplied gtx. Caller is responsible
// for only calling this when Show() reports true; layering it under
// Stack/Stacked in the root layout preserves the existing modal shape.
func (m *TelemetryModal) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	m.anim.Show()
	return AnimatedModalOverlay(gtx, th, m.anim, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.H6(th.Theme, "Help improve Locorum?")
				lbl.Color = th.Color.Fg
				lbl.Font.Weight = font.SemiBold
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				body := "Locorum can send anonymous usage data — site lifecycle counts, feature use, and crash reports — so we can prioritise the right work. We never send file paths, URLs, or credentials."
				lbl := material.Body2(th.Theme, body)
				lbl.Color = th.Color.Fg2
				lbl.TextSize = th.Sizes.Body
				return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return PrimaryButton(gtx, th, &m.optIn, "Opt in")
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return SecondaryButton(gtx, th, &m.decline, "Decline")
						})
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Dimensions{Size: gtx.Constraints.Min}
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return th.Small(gtx, &m.docs, "Read privacy doc")
					}),
				)
			}),
		)
	})
}
