package ui

import (
	"image/color"

	"gioui.org/font/gofont"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// ─── Color Palette (Hacktoberfest-inspired dark theme) ───────────────────────

var (
	// Core text & overlay
	ColorWhite   = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	ColorBlack50 = color.NRGBA{R: 0, G: 0, B: 0, A: 160} // modal overlay

	// Backgrounds
	ColorSidebarBg = color.NRGBA{R: 7, G: 7, B: 26, A: 255}   // #07071a — darkest navy
	ColorContentBg = color.NRGBA{R: 13, G: 13, B: 43, A: 255}  // #0d0d2b — main content area
	ColorModalBg   = color.NRGBA{R: 37, G: 37, B: 80, A: 255}  // #252550 — modals & dropdown popups
	ColorNavyDark  = color.NRGBA{R: 13, G: 13, B: 43, A: 255}  // #0d0d2b — dark text on bright buttons

	// Text hierarchy
	ColorTextPrimary = color.NRGBA{R: 232, G: 232, B: 255, A: 255} // #e8e8ff — primary text

	// Accent
	ColorGold = color.NRGBA{R: 255, G: 215, B: 0, A: 255} // #ffd700 — gold highlights

	// Surface scale (dark theme)
	ColorGray100 = color.NRGBA{R: 26, G: 26, B: 62, A: 255}   // #1a1a3e — surface (output areas)
	ColorGray200 = color.NRGBA{R: 42, G: 42, B: 80, A: 255}   // #2a2a50 — surface light (compact btn bg)
	ColorGray300 = color.NRGBA{R: 53, G: 53, B: 102, A: 255}  // #353566 — subtle border
	ColorGray400 = color.NRGBA{R: 120, G: 120, B: 180, A: 255} // #7878b4 — muted text & icons
	ColorGray500 = color.NRGBA{R: 144, G: 144, B: 200, A: 255} // #9090c8 — secondary labels
	ColorGray700 = color.NRGBA{R: 200, G: 200, B: 230, A: 255} // #c8c8e6 — label text
	ColorGray900 = color.NRGBA{R: 18, G: 18, B: 42, A: 255}   // #12122a — deep surface

	// Primary — Neon Cyan
	ColorBlue600 = color.NRGBA{R: 0, G: 212, B: 255, A: 255} // #00d4ff — neon cyan
	ColorBlue700 = color.NRGBA{R: 0, G: 180, B: 220, A: 255} // #00b4dc — darker cyan

	// Danger — Hot Pink
	ColorRed600 = color.NRGBA{R: 255, G: 45, B: 117, A: 255} // #ff2d75 — hot pink
	ColorRed700 = color.NRGBA{R: 180, G: 30, B: 80, A: 255}  // #b41e50 — deep pink (error banner)

	// Success — Neon Green
	ColorGreen600 = color.NRGBA{R: 0, G: 230, B: 118, A: 255}  // #00e676 — neon green
	ColorGreen100 = color.NRGBA{R: 13, G: 55, B: 33, A: 255}   // #0d3721 — dark green surface
	ColorGreen800 = color.NRGBA{R: 105, G: 240, B: 174, A: 255} // #69f0ae — bright green text

	// Error toast
	ColorRed100 = color.NRGBA{R: 55, G: 13, B: 25, A: 255}  // #370d19 — dark red surface
	ColorRed800 = color.NRGBA{R: 255, G: 82, B: 82, A: 255}  // #ff5252 — bright red text

	// Info toast
	ColorBlue100 = color.NRGBA{R: 13, G: 33, B: 55, A: 255}   // #0d2137 — dark blue surface
	ColorBlue800 = color.NRGBA{R: 79, G: 195, B: 247, A: 255}  // #4fc3f7 — bright cyan text

	// Border
	ColorBorder = color.NRGBA{R: 53, G: 53, B: 102, A: 255} // #353566 — muted purple border
)

// ─── Spacing Scale (dp) — generous for accessibility ────────────────────────

var (
	SpaceXS  = unit.Dp(6)
	SpaceSM  = unit.Dp(10)
	SpaceMD  = unit.Dp(16)
	SpaceLG  = unit.Dp(20)
	SpaceXL  = unit.Dp(32)
	Space2XL = unit.Dp(40)
)

// ─── Typography Scale (sp) — minimum 18 sp for accessibility ────────────────

var (
	TextXS   = unit.Sp(18) // compact elements (output lines, small buttons)
	TextSM   = unit.Sp(18) // badges, secondary text
	TextBase = unit.Sp(20) // body text
	TextLG   = unit.Sp(24) // larger body / sub-headings
)

// ─── Border Radii (dp) ─────────────────────────────────────────────────────

var (
	RadiusSM = unit.Dp(6)
	RadiusMD = unit.Dp(8)
	RadiusLG = unit.Dp(12)
)

// ─── Layout Dimensions (dp) ────────────────────────────────────────────────

var (
	SidebarWidth  = unit.Dp(300)
	ModalWidth    = unit.Dp(560)
	LoaderSize    = unit.Dp(40)
	LoaderSizeSM  = unit.Dp(32)
	OutputAreaMax = unit.Dp(350)
	LabelColWidth = unit.Dp(140)
)

// ─── Theme ─────────────────────────────────────────────────────────────────

func NewTheme() *material.Theme {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	th.Fg = ColorTextPrimary // light text for dark backgrounds
	th.Bg = ColorContentBg   // dark background
	th.ContrastBg = ColorBlue600 // neon cyan for focus indicators & loaders
	th.ContrastFg = ColorNavyDark
	th.TextSize = TextBase
	return th
}
