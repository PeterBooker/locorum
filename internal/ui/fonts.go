package ui

import (
	"embed"
	"fmt"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
)

//go:embed fonts/*.ttf
var fontFS embed.FS

// LoadFontCollection parses the embedded Inter (sans-serif) and JetBrains
// Mono (monospace) faces, plus the bundled gofont family as a unicode
// fallback, and returns them as a font.FontFace collection suitable for
// text.NewShaper. Without the gofont fallback, glyphs missing from Inter or
// JetBrains Mono cause Gio to escape to system fontconfig, which on Linux
// triggers GLib-GIO-CRITICAL warnings on every frame.
func LoadFontCollection() ([]font.FontFace, error) {
	specs := []struct {
		path     string
		typeface string
		style    font.Style
		weight   font.Weight
	}{
		{"fonts/Inter-Regular.ttf", "Inter", font.Regular, font.Normal},
		{"fonts/Inter-Bold.ttf", "Inter", font.Regular, font.Bold},
		{"fonts/Inter-Italic.ttf", "Inter", font.Italic, font.Normal},
		{"fonts/JetBrainsMono-Regular.ttf", "JetBrains Mono", font.Regular, font.Normal},
		{"fonts/JetBrainsMono-Bold.ttf", "JetBrains Mono", font.Regular, font.Bold},
	}

	out := make([]font.FontFace, 0, len(specs)+4)
	for _, s := range specs {
		data, err := fontFS.ReadFile(s.path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", s.path, err)
		}
		face, err := opentype.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", s.path, err)
		}
		out = append(out, font.FontFace{
			Font: font.Font{
				Typeface: font.Typeface(s.typeface),
				Style:    s.style,
				Weight:   s.weight,
			},
			Face: face,
		})
	}

	// Append gofont as a coverage fallback. Gio's shaper consults registered
	// faces in order; appending after Inter/JBM means it only kicks in for
	// glyphs the primary fonts can't render.
	out = append(out, gofont.Collection()...)
	return out, nil
}

// MonoFont is the font.Font value to use for code/output text (logs, WP-CLI
// output, link checker, DB credentials).
var MonoFont = font.Font{Typeface: "JetBrains Mono"}
