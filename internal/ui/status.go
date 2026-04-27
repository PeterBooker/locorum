package ui

import (
	"image"
	"image/color"
	"math"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// Status keys mirror the design's four states. Locorum's data model only
// distinguishes Running (ok) and Stopped (idle); Warning and Error are
// reserved for richer health signals later.
const (
	StatusOk   = "ok"
	StatusWarn = "warn"
	StatusErr  = "err"
	StatusIdle = "idle"
)

// StatusForSite maps a site's Started flag onto a (status key, label) pair.
// A running site uses the live-pulse "ok" treatment; a stopped site is the
// neutral "idle" gray (rather than red "err"), since stopping is normal.
func StatusForSite(started bool) (key, label string) {
	if started {
		return StatusOk, "Running"
	}
	return StatusIdle, "Stopped"
}

type statusColors struct {
	dot, fg, bg, border color.NRGBA
}

func statusPalette(th *Theme, key string) statusColors {
	switch key {
	case StatusOk:
		return statusColors{
			dot:    th.Color.Ok,
			fg:     th.Color.Ok,
			bg:     th.Color.OkSoft,
			border: withAlpha(th.Color.Ok, 82),
		}
	case StatusWarn:
		return statusColors{
			dot:    th.Color.Warn,
			fg:     th.Color.Warn,
			bg:     th.Color.WarnSoft,
			border: withAlpha(th.Color.Warn, 82),
		}
	case StatusErr:
		return statusColors{
			dot:    th.Color.Err,
			fg:     th.Color.Err,
			bg:     th.Color.ErrSoft,
			border: withAlpha(th.Color.Err, 82),
		}
	default:
		return statusColors{
			dot:    th.Color.Fg3,
			fg:     th.Color.Fg2,
			bg:     th.Color.Bg2,
			border: th.Color.Line,
		}
	}
}

// LiveStatusDot draws a small filled circle in the status color. When
// status==StatusOk and live==true, it animates an expanding pulse ring on
// a 2-second cycle. The widget reports a square layout sized to fit the
// largest pulse extent so neighbouring text isn't shoved around as the
// ring grows.
func LiveStatusDot(gtx layout.Context, th *Theme, key string, live bool) layout.Dimensions {
	dotDp := unit.Dp(7)
	canvasDp := unit.Dp(13) // dot + 3dp ring extent on each side
	dotPx := gtx.Dp(dotDp)
	canvasPx := gtx.Dp(canvasDp)
	cx, cy := canvasPx/2, canvasPx/2

	pal := statusPalette(th, key)

	if live && key == StatusOk {
		// 2s cycle. We use the wall-clock nanos so the animation keeps
		// phase across resize / reflow rather than restarting whenever
		// gtx.Now is captured fresh.
		const periodNs = int64(2 * time.Second)
		nanos := gtx.Now.UnixNano()
		pos := math.Mod(float64(nanos), float64(periodNs)) / float64(periodNs)
		gtx.Execute(op.InvalidateCmd{})
		const grow = 0.7
		if pos <= grow {
			t := float32(pos / grow)
			ringExtra := int(float32(gtx.Dp(unit.Dp(5))) * t)
			ringR := dotPx/2 + ringExtra
			alpha := uint8((1 - t) * 0.55 * 255)
			ringRect := image.Rectangle{
				Min: image.Pt(cx-ringR, cy-ringR),
				Max: image.Pt(cx+ringR, cy+ringR),
			}
			ringStack := clip.Ellipse(ringRect).Push(gtx.Ops)
			paint.Fill(gtx.Ops, color.NRGBA{R: pal.dot.R, G: pal.dot.G, B: pal.dot.B, A: alpha})
			ringStack.Pop()
		}
	}

	dotR := dotPx / 2
	dotRect := image.Rectangle{
		Min: image.Pt(cx-dotR, cy-dotR),
		Max: image.Pt(cx+dotR, cy+dotR),
	}
	dotStack := clip.Ellipse(dotRect).Push(gtx.Ops)
	paint.Fill(gtx.Ops, pal.dot)
	dotStack.Pop()

	return layout.Dimensions{Size: image.Point{X: canvasPx, Y: canvasPx}}
}

// StatusPill renders a rounded pill containing a colored dot + uppercase
// mono status label. Used in the site detail header and at the top of the
// nav rail brand area. live=true enables the pulse ring on "ok".
func StatusPill(gtx layout.Context, th *Theme, key string, live bool) layout.Dimensions {
	pal := statusPalette(th, key)
	label := strings.ToUpper(statusLabel(key))

	return drawPill(gtx, th, pal, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return LiveStatusDot(gtx, th, key, live)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body2(th.Theme, label)
					lbl.Color = pal.fg
					lbl.TextSize = th.Sizes.Micro
					lbl.Font = MonoFont
					return lbl.Layout(gtx)
				})
			}),
		)
	})
}

func statusLabel(key string) string {
	switch key {
	case StatusOk:
		return "Running"
	case StatusWarn:
		return "Warning"
	case StatusErr:
		return "Stopped"
	default:
		return "Idle"
	}
}

// drawPill renders an inline rounded-rect chip with the given status
// colors. Internal helper shared by StatusPill and any future custom pill.
func drawPill(gtx layout.Context, th *Theme, pal statusColors, content layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := layout.Inset{
		Top:    unit.Dp(4),
		Bottom: unit.Dp(4),
		Left:   unit.Dp(8),
		Right:  unit.Dp(8),
	}.Layout(gtx, content)
	call := macro.Stop()

	rr := gtx.Dp(unit.Dp(999)) // pill — fully rounded
	if rr*2 > dims.Size.Y {
		rr = dims.Size.Y / 2
	}
	rect := image.Rectangle{Max: dims.Size}

	bgStack := clip.RRect{Rect: rect, NE: rr, NW: rr, SE: rr, SW: rr}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, pal.bg)
	bgStack.Pop()

	// Border via a 1-px-inset bg fill behind a slightly-smaller content fill.
	// Cheaper than building a stroke path.
	borderStack := clip.RRect{Rect: rect, NE: rr, NW: rr, SE: rr, SW: rr}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, pal.border)
	borderStack.Pop()
	bw := gtx.Dp(unit.Dp(1))
	innerRect := image.Rectangle{Min: image.Pt(bw, bw), Max: image.Pt(dims.Size.X-bw, dims.Size.Y-bw)}
	innerR := rr - bw
	if innerR < 0 {
		innerR = 0
	}
	innerStack := clip.RRect{Rect: innerRect, NE: innerR, NW: innerR, SE: innerR, SW: innerR}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, pal.bg)
	innerStack.Pop()

	call.Add(gtx.Ops)
	return dims
}
