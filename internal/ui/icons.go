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

// IconSize is the default icon dimension, sized to balance with body text.
var IconSize = unit.Dp(16)

// IconFunc paints a stroke icon at the requested size in the given color.
// All icons are designed against a 24×24 grid with a 1.6 stroke width and
// scale uniformly.
type IconFunc func(gtx layout.Context, th *Theme, size unit.Dp, col color.NRGBA) layout.Dimensions

// strokePolyline traces a sequence of points and renders it as a stroked
// path. Points are in the icon's design grid (0..24); scale converts to px.
func strokePolyline(gtx layout.Context, pts []f32.Point, scale, sw float32, col color.NRGBA, closed bool) {
	if len(pts) < 2 {
		return
	}
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(pts[0].X*scale, pts[0].Y*scale))
	for _, pt := range pts[1:] {
		p.LineTo(f32.Pt(pt.X*scale, pt.Y*scale))
	}
	if closed {
		p.Close()
	}
	spec := p.End()
	stack := clip.Stroke{Path: spec, Width: sw}.Op().Push(gtx.Ops)
	paint.Fill(gtx.Ops, col)
	stack.Pop()
}

// strokeCircle approximates a circle with four cubic Bezier segments and
// strokes the resulting closed path.
func strokeCircle(gtx layout.Context, cx, cy, r, sw float32, col color.NRGBA) {
	const k = 0.5522847498 // 4*(sqrt(2)-1)/3
	d := r * k
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(cx+r, cy))
	p.CubeTo(f32.Pt(cx+r, cy-d), f32.Pt(cx+d, cy-r), f32.Pt(cx, cy-r))
	p.CubeTo(f32.Pt(cx-d, cy-r), f32.Pt(cx-r, cy-d), f32.Pt(cx-r, cy))
	p.CubeTo(f32.Pt(cx-r, cy+d), f32.Pt(cx-d, cy+r), f32.Pt(cx, cy+r))
	p.CubeTo(f32.Pt(cx+d, cy+r), f32.Pt(cx+r, cy+d), f32.Pt(cx+r, cy))
	p.Close()
	spec := p.End()
	stack := clip.Stroke{Path: spec, Width: sw}.Op().Push(gtx.Ops)
	paint.Fill(gtx.Ops, col)
	stack.Pop()
}

// strokeArc paints a curved stroke approximated as a single cubic Bezier
// from (x1,y1) to (x2,y2), with control points (cx1,cy1) and (cx2,cy2).
func strokeArc(gtx layout.Context, x1, y1, cx1, cy1, cx2, cy2, x2, y2, scale, sw float32, col color.NRGBA) {
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(x1*scale, y1*scale))
	p.CubeTo(
		f32.Pt(cx1*scale, cy1*scale),
		f32.Pt(cx2*scale, cy2*scale),
		f32.Pt(x2*scale, y2*scale),
	)
	spec := p.End()
	stack := clip.Stroke{Path: spec, Width: sw}.Op().Push(gtx.Ops)
	paint.Fill(gtx.Ops, col)
	stack.Pop()
}

// iconBase returns the px size, design-grid scale, stroke width, and a
// transform stack that must be popped (typically via defer) when the icon
// is done painting. The stack offsets painting so the design-grid center
// (12, 12) lands at the geometric center of the px×px bounding box and
// the inner 18-unit (3..21) span — where every icon's visible content
// lives — fills the requested size exactly.
func iconBase(gtx layout.Context, size unit.Dp) (px int, scale, sw float32, stack op.TransformStack) {
	px = gtx.Dp(size)
	// 18-unit visible span maps to `size`. Stroke width scales with that
	// same ratio so glyph weight stays proportional.
	scale = float32(px) / 18.0
	sw = 1.6 * scale
	if sw < 1 {
		sw = 1
	}
	// Translate by -px/6 on each axis so the design-grid (12, 12) — every
	// icon's intended center — lands at (px/2, px/2).
	stack = op.Offset(image.Pt(-px/6, -px/6)).Push(gtx.Ops)
	return
}

// IconSites — a globe: circle + horizontal equator + meridian arc.
func IconSites(gtx layout.Context, th *Theme, size unit.Dp, col color.NRGBA) layout.Dimensions {
	px, scale, sw, st := iconBase(gtx, size)
	defer st.Pop()
	strokeCircle(gtx, 12*scale, 12*scale, 8*scale, sw, col)
	strokePolyline(gtx, []f32.Point{{X: 4, Y: 12}, {X: 20, Y: 12}}, scale, sw, col, false)
	// Two cubic arcs giving a thin meridian ellipse through the poles.
	strokeArc(gtx, 12, 4, 16, 8, 16, 16, 12, 20, scale, sw, col)
	strokeArc(gtx, 12, 4, 8, 8, 8, 16, 12, 20, scale, sw, col)
	return layout.Dimensions{Size: image.Pt(px, px)}
}

// IconSettings — three horizontal sliders with knob dots, an unambiguous
// "preferences" silhouette that scales cleanly even at 14dp.
func IconSettings(gtx layout.Context, th *Theme, size unit.Dp, col color.NRGBA) layout.Dimensions {
	px, scale, sw, st := iconBase(gtx, size)
	defer st.Pop()
	rows := []struct{ y, knobX float32 }{
		{6, 16},
		{12, 9},
		{18, 14},
	}
	for _, r := range rows {
		strokePolyline(gtx, []f32.Point{{X: 3, Y: r.y}, {X: 21, Y: r.y}}, scale, sw, col, false)
		strokeCircle(gtx, r.knobX*scale, r.y*scale, 1.8*scale, sw, col)
	}
	return layout.Dimensions{Size: image.Pt(px, px)}
}

// IconChevronLeft — a left-pointing angle bracket "‹".
func IconChevronLeft(gtx layout.Context, th *Theme, size unit.Dp, col color.NRGBA) layout.Dimensions {
	px, scale, sw, st := iconBase(gtx, size)
	defer st.Pop()
	strokePolyline(gtx, []f32.Point{
		{X: 15, Y: 6}, {X: 9, Y: 12}, {X: 15, Y: 18},
	}, scale, sw, col, false)
	return layout.Dimensions{Size: image.Pt(px, px)}
}

// IconChevronRight — a right-pointing angle bracket "›".
func IconChevronRight(gtx layout.Context, th *Theme, size unit.Dp, col color.NRGBA) layout.Dimensions {
	px, scale, sw, st := iconBase(gtx, size)
	defer st.Pop()
	strokePolyline(gtx, []f32.Point{
		{X: 9, Y: 6}, {X: 15, Y: 12}, {X: 9, Y: 18},
	}, scale, sw, col, false)
	return layout.Dimensions{Size: image.Pt(px, px)}
}

// IconPlus — a "+" sign.
func IconPlus(gtx layout.Context, th *Theme, size unit.Dp, col color.NRGBA) layout.Dimensions {
	px, scale, sw, st := iconBase(gtx, size)
	defer st.Pop()
	strokePolyline(gtx, []f32.Point{{X: 12, Y: 5}, {X: 12, Y: 19}}, scale, sw, col, false)
	strokePolyline(gtx, []f32.Point{{X: 5, Y: 12}, {X: 19, Y: 12}}, scale, sw, col, false)
	return layout.Dimensions{Size: image.Pt(px, px)}
}

// IconSearch — a magnifying glass: circle + diagonal handle.
func IconSearch(gtx layout.Context, th *Theme, size unit.Dp, col color.NRGBA) layout.Dimensions {
	px, scale, sw, st := iconBase(gtx, size)
	defer st.Pop()
	strokeCircle(gtx, 11*scale, 11*scale, 6*scale, sw, col)
	strokePolyline(gtx, []f32.Point{{X: 16, Y: 16}, {X: 20, Y: 20}}, scale, sw, col, false)
	return layout.Dimensions{Size: image.Pt(px, px)}
}

// IconFilter — a funnel: a closed flared shape narrowing to the bottom.
func IconFilter(gtx layout.Context, th *Theme, size unit.Dp, col color.NRGBA) layout.Dimensions {
	px, scale, sw, st := iconBase(gtx, size)
	defer st.Pop()
	strokePolyline(gtx, []f32.Point{
		{X: 3, Y: 5},
		{X: 21, Y: 5},
		{X: 14, Y: 14},
		{X: 14, Y: 20},
		{X: 10, Y: 18},
		{X: 10, Y: 14},
		{X: 3, Y: 5},
	}, scale, sw, col, true)
	return layout.Dimensions{Size: image.Pt(px, px)}
}

// IconTerminal — a rounded rect with a "> " prompt, used for Shell action.
func IconTerminal(gtx layout.Context, th *Theme, size unit.Dp, col color.NRGBA) layout.Dimensions {
	px, scale, sw, st := iconBase(gtx, size)
	defer st.Pop()
	strokePolyline(gtx, []f32.Point{
		{X: 3, Y: 5}, {X: 21, Y: 5}, {X: 21, Y: 19}, {X: 3, Y: 19}, {X: 3, Y: 5},
	}, scale, sw, col, true)
	strokePolyline(gtx, []f32.Point{
		{X: 7, Y: 10}, {X: 10, Y: 12}, {X: 7, Y: 14},
	}, scale, sw, col, false)
	strokePolyline(gtx, []f32.Point{{X: 13, Y: 14}, {X: 17, Y: 14}}, scale, sw, col, false)
	return layout.Dimensions{Size: image.Pt(px, px)}
}

// IconFolder — a folder with a tab corner.
func IconFolder(gtx layout.Context, th *Theme, size unit.Dp, col color.NRGBA) layout.Dimensions {
	px, scale, sw, st := iconBase(gtx, size)
	defer st.Pop()
	strokePolyline(gtx, []f32.Point{
		{X: 3, Y: 7}, {X: 9, Y: 7}, {X: 11, Y: 9},
		{X: 21, Y: 9}, {X: 21, Y: 19}, {X: 3, Y: 19}, {X: 3, Y: 7},
	}, scale, sw, col, true)
	return layout.Dimensions{Size: image.Pt(px, px)}
}

// IconDatabase — a stacked-cylinder database mark.
func IconDatabase(gtx layout.Context, th *Theme, size unit.Dp, col color.NRGBA) layout.Dimensions {
	px, scale, sw, st := iconBase(gtx, size)
	defer st.Pop()
	// Three horizontal ellipses using cubic arcs (top, middle, bottom).
	for _, cy := range []float32{6, 12, 18} {
		// Half-ellipses; combined they trace a full ellipse.
		strokeArc(gtx, 5, cy, 5, cy-2, 19, cy-2, 19, cy, scale, sw, col)
		strokeArc(gtx, 19, cy, 19, cy+2, 5, cy+2, 5, cy, scale, sw, col)
	}
	// Side walls.
	strokePolyline(gtx, []f32.Point{{X: 5, Y: 6}, {X: 5, Y: 18}}, scale, sw, col, false)
	strokePolyline(gtx, []f32.Point{{X: 19, Y: 6}, {X: 19, Y: 18}}, scale, sw, col, false)
	return layout.Dimensions{Size: image.Pt(px, px)}
}

// IconEye — a lens with a pupil dot, used for "Open site / view".
func IconEye(gtx layout.Context, th *Theme, size unit.Dp, col color.NRGBA) layout.Dimensions {
	px, scale, sw, st := iconBase(gtx, size)
	defer st.Pop()
	strokeArc(gtx, 2, 12, 6, 5, 18, 5, 22, 12, scale, sw, col)
	strokeArc(gtx, 22, 12, 18, 19, 6, 19, 2, 12, scale, sw, col)
	strokeCircle(gtx, 12*scale, 12*scale, 3*scale, sw, col)
	return layout.Dimensions{Size: image.Pt(px, px)}
}

// IconMail — an envelope outline + flap.
func IconMail(gtx layout.Context, th *Theme, size unit.Dp, col color.NRGBA) layout.Dimensions {
	px, scale, sw, st := iconBase(gtx, size)
	defer st.Pop()
	strokePolyline(gtx, []f32.Point{
		{X: 3, Y: 6}, {X: 21, Y: 6}, {X: 21, Y: 18}, {X: 3, Y: 18}, {X: 3, Y: 6},
	}, scale, sw, col, true)
	strokePolyline(gtx, []f32.Point{
		{X: 3, Y: 6}, {X: 12, Y: 13}, {X: 21, Y: 6},
	}, scale, sw, col, false)
	return layout.Dimensions{Size: image.Pt(px, px)}
}

// IconCog — a six-tooth gear silhouette, used for "panel settings" actions.
func IconCog(gtx layout.Context, th *Theme, size unit.Dp, col color.NRGBA) layout.Dimensions {
	px, scale, sw, st := iconBase(gtx, size)
	defer st.Pop()
	// Outer toothed ring approximated as 12 short radial spokes around a
	// circle. A real gear path is overkill at icon scale.
	cx, cy := float32(12), float32(12)
	rOuter := float32(10)
	rInner := float32(7.5)
	const spokes = 6
	for i := 0; i < spokes; i++ {
		a := float64(i) * (2 * math.Pi / float64(spokes))
		dx := float32(math.Cos(a))
		dy := float32(math.Sin(a))
		x1, y1 := cx+rInner*dx, cy+rInner*dy
		x2, y2 := cx+rOuter*dx, cy+rOuter*dy
		strokePolyline(gtx, []f32.Point{{X: x1, Y: y1}, {X: x2, Y: y2}}, scale, sw, col, false)
	}
	strokeCircle(gtx, cx*scale, cy*scale, 6*scale, sw, col)
	strokeCircle(gtx, cx*scale, cy*scale, 2.4*scale, sw, col)
	return layout.Dimensions{Size: image.Pt(px, px)}
}

// IconLogs — three horizontal lines suggesting log entries.
func IconLogs(gtx layout.Context, th *Theme, size unit.Dp, col color.NRGBA) layout.Dimensions {
	px, scale, sw, st := iconBase(gtx, size)
	defer st.Pop()
	strokePolyline(gtx, []f32.Point{{X: 5, Y: 7}, {X: 19, Y: 7}}, scale, sw, col, false)
	strokePolyline(gtx, []f32.Point{{X: 5, Y: 12}, {X: 16, Y: 12}}, scale, sw, col, false)
	strokePolyline(gtx, []f32.Point{{X: 5, Y: 17}, {X: 19, Y: 17}}, scale, sw, col, false)
	return layout.Dimensions{Size: image.Pt(px, px)}
}

// IconClose — a diagonal cross "×", used as a modal close affordance.
func IconClose(gtx layout.Context, th *Theme, size unit.Dp, col color.NRGBA) layout.Dimensions {
	px, scale, sw, st := iconBase(gtx, size)
	defer st.Pop()
	strokePolyline(gtx, []f32.Point{{X: 6, Y: 6}, {X: 18, Y: 18}}, scale, sw, col, false)
	strokePolyline(gtx, []f32.Point{{X: 18, Y: 6}, {X: 6, Y: 18}}, scale, sw, col, false)
	return layout.Dimensions{Size: image.Pt(px, px)}
}
