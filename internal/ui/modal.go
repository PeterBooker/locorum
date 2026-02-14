package ui

import (
	"image"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// modalTag is a unique tag for the modal overlay pointer events.
var modalTag = new(bool)

// ModalOverlay draws a semi-transparent overlay and centers the content widget.
func ModalOverlay(gtx layout.Context, content layout.Widget) layout.Dimensions {
	// Draw full-screen overlay
	areaStack := clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Push(gtx.Ops)
	paint.Fill(gtx.Ops, ColorBlack50)

	// Block pointer events from passing through to content behind the overlay
	event.Op(gtx.Ops, modalTag)
	_ = pointer.Drag // ensure pointer package is used

	areaStack.Pop()

	// Center the modal content
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// Constrain modal width
		w := gtx.Dp(ModalWidth)
		gtx.Constraints.Max.X = w
		gtx.Constraints.Min.X = w

		// Rounded white background
		return FillBackground(gtx, ColorWhite, func(gtx layout.Context) layout.Dimensions {
			rr := gtx.Dp(RadiusLG)
			defer clip.RRect{
				Rect: image.Rectangle{Max: gtx.Constraints.Min},
				NE:   rr, NW: rr, SE: rr, SW: rr,
			}.Push(gtx.Ops).Pop()

			return layout.UniformInset(SpaceXL).Layout(gtx, content)
		})
	})
}
