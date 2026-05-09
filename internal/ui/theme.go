package ui

import (
	"image/color"
	"log/slog"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// ─── Theme Mode ─────────────────────────────────────────────────────────────

// ThemeMode selects the active color palette. ThemeSystem follows the host OS
// setting (resolved to Dark or Light at theme construction / switch time).
type ThemeMode int

const (
	ThemeDark ThemeMode = iota
	ThemeLight
	ThemeSystem
)

// ─── Theme ──────────────────────────────────────────────────────────────────

// Theme bundles the material theme plus the custom palette, spacing, text
// sizes, corner radii, and layout dimensions. It is threaded through every
// Layout() function in the UI. Access the underlying *material.Theme via
// th.Theme (e.g. material.Body2(th.Theme, "...")).
type Theme struct {
	*material.Theme
	Color   *Palette
	Spacing *Spacing
	Sizes   *TextSizes
	Radii   *Radii
	Dims    *Dims
	Mode    ThemeMode // user preference (Dark, Light, or System)
}

// NewTheme constructs a Theme with the system-resolved palette as default.
// Use (*Theme).SetMode to switch palettes at runtime.
func NewTheme() *Theme {
	mt := material.NewTheme()

	collection, err := LoadFontCollection()
	if err != nil {
		slog.Warn("Falling back to gofont", "err", err)
		collection = gofont.Collection()
	}
	mt.Shaper = text.NewShaper(text.WithCollection(collection))
	mt.Face = font.Typeface("Inter")

	sizes := DefaultTextSizes()
	mt.TextSize = sizes.Body

	t := &Theme{
		Theme:   mt,
		Spacing: DefaultSpacing(),
		Sizes:   sizes,
		Radii:   DefaultRadii(),
		Dims:    DefaultDims(),
	}
	t.SetMode(ThemeSystem)
	return t
}

// SetMode swaps the active palette to the requested mode. ThemeSystem resolves
// to Dark or Light via DetectSystemTheme().
func (th *Theme) SetMode(mode ThemeMode) {
	th.Mode = mode
	if th.ResolvedMode() == ThemeLight {
		th.Color = LightPalette()
	} else {
		th.Color = DarkPalette()
	}
	th.Fg = th.Color.Fg
	th.Bg = th.Color.Bg
	th.ContrastBg = th.Color.Accent
	th.ContrastFg = th.Color.AccentFg
}

// ResolvedMode returns the effective theme mode, resolving ThemeSystem to
// either ThemeDark or ThemeLight via DetectSystemTheme.
func (th *Theme) ResolvedMode() ThemeMode {
	if th.Mode == ThemeSystem {
		return DetectSystemTheme()
	}
	return th.Mode
}

// ParseThemeMode converts a stored string into a ThemeMode. Unknown / empty
// values fall back to ThemeSystem (so a freshly-installed app follows the OS).
func ParseThemeMode(s string) ThemeMode {
	switch s {
	case "light":
		return ThemeLight
	case "dark":
		return ThemeDark
	default:
		return ThemeSystem
	}
}

// String returns the canonical persistence string for a ThemeMode.
func (m ThemeMode) String() string {
	switch m {
	case ThemeLight:
		return "light"
	case ThemeDark:
		return "dark"
	default:
		return "system"
	}
}

// ─── Color math helpers ─────────────────────────────────────────────────────

// Hovered returns a slightly lighter (dark theme) or darker (light theme)
// variant of the input color, suitable for hover states.
func (th *Theme) Hovered(c color.NRGBA) color.NRGBA {
	if th.ResolvedMode() == ThemeLight {
		return darken(c, 0.08)
	}
	return lighten(c, 0.10)
}

// Disabled returns a desaturated, alpha-reduced version of the input color,
// suitable for disabled controls.
func (th *Theme) Disabled(c color.NRGBA) color.NRGBA {
	out := c
	out.A = uint8(float32(out.A) * 0.45)
	return out
}

// ContrastText returns black or white depending on whether the input
// background color is light or dark, optimised for body text legibility.
func (th *Theme) ContrastText(bg color.NRGBA) color.NRGBA {
	r := float32(bg.R) / 255
	g := float32(bg.G) / 255
	b := float32(bg.B) / 255
	luma := 0.2126*r + 0.7152*g + 0.0722*b
	if luma > 0.5 {
		return color.NRGBA{R: 19, G: 19, B: 22, A: 255}
	}
	return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
}

func lighten(c color.NRGBA, frac float32) color.NRGBA {
	return color.NRGBA{
		R: clamp8(float32(c.R) + (255-float32(c.R))*frac),
		G: clamp8(float32(c.G) + (255-float32(c.G))*frac),
		B: clamp8(float32(c.B) + (255-float32(c.B))*frac),
		A: c.A,
	}
}

func darken(c color.NRGBA, frac float32) color.NRGBA {
	return color.NRGBA{
		R: clamp8(float32(c.R) * (1 - frac)),
		G: clamp8(float32(c.G) * (1 - frac)),
		B: clamp8(float32(c.B) * (1 - frac)),
		A: c.A,
	}
}

func clamp8(v float32) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// withAlpha returns a copy of c with its alpha replaced by a (0–255).
func withAlpha(c color.NRGBA, a uint8) color.NRGBA {
	return color.NRGBA{R: c.R, G: c.G, B: c.B, A: a}
}

// ─── Palette ────────────────────────────────────────────────────────────────

// Palette holds the semantic color set for a single theme mode (dark or
// light). The "design token" fields (Bg, Bg1, Fg, Accent, etc.) are the
// canonical names used by new layout code; the legacy fields below them
// (SidebarBg, Surface, TextPrimary, etc.) are aliases retained so existing
// widgets keep compiling — they map onto the same underlying colors.
type Palette struct {
	// ── Design tokens ─────────────────────────────────────────────────
	// Surface hierarchy: Bg flush / Bg1 panels / Bg2 hover-active /
	// Bg3 input fills.
	Bg  color.NRGBA
	Bg1 color.NRGBA
	Bg2 color.NRGBA
	Bg3 color.NRGBA

	// Lines: Line for muted dividers; LineStrong for input/button borders.
	Line       color.NRGBA
	LineStrong color.NRGBA

	// Foreground hierarchy.
	Fg  color.NRGBA // body text
	Fg2 color.NRGBA // secondary
	Fg3 color.NRGBA // muted

	// Accent (single retro cyan, used sparingly).
	Accent     color.NRGBA
	AccentSoft color.NRGBA // tint background
	AccentLine color.NRGBA // tint border
	AccentFg   color.NRGBA // text on accent surface

	// Status: each pair is solid (foreground / dot / border-base) and
	// soft (background tint).
	Ok       color.NRGBA
	OkSoft   color.NRGBA
	Warn     color.NRGBA
	WarnSoft color.NRGBA
	Err      color.NRGBA
	ErrSoft  color.NRGBA

	// Active/hover row tint (semi-transparent — paint over Bg1).
	RowActive color.NRGBA

	// Modal backdrop overlay.
	Overlay color.NRGBA

	// ── Legacy aliases (back-compat with existing widget code) ───────
	// Surface aliases.
	SidebarBg       color.NRGBA // Bg
	ContentBg       color.NRGBA // Bg
	SurfaceDeep     color.NRGBA // Bg1
	Surface         color.NRGBA // Bg1
	SurfaceAlt      color.NRGBA // Bg2
	SurfaceElevated color.NRGBA // Bg1

	// Text aliases.
	TextPrimary   color.NRGBA // Fg
	TextStrong    color.NRGBA // Fg
	TextSecondary color.NRGBA // Fg2
	TextMuted     color.NRGBA // Fg3
	OnPrimary     color.NRGBA // AccentFg

	// Brand alias — formerly gold; now the cyan accent.
	Brand color.NRGBA // Accent

	// Action aliases.
	Primary    color.NRGBA // Accent
	Danger     color.NRGBA // Err
	DangerDeep color.NRGBA // Err
	Success    color.NRGBA // Ok

	// Banner aliases.
	InfoBg    color.NRGBA // pre-blended cyan-tinted surface
	InfoFg    color.NRGBA // Accent
	SuccessBg color.NRGBA // pre-blended green-tinted surface
	SuccessFg color.NRGBA // Ok
	DangerBg  color.NRGBA // pre-blended red-tinted surface
	DangerFg  color.NRGBA // Err

	// Structural aliases.
	Border        color.NRGBA // Line
	BorderFocused color.NRGBA // Accent
	Separator     color.NRGBA // Line

	// Utility.
	White color.NRGBA
}

// LightPalette returns the light-mode palette: warm whites + neutral grays
// with the same retro-cyan accent.
func LightPalette() *Palette {
	bg := color.NRGBA{R: 254, G: 254, B: 254, A: 255}
	bg1 := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	bg2 := color.NRGBA{R: 244, G: 244, B: 246, A: 255}
	bg3 := color.NRGBA{R: 233, G: 233, B: 236, A: 255}

	line := color.NRGBA{R: 0, G: 0, B: 0, A: 20}       // ~8% black
	lineStrong := color.NRGBA{R: 0, G: 0, B: 0, A: 36} // ~14% black

	fg := color.NRGBA{R: 19, G: 19, B: 22, A: 255}
	fg2 := color.NRGBA{R: 63, G: 63, B: 70, A: 255}
	fg3 := color.NRGBA{R: 107, G: 107, B: 115, A: 255}

	accent := color.NRGBA{R: 79, G: 177, B: 199, A: 255}
	accentSoft := withAlpha(accent, 31) // 12%
	accentLine := withAlpha(accent, 82) // 32%
	accentFg := color.NRGBA{R: 255, G: 255, B: 255, A: 255}

	ok := color.NRGBA{R: 26, G: 159, B: 109, A: 255}
	okSoft := withAlpha(ok, 31)
	warn := color.NRGBA{R: 181, G: 138, B: 44, A: 255}
	warnSoft := withAlpha(warn, 36)
	errc := color.NRGBA{R: 188, G: 44, B: 44, A: 255}
	errSoft := withAlpha(errc, 31)

	rowActive := color.NRGBA{R: 0, G: 0, B: 0, A: 15} // 6% black
	overlay := color.NRGBA{R: 0, G: 0, B: 0, A: 96}

	// Pre-blended banner backgrounds (alpha-flattened over Bg1).
	infoBg := color.NRGBA{R: 233, G: 245, B: 248, A: 255}
	successBg := color.NRGBA{R: 226, G: 245, B: 235, A: 255}
	dangerBg := color.NRGBA{R: 250, G: 230, B: 230, A: 255}

	return &Palette{
		Bg: bg, Bg1: bg1, Bg2: bg2, Bg3: bg3,
		Line: line, LineStrong: lineStrong,
		Fg: fg, Fg2: fg2, Fg3: fg3,
		Accent: accent, AccentSoft: accentSoft, AccentLine: accentLine, AccentFg: accentFg,
		Ok: ok, OkSoft: okSoft,
		Warn: warn, WarnSoft: warnSoft,
		Err: errc, ErrSoft: errSoft,
		RowActive: rowActive,
		Overlay:   overlay,

		// Aliases.
		SidebarBg: bg, ContentBg: bg,
		SurfaceDeep: bg1, Surface: bg1,
		SurfaceAlt: bg2, SurfaceElevated: bg1,
		TextPrimary: fg, TextStrong: fg, TextSecondary: fg2, TextMuted: fg3,
		OnPrimary:  accentFg,
		Brand:      accent,
		Primary:    accent,
		Danger:     errc,
		DangerDeep: errc,
		Success:    ok,
		InfoBg:     infoBg, InfoFg: accent,
		SuccessBg: successBg, SuccessFg: ok,
		DangerBg: dangerBg, DangerFg: errc,
		Border:        line,
		BorderFocused: accent,
		Separator:     line,
		White:         color.NRGBA{R: 255, G: 255, B: 255, A: 255},
	}
}

// DarkPalette returns the dark-mode palette: deep neutral grays with the
// same retro-cyan accent.
func DarkPalette() *Palette {
	bg := color.NRGBA{R: 14, G: 15, B: 18, A: 255}
	bg1 := color.NRGBA{R: 22, G: 23, B: 27, A: 255}
	bg2 := color.NRGBA{R: 28, G: 29, B: 34, A: 255}
	bg3 := color.NRGBA{R: 37, G: 38, B: 44, A: 255}

	line := color.NRGBA{R: 255, G: 255, B: 255, A: 18}       // ~7% white
	lineStrong := color.NRGBA{R: 255, G: 255, B: 255, A: 33} // ~13% white

	fg := color.NRGBA{R: 244, G: 244, B: 246, A: 255}
	fg2 := color.NRGBA{R: 190, G: 190, B: 194, A: 255}
	fg3 := color.NRGBA{R: 151, G: 151, B: 156, A: 255}

	accent := color.NRGBA{R: 79, G: 177, B: 199, A: 255}
	accentSoft := withAlpha(accent, 31)
	accentLine := withAlpha(accent, 82)
	accentFg := color.NRGBA{R: 255, G: 255, B: 255, A: 255}

	ok := color.NRGBA{R: 79, G: 203, B: 139, A: 255}
	okSoft := withAlpha(ok, 41)
	warn := color.NRGBA{R: 221, G: 178, B: 99, A: 255}
	warnSoft := withAlpha(warn, 41)
	errc := color.NRGBA{R: 229, G: 94, B: 84, A: 255}
	errSoft := withAlpha(errc, 41)

	rowActive := color.NRGBA{R: 255, G: 255, B: 255, A: 13} // ~5% white
	overlay := color.NRGBA{R: 0, G: 0, B: 0, A: 160}

	// Pre-blended banner backgrounds for dark mode.
	infoBg := color.NRGBA{R: 28, G: 44, B: 53, A: 255}
	successBg := color.NRGBA{R: 28, G: 50, B: 38, A: 255}
	dangerBg := color.NRGBA{R: 53, G: 28, B: 28, A: 255}

	return &Palette{
		Bg: bg, Bg1: bg1, Bg2: bg2, Bg3: bg3,
		Line: line, LineStrong: lineStrong,
		Fg: fg, Fg2: fg2, Fg3: fg3,
		Accent: accent, AccentSoft: accentSoft, AccentLine: accentLine, AccentFg: accentFg,
		Ok: ok, OkSoft: okSoft,
		Warn: warn, WarnSoft: warnSoft,
		Err: errc, ErrSoft: errSoft,
		RowActive: rowActive,
		Overlay:   overlay,

		// Aliases.
		SidebarBg: bg, ContentBg: bg,
		SurfaceDeep: bg1, Surface: bg1,
		SurfaceAlt: bg2, SurfaceElevated: bg1,
		TextPrimary: fg, TextStrong: fg, TextSecondary: fg2, TextMuted: fg3,
		OnPrimary:  accentFg,
		Brand:      accent,
		Primary:    accent,
		Danger:     errc,
		DangerDeep: errc,
		Success:    ok,
		InfoBg:     infoBg, InfoFg: accent,
		SuccessBg: successBg, SuccessFg: ok,
		DangerBg: dangerBg, DangerFg: errc,
		Border:        line,
		BorderFocused: accent,
		Separator:     line,
		White:         color.NRGBA{R: 255, G: 255, B: 255, A: 255},
	}
}

// ─── Spacing ────────────────────────────────────────────────────────────────

// Spacing is the padding/margin scale, calibrated to the design's em rhythm
// at an 18px base (so 1em ≈ 18dp). XS≈0.35em, SM≈0.55em, MD≈0.85em,
// LG≈1.1em, XL≈1.55em.
type Spacing struct {
	XXS unit.Dp
	XS  unit.Dp
	SM  unit.Dp
	MD  unit.Dp
	LG  unit.Dp
	XL  unit.Dp
	XXL unit.Dp
}

func DefaultSpacing() *Spacing {
	return &Spacing{
		XXS: unit.Dp(4),
		XS:  unit.Dp(6),
		SM:  unit.Dp(10),
		MD:  unit.Dp(15),
		LG:  unit.Dp(20),
		XL:  unit.Dp(28),
		XXL: unit.Dp(40),
	}
}

// ─── Text Sizes ─────────────────────────────────────────────────────────────

// TextSizes is the typography scale, em-based at an 18sp base.
//
//	Micro   12sp — uppercase mono labels (~0.65em)
//	Mono    13sp — mono captions, status, domain (~0.72em)
//	MonoSM  14sp — mono values (~0.78em)
//	Body    15sp — body text, nav items (~0.85em)
//	Tab     15sp — tab labels (~0.82em, rounded up)
//	Header  17sp — detail header name, section heading (~0.92em)
//	H1      22sp — modal titles
//
// XS/SM/Base/LG are aliases retained for back-compat with existing widgets.
type TextSizes struct {
	Micro   unit.Sp
	Mono    unit.Sp
	MonoSM  unit.Sp
	Body    unit.Sp
	Tab     unit.Sp
	Header  unit.Sp
	Section unit.Sp
	H1      unit.Sp

	// Legacy aliases.
	XS   unit.Sp
	SM   unit.Sp
	Base unit.Sp
	LG   unit.Sp
}

func DefaultTextSizes() *TextSizes {
	micro := unit.Sp(12)
	mono := unit.Sp(13)
	monoSM := unit.Sp(14)
	body := unit.Sp(15)
	tab := unit.Sp(15)
	header := unit.Sp(17)
	section := unit.Sp(17)
	h1 := unit.Sp(22)
	return &TextSizes{
		Micro: micro, Mono: mono, MonoSM: monoSM,
		Body: body, Tab: tab,
		Header: header, Section: section,
		H1: h1,

		XS:   mono,
		SM:   monoSM,
		Base: body,
		LG:   section,
	}
}

// ─── Radii ──────────────────────────────────────────────────────────────────

// Radii is the corner-radius scale: R1 (4dp) for tight chips, R2 (6dp) for
// buttons/inputs, R3 (10dp) for panels, R4 (14dp) for the outer window.
// SM/MD/LG aliases retained for back-compat.
type Radii struct {
	R1 unit.Dp
	R2 unit.Dp
	R3 unit.Dp
	R4 unit.Dp

	SM unit.Dp
	MD unit.Dp
	LG unit.Dp
}

func DefaultRadii() *Radii {
	r1 := unit.Dp(4)
	r2 := unit.Dp(6)
	r3 := unit.Dp(10)
	r4 := unit.Dp(14)
	return &Radii{
		R1: r1, R2: r2, R3: r3, R4: r4,
		SM: r2, MD: r2, LG: r3,
	}
}

// ─── Dimensions ─────────────────────────────────────────────────────────────

// Dims holds fixed layout dimensions independent of theme mode.
//
//	RailExpanded   — Column 1 nav rail width, expanded.
//	RailCollapsed  — Column 1 nav rail width, collapsed (icons only).
//	SitesListWidth — Column 2 sites-list panel width.
type Dims struct {
	RailExpanded   unit.Dp
	RailCollapsed  unit.Dp
	SitesListWidth unit.Dp
	ModalWidth     unit.Dp
	LoaderSize     unit.Dp
	LoaderSizeSM   unit.Dp
	OutputAreaMax  unit.Dp
	LabelColWidth  unit.Dp

	// Legacy alias for the old single-sidebar layout.
	SidebarWidth unit.Dp
}

func DefaultDims() *Dims {
	rail := unit.Dp(216)
	railSm := unit.Dp(80)
	listW := unit.Dp(360)
	return &Dims{
		RailExpanded:   rail,
		RailCollapsed:  railSm,
		SitesListWidth: listW,
		ModalWidth:     unit.Dp(560),
		LoaderSize:     unit.Dp(40),
		LoaderSizeSM:   unit.Dp(32),
		OutputAreaMax:  unit.Dp(350),
		LabelColWidth:  unit.Dp(140),

		SidebarWidth: listW,
	}
}
