package ui

import (
	"image/color"

	"gioui.org/font/gofont"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// ─── Color Palette (Tailwind-inspired) ───────────────────────────────────────

var (
	ColorWhite     = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	ColorBlack50   = color.NRGBA{R: 0, G: 0, B: 0, A: 128} // modal overlay
	ColorSidebarBg = color.NRGBA{R: 17, G: 24, B: 39, A: 255}

	ColorGray100 = color.NRGBA{R: 243, G: 244, B: 246, A: 255}
	ColorGray200 = color.NRGBA{R: 229, G: 231, B: 235, A: 255}
	ColorGray300 = color.NRGBA{R: 209, G: 213, B: 219, A: 255}
	ColorGray400 = color.NRGBA{R: 156, G: 163, B: 175, A: 255}
	ColorGray500 = color.NRGBA{R: 107, G: 114, B: 128, A: 255}
	ColorGray700 = color.NRGBA{R: 55, G: 65, B: 81, A: 255}
	ColorGray900 = color.NRGBA{R: 17, G: 24, B: 39, A: 255}

	ColorBlue600 = color.NRGBA{R: 37, G: 99, B: 235, A: 255}
	ColorBlue700 = color.NRGBA{R: 29, G: 78, B: 216, A: 255}

	ColorRed600 = color.NRGBA{R: 220, G: 38, B: 38, A: 255}
	ColorRed700 = color.NRGBA{R: 185, G: 28, B: 28, A: 255}

	ColorGreen600 = color.NRGBA{R: 22, G: 163, B: 74, A: 255}
	ColorGreen100 = color.NRGBA{R: 220, G: 252, B: 231, A: 255}
	ColorGreen800 = color.NRGBA{R: 22, G: 101, B: 52, A: 255}

	ColorRed100 = color.NRGBA{R: 254, G: 226, B: 226, A: 255}
	ColorRed800 = color.NRGBA{R: 153, G: 27, B: 27, A: 255}

	ColorBlue100 = color.NRGBA{R: 219, G: 234, B: 254, A: 255}
	ColorBlue800 = color.NRGBA{R: 30, G: 64, B: 175, A: 255}

	ColorBorder = color.NRGBA{R: 209, G: 213, B: 219, A: 255} // gray-300
)

// ─── Spacing Scale (dp) ─────────────────────────────────────────────────────

var (
	SpaceXS  = unit.Dp(4)
	SpaceSM  = unit.Dp(8)
	SpaceMD  = unit.Dp(12)
	SpaceLG  = unit.Dp(16)
	SpaceXL  = unit.Dp(24)
	Space2XL = unit.Dp(32)
)

// ─── Typography Scale (sp) ──────────────────────────────────────────────────

var (
	TextXS   = unit.Sp(11)
	TextSM   = unit.Sp(12)
	TextBase = unit.Sp(14)
	TextLG   = unit.Sp(16)
)

// ─── Border Radii (dp) ─────────────────────────────────────────────────────

var (
	RadiusSM = unit.Dp(4)
	RadiusMD = unit.Dp(6)
	RadiusLG = unit.Dp(8)
)

// ─── Layout Dimensions (dp) ────────────────────────────────────────────────

var (
	SidebarWidth  = unit.Dp(256)
	ModalWidth    = unit.Dp(500)
	LoaderSize    = unit.Dp(36)
	LoaderSizeSM  = unit.Dp(28)
	OutputAreaMax = unit.Dp(300)
	LabelColWidth = unit.Dp(100)
)

// ─── Theme ─────────────────────────────────────────────────────────────────

func NewTheme() *material.Theme {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	return th
}
