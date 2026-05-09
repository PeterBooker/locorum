package ui

import (
	"image"
	"image/color"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/utils"
)

// ─── Panel card ─────────────────────────────────────────────────────────────

// panel renders a titled card on Bg1 with a 1px Line border, R3 corners,
// and a uppercase mono micro heading. Title may be empty to skip the
// heading line.
func panel(gtx layout.Context, th *Theme, title string, content layout.Widget) layout.Dimensions {
	return panelWithAction(gtx, th, title, nil, content)
}

// panelWithAction is panel with an optional trailing widget rendered on the
// right-hand side of the title row (e.g. a settings cog icon button). Pass
// action == nil to behave like panel.
func panelWithAction(gtx layout.Context, th *Theme, title string, action layout.Widget, content layout.Widget) layout.Dimensions {
	return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return drawCard(gtx, th, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(15)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				if title == "" {
					return content(gtx)
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return panelHeaderRow(gtx, th, title, action)
						})
					}),
					layout.Rigid(content),
				)
			})
		})
	})
}

func panelHeaderRow(gtx layout.Context, th *Theme, title string, action layout.Widget) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, microize(title))
			lbl.Color = th.Color.Fg3
			lbl.TextSize = th.Sizes.Micro
			lbl.Font = MonoFont
			lbl.Font.Weight = font.Medium
			return lbl.Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Min.X}}
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if action == nil {
				return layout.Dimensions{}
			}
			return action(gtx)
		}),
	)
}

func drawCard(gtx layout.Context, th *Theme, w layout.Widget) layout.Dimensions {
	return widget.Border{
		Color:        th.Color.Line,
		CornerRadius: th.Radii.R3,
		Width:        unit.Dp(1),
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return RoundedFill(gtx, th.Color.Bg1, th.Radii.R3, w)
	})
}

// microize uppercases title (display only) for the mono micro section
// label. Lowercased input is normal — input shouldn't be assumed already
// uppercase.
func microize(s string) string {
	return uppercaseASCII(s)
}

func uppercaseASCII(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			r -= 32
		}
		out = append(out, r)
	}
	return string(out)
}

// ─── Environment grid ───────────────────────────────────────────────────────

// envCell is one key/value entry in the environment grid.
type envCell struct {
	Key   string
	Value string
}

// envGrid renders a list of (key, value) cells in cols columns. Keys are
// uppercase mono micro labels; values are mono single-line truncated.
func envGrid(gtx layout.Context, th *Theme, cells []envCell, cols int) layout.Dimensions {
	if cols <= 0 {
		cols = 4
	}
	rows := (len(cells) + cols - 1) / cols
	rowChildren := make([]layout.FlexChild, 0, rows)
	for r := 0; r < rows; r++ {
		rowChildren = append(rowChildren, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			colChildren := make([]layout.FlexChild, 0, cols)
			for c := 0; c < cols; c++ {
				idx := r*cols + c
				if idx >= len(cells) {
					colChildren = append(colChildren, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Min.X}}
					}))
					continue
				}
				cell := cells[idx]
				colChildren = append(colChildren, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return envCellLayout(gtx, th, cell)
					})
				}))
			}
			return layout.Inset{Bottom: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, colChildren...)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rowChildren...)
}

func envCellLayout(gtx layout.Context, th *Theme, cell envCell) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, microize(cell.Key))
			lbl.Color = th.Color.Fg3
			lbl.TextSize = th.Sizes.Micro
			lbl.Font = MonoFont
			lbl.Font.Weight = font.Medium
			return layout.Inset{Bottom: unit.Dp(2)}.Layout(gtx, lbl.Layout)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, cell.Value)
			lbl.Color = th.Color.Fg
			lbl.TextSize = th.Sizes.MonoSM
			lbl.Font = MonoFont
			lbl.MaxLines = 1
			lbl.Truncator = "…"
			return lbl.Layout(gtx)
		}),
	)
}

// ─── Action buttons (header + actions row) ─────────────────────────────────

// btnVariant selects the visual treatment for iconLabelButton.
type btnVariant int

const (
	btnSecondary btnVariant = iota
	btnPrimary
	btnGhost
	btnDanger
)

// iconLabelButton renders a 2em-tall button with optional leading icon.
// Variant controls bg/fg/border; secondary buttons sit on Bg1 with the
// Line-strong border, primary uses the accent fill, ghost is transparent
// until hovered, danger uses the err palette.
func iconLabelButton(
	gtx layout.Context, th *Theme,
	btn *widget.Clickable,
	icon IconFunc, label string,
	variant btnVariant,
) layout.Dimensions {
	var (
		bg, fg, border color.NRGBA
		hasBorder      = true
	)
	switch variant {
	case btnPrimary:
		bg = th.Color.Accent
		fg = th.Color.AccentFg
		border = darken(th.Color.Accent, 0.08)
	case btnGhost:
		bg = color.NRGBA{} // transparent
		fg = th.Color.Fg2
		hasBorder = false
	case btnDanger:
		bg = th.Color.Bg1
		fg = th.Color.Err
		border = withAlpha(th.Color.Err, 110)
	default: // btnSecondary
		bg = th.Color.Bg1
		fg = th.Color.Fg
		border = th.Color.LineStrong
	}

	return material.Clickable(gtx, btn, func(gtx layout.Context) layout.Dimensions {
		render := func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{
				Top: unit.Dp(7), Bottom: unit.Dp(7),
				Left: unit.Dp(12), Right: unit.Dp(12),
			}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				children := make([]layout.FlexChild, 0, 3)
				if icon != nil {
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return icon(gtx, th, unit.Dp(14), fg)
					}))
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Spacer{Width: unit.Dp(6)}.Layout(gtx)
					}))
				}
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body2(th.Theme, label)
					lbl.Color = fg
					lbl.TextSize = th.Sizes.MonoSM
					lbl.Font.Weight = font.Medium
					lbl.MaxLines = 1
					return lbl.Layout(gtx)
				}))
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
			})
		}
		if !hasBorder {
			return RoundedFill(gtx, bg, th.Radii.R2, render)
		}
		return widget.Border{
			Color:        border,
			CornerRadius: th.Radii.R2,
			Width:        unit.Dp(1),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return RoundedFill(gtx, bg, th.Radii.R2, render)
		})
	})
}

// startButton renders a green "Start" primary button with a play glyph.
// Used in the site detail header when the site is stopped.
func startButton(gtx layout.Context, th *Theme, btn *widget.Clickable) layout.Dimensions {
	return material.Clickable(gtx, btn, func(gtx layout.Context) layout.Dimensions {
		return RoundedFill(gtx, th.Color.Ok, th.Radii.R2, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{
				Top: unit.Dp(7), Bottom: unit.Dp(7),
				Left: unit.Dp(14), Right: unit.Dp(14),
			}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return drawPlayGlyph(gtx, unit.Dp(10), th.Color.AccentFg)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(7)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							lbl := material.Body2(th.Theme, "Start")
							lbl.Color = th.Color.AccentFg
							lbl.TextSize = th.Sizes.MonoSM
							lbl.Font.Weight = font.Medium
							lbl.MaxLines = 1
							return lbl.Layout(gtx)
						})
					}),
				)
			})
		})
	})
}

// drawPlayGlyph paints a filled triangle (play arrow) at the given size.
func drawPlayGlyph(gtx layout.Context, size unit.Dp, col color.NRGBA) layout.Dimensions {
	s := gtx.Dp(size)
	sf := float32(s)
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(0, 0))
	p.LineTo(f32.Pt(sf, sf/2))
	p.LineTo(f32.Pt(0, sf))
	p.Close()
	spec := p.End()
	stack := clip.Outline{Path: spec}.Op().Push(gtx.Ops)
	paint.Fill(gtx.Ops, col)
	stack.Pop()
	return layout.Dimensions{Size: image.Point{X: s, Y: s}}
}

// stopButton renders a red "Stop" primary button with a square glyph.
// Used in the site detail header when the site is running, in the same
// position Start occupies when stopped — toggling Start ↔ Stop must not
// shift visually.
func stopButton(gtx layout.Context, th *Theme, btn *widget.Clickable) layout.Dimensions {
	return material.Clickable(gtx, btn, func(gtx layout.Context) layout.Dimensions {
		return RoundedFill(gtx, th.Color.Danger, th.Radii.R2, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{
				Top: unit.Dp(7), Bottom: unit.Dp(7),
				Left: unit.Dp(14), Right: unit.Dp(14),
			}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return drawStopGlyph(gtx, unit.Dp(10), th.Color.AccentFg)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(7)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							lbl := material.Body2(th.Theme, "Stop")
							lbl.Color = th.Color.AccentFg
							lbl.TextSize = th.Sizes.MonoSM
							lbl.Font.Weight = font.Medium
							lbl.MaxLines = 1
							return lbl.Layout(gtx)
						})
					}),
				)
			})
		})
	})
}

// drawStopGlyph paints a filled square (stop icon) at the given size.
func drawStopGlyph(gtx layout.Context, size unit.Dp, col color.NRGBA) layout.Dimensions {
	s := gtx.Dp(size)
	defer clip.Rect{Max: image.Point{X: s, Y: s}}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, col)
	return layout.Dimensions{Size: image.Point{X: s, Y: s}}
}

// ─── Activity panel ─────────────────────────────────────────────────────────

// activityRowKind tells the row renderer which status colour to use for the
// leading dot. UI-only — it does not reach storage.
type activityRowKind int

const (
	activityRowOK activityRowKind = iota
	activityRowFailed
	activityRowRolledBack
)

// activityEntry is one row in the activity feed. Constructed by
// activityEntryFor(ev) in activitytab.go from a storage.ActivityEvent, or
// hand-built by callers that want an empty/placeholder row.
type activityEntry struct {
	Time    string
	Message string
	Kind    activityRowKind
}

// activityPanel renders the "Activity" card with a header (title + ghost
// "View all" link) and a list of (timestamp, message) rows. The View-all
// click is a no-op for now; the caller may pass nil for entries to render
// an empty state.
func activityPanel(gtx layout.Context, th *Theme, entries []activityEntry, viewAll *widget.Clickable) layout.Dimensions {
	return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return drawCard(gtx, th, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(15)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return activityHeader(gtx, th, viewAll)
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if len(entries) == 0 {
							lbl := material.Body2(th.Theme, "No recent activity.")
							lbl.Color = th.Color.Fg3
							lbl.TextSize = th.Sizes.Body
							return lbl.Layout(gtx)
						}
						children := make([]layout.FlexChild, 0, len(entries))
						for i, e := range entries {
							top := unit.Dp(0)
							if i > 0 {
								top = unit.Dp(7)
							}
							children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Top: top}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return activityRow(gtx, th, e)
								})
							}))
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
					}),
				)
			})
		})
	})
}

func activityHeader(gtx layout.Context, th *Theme, viewAll *widget.Clickable) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, microize("Activity"))
			lbl.Color = th.Color.Fg3
			lbl.TextSize = th.Sizes.Micro
			lbl.Font = MonoFont
			lbl.Font.Weight = font.Medium
			return lbl.Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Min.X}}
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if viewAll == nil {
				return layout.Dimensions{}
			}
			return material.Clickable(gtx, viewAll, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{
					Top: unit.Dp(2), Bottom: unit.Dp(2),
					Left: unit.Dp(6), Right: unit.Dp(6),
				}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body2(th.Theme, "View all")
					lbl.Color = th.Color.Fg3
					lbl.TextSize = th.Sizes.Mono
					lbl.Font = MonoFont
					return lbl.Layout(gtx)
				})
			})
		}),
	)
}

func activityRow(gtx layout.Context, th *Theme, e activityEntry) layout.Dimensions {
	dotCol := activityRowDotColor(th, e.Kind)
	msgCol := th.Color.Fg2
	if e.Kind == activityRowFailed {
		msgCol = th.Color.Err
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return activityStatusDot(gtx, dotCol, unit.Dp(6))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Dp(unit.Dp(96))
			gtx.Constraints.Max.X = gtx.Constraints.Min.X
			lbl := material.Body2(th.Theme, e.Time)
			lbl.Color = th.Color.Fg3
			lbl.TextSize = th.Sizes.Mono
			lbl.Font = MonoFont
			lbl.MaxLines = 1
			return lbl.Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, e.Message)
			lbl.Color = msgCol
			lbl.TextSize = th.Sizes.Mono
			lbl.Font = MonoFont
			lbl.MaxLines = 1
			lbl.Truncator = "…"
			return lbl.Layout(gtx)
		}),
	)
}

// activityRowDotColor maps the row kind to its leading status dot colour.
// Succeeded uses the muted Fg3 (a quiet bullet); failed uses Err; rolled
// back uses Warn so the user can distinguish "we noticed and reverted"
// from "this just blew up".
func activityRowDotColor(th *Theme, kind activityRowKind) color.NRGBA {
	switch kind {
	case activityRowFailed:
		return th.Color.Err
	case activityRowRolledBack:
		return th.Color.Warn
	}
	return th.Color.Fg3
}

// activityStatusDot draws a filled circle of size diameter in col. Used as
// the leading marker on each activity row.
func activityStatusDot(gtx layout.Context, col color.NRGBA, diameter unit.Dp) layout.Dimensions {
	d := gtx.Dp(diameter)
	defer clip.Ellipse{Max: image.Point{X: d, Y: d}}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, col)
	return layout.Dimensions{Size: image.Point{X: d, Y: d}}
}

// ─── Placeholders ───────────────────────────────────────────────────────────

func tabPlaceholder(gtx layout.Context, th *Theme, msg string) layout.Dimensions {
	return panel(gtx, th, "", func(gtx layout.Context) layout.Dimensions {
		lbl := material.Body2(th.Theme, msg)
		lbl.Color = th.Color.Fg3
		lbl.TextSize = th.Sizes.Body
		return lbl.Layout(gtx)
	})
}

// openInBrowser opens a URL using the OS-native handler. Thin wrapper so
// the call site reads naturally; falls through to utils.OpenURL.
func openInBrowser(url string) error { return utils.OpenURL(url) }
