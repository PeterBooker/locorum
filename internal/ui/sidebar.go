package ui

import (
	"image"
	"image/color"
	"strings"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/types"
)

type Sidebar struct {
	state  *UIState
	sm     *sites.SiteManager
	toasts *Notifications

	searchField widget.Editor
	newSiteBtn  widget.Clickable
	list        widget.List

	// Per-site clickables (grown dynamically)
	siteClicks   []widget.Clickable
	deleteClicks []widget.Clickable
}

func NewSidebar(state *UIState, sm *sites.SiteManager, toasts *Notifications) *Sidebar {
	s := &Sidebar{state: state, sm: sm, toasts: toasts}
	s.searchField.SingleLine = true
	s.list.List.Axis = layout.Vertical
	return s
}

// HandleUserInteractions processes the search editor, New Site button, and
// per-site selection/delete clicks. Called by the root UI before Layout.
func (s *Sidebar) HandleUserInteractions(gtx layout.Context) {
	// Pull search term from editor into app state so filtering is consistent.
	s.state.SetSearchTerm(s.searchField.Text())

	if s.newSiteBtn.Clicked(gtx) {
		s.state.SetShowNewSiteModal(true)
	}

	filtered := s.filteredSites()
	for len(s.siteClicks) < len(filtered) {
		s.siteClicks = append(s.siteClicks, widget.Clickable{})
	}
	for len(s.deleteClicks) < len(filtered) {
		s.deleteClicks = append(s.deleteClicks, widget.Clickable{})
	}
	for i, site := range filtered {
		if s.siteClicks[i].Clicked(gtx) {
			s.state.SetSelectedID(site.ID)
		}
		if s.deleteClicks[i].Clicked(gtx) {
			s.state.ShowDeleteConfirm(site.ID, site.Name)
		}
	}
}

// filteredSites returns the filtered site list shown in the sidebar.
func (s *Sidebar) filteredSites() []types.Site {
	allSites := s.state.GetSites()
	search := s.state.GetSearchTerm()
	var filtered []types.Site
	for _, site := range allSites {
		if search == "" || strings.Contains(strings.ToLower(site.Name), strings.ToLower(search)) {
			filtered = append(filtered, site)
		}
	}
	return filtered
}

func (s *Sidebar) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	// Fixed sidebar width
	gtx.Constraints.Max.X = gtx.Dp(th.Dims.SidebarWidth)
	gtx.Constraints.Min.X = gtx.Constraints.Max.X

	return FillBackground(gtx, th.Color.SidebarBg, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top: th.Spacing.XL, Bottom: th.Spacing.LG,
			Left: th.Spacing.LG, Right: th.Spacing.LG,
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				// Logo + Title + Bell
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Bottom: th.Spacing.LG}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return LayoutLogo(gtx, th, LogoSize)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									lbl := material.H5(th.Theme, "Locorum")
									lbl.Color = th.Color.Brand
									return lbl.Layout(gtx)
								})
							}),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Max.X}}
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return s.toasts.LayoutBell(gtx, th)
							}),
						)
					})
				}),
				// Divider
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return Divider(gtx, th.Color.Border, th.Spacing.SM)
				}),
				// "Sites" heading
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.H6(th.Theme, "Sites")
					lbl.Color = th.Color.White
					return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
				}),
				// Search field
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return s.layoutSearch(gtx, th)
					})
				}),
				// Site list
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return s.layoutSiteList(gtx, th)
				}),
				// "New Site" button
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return PrimaryButton(gtx, th, &s.newSiteBtn, "New Site")
					})
				}),
			)
		})
	})
}

func (s *Sidebar) layoutSearch(gtx layout.Context, th *Theme) layout.Dimensions {
	border := widget.Border{
		Color:        th.Color.TextSecondary,
		CornerRadius: th.Radii.SM,
		Width:        unit.Dp(1),
	}
	return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top: unit.Dp(6), Bottom: unit.Dp(6),
			Left: th.Spacing.SM, Right: th.Spacing.SM,
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			ed := material.Editor(th.Theme, &s.searchField, "Search sites...")
			ed.Color = th.Color.White
			ed.HintColor = th.Color.TextMuted
			ed.TextSize = th.Sizes.Base
			return ed.Layout(gtx)
		})
	})
}

func (s *Sidebar) layoutSiteList(gtx layout.Context, th *Theme) layout.Dimensions {
	filtered := s.filteredSites()
	selectedID := s.state.GetSelectedID()

	// Ensure enough clickables — HandleUserInteractions also grows these, but
	// guard here so Layout is safe if ever invoked alone (e.g., tests).
	for len(s.siteClicks) < len(filtered) {
		s.siteClicks = append(s.siteClicks, widget.Clickable{})
	}
	for len(s.deleteClicks) < len(filtered) {
		s.deleteClicks = append(s.deleteClicks, widget.Clickable{})
	}

	if len(filtered) == 0 {
		lbl := material.Body2(th.Theme, "No sites found")
		lbl.Color = th.Color.TextMuted
		return lbl.Layout(gtx)
	}

	return material.List(th.Theme, &s.list).Layout(gtx, len(filtered), func(gtx layout.Context, i int) layout.Dimensions {
		site := filtered[i]
		isSelected := site.ID == selectedID

		return layout.Inset{Bottom: th.Spacing.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return s.layoutSiteItem(gtx, th, &s.siteClicks[i], &s.deleteClicks[i], site.Name, isSelected, site.Started)
		})
	})
}

func (s *Sidebar) layoutSiteItem(gtx layout.Context, th *Theme, click *widget.Clickable, deleteClick *widget.Clickable, name string, selected, started bool) layout.Dimensions {
	bgColor := th.Color.SurfaceDeep
	if selected {
		bgColor = th.Color.Surface
	}

	return FillBackground(gtx, bgColor, func(gtx layout.Context) layout.Dimensions {
		rr := gtx.Dp(th.Radii.SM)
		defer clip.RRect{
			Rect: image.Rectangle{Max: gtx.Constraints.Max},
			NE:   rr, NW: rr, SE: rr, SW: rr,
		}.Push(gtx.Ops).Pop()

		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			// Status indicator dot
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layoutStatusDot(gtx, th, started)
				})
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return material.Clickable(gtx, click, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{
						Top: th.Spacing.SM, Bottom: th.Spacing.SM,
						Left: unit.Dp(6), Right: th.Spacing.XS,
					}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th.Theme, TruncateWords(name, 22))
						lbl.Color = th.Color.White
						lbl.TextSize = th.Sizes.Base
						lbl.MaxLines = 1
						lbl.Truncator = "…"
						return lbl.Layout(gtx)
					})
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return material.Clickable(gtx, deleteClick, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{
						Top: th.Spacing.SM, Bottom: th.Spacing.SM,
						Left: th.Spacing.XS, Right: th.Spacing.SM,
					}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th.Theme, "✕")
						lbl.Color = th.Color.TextMuted
						lbl.TextSize = th.Sizes.Base
						return lbl.Layout(gtx)
					})
				})
			}),
		)
	})
}

// layoutStatusDot draws a small filled circle as a status indicator.
func layoutStatusDot(gtx layout.Context, th *Theme, started bool) layout.Dimensions {
	size := gtx.Dp(unit.Dp(8))
	var col color.NRGBA
	if started {
		col = th.Color.Success
	} else {
		col = th.Color.TextMuted
	}

	defer clip.Ellipse{
		Min: image.Point{},
		Max: image.Point{X: size, Y: size},
	}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, col)

	return layout.Dimensions{Size: image.Point{X: size, Y: size}}
}
