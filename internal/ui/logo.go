package ui

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// LogoSize is the default mark dimension used in the rail brand.
var LogoSize = unit.Dp(20)

// LayoutLogo draws the LocorumMark — a vertical accent bar plus three
// horizontal bars of decreasing width and decreasing opacity, forming a
// stylised "L". Geometry is specified in a 32×32 design grid; visible
// content occupies x: 5..27 (span 22) × y: 6..26 (span 20). The bars are
// scaled and offset so that visible content fills the requested size,
// preserving the original 22:20 aspect ratio.
func LayoutLogo(gtx layout.Context, th *Theme, size unit.Dp) layout.Dimensions {
	s := gtx.Dp(size)
	if s <= 0 {
		return layout.Dimensions{}
	}

	const (
		minX, minY = 5, 6
		spanX      = 22 // 27-5
		spanY      = 20 // 26-6
	)
	// Use the larger span (22) so the longer dimension fills `size`.
	scale := float32(s) / float32(spanX)
	contentW := int(float32(spanX) * scale)
	contentH := int(float32(spanY) * scale)
	// Center the (22×20) content in the (s×s) bounding box.
	offX := (s - contentW) / 2
	offY := (s - contentH) / 2

	bars := []struct {
		x, y, w, h int
		alpha      uint8
	}{
		{5, 6, 4, 20, 255},   // vertical
		{11, 22, 16, 4, 242}, // bottom horizontal
		{11, 15, 10, 3, 128}, // mid horizontal
		{11, 9, 6, 3, 71},    // top horizontal
	}
	for _, b := range bars {
		rect := image.Rect(
			offX+int(float32(b.x-minX)*scale),
			offY+int(float32(b.y-minY)*scale),
			offX+int(float32(b.x-minX+b.w)*scale),
			offY+int(float32(b.y-minY+b.h)*scale),
		)
		drawLogoBar(gtx, rect, withAlpha(th.Color.Accent, b.alpha))
	}
	return layout.Dimensions{Size: image.Point{X: s, Y: s}}
}

// drawLogoBar fills a rounded rectangle. The clip stack is balanced before
// returning so subsequent bars draw without leaking the prior shape.
func drawLogoBar(gtx layout.Context, rect image.Rectangle, col color.NRGBA) {
	r := gtx.Dp(unit.Dp(1))
	stack := clip.RRect{
		Rect: rect,
		NE:   r, NW: r, SE: r, SW: r,
	}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, col)
	stack.Pop()
}
