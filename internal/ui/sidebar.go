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
	toasts *ToastManager

	searchField widget.Editor
	newSiteBtn  widget.Clickable
	list        widget.List

	// Per-site clickables (grown dynamically)
	siteClicks   []widget.Clickable
	deleteClicks []widget.Clickable
}

func NewSidebar(state *UIState, sm *sites.SiteManager, toasts *ToastManager) *Sidebar {
	s := &Sidebar{state: state, sm: sm, toasts: toasts}
	s.searchField.SingleLine = true
	s.list.List.Axis = layout.Vertical
	return s
}

func (s *Sidebar) Layout(gtx layout.Context, th *material.Theme) layout.Dimensions {
	// Fixed sidebar width
	gtx.Constraints.Max.X = gtx.Dp(SidebarWidth)
	gtx.Constraints.Min.X = gtx.Constraints.Max.X

	return FillBackground(gtx, ColorSidebarBg, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top: SpaceXL, Bottom: SpaceLG,
			Left: SpaceLG, Right: SpaceLG,
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				// Title
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.H5(th, "Locorum")
					lbl.Color = ColorWhite
					return layout.Inset{Bottom: SpaceLG}.Layout(gtx, lbl.Layout)
				}),
				// Divider
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return Divider(gtx, ColorGray700, SpaceSM)
				}),
				// "Sites" heading
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.H6(th, "Sites")
					lbl.Color = ColorGray100
					return layout.Inset{Bottom: SpaceSM}.Layout(gtx, lbl.Layout)
				}),
				// Search field
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Bottom: SpaceMD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return s.layoutSearch(gtx, th)
					})
				}),
				// Site list
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return s.layoutSiteList(gtx, th)
				}),
				// "New Site" button
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: SpaceMD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						if s.newSiteBtn.Clicked(gtx) {
							s.state.SetShowNewSiteModal(true)
						}
						return PrimaryButton(gtx, th, &s.newSiteBtn, "New Site")
					})
				}),
			)
		})
	})
}

func (s *Sidebar) layoutSearch(gtx layout.Context, th *material.Theme) layout.Dimensions {
	s.state.SetSearchTerm(s.searchField.Text())

	border := widget.Border{
		Color:        ColorGray500,
		CornerRadius: RadiusSM,
		Width:        unit.Dp(1),
	}
	return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top: unit.Dp(6), Bottom: unit.Dp(6),
			Left: SpaceSM, Right: SpaceSM,
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			ed := material.Editor(th, &s.searchField, "Search sites...")
			ed.Color = ColorWhite
			ed.HintColor = ColorGray400
			ed.TextSize = TextBase
			return ed.Layout(gtx)
		})
	})
}

func (s *Sidebar) layoutSiteList(gtx layout.Context, th *material.Theme) layout.Dimensions {
	allSites := s.state.GetSites()
	search := s.state.GetSearchTerm()
	selectedID := s.state.GetSelectedID()

	// Filter sites
	var filtered []types.Site
	for _, site := range allSites {
		if search == "" || strings.Contains(strings.ToLower(site.Name), strings.ToLower(search)) {
			filtered = append(filtered, site)
		}
	}

	// Ensure enough clickables
	for len(s.siteClicks) < len(filtered) {
		s.siteClicks = append(s.siteClicks, widget.Clickable{})
	}
	for len(s.deleteClicks) < len(filtered) {
		s.deleteClicks = append(s.deleteClicks, widget.Clickable{})
	}

	if len(filtered) == 0 {
		lbl := material.Body2(th, "No sites found")
		lbl.Color = ColorGray400
		return lbl.Layout(gtx)
	}

	return material.List(th, &s.list).Layout(gtx, len(filtered), func(gtx layout.Context, i int) layout.Dimensions {
		site := filtered[i]

		if s.siteClicks[i].Clicked(gtx) {
			s.state.SetSelectedID(site.ID)
		}
		if s.deleteClicks[i].Clicked(gtx) {
			s.state.ShowDeleteConfirm(site.ID, site.Name)
		}

		isSelected := site.ID == selectedID

		return layout.Inset{Bottom: SpaceXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return s.layoutSiteItem(gtx, th, &s.siteClicks[i], &s.deleteClicks[i], site.Name, isSelected, site.Started)
		})
	})
}

func (s *Sidebar) layoutSiteItem(gtx layout.Context, th *material.Theme, click *widget.Clickable, deleteClick *widget.Clickable, name string, selected, started bool) layout.Dimensions {
	bgColor := ColorGray900
	if selected {
		bgColor = ColorGray700
	}

	return FillBackground(gtx, bgColor, func(gtx layout.Context) layout.Dimensions {
		rr := gtx.Dp(RadiusSM)
		defer clip.RRect{
			Rect: image.Rectangle{Max: gtx.Constraints.Max},
			NE:   rr, NW: rr, SE: rr, SW: rr,
		}.Push(gtx.Ops).Pop()

		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			// Status indicator dot
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: SpaceSM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layoutStatusDot(gtx, started)
				})
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return material.Clickable(gtx, click, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{
						Top: SpaceSM, Bottom: SpaceSM,
						Left: unit.Dp(6), Right: SpaceXS,
					}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th, name)
						lbl.Color = ColorWhite
						lbl.TextSize = TextBase
						return lbl.Layout(gtx)
					})
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return material.Clickable(gtx, deleteClick, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{
						Top: SpaceSM, Bottom: SpaceSM,
						Left: SpaceXS, Right: SpaceSM,
					}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th, "✕")
						lbl.Color = ColorGray400
						lbl.TextSize = TextBase
						return lbl.Layout(gtx)
					})
				})
			}),
		)
	})
}

// layoutStatusDot draws a small filled circle as a status indicator.
func layoutStatusDot(gtx layout.Context, started bool) layout.Dimensions {
	size := gtx.Dp(unit.Dp(8))
	var col color.NRGBA
	if started {
		col = ColorGreen600
	} else {
		col = ColorGray400
	}

	defer clip.Ellipse{
		Min: image.Point{},
		Max: image.Point{X: size, Y: size},
	}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, col)

	return layout.Dimensions{Size: image.Point{X: size, Y: size}}
}
