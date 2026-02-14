package ui

import (
	"image"
	"image/color"
	"io"
	"strings"

	"gioui.org/font"
	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// ─── Background & Layout Helpers ────────────────────────────────────────────

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

// ─── Text Inputs ────────────────────────────────────────────────────────────

// LabeledInput draws a label above a styled text editor.
func LabeledInput(gtx layout.Context, th *material.Theme, label string, editor *widget.Editor, hint string) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th, label)
			lbl.Color = ColorGray700
			return layout.Inset{Bottom: SpaceXS}.Layout(gtx, lbl.Layout)
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
		CornerRadius: RadiusSM,
		Width:        unit.Dp(1),
	}
	return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top:    SpaceSM,
			Bottom: SpaceSM,
			Left:   unit.Dp(10),
			Right:  unit.Dp(10),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			ed := material.Editor(th, editor, hint)
			ed.TextSize = TextBase
			return ed.Layout(gtx)
		})
	})
}

// ─── Buttons ────────────────────────────────────────────────────────────────

// PrimaryButton draws a blue primary action button.
func PrimaryButton(gtx layout.Context, th *material.Theme, btn *widget.Clickable, text string) layout.Dimensions {
	b := material.Button(th, btn, text)
	b.Background = ColorBlue600
	b.Color = ColorWhite
	b.CornerRadius = RadiusMD
	b.TextSize = TextBase
	return b.Layout(gtx)
}

// SecondaryButton draws a bordered secondary action button.
func SecondaryButton(gtx layout.Context, th *material.Theme, btn *widget.Clickable, text string) layout.Dimensions {
	b := material.Button(th, btn, text)
	b.Background = ColorWhite
	b.Color = ColorGray700
	b.CornerRadius = RadiusMD
	b.TextSize = TextBase
	return b.Layout(gtx)
}

// DangerButton draws a red destructive action button.
func DangerButton(gtx layout.Context, th *material.Theme, btn *widget.Clickable, text string) layout.Dimensions {
	b := material.Button(th, btn, text)
	b.Background = ColorRed600
	b.Color = ColorWhite
	b.CornerRadius = RadiusMD
	b.TextSize = TextBase
	return b.Layout(gtx)
}

// SuccessButton draws a green confirmation action button.
func SuccessButton(gtx layout.Context, th *material.Theme, btn *widget.Clickable, text string) layout.Dimensions {
	b := material.Button(th, btn, text)
	b.Background = ColorGreen600
	b.Color = ColorWhite
	b.CornerRadius = RadiusMD
	b.TextSize = TextBase
	return b.Layout(gtx)
}

// SmallButton draws a compact secondary button for inline use (e.g., copy buttons).
func SmallButton(gtx layout.Context, th *material.Theme, btn *widget.Clickable, text string) layout.Dimensions {
	b := material.Button(th, btn, text)
	b.Background = ColorGray200
	b.Color = ColorGray700
	b.CornerRadius = RadiusSM
	b.TextSize = TextXS
	b.Inset = layout.Inset{
		Top:    unit.Dp(2),
		Bottom: unit.Dp(2),
		Left:   unit.Dp(6),
		Right:  unit.Dp(6),
	}
	return b.Layout(gtx)
}

// ─── Selectable Text & Clipboard ────────────────────────────────────────────

// SelectableLabel renders a widget.Selectable with material-style text appearance.
// Users can click-drag to select text and Ctrl+C to copy.
func SelectableLabel(gtx layout.Context, th *material.Theme, sel *widget.Selectable, text string, size unit.Sp, col color.NRGBA) layout.Dimensions {
	sel.SetText(text)

	textMacro := op.Record(gtx.Ops)
	paint.ColorOp{Color: col}.Add(gtx.Ops)
	textCall := textMacro.Stop()

	selMacro := op.Record(gtx.Ops)
	paint.ColorOp{Color: color.NRGBA{R: 180, G: 215, B: 255, A: 255}}.Add(gtx.Ops)
	selCall := selMacro.Stop()

	return sel.Layout(gtx, th.Shaper, font.Font{}, size, textCall, selCall)
}

// CopyToClipboard writes text to the system clipboard.
func CopyToClipboard(gtx layout.Context, text string) {
	gtx.Execute(clipboard.WriteCmd{
		Type: "application/text",
		Data: io.NopCloser(strings.NewReader(text)),
	})
}

// ─── Status Badge ───────────────────────────────────────────────────────────

// StatusBadge renders a pill-shaped badge showing "Running" or "Stopped"
// with a colored dot and tinted background.
func StatusBadge(gtx layout.Context, th *material.Theme, started bool) layout.Dimensions {
	var (
		bg    color.NRGBA
		fg    color.NRGBA
		dot   color.NRGBA
		label string
	)
	if started {
		bg = ColorGreen100
		fg = ColorGreen800
		dot = ColorGreen600
		label = "Running"
	} else {
		bg = ColorGray200
		fg = ColorGray700
		dot = ColorGray400
		label = "Stopped"
	}

	return FillBackground(gtx, bg, func(gtx layout.Context) layout.Dimensions {
		rr := gtx.Dp(RadiusLG)
		defer clip.RRect{
			Rect: image.Rectangle{Max: gtx.Constraints.Min},
			NE:   rr, NW: rr, SE: rr, SW: rr,
		}.Push(gtx.Ops).Pop()

		return layout.Inset{
			Top: unit.Dp(3), Bottom: unit.Dp(3),
			Left: SpaceSM, Right: SpaceSM,
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(5)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layoutDot(gtx, dot, unit.Dp(6))
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body2(th, label)
					lbl.Color = fg
					lbl.TextSize = TextSM
					return lbl.Layout(gtx)
				}),
			)
		})
	})
}

// layoutDot draws a small filled circle.
func layoutDot(gtx layout.Context, col color.NRGBA, diameter unit.Dp) layout.Dimensions {
	size := gtx.Dp(diameter)
	defer clip.Ellipse{
		Min: image.Point{},
		Max: image.Point{X: size, Y: size},
	}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, col)
	return layout.Dimensions{Size: image.Point{X: size, Y: size}}
}

// ─── Divider ────────────────────────────────────────────────────────────────

// Divider renders a horizontal 1px line with configurable vertical margin.
func Divider(gtx layout.Context, col color.NRGBA, verticalMargin unit.Dp) layout.Dimensions {
	return layout.Inset{Top: verticalMargin, Bottom: verticalMargin}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		size := image.Point{X: gtx.Constraints.Max.X, Y: gtx.Dp(unit.Dp(1))}
		defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, col)
		return layout.Dimensions{Size: size}
	})
}

// ─── Section ────────────────────────────────────────────────────────────────

// Section renders a titled section with consistent header styling and bottom spacing.
func Section(gtx layout.Context, th *material.Theme, title string, content layout.Widget) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.H6(th, title)
			return layout.Inset{Bottom: SpaceSM}.Layout(gtx, lbl.Layout)
		}),
		layout.Rigid(content),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Spacer{Height: unit.Dp(20)}.Layout(gtx)
		}),
	)
}

// ─── Key-Value Row ──────────────────────────────────────────────────────────

// KV holds a key-value pair for display in info sections.
type KV struct {
	Key   string
	Value string
}

// KVRows renders a list of key-value pairs with consistent label column width.
func KVRows(gtx layout.Context, th *material.Theme, items []KV) layout.Dimensions {
	children := make([]layout.FlexChild, len(items))
	for i, item := range items {
		item := item
		children[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: SpaceXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Dp(LabelColWidth)
						lbl := material.Body2(th, item.Key)
						lbl.Color = ColorGray500
						lbl.TextSize = TextBase
						return lbl.Layout(gtx)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th, item.Value)
						lbl.TextSize = TextBase
						return lbl.Layout(gtx)
					}),
				)
			})
		})
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// ─── Output Area ────────────────────────────────────────────────────────────

// OutputArea renders a scrollable monospace-style text area with a gray background.
// Used for log viewers, CLI output, etc.
func OutputArea(gtx layout.Context, th *material.Theme, list *widget.List, output string, placeholder string, maxHeight unit.Dp) layout.Dimensions {
	mh := gtx.Dp(maxHeight)
	gtx.Constraints.Max.Y = mh
	gtx.Constraints.Min.X = gtx.Constraints.Max.X

	return FillBackground(gtx, ColorGray100, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(SpaceSM).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if output == "" {
				lbl := material.Body2(th, placeholder)
				lbl.Color = ColorGray400
				lbl.TextSize = TextSM
				return lbl.Layout(gtx)
			}
			lines := strings.Split(output, "\n")
			return material.List(th, list).Layout(gtx, len(lines), func(gtx layout.Context, i int) layout.Dimensions {
				lbl := material.Body2(th, lines[i])
				lbl.TextSize = TextXS
				return lbl.Layout(gtx)
			})
		})
	})
}

// ─── Loader ─────────────────────────────────────────────────────────────────

// Loader renders a material loading spinner at the given size.
func Loader(gtx layout.Context, th *material.Theme, size unit.Dp) layout.Dimensions {
	loader := material.Loader(th)
	s := gtx.Dp(size)
	gtx.Constraints.Max.X = s
	gtx.Constraints.Max.Y = s
	return loader.Layout(gtx)
}

// ─── Dropdown ───────────────────────────────────────────────────────────────

// Dropdown widget for selecting from a list of options.
type Dropdown struct {
	Selected int
	Options  []string
	button   widget.Clickable
	expanded bool
	items    []widget.Clickable
}

func NewDropdown(options []string) *Dropdown {
	return &Dropdown{
		Options: options,
		items:   make([]widget.Clickable, len(options)),
	}
}

func (d *Dropdown) Layout(gtx layout.Context, th *material.Theme, label string) layout.Dimensions {
	if d.button.Clicked(gtx) {
		d.expanded = !d.expanded
	}
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
			return layout.Inset{Bottom: SpaceXS}.Layout(gtx, lbl.Layout)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return d.layoutDropdown(gtx, th)
		}),
	)
}

func (d *Dropdown) layoutDropdown(gtx layout.Context, th *material.Theme) layout.Dimensions {
	border := widget.Border{
		Color:        ColorBorder,
		CornerRadius: RadiusSM,
		Width:        unit.Dp(1),
	}

	return layout.Stack{}.Layout(gtx,
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return material.Clickable(gtx, &d.button, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{
						Top: SpaceSM, Bottom: SpaceSM,
						Left: unit.Dp(10), Right: unit.Dp(10),
					}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						selectedText := ""
						if d.Selected >= 0 && d.Selected < len(d.Options) {
							selectedText = d.Options[d.Selected]
						}
						lbl := material.Body2(th, selectedText+" ▾")
						lbl.TextSize = TextBase
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
		CornerRadius: RadiusSM,
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
							Top: unit.Dp(6), Bottom: unit.Dp(6),
							Left: unit.Dp(10), Right: unit.Dp(10),
						}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							lbl := material.Body2(th, d.Options[idx])
							lbl.TextSize = TextBase
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

// ─── Confirm Dialog ─────────────────────────────────────────────────────────

// ConfirmDialog holds state for a reusable confirmation modal dialog.
type ConfirmDialog struct {
	confirmBtn widget.Clickable
	cancelBtn  widget.Clickable
}

// ConfirmDialogStyle configures the appearance of a ConfirmDialog.
type ConfirmDialogStyle struct {
	Title        string
	Message      string
	ConfirmLabel string
	ConfirmColor color.NRGBA
}

// Layout renders the confirmation dialog inside a modal overlay.
// Returns (confirmed, cancelled) booleans indicating which button was clicked.
func (cd *ConfirmDialog) Layout(gtx layout.Context, th *material.Theme, style ConfirmDialogStyle) (confirmed, cancelled bool, dims layout.Dimensions) {
	confirmed = cd.confirmBtn.Clicked(gtx)
	cancelled = cd.cancelBtn.Clicked(gtx)

	dims = ModalOverlay(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.H6(th, style.Title)
				return layout.Inset{Bottom: SpaceMD}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body1(th, style.Message)
				return layout.Inset{Bottom: unit.Dp(20)}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceEnd}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: SpaceSM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return SecondaryButton(gtx, th, &cd.cancelBtn, "Cancel")
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						b := material.Button(th, &cd.confirmBtn, style.ConfirmLabel)
						b.Background = style.ConfirmColor
						b.Color = ColorWhite
						b.CornerRadius = RadiusMD
						b.TextSize = TextBase
						return b.Layout(gtx)
					}),
				)
			}),
		)
	})
	return
}
