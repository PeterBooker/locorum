package ui

import (
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
)

// SettingsPanel takes over columns 2+3 when the nav rail's Settings item
// is active. It currently exposes a single Appearance section with a
// three-way theme picker (System / Light / Dark); changes are persisted
// via the parent UI's onThemeChange callback.
type SettingsPanel struct {
	state         *UIState
	sm            *sites.SiteManager
	onThemeChange func(ThemeMode)

	themeEnum widget.Enum
	syncedTo  string // remembers which value we last seeded from settings
}

// NewSettingsPanel constructs a SettingsPanel. onThemeChange is invoked
// whenever the user changes the theme mode (e.g. to apply + persist).
func NewSettingsPanel(state *UIState, sm *sites.SiteManager, onThemeChange func(ThemeMode)) *SettingsPanel {
	s := &SettingsPanel{state: state, sm: sm, onThemeChange: onThemeChange}
	if stored, err := sm.GetSetting(SettingKeyThemeMode); err == nil && stored != "" {
		s.themeEnum.Value = stored
		s.syncedTo = stored
	} else {
		s.themeEnum.Value = ThemeSystem.String()
		s.syncedTo = ThemeSystem.String()
	}
	return s
}

// HandleUserInteractions watches the theme picker for selection changes
// and forwards them through onThemeChange.
func (s *SettingsPanel) HandleUserInteractions(gtx layout.Context) {
	if s.themeEnum.Update(gtx) {
		if s.themeEnum.Value != s.syncedTo {
			s.syncedTo = s.themeEnum.Value
			if s.onThemeChange != nil {
				s.onThemeChange(ParseThemeMode(s.themeEnum.Value))
			}
		}
	}
}

func (s *SettingsPanel) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	return FillBackground(gtx, th.Color.Bg, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(28)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Bottom: th.Spacing.LG}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th.Theme, "Settings")
						lbl.Color = th.Color.Fg
						lbl.TextSize = th.Sizes.H1
						lbl.Font.Weight = font.SemiBold
						return lbl.Layout(gtx)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return s.layoutAppearance(gtx, th)
				}),
			)
		})
	})
}

func (s *SettingsPanel) layoutAppearance(gtx layout.Context, th *Theme) layout.Dimensions {
	return panel(gtx, th, "Appearance", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, "Choose how Locorum looks to you.")
				lbl.Color = th.Color.Fg2
				lbl.TextSize = th.Sizes.Body
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return s.layoutThemeOption(gtx, th, ThemeSystem.String(), "Follow system", "Match your OS appearance setting.")
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return s.layoutThemeOption(gtx, th, ThemeLight.String(), "Light", "Bright surfaces with high contrast.")
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return s.layoutThemeOption(gtx, th, ThemeDark.String(), "Dark", "Deep neutral grays for low-light work.")
			}),
		)
	})
}

func (s *SettingsPanel) layoutThemeOption(gtx layout.Context, th *Theme, key, title, desc string) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		rb := material.RadioButton(th.Theme, &s.themeEnum, key, title)
		rb.Color = th.Color.Fg
		rb.IconColor = th.Color.Accent
		rb.TextSize = th.Sizes.Body
		rb.Size = unit.Dp(20)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(rb.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, desc)
				lbl.Color = th.Color.Fg3
				lbl.TextSize = th.Sizes.Mono
				return layout.Inset{Top: unit.Dp(2), Left: unit.Dp(28)}.Layout(gtx, lbl.Layout)
			}),
		)
	})
}
