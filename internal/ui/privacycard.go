package ui

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	settings "github.com/PeterBooker/locorum/internal/config"
)

// PrivacyCard is the Settings → Privacy panel: telemetry opt-in toggle
// + reset-client-id button. Tied to the same KeyTelemetryOptIn /
// KeyTelemetryClient settings that the first-launch modal flips, so a
// user who declined at launch can come back here and opt in later (or
// the reverse).
//
// Reset Telemetry ID clears the persisted client ID so the next event
// generates a fresh one — useful when the user wants to disconnect
// their session from prior history without disabling telemetry.
type PrivacyCard struct {
	state  *UIState
	cfg    *settings.Config
	toasts *Notifications

	optIn       widget.Bool
	resetIDBtn  widget.Clickable
	syncedOptIn bool
}

func NewPrivacyCard(state *UIState, cfg *settings.Config, toasts *Notifications) *PrivacyCard {
	p := &PrivacyCard{state: state, cfg: cfg, toasts: toasts}
	if cfg != nil {
		p.optIn.Value = cfg.TelemetryOptIn()
		p.syncedOptIn = p.optIn.Value
	}
	return p
}

func (p *PrivacyCard) HandleUserInteractions(gtx layout.Context) {
	if p.cfg == nil {
		return
	}
	if p.optIn.Update(gtx) && p.optIn.Value != p.syncedOptIn {
		p.syncedOptIn = p.optIn.Value
		if err := p.cfg.SetTelemetryOptIn(p.optIn.Value); err != nil {
			p.state.ShowError("Save telemetry preference: " + err.Error())
		}
	}
	if p.resetIDBtn.Clicked(gtx) {
		if err := p.cfg.SetTelemetryClientID(""); err != nil {
			p.state.ShowError("Reset telemetry ID: " + err.Error())
			return
		}
		p.toasts.ShowSuccess("Telemetry ID cleared — a fresh ID will be generated on the next opt-in event")
	}
}

func (p *PrivacyCard) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	return panel(gtx, th, "Privacy", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, "Anonymous usage data helps us prioritise the right work. We never send file paths, URLs, or credentials.")
				lbl.Color = th.Color.Fg2
				lbl.TextSize = th.Sizes.Body
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				cb := material.CheckBox(th.Theme, &p.optIn, "Send anonymous usage data")
				cb.Color = th.Color.Fg
				cb.IconColor = th.Color.Accent
				cb.Size = unit.Dp(20)
				cb.TextSize = th.Sizes.Body
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, cb.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return SecondaryButton(gtx, th, &p.resetIDBtn, "Reset Telemetry ID")
			}),
		)
	})
}
