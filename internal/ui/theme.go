package ui

import (
	"image/color"

	"gioui.org/font/gofont"
	"gioui.org/text"
	"gioui.org/widget/material"
)

var (
	ColorSidebarBg = color.NRGBA{R: 17, G: 24, B: 39, A: 255}   // gray-900
	ColorWhite     = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	ColorGray100   = color.NRGBA{R: 243, G: 244, B: 246, A: 255}
	ColorGray200   = color.NRGBA{R: 229, G: 231, B: 235, A: 255}
	ColorGray300   = color.NRGBA{R: 209, G: 213, B: 219, A: 255}
	ColorGray400   = color.NRGBA{R: 156, G: 163, B: 175, A: 255}
	ColorGray500   = color.NRGBA{R: 107, G: 114, B: 128, A: 255}
	ColorGray700   = color.NRGBA{R: 55, G: 65, B: 81, A: 255}
	ColorGray900   = color.NRGBA{R: 17, G: 24, B: 39, A: 255}
	ColorBlue600   = color.NRGBA{R: 37, G: 99, B: 235, A: 255}
	ColorBlue700   = color.NRGBA{R: 29, G: 78, B: 216, A: 255}
	ColorRed600    = color.NRGBA{R: 220, G: 38, B: 38, A: 255}
	ColorGreen600  = color.NRGBA{R: 22, G: 163, B: 74, A: 255}
	ColorBlack50   = color.NRGBA{R: 0, G: 0, B: 0, A: 128} // modal overlay
	ColorBorder    = color.NRGBA{R: 209, G: 213, B: 219, A: 255}
)

func NewTheme() *material.Theme {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	return th
}
