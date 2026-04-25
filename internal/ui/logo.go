package ui

import (
	"image"
	"image/color"
	"math"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// LogoSize is the default logo dimension used in the sidebar.
var LogoSize = unit.Dp(44)

// LayoutLogo draws the Locorum logo: a neon cyan hexagonal frame enclosing
// a hot-pink location pin with a gold center dot.
func LayoutLogo(gtx layout.Context, th *Theme, size unit.Dp) layout.Dimensions {
	s := float32(gtx.Dp(size))
	px := int(s)
	cx, cy := s/2, s/2

	logoHexOutline(gtx.Ops, cx, cy, s*0.45, s*0.055, th.Color.Primary)

	pinCy := cy - s*0.10
	pinR := s * 0.18
	logoFilledCircle(gtx.Ops, cx, pinCy, pinR, th.Color.Danger)

	triTopY := pinCy + pinR*0.50
	triBotY := cy + s*0.32
	triHW := s * 0.155
	logoFilledTriangle(gtx.Ops,
		cx-triHW, triTopY,
		cx+triHW, triTopY,
		cx, triBotY,
		th.Color.Danger,
	)

	logoFilledCircle(gtx.Ops, cx, pinCy, s*0.075, th.Color.Brand)

	return layout.Dimensions{Size: image.Pt(px, px)}
}

// logoHexOutline draws a pointy-top hexagon stroke.
func logoHexOutline(ops *op.Ops, cx, cy, r, width float32, col color.NRGBA) {
	var p clip.Path
	p.Begin(ops)
	for i := 0; i < 6; i++ {
		a := float64(i)*math.Pi/3.0 - math.Pi/2.0
		x := cx + r*float32(math.Cos(a))
		y := cy + r*float32(math.Sin(a))
		if i == 0 {
			p.MoveTo(f32.Pt(x, y))
		} else {
			p.LineTo(f32.Pt(x, y))
		}
	}
	p.Close()
	defer clip.Stroke{Path: p.End(), Width: width}.Op().Push(ops).Pop()
	paint.Fill(ops, col)
}

// logoFilledCircle draws a filled circle.
func logoFilledCircle(ops *op.Ops, cx, cy, r float32, col color.NRGBA) {
	ri := int(math.Round(float64(r)))
	cxi := int(math.Round(float64(cx)))
	cyi := int(math.Round(float64(cy)))
	defer clip.Ellipse{
		Min: image.Pt(cxi-ri, cyi-ri),
		Max: image.Pt(cxi+ri, cyi+ri),
	}.Push(ops).Pop()
	paint.Fill(ops, col)
}

// logoFilledTriangle draws a filled triangle.
func logoFilledTriangle(ops *op.Ops, x1, y1, x2, y2, x3, y3 float32, col color.NRGBA) {
	var p clip.Path
	p.Begin(ops)
	p.MoveTo(f32.Pt(x1, y1))
	p.LineTo(f32.Pt(x2, y2))
	p.LineTo(f32.Pt(x3, y3))
	p.Close()
	defer clip.Outline{Path: p.End()}.Op().Push(ops).Pop()
	paint.Fill(ops, col)
}
