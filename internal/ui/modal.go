package ui

import (
	"image"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
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
		gtx.Constraints.Max.X = gtx.Dp(unit.Dp(500))
		gtx.Constraints.Min.X = gtx.Dp(unit.Dp(500))

		return FillBackground(gtx, ColorWhite, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(24)).Layout(gtx, content)
		})
	})
}
