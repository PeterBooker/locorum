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

// RoundedFill paints a rounded-rect background behind the widget.
func RoundedFill(gtx layout.Context, col color.NRGBA, radius unit.Dp, w layout.Widget) layout.Dimensions {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			r := gtx.Dp(radius)
			rect := image.Rectangle{Max: gtx.Constraints.Min}
			defer clip.RRect{Rect: rect, NE: r, NW: r, SE: r, SW: r}.Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, col)
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}),
		layout.Stacked(w),
	)
}

// EdgeLine paints a 1-px line along one edge of the constrained area.
// edge: "top" | "bottom" | "left" | "right". The widget reports its size
// as gtx.Constraints.Min so it doesn't inflate the surrounding Stack —
// inside a Stack's Expanded pass, Min equals the largest Stacked child.
func EdgeLine(gtx layout.Context, col color.NRGBA, edge string) layout.Dimensions {
	w := gtx.Dp(unit.Dp(1))
	mx, my := gtx.Constraints.Min.X, gtx.Constraints.Min.Y
	if mx == 0 {
		mx = gtx.Constraints.Max.X
	}
	if my == 0 {
		my = gtx.Constraints.Max.Y
	}
	var rect image.Rectangle
	switch edge {
	case "top":
		rect = image.Rectangle{Max: image.Point{X: mx, Y: w}}
	case "bottom":
		rect = image.Rectangle{Min: image.Point{Y: my - w}, Max: image.Point{X: mx, Y: my}}
	case "left":
		rect = image.Rectangle{Max: image.Point{X: w, Y: my}}
	case "right":
		rect = image.Rectangle{Min: image.Point{X: mx - w}, Max: image.Point{X: mx, Y: my}}
	}
	defer clip.Rect(rect).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, col)
	return layout.Dimensions{Size: image.Point{X: mx, Y: my}}
}

// ─── Text Inputs ────────────────────────────────────────────────────────────

// LabeledInput draws a label above a styled text editor.
func LabeledInput(gtx layout.Context, th *Theme, label string, editor *widget.Editor, hint string) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, label)
			lbl.Color = th.Color.TextStrong
			return layout.Inset{Bottom: th.Spacing.XS}.Layout(gtx, lbl.Layout)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return BorderedEditor(gtx, th, editor, hint)
		}),
	)
}

// BorderedEditor draws a text editor with a border.
func BorderedEditor(gtx layout.Context, th *Theme, editor *widget.Editor, hint string) layout.Dimensions {
	return borderedEditor(gtx, th, editor, hint, font.Font{})
}

// BorderedMonoEditor draws a bordered text editor using the monospace font,
// suitable for code or shell-command input.
func BorderedMonoEditor(gtx layout.Context, th *Theme, editor *widget.Editor, hint string) layout.Dimensions {
	return borderedEditor(gtx, th, editor, hint, MonoFont)
}

func borderedEditor(gtx layout.Context, th *Theme, editor *widget.Editor, hint string, f font.Font) layout.Dimensions {
	border := widget.Border{
		Color:        th.Color.Border,
		CornerRadius: th.Radii.SM,
		Width:        unit.Dp(1),
	}
	return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top:    th.Spacing.SM,
			Bottom: th.Spacing.SM,
			Left:   unit.Dp(10),
			Right:  unit.Dp(10),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			ed := material.Editor(th.Theme, editor, hint)
			ed.TextSize = th.Sizes.Base
			ed.Font = f
			return ed.Layout(gtx)
		})
	})
}

// ─── Buttons ────────────────────────────────────────────────────────────────

// Primary draws a neon cyan primary action button.
func (th *Theme) Primary(gtx layout.Context, btn *widget.Clickable, text string) layout.Dimensions {
	b := material.Button(th.Theme, btn, text)
	b.Background = th.Color.Primary
	b.Color = th.Color.OnPrimary
	b.CornerRadius = th.Radii.MD
	b.TextSize = th.Sizes.Base
	return b.Layout(gtx)
}

// PrimaryGated draws a primary button that is visually muted and ignores
// clicks when enabled is false. Pair with .Changed() / dirty-tracking so the
// button only activates when the user has edited something.
func (th *Theme) PrimaryGated(gtx layout.Context, btn *widget.Clickable, text string, enabled bool) layout.Dimensions {
	if !enabled {
		gtx = gtx.Disabled()
	}
	b := material.Button(th.Theme, btn, text)
	if enabled {
		b.Background = th.Color.Primary
		b.Color = th.Color.OnPrimary
	} else {
		b.Background = th.Disabled(th.Color.Primary)
		b.Color = th.Disabled(th.Color.OnPrimary)
	}
	b.CornerRadius = th.Radii.MD
	b.TextSize = th.Sizes.Base
	return b.Layout(gtx)
}

// Secondary draws a muted surface secondary action button.
func (th *Theme) Secondary(gtx layout.Context, btn *widget.Clickable, text string) layout.Dimensions {
	b := material.Button(th.Theme, btn, text)
	b.Background = th.Color.SurfaceAlt
	b.Color = th.Color.TextStrong
	b.CornerRadius = th.Radii.MD
	b.TextSize = th.Sizes.Base
	return b.Layout(gtx)
}

// Danger draws a destructive action button.
func (th *Theme) Danger(gtx layout.Context, btn *widget.Clickable, text string) layout.Dimensions {
	b := material.Button(th.Theme, btn, text)
	b.Background = th.Color.Danger
	b.Color = th.Color.White
	b.CornerRadius = th.Radii.MD
	b.TextSize = th.Sizes.Base
	return b.Layout(gtx)
}

// Success draws a confirmation action button.
func (th *Theme) Success(gtx layout.Context, btn *widget.Clickable, text string) layout.Dimensions {
	b := material.Button(th.Theme, btn, text)
	b.Background = th.Color.Success
	b.Color = th.Color.OnPrimary
	b.CornerRadius = th.Radii.MD
	b.TextSize = th.Sizes.Base
	return b.Layout(gtx)
}

// Small draws a compact secondary button for inline use (e.g., Copy buttons).
func (th *Theme) Small(gtx layout.Context, btn *widget.Clickable, text string) layout.Dimensions {
	return th.smallStyled(gtx, btn, text, true)
}

// SmallGated mirrors Small but is muted and ignores clicks when enabled is false.
func (th *Theme) SmallGated(gtx layout.Context, btn *widget.Clickable, text string, enabled bool) layout.Dimensions {
	return th.smallStyled(gtx, btn, text, enabled)
}

func (th *Theme) smallStyled(gtx layout.Context, btn *widget.Clickable, text string, enabled bool) layout.Dimensions {
	if !enabled {
		gtx = gtx.Disabled()
	}
	b := material.Button(th.Theme, btn, text)
	if enabled {
		b.Background = th.Color.SurfaceAlt
		b.Color = th.Color.TextStrong
	} else {
		b.Background = th.Disabled(th.Color.SurfaceAlt)
		b.Color = th.Disabled(th.Color.TextStrong)
	}
	b.CornerRadius = th.Radii.SM
	b.TextSize = th.Sizes.XS
	b.Inset = layout.Inset{
		Top:    unit.Dp(4),
		Bottom: unit.Dp(4),
		Left:   unit.Dp(10),
		Right:  unit.Dp(10),
	}
	return b.Layout(gtx)
}

// Backward-compatible top-level helpers (delegate to *Theme methods).
func PrimaryButton(gtx layout.Context, th *Theme, btn *widget.Clickable, text string) layout.Dimensions {
	return th.Primary(gtx, btn, text)
}
func SecondaryButton(gtx layout.Context, th *Theme, btn *widget.Clickable, text string) layout.Dimensions {
	return th.Secondary(gtx, btn, text)
}
func DangerButton(gtx layout.Context, th *Theme, btn *widget.Clickable, text string) layout.Dimensions {
	return th.Danger(gtx, btn, text)
}
func SuccessButton(gtx layout.Context, th *Theme, btn *widget.Clickable, text string) layout.Dimensions {
	return th.Success(gtx, btn, text)
}
func SmallButton(gtx layout.Context, th *Theme, btn *widget.Clickable, text string) layout.Dimensions {
	return th.Small(gtx, btn, text)
}

// ─── Selectable Text & Clipboard ────────────────────────────────────────────

// SelectableLabel renders a widget.Selectable with material-style text appearance.
// Users can click-drag to select text and Ctrl+C to copy.
func SelectableLabel(gtx layout.Context, th *Theme, sel *widget.Selectable, text string, size unit.Sp, col color.NRGBA, f font.Font) layout.Dimensions {
	sel.SetText(text)

	textMacro := op.Record(gtx.Ops)
	paint.ColorOp{Color: col}.Add(gtx.Ops)
	textCall := textMacro.Stop()

	selMacro := op.Record(gtx.Ops)
	paint.ColorOp{Color: color.NRGBA{R: 0, G: 120, B: 180, A: 180}}.Add(gtx.Ops)
	selCall := selMacro.Stop()

	return sel.Layout(gtx, th.Shaper, f, size, textCall, selCall)
}

// TruncateWords returns s shortened to at most maxRunes runes, breaking at the
// last word boundary that fits and appending an ellipsis. Strings shorter than
// the budget are returned unchanged. If no whitespace exists within the
// budget, falls back to a hard rune-boundary cut.
func TruncateWords(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	cut := maxRunes
	// Prefer the last whitespace before the budget.
	for i := cut - 1; i > 0; i-- {
		if runes[i] == ' ' || runes[i] == '\t' || runes[i] == '\n' {
			cut = i
			break
		}
	}
	return strings.TrimRight(string(runes[:cut]), " \t\n") + "…"
}

// CopyToClipboard writes text to the system clipboard.
func CopyToClipboard(gtx layout.Context, text string) {
	gtx.Execute(clipboard.WriteCmd{
		Type: "application/text",
		Data: io.NopCloser(strings.NewReader(text)),
	})
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
func Section(gtx layout.Context, th *Theme, title string, content layout.Widget) layout.Dimensions {
	return sectionWithColor(gtx, th, title, th.Color.TextPrimary, content)
}

// SectionDirty renders a Section with the title coloured by th.Color.Brand,
// signalling that the contained controls have unsaved changes.
func SectionDirty(gtx layout.Context, th *Theme, title string, content layout.Widget) layout.Dimensions {
	return sectionWithColor(gtx, th, title, th.Color.Brand, content)
}

func sectionWithColor(gtx layout.Context, th *Theme, title string, titleColor color.NRGBA, content layout.Widget) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.H6(th.Theme, title)
			lbl.Color = titleColor
			return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
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
func KVRows(gtx layout.Context, th *Theme, items []KV) layout.Dimensions {
	children := make([]layout.FlexChild, len(items))
	for i, item := range items {
		item := item
		children[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Spacing.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Dp(th.Dims.LabelColWidth)
						lbl := material.Body2(th.Theme, item.Key)
						lbl.Color = th.Color.TextSecondary
						lbl.TextSize = th.Sizes.Base
						return lbl.Layout(gtx)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th.Theme, item.Value)
						lbl.TextSize = th.Sizes.Base
						return lbl.Layout(gtx)
					}),
				)
			})
		})
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// ─── Output Area ────────────────────────────────────────────────────────────

// OutputView is the persistent state for an output panel (logs, WP-CLI output,
// link-checker results). It wraps a read-only widget.Editor so the user can
// click-drag to select text and Ctrl+C to copy. lastText caches the most
// recently applied content; SetText resets the caret and selection, so we only
// re-apply when the output actually changes.
type OutputView struct {
	editor   widget.Editor
	lastText string
}

// NewOutputView constructs an OutputView with a read-only editor.
func NewOutputView() *OutputView {
	ov := &OutputView{}
	ov.editor.ReadOnly = true
	return ov
}

// Layout renders the output panel: a Surface-colored card containing a
// scrollable, selectable monospace text area. When output is empty, placeholder
// is shown via the editor's hint.
func (ov *OutputView) Layout(gtx layout.Context, th *Theme, output, placeholder string, maxHeight unit.Dp) layout.Dimensions {
	if output != ov.lastText {
		ov.editor.SetText(output)
		ov.lastText = output
	}

	gtx.Constraints.Max.Y = gtx.Dp(maxHeight)
	gtx.Constraints.Min.X = gtx.Constraints.Max.X

	return FillBackground(gtx, th.Color.Surface, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(th.Spacing.SM).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			ed := material.Editor(th.Theme, &ov.editor, placeholder)
			ed.TextSize = th.Sizes.XS
			ed.Font = MonoFont
			ed.Color = th.Color.TextPrimary
			ed.HintColor = th.Color.TextMuted
			return ed.Layout(gtx)
		})
	})
}

// ─── Loader ─────────────────────────────────────────────────────────────────

// Loader renders a material loading spinner at the given size.
func Loader(gtx layout.Context, th *Theme, size unit.Dp) layout.Dimensions {
	loader := material.Loader(th.Theme)
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

func (d *Dropdown) Layout(gtx layout.Context, th *Theme, label string) layout.Dimensions {
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
			lbl := material.Body2(th.Theme, label)
			lbl.Color = th.Color.TextStrong
			return layout.Inset{Bottom: th.Spacing.XS}.Layout(gtx, lbl.Layout)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return d.layoutDropdown(gtx, th)
		}),
	)
}

func (d *Dropdown) layoutDropdown(gtx layout.Context, th *Theme) layout.Dimensions {
	border := widget.Border{
		Color:        th.Color.Border,
		CornerRadius: th.Radii.SM,
		Width:        unit.Dp(1),
	}

	return layout.Stack{}.Layout(gtx,
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return material.Clickable(gtx, &d.button, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{
						Top: th.Spacing.SM, Bottom: th.Spacing.SM,
						Left: unit.Dp(10), Right: unit.Dp(10),
					}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						selectedText := ""
						if d.Selected >= 0 && d.Selected < len(d.Options) {
							selectedText = d.Options[d.Selected]
						}
						lbl := material.Body2(th.Theme, selectedText+" ▾")
						lbl.TextSize = th.Sizes.Base
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

func (d *Dropdown) layoutOptions(gtx layout.Context, th *Theme) layout.Dimensions {
	border := widget.Border{
		Color:        th.Color.Border,
		CornerRadius: th.Radii.SM,
		Width:        unit.Dp(1),
	}

	return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return FillBackground(gtx, th.Color.SurfaceElevated, func(gtx layout.Context) layout.Dimensions {
			items := make([]layout.FlexChild, len(d.Options))
			for i := range d.Options {
				idx := i
				items[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return material.Clickable(gtx, &d.items[idx], func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{
							Top: unit.Dp(6), Bottom: unit.Dp(6),
							Left: unit.Dp(10), Right: unit.Dp(10),
						}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							lbl := material.Body2(th.Theme, d.Options[idx])
							lbl.TextSize = th.Sizes.Base
							if idx == d.Selected {
								lbl.Color = th.Color.Primary
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

// ─── Tab Bar ─────────────────────────────────────────────────────────────────

// TabBar renders a horizontal row of tab buttons. The active tab is highlighted
// with the primary accent color and an underline indicator.
func TabBar(gtx layout.Context, th *Theme, tabs []string, active int, clicks []*widget.Clickable) layout.Dimensions {
	children := make([]layout.FlexChild, len(tabs))
	for i, label := range tabs {
		i, label := i, label
		children[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			isActive := i == active
			return layoutTab(gtx, th, clicks[i], label, isActive)
		})
	}
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
}

// layoutTab renders a single tab with an underline indicator when active.
func layoutTab(gtx layout.Context, th *Theme, btn *widget.Clickable, label string, active bool) layout.Dimensions {
	textColor := th.Color.TextSecondary
	if active {
		textColor = th.Color.Primary
	}

	return material.Clickable(gtx, btn, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{
					Top: th.Spacing.SM, Bottom: th.Spacing.SM,
					Left: th.Spacing.MD, Right: th.Spacing.MD,
				}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body1(th.Theme, label)
					lbl.Color = textColor
					lbl.TextSize = th.Sizes.Base
					return lbl.Layout(gtx)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !active {
					return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Min.X, Y: gtx.Dp(unit.Dp(2))}}
				}
				size := image.Point{X: gtx.Constraints.Min.X, Y: gtx.Dp(unit.Dp(2))}
				defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
				paint.Fill(gtx.Ops, th.Color.Primary)
				return layout.Dimensions{Size: size}
			}),
		)
	})
}

// ─── Checkbox ───────────────────────────────────────────────────────────────

// layoutCheckbox draws a 16dp square indicator that fills with the primary
// accent when checked. Pair with a widget.Clickable that toggles the bool.
func layoutCheckbox(gtx layout.Context, th *Theme, checked bool) layout.Dimensions {
	size := gtx.Dp(unit.Dp(16))
	rect := image.Rectangle{Max: image.Point{X: size, Y: size}}
	rr := gtx.Dp(th.Radii.SM)

	defer clip.RRect{Rect: rect, NE: rr, NW: rr, SE: rr, SW: rr}.Push(gtx.Ops).Pop()
	if checked {
		paint.Fill(gtx.Ops, th.Color.Primary)
	} else {
		paint.Fill(gtx.Ops, th.Color.SurfaceAlt)
	}
	if checked {
		// Draw a tick by overlaying a slightly inset white rectangle. The
		// extra layout pass costs us nothing here and keeps the visual
		// language consistent with the dropdown's selected indicator.
		inset := gtx.Dp(unit.Dp(4))
		inner := image.Rectangle{
			Min: image.Point{X: inset, Y: inset},
			Max: image.Point{X: size - inset, Y: size - inset},
		}
		defer clip.Rect(inner).Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, th.Color.OnPrimary)
	}
	return layout.Dimensions{Size: image.Point{X: size, Y: size}}
}

// ─── Confirm Dialog ─────────────────────────────────────────────────────────

// ConfirmDialog holds state for a reusable confirmation modal dialog.
type ConfirmDialog struct {
	confirmBtn widget.Clickable
	cancelBtn  widget.Clickable

	keys      *ModalFocus
	anim      *modalShowState
	keyResult ModalKeyResult
}

// ConfirmDialogStyle configures the appearance of a ConfirmDialog.
type ConfirmDialogStyle struct {
	Title        string
	Message      string
	ConfirmLabel string
	ConfirmColor color.NRGBA
}

// HandleUserInteractions reads the confirm/cancel button click state for the frame.
// Must be called before Layout each frame and only once (Clicked() consumes events).
func (cd *ConfirmDialog) HandleUserInteractions(gtx layout.Context) (confirmed, cancelled bool) {
	if cd.keys == nil {
		cd.keys = NewModalFocus()
	}
	cd.keyResult = ProcessModalKeys(gtx, cd.keys.Tag)
	confirmed = cd.confirmBtn.Clicked(gtx) || cd.keyResult.Enter
	cancelled = cd.cancelBtn.Clicked(gtx) || cd.keyResult.Escape
	if confirmed || cancelled {
		cd.keys.OnHide()
		if cd.anim != nil {
			cd.anim.Hide()
		}
	}
	return
}

// Layout renders the confirmation dialog inside a modal overlay.
// Use HandleUserInteractions to detect confirm/cancel clicks.
func (cd *ConfirmDialog) Layout(gtx layout.Context, th *Theme, style ConfirmDialogStyle) layout.Dimensions {
	return cd.LayoutWithExtras(gtx, th, style, nil)
}

// LayoutWithExtras renders the confirmation dialog with an optional extras
// widget (e.g. a checkbox) sandwiched between the message and the action
// buttons. Pass nil for extras when no additional UI is needed; the result
// is identical to Layout().
func (cd *ConfirmDialog) LayoutWithExtras(gtx layout.Context, th *Theme, style ConfirmDialogStyle, extras layout.Widget) layout.Dimensions {
	if cd.keys == nil {
		cd.keys = NewModalFocus()
	}
	if cd.anim == nil {
		cd.anim = NewModalAnim()
	}
	cd.anim.Show()
	return AnimatedModalOverlay(gtx, th, cd.anim, func(gtx layout.Context) layout.Dimensions {
		cd.keys.Layout(gtx)
		children := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.H6(th.Theme, style.Title)
				return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body1(th.Theme, style.Message)
				return layout.Inset{Bottom: unit.Dp(20)}.Layout(gtx, lbl.Layout)
			}),
		}
		if extras != nil {
			children = append(children, layout.Rigid(extras))
		}
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceEnd}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return SecondaryButton(gtx, th, &cd.cancelBtn, "Cancel")
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					b := material.Button(th.Theme, &cd.confirmBtn, style.ConfirmLabel)
					b.Background = style.ConfirmColor
					b.Color = th.Color.White
					b.CornerRadius = th.Radii.MD
					b.TextSize = th.Sizes.Base
					return b.Layout(gtx)
				}),
			)
		}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}
