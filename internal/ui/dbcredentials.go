package ui

import (
	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/types"
)

// dbCredField holds the state for a single copyable database credential row.
type dbCredField struct {
	sel  widget.Selectable
	copy widget.Clickable
}

// DBCredentials renders the Database section with selectable values and copy buttons.
type DBCredentials struct {
	creds [5]dbCredField
}

func NewDBCredentials() *DBCredentials {
	return &DBCredentials{}
}

// credItems returns the list of credential rows shown in the Database section.
func (dc *DBCredentials) credItems(site *types.Site) []KV {
	return []KV{
		{"Hostname", "database"},
		{"Adminer Host", "locorum-" + site.Slug + "-database"},
		{"Database", "wordpress"},
		{"User", "wordpress"},
		{"Password", site.DBPassword},
	}
}

// HandleUserInteractions processes per-row Copy button clicks.
func (dc *DBCredentials) HandleUserInteractions(gtx layout.Context, site *types.Site) {
	items := dc.credItems(site)
	for i, item := range items {
		if dc.creds[i].copy.Clicked(gtx) {
			CopyToClipboard(gtx, item.Value)
		}
	}
}

func (dc *DBCredentials) Layout(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	items := dc.credItems(site)

	return Section(gtx, th, "Database", func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, len(items))
		for i, item := range items {
			item := item
			idx := i
			children[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						// Key label
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.X = gtx.Dp(th.Dims.LabelColWidth)
							lbl := material.Body2(th.Theme, item.Key)
							lbl.Color = th.Color.TextSecondary
							lbl.TextSize = th.Sizes.Base
							return lbl.Layout(gtx)
						}),
						// Selectable value
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return SelectableLabel(gtx, th, &dc.creds[idx].sel, item.Value, th.Sizes.Base, th.Fg, MonoFont)
						}),
						// Copy button
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return SmallButton(gtx, th, &dc.creds[idx].copy, "Copy")
							})
						}),
					)
				})
			})
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}
