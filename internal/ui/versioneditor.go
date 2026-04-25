package ui

import (
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/types"
)

// VersionEditor shows read-only version info when a site is running,
// and editable dropdowns when it is stopped.
type VersionEditor struct {
	state  *UIState
	sm     *sites.SiteManager
	toasts *Notifications

	phpDropdown   *Dropdown
	mysqlDropdown *Dropdown
	redisDropdown *Dropdown
	saveBtn       widget.Clickable

	// Track which site we last synced dropdowns for, and the baseline values
	// used to compute the dirty flag.
	lastSiteID                             string
	initialPHP, initialMySQL, initialRedis string
}

func NewVersionEditor(state *UIState, sm *sites.SiteManager, toasts *Notifications) *VersionEditor {
	return &VersionEditor{
		state:         state,
		sm:            sm,
		toasts:        toasts,
		phpDropdown:   NewDropdown(phpVersions),
		mysqlDropdown: NewDropdown(mysqlVersions),
		redisDropdown: NewDropdown(redisVersions),
	}
}

func (ve *VersionEditor) Layout(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	if site.Started {
		return layoutVersionsSection(gtx, th, site)
	}

	// Sync dropdown selections to current site values when site changes.
	if ve.lastSiteID != site.ID {
		ve.lastSiteID = site.ID
		ve.syncDropdowns(site)
	}

	dirty := ve.isDirty()
	sectionFn := Section
	title := "Versions (editable while stopped)"
	if dirty {
		title = "● Versions — unsaved changes"
		sectionFn = SectionDirty
	}

	return sectionFn(gtx, th, title, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return ve.phpDropdown.Layout(gtx, th, "PHP Version")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return ve.mysqlDropdown.Layout(gtx, th, "MySQL Version")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return ve.redisDropdown.Layout(gtx, th, "Redis Version")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return th.PrimaryGated(gtx, &ve.saveBtn, "Save Changes", dirty)
			}),
		)
	})
}

// isDirty reports whether any dropdown selection differs from the baseline
// captured when the site was last synced.
func (ve *VersionEditor) isDirty() bool {
	return phpVersions[ve.phpDropdown.Selected] != ve.initialPHP ||
		mysqlVersions[ve.mysqlDropdown.Selected] != ve.initialMySQL ||
		redisVersions[ve.redisDropdown.Selected] != ve.initialRedis
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
	ve.initialPHP = site.PHPVersion
	ve.initialMySQL = site.MySQLVersion
	ve.initialRedis = site.RedisVersion
}

// HandleUserInteractions processes the Save button click on the version editor.
// Only meaningful when the site is stopped; no-op otherwise.
func (ve *VersionEditor) HandleUserInteractions(gtx layout.Context, site *types.Site) {
	if site.Started {
		return
	}
	if ve.saveBtn.Clicked(gtx) && ve.isDirty() {
		siteID := site.ID
		phpVer := phpVersions[ve.phpDropdown.Selected]
		mysqlVer := mysqlVersions[ve.mysqlDropdown.Selected]
		redisVer := redisVersions[ve.redisDropdown.Selected]

		ve.initialPHP = phpVer
		ve.initialMySQL = mysqlVer
		ve.initialRedis = redisVer

		go func() {
			if err := ve.sm.UpdateSiteVersions(siteID, phpVer, mysqlVer, redisVer); err != nil {
				ve.state.ShowError("Failed to update versions: " + err.Error())
				return
			}
			ve.toasts.ShowSuccess("Versions updated — start the site to apply.")
		}()
	}
}
