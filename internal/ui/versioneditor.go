package ui

import (
	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/types"
)

// VersionEditor shows read-only version info when a site is running,
// and editable dropdowns when it is stopped.
type VersionEditor struct {
	state  *UIState
	sm     *sites.SiteManager
	toasts *ToastManager

	phpDropdown   *Dropdown
	mysqlDropdown *Dropdown
	redisDropdown *Dropdown
	saveBtn       widget.Clickable

	// Track which site we last synced dropdowns for.
	lastSiteID string
}

func NewVersionEditor(state *UIState, sm *sites.SiteManager, toasts *ToastManager) *VersionEditor {
	return &VersionEditor{
		state:         state,
		sm:            sm,
		toasts:        toasts,
		phpDropdown:   NewDropdown(phpVersions),
		mysqlDropdown: NewDropdown(mysqlVersions),
		redisDropdown: NewDropdown(redisVersions),
	}
}

func (ve *VersionEditor) Layout(gtx layout.Context, th *material.Theme, site *types.Site) layout.Dimensions {
	if site.Started {
		return layoutVersionsSection(gtx, th, site)
	}

	// Sync dropdown selections to current site values when site changes.
	if ve.lastSiteID != site.ID {
		ve.lastSiteID = site.ID
		ve.syncDropdowns(site)
	}

	ve.handleClicks(gtx, site)

	return Section(gtx, th, "Versions (editable while stopped)", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: SpaceSM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return ve.phpDropdown.Layout(gtx, th, "PHP Version")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: SpaceSM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return ve.mysqlDropdown.Layout(gtx, th, "MySQL Version")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: SpaceMD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return ve.redisDropdown.Layout(gtx, th, "Redis Version")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return PrimaryButton(gtx, th, &ve.saveBtn, "Save Changes")
			}),
		)
	})
}

func (ve *VersionEditor) syncDropdowns(site *types.Site) {
	for i, v := range phpVersions {
		if v == site.PHPVersion {
			ve.phpDropdown.Selected = i
			break
		}
	}
	for i, v := range mysqlVersions {
		if v == site.MySQLVersion {
			ve.mysqlDropdown.Selected = i
			break
		}
	}
	for i, v := range redisVersions {
		if v == site.RedisVersion {
			ve.redisDropdown.Selected = i
			break
		}
	}
}

func (ve *VersionEditor) handleClicks(gtx layout.Context, site *types.Site) {
	if ve.saveBtn.Clicked(gtx) {
		siteID := site.ID
		phpVer := phpVersions[ve.phpDropdown.Selected]
		mysqlVer := mysqlVersions[ve.mysqlDropdown.Selected]
		redisVer := redisVersions[ve.redisDropdown.Selected]

		go func() {
			if err := ve.sm.UpdateSiteVersions(siteID, phpVer, mysqlVer, redisVer); err != nil {
				ve.state.ShowError("Failed to update versions: " + err.Error())
				return
			}
			ve.toasts.ShowSuccess("Versions updated — start the site to apply.")
		}()
	}
}
