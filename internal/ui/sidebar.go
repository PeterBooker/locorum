package ui

import (
	"image"
	"strings"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/types"
)

type Sidebar struct {
	ui          *UI
	searchField widget.Editor
	newSiteBtn  widget.Clickable
	list        widget.List

	// Per-site clickables (grown dynamically)
	siteClicks  []widget.Clickable
	deleteClicks []widget.Clickable
}

func NewSidebar(ui *UI) *Sidebar {
	s := &Sidebar{ui: ui}
	s.searchField.SingleLine = true
	s.list.List.Axis = layout.Vertical
	return s
}

func (s *Sidebar) Layout(gtx layout.Context, th *material.Theme) layout.Dimensions {
	// Fixed sidebar width
	gtx.Constraints.Max.X = gtx.Dp(unit.Dp(256))
	gtx.Constraints.Min.X = gtx.Constraints.Max.X

	return FillBackground(gtx, ColorSidebarBg, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top:    unit.Dp(24),
			Bottom: unit.Dp(16),
			Left:   unit.Dp(16),
			Right:  unit.Dp(16),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				// Title
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.H5(th, "Locorum")
					lbl.Color = ColorWhite
					return layout.Inset{Bottom: unit.Dp(16)}.Layout(gtx, lbl.Layout)
				}),
				// "Sites" heading
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.H6(th, "Sites")
					lbl.Color = ColorGray100
					return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, lbl.Layout)
				}),
				// Search field
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return s.layoutSearch(gtx, th)
					})
				}),
				// Site list
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return s.layoutSiteList(gtx, th)
				}),
				// "New Site" button
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						if s.newSiteBtn.Clicked(gtx) {
							s.ui.State.mu.Lock()
							s.ui.State.ShowNewSiteModal = true
							s.ui.State.mu.Unlock()
						}
						return PrimaryButton(gtx, th, &s.newSiteBtn, "New Site")
					})
				}),
			)
		})
	})
}

func (s *Sidebar) layoutSearch(gtx layout.Context, th *material.Theme) layout.Dimensions {
	// Update search term
	s.ui.State.SearchTerm = s.searchField.Text()

	border := widget.Border{
		Color:        ColorGray500,
		CornerRadius: unit.Dp(4),
		Width:        unit.Dp(1),
	}
	return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top:    unit.Dp(6),
			Bottom: unit.Dp(6),
			Left:   unit.Dp(8),
			Right:  unit.Dp(8),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			ed := material.Editor(th, &s.searchField, "Search sites...")
			ed.Color = ColorWhite
			ed.HintColor = ColorGray400
			ed.TextSize = unit.Sp(14)
			return ed.Layout(gtx)
		})
	})
}

func (s *Sidebar) layoutSiteList(gtx layout.Context, th *material.Theme) layout.Dimensions {
	s.ui.State.mu.Lock()
	allSites := s.ui.State.Sites
	search := s.ui.State.SearchTerm
	selectedID := s.ui.State.SelectedID
	s.ui.State.mu.Unlock()

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

		// Handle clicks
		if s.siteClicks[i].Clicked(gtx) {
			s.ui.State.mu.Lock()
			s.ui.State.SelectedID = site.ID
			s.ui.State.mu.Unlock()
		}
		if s.deleteClicks[i].Clicked(gtx) {
			id := site.ID
			go func() {
				if err := s.ui.SM.DeleteSite(id); err != nil {
					s.ui.State.ShowError("Failed to delete site: " + err.Error())
				}
			}()
		}

		isSelected := site.ID == selectedID

		return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return s.layoutSiteItem(gtx, th, &s.siteClicks[i], &s.deleteClicks[i], site.Name, isSelected)
		})
	})
}

func (s *Sidebar) layoutSiteItem(gtx layout.Context, th *material.Theme, click *widget.Clickable, deleteClick *widget.Clickable, name string, selected bool) layout.Dimensions {
	bgColor := ColorGray900
	if selected {
		bgColor = ColorGray700
	}

	return FillBackground(gtx, bgColor, func(gtx layout.Context) layout.Dimensions {
		// Round corners
		defer clip.RRect{
			Rect: image.Rectangle{Max: gtx.Constraints.Max},
			NE:   gtx.Dp(unit.Dp(4)), NW: gtx.Dp(unit.Dp(4)),
			SE:   gtx.Dp(unit.Dp(4)), SW: gtx.Dp(unit.Dp(4)),
		}.Push(gtx.Ops).Pop()

		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return material.Clickable(gtx, click, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{
						Top:    unit.Dp(8),
						Bottom: unit.Dp(8),
						Left:   unit.Dp(10),
						Right:  unit.Dp(4),
					}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th, name)
						lbl.Color = ColorWhite
						lbl.TextSize = unit.Sp(14)
						return lbl.Layout(gtx)
					})
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return material.Clickable(gtx, deleteClick, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{
						Top:    unit.Dp(8),
						Bottom: unit.Dp(8),
						Left:   unit.Dp(4),
						Right:  unit.Dp(8),
					}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th, "✕")
						lbl.Color = ColorGray400
						lbl.TextSize = unit.Sp(14)
						return lbl.Layout(gtx)
					})
				})
			}),
		)
	})
}
