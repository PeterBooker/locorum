package ui

import (
	"image"
	"image/color"
	"strconv"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
)

// NavRail is the leftmost column: the brand mark, two top-level nav items
// (Sites / Settings), a hardcoded GROUPS section, and a collapse chevron at
// the bottom. The rail's width is read from UIState.NavCollapsed and its
// right edge bears a 1-px line.
type NavRail struct {
	state *UIState
	sm    *sites.SiteManager

	sitesNav    widget.Clickable
	settingsNav widget.Clickable
	toggle      widget.Clickable

	// Hardcoded groups stub. Real grouping will replace this.
	groupClicks [3]widget.Clickable
	activeGroup int // 0 = Client work
}

// stubGroup defines a placeholder entry in the GROUPS section. The first
// group's swatch uses the accent color; the rest use the muted Fg3.
type stubGroup struct {
	Label string
	Count int
}

var stubGroups = []stubGroup{
	{Label: "Client work", Count: 4},
	{Label: "Personal", Count: 2},
	{Label: "Archived", Count: 2},
}

func NewNavRail(state *UIState, sm *sites.SiteManager) *NavRail {
	return &NavRail{state: state, sm: sm}
}

// HandleUserInteractions processes clicks on the nav items and the collapse
// toggle. Called by the root UI before Layout each frame.
func (n *NavRail) HandleUserInteractions(gtx layout.Context) {
	if n.sitesNav.Clicked(gtx) {
		n.state.SetNavView(NavViewSites)
	}
	if n.settingsNav.Clicked(gtx) {
		n.state.SetNavView(NavViewSettings)
	}
	if n.toggle.Clicked(gtx) {
		n.state.SetNavCollapsed(!n.state.NavCollapsed())
	}
	for i := range n.groupClicks {
		if n.groupClicks[i].Clicked(gtx) {
			n.activeGroup = i
		}
	}
}

func (n *NavRail) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	collapsed := n.state.NavCollapsed()
	width := th.Dims.RailExpanded
	if collapsed {
		width = th.Dims.RailCollapsed
	}
	gtx.Constraints.Max.X = gtx.Dp(width)
	gtx.Constraints.Min.X = gtx.Constraints.Max.X

	return FillBackground(gtx, th.Color.Bg, func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				return EdgeLine(gtx, th.Color.Line, "right")
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				return n.layoutContent(gtx, th, collapsed)
			}),
		)
	})
}

func (n *NavRail) layoutContent(gtx layout.Context, th *Theme, collapsed bool) layout.Dimensions {
	pad := layout.Inset{
		Top:    th.Spacing.MD,
		Bottom: th.Spacing.MD,
		Left:   unit.Dp(12),
		Right:  unit.Dp(12),
	}
	if collapsed {
		pad.Left = unit.Dp(6)
		pad.Right = unit.Dp(6)
	}
	// railRow forces Min.X = Max.X on the rigid child so the wrapped
	// widget can fill the rail width — collapsed items need it to centre
	// an icon pill, expanded items need it so each nav item's bg /
	// hover / click area spans the whole column instead of just the
	// icon+label content.
	railRow := func(w layout.Widget) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return w(gtx)
		})
	}
	return pad.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			railRow(func(gtx layout.Context) layout.Dimensions {
				return n.layoutBrand(gtx, th, collapsed)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Spacer{Height: th.Spacing.SM}.Layout(gtx)
			}),
			railRow(func(gtx layout.Context) layout.Dimensions {
				return n.layoutNavItem(gtx, th, &n.sitesNav, NavViewSites, IconSites, "Sites", collapsed)
			}),
			railRow(func(gtx layout.Context) layout.Dimensions {
				return n.layoutNavItem(gtx, th, &n.settingsNav, NavViewSettings, IconSettings, "Settings", collapsed)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if collapsed {
					return layout.Dimensions{}
				}
				return n.layoutGroups(gtx, th)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Min.X, Y: gtx.Constraints.Max.Y}}
			}),
			railRow(func(gtx layout.Context) layout.Dimensions {
				return n.layoutToggle(gtx, th, collapsed)
			}),
		)
	})
}

func (n *NavRail) layoutBrand(gtx layout.Context, th *Theme, collapsed bool) layout.Dimensions {
	if collapsed {
		return layout.Inset{Top: unit.Dp(4), Bottom: th.Spacing.LG}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return LayoutLogo(gtx, th, unit.Dp(36))
			})
		})
	}
	return layout.Inset{
		Top: unit.Dp(2), Bottom: th.Spacing.SM,
		Left: unit.Dp(6),
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return LayoutLogo(gtx, th, LogoSize)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body2(th.Theme, "Locorum")
					lbl.Color = th.Color.Fg
					lbl.TextSize = th.Sizes.Section
					lbl.Font.Weight = font.SemiBold
					return lbl.Layout(gtx)
				})
			}),
		)
	})
}

func (n *NavRail) layoutNavItem(
	gtx layout.Context, th *Theme,
	click *widget.Clickable, key NavView, icon IconFunc, label string,
	collapsed bool,
) layout.Dimensions {
	active := n.state.NavView() == key
	bg := th.Color.Bg
	iconCol := th.Color.Fg2
	textCol := th.Color.Fg2
	weight := font.Normal
	if active {
		bg = th.Color.Bg2
		iconCol = th.Color.Accent
		textCol = th.Color.Fg
		weight = font.Medium
	}

	// Settings entry shows the System Health badge — only it; other
	// nav items don't carry health meaning.
	badge := HealthBadgeNone
	if key == NavViewSettings {
		badge = HealthBadgeFor(n.state.HealthSnapshot())
	}

	if collapsed {
		return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return material.Clickable(gtx, click, func(gtx layout.Context) layout.Dimensions {
					pillSz := gtx.Dp(unit.Dp(48))
					gtx.Constraints.Min = image.Pt(pillSz, pillSz)
					gtx.Constraints.Max = image.Pt(pillSz, pillSz)
					return layout.Stack{}.Layout(gtx,
						layout.Stacked(func(gtx layout.Context) layout.Dimensions {
							return RoundedFill(gtx, bg, th.Radii.R2, func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min = image.Pt(pillSz, pillSz)
								return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return icon(gtx, th, unit.Dp(36), iconCol)
								})
							})
						}),
						layout.Stacked(func(gtx layout.Context) layout.Dimensions {
							if badge == HealthBadgeNone {
								return layout.Dimensions{Size: image.Pt(pillSz, pillSz)}
							}
							gtx.Constraints.Min = image.Pt(pillSz, pillSz)
							return layoutBadgeOverlay(gtx, badge, th)
						}),
					)
				})
			})
		})
	}
	return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return material.Clickable(gtx, click, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return RoundedFill(gtx, bg, th.Radii.R2, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{
					Top: unit.Dp(9), Bottom: unit.Dp(9),
					Left: unit.Dp(12), Right: unit.Dp(12),
				}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return icon(gtx, th, IconSize, iconCol)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								lbl := material.Body2(th.Theme, label)
								lbl.Color = textCol
								lbl.TextSize = th.Sizes.Body
								lbl.Font.Weight = weight
								return lbl.Layout(gtx)
							})
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Min.X}}
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return LayoutHealthBadge(gtx, badge, th)
						}),
					)
				})
			})
		})
	})
}

// layoutBadgeOverlay positions a badge dot in the top-right corner of
// the surrounding pill (used by the collapsed rail). The pillSz is the
// outer square's side length in pixels (already passed via Constraints.Min).
func layoutBadgeOverlay(gtx layout.Context, badge HealthBadgeKind, th *Theme) layout.Dimensions {
	pillSz := gtx.Constraints.Min.X
	if pillSz == 0 {
		pillSz = gtx.Constraints.Max.X
	}
	margin := gtx.Dp(unit.Dp(8))
	off := image.Pt(pillSz-margin-gtx.Dp(unit.Dp(10)), margin-gtx.Dp(unit.Dp(2)))
	defer op.Offset(off).Push(gtx.Ops).Pop()
	return LayoutHealthBadge(gtx, badge, th)
}

// layoutGroups renders the "GROUPS" section header plus the three hardcoded
// group rows. Hidden when the rail is collapsed.
func (n *NavRail) layoutGroups(gtx layout.Context, th *Theme) layout.Dimensions {
	return layout.Inset{Top: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{
					Top: unit.Dp(2), Bottom: th.Spacing.XS,
					Left: unit.Dp(12),
				}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body2(th.Theme, "GROUPS")
					lbl.Color = th.Color.Fg3
					lbl.TextSize = th.Sizes.Micro
					lbl.Font = MonoFont
					lbl.Font.Weight = font.Medium
					return lbl.Layout(gtx)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				children := make([]layout.FlexChild, 0, len(stubGroups))
				for i, g := range stubGroups {
					i, g := i, g
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return n.layoutGroupRow(gtx, th, i, g)
					}))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
			}),
		)
	})
}

func (n *NavRail) layoutGroupRow(gtx layout.Context, th *Theme, idx int, g stubGroup) layout.Dimensions {
	active := idx == n.activeGroup
	swatch := th.Color.Fg3
	if idx == 0 {
		swatch = th.Color.Accent
	}
	textCol := th.Color.Fg2
	weight := font.Normal
	if active {
		textCol = th.Color.Fg
		weight = font.Medium
	}
	return layout.Inset{Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return material.Clickable(gtx, &n.groupClicks[idx], func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{
				Top: unit.Dp(7), Bottom: unit.Dp(7),
				Left: unit.Dp(12), Right: unit.Dp(12),
			}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return groupSwatch(gtx, swatch)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							lbl := material.Body2(th.Theme, g.Label)
							lbl.Color = textCol
							lbl.TextSize = th.Sizes.Body
							lbl.Font.Weight = weight
							return lbl.Layout(gtx)
						})
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Min.X}}
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th.Theme, strconv.Itoa(g.Count))
						lbl.Color = th.Color.Fg3
						lbl.TextSize = th.Sizes.Mono
						lbl.Font = MonoFont
						return lbl.Layout(gtx)
					}),
				)
			})
		})
	})
}

// groupSwatch paints a small (9×9) rounded color square used as the group
// indicator next to its label.
func groupSwatch(gtx layout.Context, col color.NRGBA) layout.Dimensions {
	s := gtx.Dp(unit.Dp(9))
	rr := gtx.Dp(unit.Dp(2))
	rect := image.Rectangle{Max: image.Point{X: s, Y: s}}
	defer clip.RRect{Rect: rect, NE: rr, NW: rr, SE: rr, SW: rr}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, col)
	return layout.Dimensions{Size: image.Point{X: s, Y: s}}
}

func (n *NavRail) layoutToggle(gtx layout.Context, th *Theme, collapsed bool) layout.Dimensions {
	icon := IconChevronLeft
	if collapsed {
		icon = IconChevronRight
	}
	if collapsed {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return material.Clickable(gtx, &n.toggle, func(gtx layout.Context) layout.Dimensions {
				pillSz := gtx.Dp(unit.Dp(48))
				gtx.Constraints.Min = image.Pt(pillSz, pillSz)
				gtx.Constraints.Max = image.Pt(pillSz, pillSz)
				return RoundedFill(gtx, th.Color.Bg2, th.Radii.R2, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min = image.Pt(pillSz, pillSz)
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return icon(gtx, th, unit.Dp(28), th.Color.Fg2)
					})
				})
			})
		})
	}
	return material.Clickable(gtx, &n.toggle, func(gtx layout.Context) layout.Dimensions {
		return RoundedFill(gtx, th.Color.Bg, th.Radii.R2, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return icon(gtx, th, unit.Dp(14), th.Color.Fg3)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Min.X}}
					}),
				)
			})
		})
	})
}
