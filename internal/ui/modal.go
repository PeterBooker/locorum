package ui

import (
	"image"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// modalTag is a unique tag for the modal overlay pointer events.
var modalTag = new(bool)

// modalFadeDuration is the time over which a modal fades from invisible to
// fully opaque on first appearance.
const modalFadeDuration = 200 * time.Millisecond

// modalShowState holds the per-modal-instance fade animation state. Keyed by
// the content widget's call site is impractical, so we key by the address of
// a small helper struct that callers embed (see ModalAnim) — but for the
// existing ModalOverlay shape we use a process-wide map keyed by tag.
//
// The simplest workable approach is to derive a synthetic "fade tag" from a
// counter on the call. To keep the public API stable, we instead expose an
// AnimatedModalOverlay variant; the legacy ModalOverlay forwards to it with a
// nil animation (i.e. instant).
type modalShowState struct {
	startedAt time.Time
	visible   bool
}

// ModalOverlay draws a semi-transparent overlay and centers the content
// widget. It is the legacy entry point with no fade animation.
func ModalOverlay(gtx layout.Context, th *Theme, content layout.Widget) layout.Dimensions {
	return drawModal(gtx, th, content, 1)
}

// AnimatedModalOverlay draws the overlay with a 200ms fade-in driven by anim.
// Callers should keep an *modalShowState (via NewModalAnim) on their modal
// struct and pass it here.
func AnimatedModalOverlay(gtx layout.Context, th *Theme, anim *modalShowState, content layout.Widget) layout.Dimensions {
	progress := anim.progress(gtx)
	return drawModal(gtx, th, content, progress)
}

// NewModalAnim returns a fresh animation state. Call (*ModalAnim).Show on the
// frame where the modal becomes visible and (*ModalAnim).Hide on the frame
// where it disappears. Pass the returned value to AnimatedModalOverlay.
func NewModalAnim() *modalShowState { return &modalShowState{} }

// Show marks the modal as visible. Idempotent: subsequent calls while already
// visible are no-ops, so it can be safely called every frame.
func (m *modalShowState) Show() {
	if m.visible {
		return
	}
	m.visible = true
	m.startedAt = time.Now()
}

// Hide marks the modal as hidden so the next Show triggers a fresh fade-in.
func (m *modalShowState) Hide() {
	m.visible = false
	m.startedAt = time.Time{}
}

// progress returns the current fade-in opacity in [0,1] and schedules a
// redraw if the animation is still in flight.
func (m *modalShowState) progress(gtx layout.Context) float32 {
	if !m.visible {
		return 0
	}
	if m.startedAt.IsZero() {
		m.startedAt = time.Now()
	}
	elapsed := time.Since(m.startedAt)
	if elapsed >= modalFadeDuration {
		return 1
	}
	gtx.Execute(op.InvalidateCmd{})
	return float32(elapsed) / float32(modalFadeDuration)
}

func drawModal(gtx layout.Context, th *Theme, content layout.Widget, progress float32) layout.Dimensions {
	// Force full-screen so layout.Center has space to center within.
	gtx.Constraints.Min = gtx.Constraints.Max

	// Overlay backdrop with progress-scaled alpha.
	overlay := th.Color.Overlay
	overlay.A = uint8(float32(overlay.A) * progress)
	areaStack := clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Push(gtx.Ops)
	paint.Fill(gtx.Ops, overlay)

	event.Op(gtx.Ops, modalTag)
	_ = pointer.Drag

	areaStack.Pop()

	// Wrap the content in an opacity layer so it fades together with the
	// backdrop. Skip the layer when fully visible to avoid the cost of an
	// offscreen blend.
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		w := gtx.Dp(th.Dims.ModalWidth)
		gtx.Constraints.Max.X = w
		gtx.Constraints.Min.X = w

		macro := op.Record(gtx.Ops)
		dims := layout.UniformInset(th.Spacing.XL).Layout(gtx, content)
		call := macro.Stop()

		rr := gtx.Dp(th.Radii.LG)

		var opacityStack paint.OpacityStack
		var hasOpacity bool
		if progress < 1 {
			opacityStack = paint.PushOpacity(gtx.Ops, progress)
			hasOpacity = true
		}

		clipStack := clip.RRect{
			Rect: image.Rectangle{Max: dims.Size},
			NE:   rr, NW: rr, SE: rr, SW: rr,
		}.Push(gtx.Ops)

		paint.Fill(gtx.Ops, th.Color.SurfaceElevated)
		call.Add(gtx.Ops)

		clipStack.Pop()
		if hasOpacity {
			opacityStack.Pop()
		}

		return dims
	})
}

