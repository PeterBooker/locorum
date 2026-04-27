package ui

import (
	"image"
	"strings"
	"unicode"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// SiteAvatar renders a rounded square containing the first two
// alphanumerics from name (uppercase mono semibold) on a Bg2 surface with a
// 1px Line border. Used in the sites list (size=36) and in the site
// detail header (size=30). Favicons are not yet wired in.
func SiteAvatar(gtx layout.Context, th *Theme, name string, size unit.Dp) layout.Dimensions {
	s := gtx.Dp(size)
	if s <= 0 {
		return layout.Dimensions{}
	}
	radius := unit.Dp(float32(size) * 0.24)
	rr := gtx.Dp(radius)

	gtx.Constraints.Min = image.Pt(s, s)
	gtx.Constraints.Max = image.Pt(s, s)

	border := widget.Border{
		Color:        th.Color.Line,
		CornerRadius: radius,
		Width:        unit.Dp(1),
	}
	return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		rect := image.Rectangle{Max: image.Pt(s, s)}
		bgStack := clip.RRect{Rect: rect, NE: rr, NW: rr, SE: rr, SW: rr}.Push(gtx.Ops)
		paint.Fill(gtx.Ops, th.Color.Bg2)
		bgStack.Pop()

		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			letters := avatarLetters(name)
			if letters == "" {
				return layout.Dimensions{}
			}
			lbl := material.Body2(th.Theme, letters)
			lbl.Color = th.Color.Fg2
			lbl.TextSize = unit.Sp(float32(size) * 0.36)
			lbl.Font = MonoFont
			lbl.Font.Weight = font.SemiBold
			return lbl.Layout(gtx)
		})
	})
}

// avatarLetters returns the first two alphanumeric runes of name, uppercased.
// Punctuation, whitespace, and other separators are stripped first so
// "acme-storefront" becomes "AC" rather than "AC-".
func avatarLetters(name string) string {
	var out []rune
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			continue
		}
		out = append(out, r)
		if len(out) == 2 {
			break
		}
	}
	return strings.ToUpper(string(out))
}
