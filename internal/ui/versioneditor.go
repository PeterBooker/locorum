package ui

import (
	"context"
	"errors"
	"strings"

	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/PeterBooker/locorum/internal/dbengine"
	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/types"
)

// VersionEditor shows read-only version info when a site is running,
// and editable dropdowns when it is stopped. The DB row understands both
// the engine kind and the version so users can switch between MySQL and
// MariaDB; engine swaps and unsafe version transitions route through the
// migrate-via-snapshot flow rather than landing in-place.
type VersionEditor struct {
	state  *UIState
	sm     *sites.SiteManager
	toasts *Notifications

	phpDropdown       *Dropdown
	dbEngineDropdown  *Dropdown
	dbVersionDropdown *Dropdown
	redisDropdown     *Dropdown
	saveBtn           widget.Clickable

	dbVersions []string

	// Track which site we last synced dropdowns for, and the baseline
	// values used to compute the dirty flag.
	lastSiteID                                                string
	initialPHP, initialEngine, initialDBVersion, initialRedis string
}

func NewVersionEditor(state *UIState, sm *sites.SiteManager, toasts *Notifications) *VersionEditor {
	defaultVersions := dbVersionsFor(dbEngineKinds[0])
	return &VersionEditor{
		state:             state,
		sm:                sm,
		toasts:            toasts,
		phpDropdown:       NewDropdown(phpVersions),
		dbEngineDropdown:  NewDropdown(dbEngineOptions),
		dbVersionDropdown: NewDropdown(defaultVersions),
		dbVersions:        defaultVersions,
		redisDropdown:     NewDropdown(redisVersions),
	}
}

func (ve *VersionEditor) Layout(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	if site.Started {
		return KVRows(gtx, th, []KV{
			{"PHP", site.PHPVersion},
			{"DB Engine", strings.Title(site.DBEngine)},
			{"DB Version", site.DBVersion},
			{"Redis", site.RedisVersion},
		})
	}

	// Sync dropdown selections to current site values when site changes.
	if ve.lastSiteID != site.ID {
		ve.lastSiteID = site.ID
		ve.syncDropdowns(site)
	}

	// Keep the version dropdown in step with the engine dropdown.
	wantVersions := dbVersionsFor(dbEngineKinds[ve.dbEngineDropdown.Selected])
	if !slicesEqual(wantVersions, ve.dbVersions) {
		ve.dbVersions = wantVersions
		ve.dbVersionDropdown = NewDropdown(wantVersions)
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
					return ve.dbEngineDropdown.Layout(gtx, th, "Database Engine")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return ve.dbVersionDropdown.Layout(gtx, th, "Database Version")
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
		string(dbEngineKinds[ve.dbEngineDropdown.Selected]) != ve.initialEngine ||
		ve.dbVersions[ve.dbVersionDropdown.Selected] != ve.initialDBVersion ||
		redisVersions[ve.redisDropdown.Selected] != ve.initialRedis
}

func (ve *VersionEditor) syncDropdowns(site *types.Site) {
	for i, v := range phpVersions {
		if v == site.PHPVersion {
			ve.phpDropdown.Selected = i
			break
		}
	}
	for i, k := range dbEngineKinds {
		if string(k) == site.DBEngine {
			ve.dbEngineDropdown.Selected = i
			break
		}
	}
	ve.dbVersions = dbVersionsFor(dbEngineKinds[ve.dbEngineDropdown.Selected])
	ve.dbVersionDropdown = NewDropdown(ve.dbVersions)
	for i, v := range ve.dbVersions {
		if v == site.DBVersion {
			ve.dbVersionDropdown.Selected = i
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
	ve.initialEngine = site.DBEngine
	ve.initialDBVersion = site.DBVersion
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
		newEngine := string(dbEngineKinds[ve.dbEngineDropdown.Selected])
		newDBVer := ve.dbVersions[ve.dbVersionDropdown.Selected]
		redisVer := redisVersions[ve.redisDropdown.Selected]

		// Capture snapshot baselines optimistically — if the change is
		// safe in-place, this matches what UpdateSiteVersions writes.
		ve.initialPHP = phpVer
		ve.initialEngine = newEngine
		ve.initialDBVersion = newDBVer
		ve.initialRedis = redisVer

		// Engine swap or unsafe version transition → migrate flow.
		// Same engine + safe version → in-place update.
		needsMigrate := newEngine != site.DBEngine
		if !needsMigrate && newDBVer != site.DBVersion {
			if eng, err := dbengine.For(dbengine.Kind(site.DBEngine)); err == nil {
				if !eng.UpgradeAllowed(site.DBVersion, newDBVer) {
					needsMigrate = true
				}
			}
		}

		go func() {
			if needsMigrate {
				if err := ve.sm.MigrateEngine(context.Background(), siteID, sites.MigrateEngineOptions{
					TargetEngine:  newEngine,
					TargetVersion: newDBVer,
				}); err != nil {
					ve.state.ShowError("Failed to migrate engine: " + err.Error())
					return
				}
				// Apply the rest of the changes in-place after migrate
				// (same engine, possibly different PHP/Redis).
				if err := ve.sm.UpdateSiteVersionsWithEngine(context.Background(), siteID, sites.VersionsChange{
					PHPVersion:   phpVer,
					RedisVersion: redisVer,
				}); err != nil {
					ve.state.ShowError("Failed to update PHP/Redis after migrate: " + err.Error())
					return
				}
				ve.toasts.ShowSuccess("Database engine migrated; start the site to use the new version.")
				return
			}
			if err := ve.sm.UpdateSiteVersionsWithEngine(context.Background(), siteID, sites.VersionsChange{
				PHPVersion:   phpVer,
				DBVersion:    newDBVer,
				RedisVersion: redisVer,
			}); err != nil {
				if errors.Is(err, sites.ErrUnsafeVersionTransition) {
					ve.state.ShowError("That version change requires the migrate flow — try again to confirm.")
					return
				}
				ve.state.ShowError("Failed to update versions: " + err.Error())
				return
			}
			ve.toasts.ShowSuccess("Versions updated — start the site to apply.")
		}()
	}
}
