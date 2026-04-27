package ui

import (
	"image"
	"strconv"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/types"
)

// SitesPanel is column 2: a fixed-width sites list with a sticky-feeling
// header (title, count, +New button, search input) above scrollable rows.
type SitesPanel struct {
	state  *UIState
	sm     *sites.SiteManager
	toasts *Notifications

	searchField widget.Editor
	newSiteBtn  widget.Clickable
	filterBtn   widget.Clickable
	list        widget.List

	siteClicks []widget.Clickable
}

func NewSitesPanel(state *UIState, sm *sites.SiteManager, toasts *Notifications) *SitesPanel {
	p := &SitesPanel{state: state, sm: sm, toasts: toasts}
	p.searchField.SingleLine = true
	p.list.List.Axis = layout.Vertical
	return p
}

// HandleUserInteractions reads search text, the +New click, and per-row
// selection clicks into UIState.
func (p *SitesPanel) HandleUserInteractions(gtx layout.Context) {
	p.state.SetSearchTerm(p.searchField.Text())
	if p.newSiteBtn.Clicked(gtx) {
		p.state.SetShowNewSiteModal(true)
	}
	filtered := p.filteredSites()
	for len(p.siteClicks) < len(filtered) {
		p.siteClicks = append(p.siteClicks, widget.Clickable{})
	}
	for i, s := range filtered {
		if p.siteClicks[i].Clicked(gtx) {
			p.state.SetSelectedID(s.ID)
		}
	}
}

func (p *SitesPanel) filteredSites() []types.Site {
	all := p.state.GetSites()
	term := strings.ToLower(p.state.GetSearchTerm())
	if term == "" {
		return all
	}
	filtered := make([]types.Site, 0, len(all))
	for _, s := range all {
		if strings.Contains(strings.ToLower(s.Name), term) ||
			strings.Contains(strings.ToLower(s.Domain), term) {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func (p *SitesPanel) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	gtx.Constraints.Max.X = gtx.Dp(th.Dims.SitesListWidth)
	gtx.Constraints.Min.X = gtx.Constraints.Max.X

	return FillBackground(gtx, th.Color.Bg1, func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				return EdgeLine(gtx, th.Color.Line, "right")
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return p.layoutHeader(gtx, th)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return p.layoutList(gtx, th)
					}),
				)
			}),
		)
	})
}

func (p *SitesPanel) layoutHeader(gtx layout.Context, th *Theme) layout.Dimensions {
	count := len(p.state.GetSites())
	return layout.Stack{}.Layout(gtx,
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{
				Top: th.Spacing.MD, Bottom: th.Spacing.SM,
				Left: th.Spacing.MD, Right: th.Spacing.MD,
			}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return p.layoutTitleRow(gtx, th, count)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return p.layoutSearch(gtx, th)
						})
					}),
				)
			})
		}),
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			return EdgeLine(gtx, th.Color.Line, "bottom")
		}),
	)
}

func (p *SitesPanel) layoutTitleRow(gtx layout.Context, th *Theme, count int) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, "Sites")
			lbl.Color = th.Color.Fg
			lbl.TextSize = th.Sizes.Section
			lbl.Font.Weight = font.SemiBold
			return lbl.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Spacing.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, strconv.Itoa(count))
				lbl.Color = th.Color.Fg3
				lbl.TextSize = th.Sizes.Mono
				lbl.Font = MonoFont
				return lbl.Layout(gtx)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Min.X}}
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.layoutFilterButton(gtx, th)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Spacer{Width: th.Spacing.XS}.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.layoutNewButton(gtx, th)
		}),
	)
}

// layoutFilterButton paints a square ghost icon button next to the title.
// The click is a stub: filter behavior is not wired to the data layer yet.
func (p *SitesPanel) layoutFilterButton(gtx layout.Context, th *Theme) layout.Dimensions {
	return material.Clickable(gtx, &p.filterBtn, func(gtx layout.Context) layout.Dimensions {
		return RoundedFill(gtx, th.Color.Bg1, th.Radii.R2, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(7)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return IconFilter(gtx, th, unit.Dp(14), th.Color.Fg3)
			})
		})
	})
}

func (p *SitesPanel) layoutNewButton(gtx layout.Context, th *Theme) layout.Dimensions {
	return material.Clickable(gtx, &p.newSiteBtn, func(gtx layout.Context) layout.Dimensions {
		return RoundedFill(gtx, th.Color.Accent, th.Radii.R2, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{
				Top: unit.Dp(5), Bottom: unit.Dp(5),
				Left: unit.Dp(9), Right: unit.Dp(11),
			}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return IconPlus(gtx, th, unit.Dp(12), th.Color.AccentFg)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							lbl := material.Body2(th.Theme, "New")
							lbl.Color = th.Color.AccentFg
							lbl.TextSize = th.Sizes.Mono
							lbl.Font.Weight = font.Medium
							return lbl.Layout(gtx)
						})
					}),
				)
			})
		})
	})
}

func (p *SitesPanel) layoutSearch(gtx layout.Context, th *Theme) layout.Dimensions {
	border := widget.Border{
		Color:        th.Color.LineStrong,
		CornerRadius: th.Radii.R2,
		Width:        unit.Dp(1),
	}
	return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top: unit.Dp(7), Bottom: unit.Dp(7),
			Left: unit.Dp(10), Right: unit.Dp(10),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return IconSearch(gtx, th, unit.Dp(14), th.Color.Fg3)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					ed := material.Editor(th.Theme, &p.searchField, "Search…")
					ed.Color = th.Color.Fg
					ed.HintColor = th.Color.Fg3
					ed.TextSize = th.Sizes.Body
					return ed.Layout(gtx)
				}),
			)
		})
	})
}

func (p *SitesPanel) layoutList(gtx layout.Context, th *Theme) layout.Dimensions {
	filtered := p.filteredSites()
	for len(p.siteClicks) < len(filtered) {
		p.siteClicks = append(p.siteClicks, widget.Clickable{})
	}
	if len(filtered) == 0 {
		return layout.Inset{
			Top: th.Spacing.LG, Left: th.Spacing.MD, Right: th.Spacing.MD,
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, "No sites yet — click + New to create one.")
			lbl.Color = th.Color.Fg3
			lbl.TextSize = th.Sizes.Body
			return lbl.Layout(gtx)
		})
	}
	selectedID := p.state.GetSelectedID()
	return material.List(th.Theme, &p.list).Layout(gtx, len(filtered), func(gtx layout.Context, i int) layout.Dimensions {
		s := filtered[i]
		return p.layoutRow(gtx, th, &p.siteClicks[i], s, s.ID == selectedID)
	})
}

func (p *SitesPanel) layoutRow(gtx layout.Context, th *Theme, click *widget.Clickable, s types.Site, active bool) layout.Dimensions {
	bg := th.Color.Bg1
	if active {
		bg = th.Color.Bg2
	}
	return material.Clickable(gtx, click, func(gtx layout.Context) layout.Dimensions {
		return FillBackground(gtx, bg, func(gtx layout.Context) layout.Dimensions {
			return layout.Stack{}.Layout(gtx,
				layout.Expanded(func(gtx layout.Context) layout.Dimensions {
					if !active {
						return layout.Dimensions{Size: gtx.Constraints.Min}
					}
					w := gtx.Dp(unit.Dp(2))
					rect := image.Rectangle{Max: image.Pt(w, gtx.Constraints.Min.Y)}
					defer clip.Rect(rect).Push(gtx.Ops).Pop()
					paint.Fill(gtx.Ops, th.Color.Accent)
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				layout.Stacked(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{
						Top: unit.Dp(13), Bottom: unit.Dp(13),
						Left: th.Spacing.MD, Right: th.Spacing.MD,
					}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return p.layoutRowContent(gtx, th, s)
					})
				}),
				layout.Expanded(func(gtx layout.Context) layout.Dimensions {
					return EdgeLine(gtx, th.Color.Line, "bottom")
				}),
			)
		})
	})
}

func (p *SitesPanel) layoutRowContent(gtx layout.Context, th *Theme, s types.Site) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return SiteAvatar(gtx, th, s.Name, unit.Dp(36))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Spacer{Width: unit.Dp(12)}.Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body2(th.Theme, s.Name)
					lbl.Color = th.Color.Fg
					lbl.TextSize = th.Sizes.Body
					lbl.Font.Weight = font.Medium
					lbl.MaxLines = 1
					lbl.Truncator = "…"
					return lbl.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th.Theme, s.Domain)
						lbl.Color = th.Color.Fg3
						lbl.TextSize = th.Sizes.Mono
						lbl.Font = MonoFont
						lbl.MaxLines = 1
						lbl.Truncator = "…"
						return lbl.Layout(gtx)
					})
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.layoutRowStatus(gtx, th, s)
			})
		}),
	)
}

func (p *SitesPanel) layoutRowStatus(gtx layout.Context, th *Theme, s types.Site) layout.Dimensions {
	statusKey, statusLbl := StatusForSite(s.Started)
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return LiveStatusDot(gtx, th, statusKey, true)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, statusLbl)
				lbl.Color = th.Color.Fg2
				lbl.TextSize = th.Sizes.Mono
				lbl.Font = MonoFont
				return lbl.Layout(gtx)
			})
		}),
	)
}
