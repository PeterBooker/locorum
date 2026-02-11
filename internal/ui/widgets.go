package ui

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// FillBackground paints a rectangle with the given color behind the widget.
func FillBackground(gtx layout.Context, col color.NRGBA, w layout.Widget) layout.Dimensions {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			defer clip.Rect(image.Rectangle{Max: gtx.Constraints.Min}).Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, col)
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}),
		layout.Stacked(w),
	)
}

// LabeledInput draws a label above a styled text editor.
func LabeledInput(gtx layout.Context, th *material.Theme, label string, editor *widget.Editor, hint string) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th, label)
			lbl.Color = ColorGray700
			return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, lbl.Layout)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return BorderedEditor(gtx, th, editor, hint)
		}),
	)
}

// BorderedEditor draws a text editor with a border.
func BorderedEditor(gtx layout.Context, th *material.Theme, editor *widget.Editor, hint string) layout.Dimensions {
	border := widget.Border{
		Color:        ColorBorder,
		CornerRadius: unit.Dp(4),
		Width:        unit.Dp(1),
	}
	return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top:    unit.Dp(8),
			Bottom: unit.Dp(8),
			Left:   unit.Dp(10),
			Right:  unit.Dp(10),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			ed := material.Editor(th, editor, hint)
			ed.TextSize = unit.Sp(14)
			return ed.Layout(gtx)
		})
	})
}

// PrimaryButton draws a blue primary button.
func PrimaryButton(gtx layout.Context, th *material.Theme, btn *widget.Clickable, text string) layout.Dimensions {
	b := material.Button(th, btn, text)
	b.Background = ColorBlue600
	b.Color = ColorWhite
	b.CornerRadius = unit.Dp(6)
	b.TextSize = unit.Sp(14)
	return b.Layout(gtx)
}

// SecondaryButton draws a bordered secondary button.
func SecondaryButton(gtx layout.Context, th *material.Theme, btn *widget.Clickable, text string) layout.Dimensions {
	b := material.Button(th, btn, text)
	b.Background = ColorWhite
	b.Color = ColorGray700
	b.CornerRadius = unit.Dp(6)
	b.TextSize = unit.Sp(14)
	return b.Layout(gtx)
}

// Dropdown widget for selecting from a list of options.
type Dropdown struct {
	Selected int
	Options  []string
	button   widget.Clickable
	expanded bool
	items    []widget.Clickable
}

func NewDropdown(options []string) *Dropdown {
	d := &Dropdown{
		Options: options,
		items:   make([]widget.Clickable, len(options)),
	}
	return d
}

func (d *Dropdown) Layout(gtx layout.Context, th *material.Theme, label string) layout.Dimensions {
	// Check for main button click
	if d.button.Clicked(gtx) {
		d.expanded = !d.expanded
	}

	// Check for item clicks
	for i := range d.items {
		if d.items[i].Clicked(gtx) {
			d.Selected = i
			d.expanded = false
		}
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th, label)
			lbl.Color = ColorGray700
			return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, lbl.Layout)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return d.layoutDropdown(gtx, th)
		}),
	)
}

func (d *Dropdown) layoutDropdown(gtx layout.Context, th *material.Theme) layout.Dimensions {
	border := widget.Border{
		Color:        ColorBorder,
		CornerRadius: unit.Dp(4),
		Width:        unit.Dp(1),
	}

	return layout.Stack{}.Layout(gtx,
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return material.Clickable(gtx, &d.button, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{
						Top:    unit.Dp(8),
						Bottom: unit.Dp(8),
						Left:   unit.Dp(10),
						Right:  unit.Dp(10),
					}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						selectedText := ""
						if d.Selected >= 0 && d.Selected < len(d.Options) {
							selectedText = d.Options[d.Selected]
						}
						lbl := material.Body2(th, selectedText+" ▾")
						lbl.TextSize = unit.Sp(14)
						return lbl.Layout(gtx)
					})
				})
			})
		}),
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			if !d.expanded {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(36)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return d.layoutOptions(gtx, th)
			})
		}),
	)
}

func (d *Dropdown) layoutOptions(gtx layout.Context, th *material.Theme) layout.Dimensions {
	border := widget.Border{
		Color:        ColorBorder,
		CornerRadius: unit.Dp(4),
		Width:        unit.Dp(1),
	}

	return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return FillBackground(gtx, ColorWhite, func(gtx layout.Context) layout.Dimensions {
			items := make([]layout.FlexChild, len(d.Options))
			for i := range d.Options {
				idx := i
				items[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return material.Clickable(gtx, &d.items[idx], func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{
							Top:    unit.Dp(6),
							Bottom: unit.Dp(6),
							Left:   unit.Dp(10),
							Right:  unit.Dp(10),
						}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							lbl := material.Body2(th, d.Options[idx])
							lbl.TextSize = unit.Sp(14)
							if idx == d.Selected {
								lbl.Color = ColorBlue600
							}
							return lbl.Layout(gtx)
						})
					})
				})
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, items...)
		})
	})
}
