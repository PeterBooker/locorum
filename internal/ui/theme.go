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
	Mode    ThemeMode // effective mode; ThemeSystem resolved to Dark or Light
}

// NewTheme constructs a Theme with the dark palette as default. Use
// (*Theme).SetMode to switch palettes at runtime.
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
	mt.TextSize = sizes.Base

	t := &Theme{
		Theme:   mt,
		Spacing: DefaultSpacing(),
		Sizes:   sizes,
		Radii:   DefaultRadii(),
		Dims:    DefaultDims(),
	}
	t.SetMode(ThemeDark)
	return t
}

// SetMode swaps the active palette to the requested mode. ThemeSystem resolves
// to Dark or Light via DetectSystemTheme().
func (t *Theme) SetMode(mode ThemeMode) {
	t.Mode = mode
	resolved := mode
	if mode == ThemeSystem {
		resolved = DetectSystemTheme()
	}
	if resolved == ThemeLight {
		t.Color = LightPalette()
	} else {
		t.Color = DarkPalette()
	}
	t.Theme.Fg = t.Color.TextPrimary
	t.Theme.Bg = t.Color.ContentBg
	t.Theme.ContrastBg = t.Color.Primary
	t.Theme.ContrastFg = t.Color.OnPrimary
}

// ParseThemeMode converts a stored string into a ThemeMode. Unknown values
// fall back to ThemeDark.
func ParseThemeMode(s string) ThemeMode {
	switch s {
	case "light":
		return ThemeLight
	case "system":
		return ThemeSystem
	default:
		return ThemeDark
	}
}

// String returns the canonical persistence string for a ThemeMode.
func (m ThemeMode) String() string {
	switch m {
	case ThemeLight:
		return "light"
	case ThemeSystem:
		return "system"
	default:
		return "dark"
	}
}

// ─── Color math helpers ─────────────────────────────────────────────────────

// Hovered returns a slightly lighter (dark theme) or darker (light theme)
// variant of the input color, suitable for hover states.
func (t *Theme) Hovered(c color.NRGBA) color.NRGBA {
	if t.Mode == ThemeLight {
		return darken(c, 0.08)
	}
	return lighten(c, 0.10)
}

// Disabled returns a desaturated, alpha-reduced version of the input color,
// suitable for disabled controls.
func (t *Theme) Disabled(c color.NRGBA) color.NRGBA {
	out := c
	out.A = uint8(float32(out.A) * 0.45)
	return out
}

// ContrastText returns black or white depending on whether the input
// background color is light or dark, optimised for body text legibility.
func (t *Theme) ContrastText(bg color.NRGBA) color.NRGBA {
	// Relative luminance (sRGB approximation).
	r := float32(bg.R) / 255
	g := float32(bg.G) / 255
	b := float32(bg.B) / 255
	luma := 0.2126*r + 0.7152*g + 0.0722*b
	if luma > 0.5 {
		return color.NRGBA{R: 13, G: 13, B: 43, A: 255}
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

// ─── Palette ────────────────────────────────────────────────────────────────

// Palette holds the semantic color set for a single theme mode (dark or light).
type Palette struct {
	// Surface hierarchy
	SidebarBg       color.NRGBA // deepest — sidebar background
	ContentBg       color.NRGBA // main content area
	SurfaceDeep     color.NRGBA // deep elevated surface
	Surface         color.NRGBA // elevated surface (output areas)
	SurfaceAlt      color.NRGBA // secondary surface (compact buttons)
	SurfaceElevated color.NRGBA // modal / dropdown popup background

	// Text hierarchy
	TextPrimary   color.NRGBA // body text
	TextStrong    color.NRGBA // labels
	TextSecondary color.NRGBA // secondary labels
	TextMuted     color.NRGBA // muted text / icons

	// Contrast text on colored surfaces
	OnPrimary color.NRGBA // text on Primary-colored surfaces

	// Brand
	Brand color.NRGBA // gold accent

	// Semantic
	Primary    color.NRGBA // primary action (neon cyan in dark)
	Danger     color.NRGBA // destructive (hot pink in dark)
	DangerDeep color.NRGBA // error banner
	Success    color.NRGBA // success (neon green in dark)

	// Toast/badge surfaces
	InfoBg    color.NRGBA
	InfoFg    color.NRGBA
	SuccessBg color.NRGBA
	SuccessFg color.NRGBA
	DangerBg  color.NRGBA
	DangerFg  color.NRGBA

	// Structural
	Border        color.NRGBA // muted border
	BorderFocused color.NRGBA // focused input border
	Separator     color.NRGBA // divider line

	// Overlay
	Overlay color.NRGBA // modal backdrop (alpha-blended black)

	// Utility
	White color.NRGBA
}

// LightPalette returns the light-mode palette: warm white surfaces, navy
// text, and the same accent hues (teal primary, pink danger, gold brand,
// green success) tuned for contrast on a light background.
func LightPalette() *Palette {
	primary := color.NRGBA{R: 0, G: 122, B: 153, A: 255} // deep teal
	border := color.NRGBA{R: 210, G: 214, B: 230, A: 255}
	return &Palette{
		SidebarBg:       color.NRGBA{R: 240, G: 241, B: 248, A: 255},
		ContentBg:       color.NRGBA{R: 250, G: 250, B: 253, A: 255},
		SurfaceDeep:     color.NRGBA{R: 235, G: 236, B: 244, A: 255},
		Surface:         color.NRGBA{R: 255, G: 255, B: 255, A: 255},
		SurfaceAlt:      color.NRGBA{R: 244, G: 245, B: 251, A: 255},
		SurfaceElevated: color.NRGBA{R: 255, G: 255, B: 255, A: 255},

		TextPrimary:   color.NRGBA{R: 13, G: 13, B: 43, A: 255},
		TextStrong:    color.NRGBA{R: 24, G: 24, B: 64, A: 255},
		TextSecondary: color.NRGBA{R: 80, G: 80, B: 120, A: 255},
		TextMuted:     color.NRGBA{R: 130, G: 130, B: 160, A: 255},

		OnPrimary: color.NRGBA{R: 255, G: 255, B: 255, A: 255},

		Brand: color.NRGBA{R: 200, G: 150, B: 0, A: 255}, // dimmed gold

		Primary:    primary,
		Danger:     color.NRGBA{R: 200, G: 30, B: 90, A: 255},
		DangerDeep: color.NRGBA{R: 160, G: 20, B: 70, A: 255},
		Success:    color.NRGBA{R: 0, G: 150, B: 90, A: 255},

		InfoBg:    color.NRGBA{R: 224, G: 242, B: 254, A: 255},
		InfoFg:    color.NRGBA{R: 14, G: 116, B: 144, A: 255},
		SuccessBg: color.NRGBA{R: 220, G: 252, B: 231, A: 255},
		SuccessFg: color.NRGBA{R: 22, G: 101, B: 52, A: 255},
		DangerBg:  color.NRGBA{R: 254, G: 226, B: 226, A: 255},
		DangerFg:  color.NRGBA{R: 153, G: 27, B: 27, A: 255},

		Border:        border,
		BorderFocused: primary,
		Separator:     border,

		Overlay: color.NRGBA{R: 0, G: 0, B: 0, A: 96},

		White: color.NRGBA{R: 255, G: 255, B: 255, A: 255},
	}
}

// DarkPalette returns the hacktoberfest-inspired dark palette
// (navy backgrounds + neon cyan primary + gold brand).
func DarkPalette() *Palette {
	primary := color.NRGBA{R: 0, G: 212, B: 255, A: 255}  // #00d4ff
	border := color.NRGBA{R: 53, G: 53, B: 102, A: 255}   // #353566
	return &Palette{
		SidebarBg:       color.NRGBA{R: 7, G: 7, B: 26, A: 255},    // #07071a
		ContentBg:       color.NRGBA{R: 13, G: 13, B: 43, A: 255},  // #0d0d2b
		SurfaceDeep:     color.NRGBA{R: 18, G: 18, B: 42, A: 255},  // #12122a
		Surface:         color.NRGBA{R: 26, G: 26, B: 62, A: 255},  // #1a1a3e
		SurfaceAlt:      color.NRGBA{R: 42, G: 42, B: 80, A: 255},  // #2a2a50
		SurfaceElevated: color.NRGBA{R: 37, G: 37, B: 80, A: 255},  // #252550

		TextPrimary:   color.NRGBA{R: 232, G: 232, B: 255, A: 255}, // #e8e8ff
		TextStrong:    color.NRGBA{R: 200, G: 200, B: 230, A: 255}, // #c8c8e6
		TextSecondary: color.NRGBA{R: 144, G: 144, B: 200, A: 255}, // #9090c8
		TextMuted:     color.NRGBA{R: 120, G: 120, B: 180, A: 255}, // #7878b4

		OnPrimary: color.NRGBA{R: 13, G: 13, B: 43, A: 255}, // navy dark

		Brand: color.NRGBA{R: 255, G: 215, B: 0, A: 255}, // #ffd700

		Primary:    primary,
		Danger:     color.NRGBA{R: 255, G: 45, B: 117, A: 255}, // #ff2d75
		DangerDeep: color.NRGBA{R: 180, G: 30, B: 80, A: 255},  // #b41e50
		Success:    color.NRGBA{R: 0, G: 230, B: 118, A: 255},  // #00e676

		InfoBg:    color.NRGBA{R: 13, G: 33, B: 55, A: 255},    // #0d2137
		InfoFg:    color.NRGBA{R: 79, G: 195, B: 247, A: 255},  // #4fc3f7
		SuccessBg: color.NRGBA{R: 13, G: 55, B: 33, A: 255},    // #0d3721
		SuccessFg: color.NRGBA{R: 105, G: 240, B: 174, A: 255}, // #69f0ae
		DangerBg:  color.NRGBA{R: 55, G: 13, B: 25, A: 255},    // #370d19
		DangerFg:  color.NRGBA{R: 255, G: 82, B: 82, A: 255},   // #ff5252

		Border:        border,
		BorderFocused: primary,
		Separator:     border,

		Overlay: color.NRGBA{R: 0, G: 0, B: 0, A: 160},

		White: color.NRGBA{R: 255, G: 255, B: 255, A: 255},
	}
}

// ─── Spacing ────────────────────────────────────────────────────────────────

// Spacing is the padding/margin scale. Minimum 6dp for visual breathing room.
type Spacing struct {
	XS  unit.Dp
	SM  unit.Dp
	MD  unit.Dp
	LG  unit.Dp
	XL  unit.Dp
	XXL unit.Dp
}

func DefaultSpacing() *Spacing {
	return &Spacing{
		XS:  unit.Dp(6),
		SM:  unit.Dp(10),
		MD:  unit.Dp(16),
		LG:  unit.Dp(20),
		XL:  unit.Dp(32),
		XXL: unit.Dp(40),
	}
}

// ─── Text Sizes ─────────────────────────────────────────────────────────────

// TextSizes is the typography scale. Minimum 18sp for accessibility.
type TextSizes struct {
	XS   unit.Sp
	SM   unit.Sp
	Base unit.Sp
	LG   unit.Sp
}

func DefaultTextSizes() *TextSizes {
	return &TextSizes{
		XS:   unit.Sp(18),
		SM:   unit.Sp(18),
		Base: unit.Sp(20),
		LG:   unit.Sp(24),
	}
}

// ─── Radii ──────────────────────────────────────────────────────────────────

// Radii is the corner-radius scale.
type Radii struct {
	SM unit.Dp
	MD unit.Dp
	LG unit.Dp
}

func DefaultRadii() *Radii {
	return &Radii{
		SM: unit.Dp(6),
		MD: unit.Dp(8),
		LG: unit.Dp(12),
	}
}

// ─── Dimensions ─────────────────────────────────────────────────────────────

// Dims holds fixed layout dimensions (sidebar width, modal width, loader
// sizes, etc.) that are independent of theme mode.
type Dims struct {
	SidebarWidth  unit.Dp
	ModalWidth    unit.Dp
	LoaderSize    unit.Dp
	LoaderSizeSM  unit.Dp
	OutputAreaMax unit.Dp
	LabelColWidth unit.Dp
}

func DefaultDims() *Dims {
	return &Dims{
		SidebarWidth:  unit.Dp(300),
		ModalWidth:    unit.Dp(560),
		LoaderSize:    unit.Dp(40),
		LoaderSizeSM:  unit.Dp(32),
		OutputAreaMax: unit.Dp(350),
		LabelColWidth: unit.Dp(140),
	}
}
