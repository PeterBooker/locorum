package sites

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/PeterBooker/locorum/internal/dbengine"
	"github.com/PeterBooker/locorum/internal/sites/sitesteps"
)

// MigrateEngineOptions describes the destination engine + version of an
// engine migration. Empty fields are interpreted as "leave unchanged".
type MigrateEngineOptions struct {
	// TargetEngine is the destination engine kind ("mysql" or
	// "mariadb"). Pass empty to keep the current engine but change
	// version (used for unsafe in-place version transitions).
	TargetEngine string

	// TargetVersion is the destination version tag. Required.
	TargetVersion string

	// SkipSnapshot skips the safety snapshot. Hostile mode — only
	// reachable from automation that has already taken its own backup.
	SkipSnapshot bool
}

// MigrateEngine swaps a stopped (or running) site to a new database
// engine and/or version. Always destructive at the volume level, which
// is why it's a separate path from UpdateSiteVersions:
//
//  1. Take a labelled snapshot of the current data ("pre_migrate").
//  2. Stop the site (idempotent).
//  3. Purge the database volume.
//  4. Update Site.DBEngine / Site.DBVersion.
//  5. Start the site fresh (StartSite writes the new marker).
//  6. Restore the snapshot with AllowEngineMismatch=true so the new
//     engine accepts a dump originally produced by the old one.
//
// Failure between steps 4 and 6 leaves the site in a known-broken state
// — the SQL row points at the new engine, the volume is empty, the
// snapshot is on disk. The user can re-trigger MigrateEngine from the
// snapshot to finish the migration. We deliberately don't try to roll
// back the SQL update: re-rolling forward is the safer recovery.
func (sm *SiteManager) MigrateEngine(ctx context.Context, siteID string, opts MigrateEngineOptions) error {
	if opts.TargetVersion == "" {
		return errors.New("MigrateEngine: TargetVersion is required")
	}
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return fmt.Errorf("site %q not found", siteID)
	}

	targetEngine := opts.TargetEngine
	if targetEngine == "" {
		targetEngine = site.DBEngine
	}
	if !dbengine.IsValid(dbengine.Kind(targetEngine)) {
		return fmt.Errorf("unknown target engine %q", targetEngine)
	}

	noChange := targetEngine == site.DBEngine && opts.TargetVersion == site.DBVersion
	if noChange {
		return nil
	}

	// Snapshot first, before we touch anything. Snapshot requires the
	// site to be running.
	wasStarted := site.Started
	if !site.Started {
		if err := sm.StartSite(ctx, siteID); err != nil {
			return fmt.Errorf("migrate: pre-snapshot start: %w", err)
		}
		// Refresh state.
		site, err = sm.st.GetSite(siteID)
		if err != nil {
			return err
		}
	}

	mu := sm.siteMutex(siteID)

	// Snapshot phase — hold the per-site mutex across the snapshot so a
	// concurrent StartSite can't race us. Released before StopSite which
	// reacquires.
	var snapshotPath string
	if !opts.SkipSnapshot {
		mu.Lock()
		path, err := sm.snapshotLocked(ctx, site, "pre_migrate")
		mu.Unlock()
		if err != nil {
			return fmt.Errorf("migrate: pre-snapshot failed: %w (pass SkipSnapshot to override)", err)
		}
		snapshotPath = path
		slog.Info("migrate: pre-snapshot saved", "path", snapshotPath)
	}

	if err := sm.StopSite(ctx, siteID); err != nil {
		return fmt.Errorf("migrate: stop: %w", err)
	}

	// Purge volume + flip SQL row in one critical section.
	mu.Lock()
	if err := (&sitesteps.PurgeVolumeStep{Engine: sm.d, Site: site}).Apply(ctx); err != nil {
		mu.Unlock()
		return fmt.Errorf("migrate: purge volume: %w", err)
	}
	// From this point a failure leaves the user with a re-runnable
	// migration — either by re-invoking MigrateEngine or by manually
	// restoring snapshotPath.
	site.DBEngine = targetEngine
	site.DBVersion = opts.TargetVersion
	if site.DBEngine == string(dbengine.MySQL) {
		site.MySQLVersion = opts.TargetVersion
	} else {
		site.MySQLVersion = "" // legacy mirror only meaningful for MySQL
	}
	if _, err := sm.st.UpdateSite(site); err != nil {
		mu.Unlock()
		return fmt.Errorf("migrate: update site row: %w", err)
	}
	mu.Unlock()

	if err := sm.StartSite(ctx, siteID); err != nil {
		return fmt.Errorf("migrate: start on new engine: %w (snapshot at %s)", err, snapshotPath)
	}

	// Restore. Re-fetch site so DBPassword etc. is fresh.
	site, err = sm.st.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("migrate: re-fetch: %w", err)
	}
	if snapshotPath != "" {
		if err := sm.RestoreSnapshot(ctx, siteID, snapshotPath, RestoreSnapshotOptions{AllowEngineMismatch: true}); err != nil {
			return fmt.Errorf("migrate: restore: %w (snapshot at %s)", err, snapshotPath)
		}
	}

	// Restore caller's wasStarted state.
	if !wasStarted {
		if err := sm.StopSite(ctx, siteID); err != nil {
			slog.Warn("migrate: failed to restore stopped state", "err", err.Error())
		}
	}

	slog.Info("migrate: complete",
		"site", site.Slug,
		"db_engine", site.DBEngine,
		"db_version", site.DBVersion,
	)
	return nil
}
