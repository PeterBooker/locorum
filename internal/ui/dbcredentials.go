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

func (dc *DBCredentials) Layout(gtx layout.Context, th *material.Theme, site *types.Site) layout.Dimensions {
	items := []KV{
		{"Hostname", "database"},
		{"Adminer Host", "locorum-" + site.Slug + "-database"},
		{"Database", "wordpress"},
		{"User", "wordpress"},
		{"Password", site.DBPassword},
	}

	return Section(gtx, th, "Database", func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, len(items))
		for i, item := range items {
			item := item
			idx := i
			children[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if dc.creds[idx].copy.Clicked(gtx) {
					CopyToClipboard(gtx, item.Value)
				}

				return layout.Inset{Bottom: SpaceXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						// Key label
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.X = gtx.Dp(LabelColWidth)
							lbl := material.Body2(th, item.Key)
							lbl.Color = ColorGray500
							lbl.TextSize = TextBase
							return lbl.Layout(gtx)
						}),
						// Selectable value
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return SelectableLabel(gtx, th, &dc.creds[idx].sel, item.Value, TextBase, th.Fg)
						}),
						// Copy button
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: SpaceSM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
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
