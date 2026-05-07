package ui

import (
	"context"
	"errors"
	"image"
	"strings"
	"time"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/sqweek/dialog"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/types"
	"github.com/PeterBooker/locorum/internal/utils"
)

const (
	tabOverview  = 0
	tabDatabase  = 1
	tabUtilities = 2
	tabHooks     = 3
	tabActivity  = 4
	tabMail      = 5
	tabLogs      = 6
	tabProfiling = 7
)

var tabLabels = []string{"Overview", "Database", "Utilities", "Hooks", "Activity", "Mail", "Logs", "Profiling"}

// SiteDetail is column 3: a header bar (avatar/name/domain/status pill +
// action buttons), a tab strip, and the active tab's body content. Hosts
// the per-tab sub-components (DB credentials, logs, WP-CLI, hooks, etc).
type SiteDetail struct {
	state  *UIState
	sm     *sites.SiteManager
	toasts *Notifications

	list widget.List

	// Tabs
	activeTab int
	tabClicks [8]widget.Clickable

	// Header-bar actions (running-only unless noted)
	startBtn     widget.Clickable
	stopBtn      widget.Clickable
	shellBtn     widget.Clickable
	filesBtn     widget.Clickable
	databaseBtn  widget.Clickable
	openAdminBtn widget.Clickable
	openSiteBtn  widget.Clickable

	// Secondary actions row
	cloneBtn  widget.Clickable
	exportBtn widget.Clickable
	deleteBtn widget.Clickable

	// Overview tab interactive widgets
	openURLBtn      widget.Clickable
	openFilesDirBtn widget.Clickable
	publicDirEditor widget.Editor
	savePublicDir   widget.Clickable
	lastSiteID      string

	// Mail tab
	mailOpenBtn widget.Clickable

	// Overview Activity panel
	activityViewAllBtn widget.Clickable

	// Environment panel settings cog (placeholder — no-op for now)
	envSettingsBtn widget.Clickable

	// Sub-components
	dbCreds        *DBCredentials
	snapshotsPanel *SnapshotsPanel
	logViewer      *LogViewer
	wpcliPanel     *WPCLIPanel
	versionEditor  *VersionEditor
	linkChecker    *LinkChecker
	hooksPanel     *HooksPanel
	activityTab    *ActivityTab
	profilingPanel *ProfilingPanel

	// recentActivityLoadedFor records the last site for which we kicked
	// off a recent-activity load. Stops Layout() from spawning a fresh
	// goroutine on every frame for the same site.
	recentActivityLoadedFor string

	// describeCache caches sm.Describe results so Layout never blocks on
	// SQLite/Docker. Refreshed on site mount, OnSiteUpdated, plan
	// completion, and DB-credentials card open.
	describeCache *DescribeCache
	copyJSONBtn   widget.Clickable

	// Docker init error
	retryInitBtn widget.Clickable
}

func NewSiteDetail(state *UIState, sm *sites.SiteManager, toasts *Notifications) *SiteDetail {
	sd := &SiteDetail{
		state:  state,
		sm:     sm,
		toasts: toasts,
		dbCreds: func() *DBCredentials {
			c := NewDBCredentials()
			c.Bind(sm, state, toasts)
			return c
		}(),
		snapshotsPanel: NewSnapshotsPanel(state, sm, toasts),
		logViewer:      NewLogViewer(state, sm),
		wpcliPanel:     NewWPCLIPanel(state, sm),
		versionEditor:  NewVersionEditor(state, sm, toasts),
		linkChecker:    NewLinkChecker(state, sm),
		hooksPanel:     NewHooksPanel(state, sm, sm, toasts),
		activityTab:    NewActivityTab(state, sm),
		profilingPanel: NewProfilingPanel(state, sm, toasts),
	}
	sd.describeCache = NewDescribeCache(sm, state)
	sd.list.List.Axis = layout.Vertical
	sd.publicDirEditor.SingleLine = true
	return sd
}

// DescribeCache exposes the per-site description cache so other panels
// (DB credentials' "include host port" toggle) can request a refresh.
func (sd *SiteDetail) DescribeCache() *DescribeCache { return sd.describeCache }

// HooksPanel exposes the hooks tab so the root UI can render its modal
// overlays above the main chrome.
func (sd *SiteDetail) HooksPanel() *HooksPanel { return sd.hooksPanel }

// HandleUserInteractions processes header-bar clicks, tab selection, and
// per-tab sub-component interactions. Called by the root UI before Layout
// each frame.
func (sd *SiteDetail) HandleUserInteractions(gtx layout.Context) {
	if sd.state.GetInitError() != "" {
		if sd.retryInitBtn.Clicked(gtx) {
			if retry := sd.state.GetRetryInit(); retry != nil {
				go retry()
			}
		}
		return
	}

	site := sd.state.SelectedSite()
	if site == nil {
		return
	}

	for i := range sd.tabClicks {
		if sd.tabClicks[i].Clicked(gtx) {
			sd.activeTab = i
		}
	}

	// Warm the describe cache the first time we observe a site (or when
	// the user switches to a different one). The describe cache itself
	// debounces to one in-flight load per siteID.
	sd.describeCache.Get(site.ID, false)

	sd.handleHeaderActions(gtx, site)

	// Per-tab interactions.
	switch sd.activeTab {
	case tabDatabase:
		// Opening the Database tab implies the user cares about the
		// published host port — re-warm the describe cache with that
		// flag so the next "Copy as JSON" click ships HostPort.
		sd.describeCache.Get(site.ID, true)
		sd.dbCreds.HandleUserInteractions(gtx, site)
		sd.snapshotsPanel.HandleUserInteractions(gtx, site)
	case tabUtilities:
		if site.Started {
			sd.wpcliPanel.HandleUserInteractions(gtx, site.ID)
			sd.linkChecker.HandleUserInteractions(gtx, site.ID)
		}
	case tabHooks:
		sd.hooksPanel.HandleUserInteractions(gtx, site.ID)
	case tabActivity:
		sd.activityTab.HandleUserInteractions(gtx)
	case tabLogs:
		if site.Started {
			sd.logViewer.HandleUserInteractions(gtx, site.ID)
		}
	case tabProfiling:
		sd.profilingPanel.HandleUserInteractions(gtx, site)
	default: // tabOverview
		sd.handleOverviewClicks(gtx, site)
		sd.versionEditor.HandleUserInteractions(gtx, site)
		if sd.activityViewAllBtn.Clicked(gtx) {
			sd.activeTab = tabActivity
		}
	}
}

func (sd *SiteDetail) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	if msg := sd.state.GetInitError(); msg != "" {
		return sd.layoutInitError(gtx, th, msg)
	}

	site := sd.state.SelectedSite()
	if site == nil {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, "Select a site from the sidebar")
			lbl.Color = th.Color.Fg3
			lbl.TextSize = th.Sizes.Section
			return lbl.Layout(gtx)
		})
	}

	return FillBackground(gtx, th.Color.Bg, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return sd.layoutHeaderBar(gtx, th, site)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return sd.layoutTabStrip(gtx, th)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return material.List(th.Theme, &sd.list).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
					return layout.UniformInset(unit.Dp(20)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return sd.layoutTabContent(gtx, th, site)
					})
				})
			}),
		)
	})
}

// ─── Header bar ─────────────────────────────────────────────────────────────

func (sd *SiteDetail) layoutHeaderBar(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	return FillBackground(gtx, th.Color.Bg1, func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{}.Layout(gtx,
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{
					Top: unit.Dp(14), Bottom: unit.Dp(14),
					Left: th.Spacing.LG, Right: th.Spacing.LG,
				}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return SiteAvatar(gtx, th, site.Name, unit.Dp(30))
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										lbl := material.Body2(th.Theme, TruncateWords(site.Name, 50))
										lbl.Color = th.Color.Fg
										lbl.TextSize = th.Sizes.Header
										lbl.Font.Weight = font.SemiBold
										lbl.MaxLines = 1
										lbl.Truncator = "…"
										return lbl.Layout(gtx)
									}),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										lbl := material.Body2(th.Theme, site.Domain)
										lbl.Color = th.Color.Fg3
										lbl.TextSize = th.Sizes.Mono
										lbl.Font = MonoFont
										lbl.MaxLines = 1
										lbl.Truncator = "…"
										return lbl.Layout(gtx)
									}),
								)
							})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								key, _ := StatusForSite(site.Started)
								return StatusPill(gtx, th, key, site.Started)
							})
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Min.X}}
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return sd.layoutHeaderActions(gtx, th, site)
						}),
					)
				})
			}),
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				return EdgeLine(gtx, th.Color.Line, "bottom")
			}),
		)
	})
}

func (sd *SiteDetail) layoutHeaderActions(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	toggling := sd.state.IsSiteToggling(site.ID)

	if !site.Started {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return iconLabelButton(gtx, th, &sd.filesBtn, IconFolder, "Files", btnSecondary)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return th.Small(gtx, &sd.copyJSONBtn, "Copy as JSON")
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if toggling {
					return Loader(gtx, th, th.Dims.LoaderSizeSM)
				}
				return startButton(gtx, th, &sd.startBtn)
			}),
		)
	}

	// Started layout: utility buttons followed by a primary toggle (Stop)
	// at the rightmost slot — same position Start occupies when stopped, so
	// clicking Start does NOT shift the toggle to a different part of the
	// chrome. Stop replaces Start in place; everything else flows to its
	// left.
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return iconLabelButton(gtx, th, &sd.shellBtn, IconTerminal, "Shell", btnSecondary)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return iconLabelButton(gtx, th, &sd.filesBtn, IconFolder, "Files", btnSecondary)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return iconLabelButton(gtx, th, &sd.databaseBtn, IconDatabase, "Database", btnSecondary)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return iconLabelButton(gtx, th, &sd.openAdminBtn, IconEye, "Open admin", btnSecondary)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return iconLabelButton(gtx, th, &sd.openSiteBtn, IconEye, "Open site", btnSecondary)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return th.Small(gtx, &sd.copyJSONBtn, "Copy as JSON")
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if toggling {
				return Loader(gtx, th, th.Dims.LoaderSizeSM)
			}
			return stopButton(gtx, th, &sd.stopBtn)
		}),
	)
}

// handleHeaderActions wires header-bar buttons to backend operations.
func (sd *SiteDetail) handleHeaderActions(gtx layout.Context, site *types.Site) {
	if sd.startBtn.Clicked(gtx) && !site.Started {
		id := site.ID
		sd.state.SetSiteToggling(id, true)
		go func() {
			err := sd.sm.StartSite(context.Background(), id)
			sd.state.SetSiteToggling(id, false)
			SurfaceError(sd.state, "Failed to start site", err, nil)
		}()
	}
	if sd.stopBtn.Clicked(gtx) && site.Started {
		id := site.ID
		sd.state.SetSiteToggling(id, true)
		go func() {
			err := sd.sm.StopSite(context.Background(), id)
			sd.state.SetSiteToggling(id, false)
			SurfaceError(sd.state, "Failed to stop site", err, nil)
		}()
	}
	if sd.shellBtn.Clicked(gtx) && site.Started {
		id := site.ID
		slug := site.Slug
		go func() {
			err := sd.sm.OpenSiteShell(id)
			if err == nil {
				return
			}
			if errors.Is(err, utils.ErrNoTerminal) {
				// No terminal launcher detected on PATH. Surface the
				// command so the user can copy/paste it into the
				// terminal of their choice.
				cmd := "docker exec -it " + docker.SiteContainerName(slug, "php") + " /bin/bash"
				sd.toasts.ShowInfo("No terminal found — run: " + cmd)
				return
			}
			SurfaceError(sd.state, "Failed to open shell", err, func() {
				_ = sd.sm.StartSite(context.Background(), id)
			})
		}()
	}
	if sd.filesBtn.Clicked(gtx) {
		id := site.ID
		go func() {
			if err := sd.sm.OpenSiteFilesDir(id); err != nil {
				sd.state.ShowError("Failed to open files: " + err.Error())
			}
		}()
	}
	if sd.databaseBtn.Clicked(gtx) && site.Started {
		go func() {
			// Adminer runs at db.localhost in the global router.
			if err := openInBrowser("https://db.localhost"); err != nil {
				sd.state.ShowError("Failed to open Database UI: " + err.Error())
			}
		}()
	}
	if sd.openAdminBtn.Clicked(gtx) && site.Started {
		id := site.ID
		go func() {
			err := sd.sm.OpenAdminLogin(id)
			SurfaceError(sd.state, "Failed to open admin", err, func() {
				_ = sd.sm.StartSite(context.Background(), id)
			})
		}()
	}
	if sd.openSiteBtn.Clicked(gtx) && site.Started {
		id := site.ID
		go func() {
			if err := sd.sm.OpenSiteURL(id); err != nil {
				sd.state.ShowError("Failed to open site: " + err.Error())
			}
		}()
	}

	if sd.cloneBtn.Clicked(gtx) {
		sd.state.ShowCloneModal(site.ID, site.Name)
	}
	if sd.copyJSONBtn.Clicked(gtx) {
		// Force a refresh to grab the latest description, including the
		// host port if that toggle ever got opened on this site. The
		// cached value is what we copy — async refresh fills the gap on
		// the next click if cache was empty.
		body := sd.describeCache.AsJSON(site.ID)
		if body == "" {
			sd.toasts.ShowInfo("Site description still loading — try again in a moment")
			sd.describeCache.Get(site.ID, false)
		} else {
			CopyToClipboard(gtx, body)
			sd.toasts.ShowSuccess("Site description copied as JSON")
		}
	}
	if sd.exportBtn.Clicked(gtx) && site.Started {
		id := site.ID
		sd.state.SetExportLoading(true)
		go func() {
			defer sd.state.SetExportLoading(false)
			dest, err := dialog.File().Filter("Tar archive", "tar.gz").Title("Export site").Save()
			if err != nil {
				if err.Error() != "Cancelled" {
					sd.state.ShowError("Export cancelled: " + err.Error())
				}
				return
			}
			if !strings.HasSuffix(dest, ".tar.gz") {
				dest += ".tar.gz"
			}
			if err := sd.sm.ExportSite(context.Background(), id, dest); err != nil {
				sd.state.ShowError("Export failed: " + err.Error())
			}
		}()
	}
	if sd.deleteBtn.Clicked(gtx) {
		sd.state.ShowDeleteConfirm(site.ID, site.Name)
	}
}

// ─── Tab strip ──────────────────────────────────────────────────────────────

func (sd *SiteDetail) layoutTabStrip(gtx layout.Context, th *Theme) layout.Dimensions {
	return layout.Stack{}.Layout(gtx,
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Spacing.LG, Right: th.Spacing.LG}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				children := make([]layout.FlexChild, len(tabLabels))
				for i, lbl := range tabLabels {
					i, lbl := i, lbl
					children[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return sd.layoutTab(gtx, th, i, lbl)
					})
				}
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
			})
		}),
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			return EdgeLine(gtx, th.Color.Line, "bottom")
		}),
	)
}

func (sd *SiteDetail) layoutTab(gtx layout.Context, th *Theme, idx int, label string) layout.Dimensions {
	active := idx == sd.activeTab
	textCol := th.Color.Fg3
	if active {
		textCol = th.Color.Fg
	}
	weight := font.Normal
	if active {
		weight = font.Medium
	}
	return material.Clickable(gtx, &sd.tabClicks[idx], func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{
					Top: unit.Dp(11), Bottom: unit.Dp(11),
					Left: unit.Dp(12), Right: unit.Dp(12),
				}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th.Theme, label)
					l.Color = textCol
					l.TextSize = th.Sizes.Tab
					l.Font.Weight = weight
					return l.Layout(gtx)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !active {
					return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Min.X, Y: gtx.Dp(unit.Dp(2))}}
				}
				size := image.Point{X: gtx.Constraints.Min.X, Y: gtx.Dp(unit.Dp(2))}
				defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
				paint.Fill(gtx.Ops, th.Color.Accent)
				return layout.Dimensions{Size: size}
			}),
		)
	})
}

// ─── Tab content ────────────────────────────────────────────────────────────

func (sd *SiteDetail) layoutTabContent(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	switch sd.activeTab {
	case tabDatabase:
		return sd.layoutDatabaseTab(gtx, th, site)
	case tabUtilities:
		return sd.layoutUtilitiesTab(gtx, th, site)
	case tabHooks:
		return sd.hooksPanel.Layout(gtx, th, site.ID)
	case tabActivity:
		return sd.activityTab.Layout(gtx, th, site.ID)
	case tabMail:
		return sd.layoutMailTab(gtx, th)
	case tabLogs:
		return sd.layoutLogsTab(gtx, th, site)
	case tabProfiling:
		return sd.profilingPanel.Layout(gtx, th, site)
	default:
		return sd.layoutOverviewTab(gtx, th, site)
	}
}

// layoutOverviewTab renders the environment grid + activity feed + secondary
// actions row + version editor.
func (sd *SiteDetail) layoutOverviewTab(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	if sd.lastSiteID != site.ID {
		sd.lastSiteID = site.ID
		sd.publicDirEditor.SetText(site.PublicDir)
	}
	sd.ensureRecentActivityLoaded(site.ID)

	rows := sd.state.ActivityRecent(site.ID)
	entries := overviewActivityEntries(rows, time.Now())

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sd.layoutEnvPanel(gtx, th, site)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return activityPanel(gtx, th, entries, &sd.activityViewAllBtn)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sd.layoutSecondaryActions(gtx, th, site)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Spacer{Height: th.Spacing.MD}.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return panel(gtx, th, "Versions", func(gtx layout.Context) layout.Dimensions {
				return sd.versionEditor.Layout(gtx, th, site)
			})
		}),
	)
}

// ensureRecentActivityLoaded kicks off a background load of the recent
// activity rows the first time a given site becomes the overview target.
// The load is single-shot per site-switch — the OnActivityAppended
// callback keeps the cache fresh after that.
func (sd *SiteDetail) ensureRecentActivityLoaded(siteID string) {
	if siteID == "" || sd.recentActivityLoadedFor == siteID {
		return
	}
	sd.recentActivityLoadedFor = siteID
	go func() {
		evs, err := sd.sm.RecentActivity(siteID)
		if err != nil {
			return
		}
		sd.state.SetActivityRecent(siteID, evs)
	}()
}

// overviewActivityEntries converts cached events into the row primitives
// the activityPanel renders. Empty input → empty output (the panel shows
// its own "No recent activity." copy).
func overviewActivityEntries(rows []storage.ActivityEvent, now time.Time) []activityEntry {
	if len(rows) == 0 {
		return nil
	}
	out := make([]activityEntry, len(rows))
	for i, ev := range rows {
		out[i] = activityEntry{
			Time:    FormatActivityTime(ev.Time, now),
			Message: ev.Message,
			Kind:    rowKindForStatus(ev.Status),
		}
	}
	return out
}

// layoutEnvPanel renders the 4-column environment grid inside a panel card.
// Editable fields (public dir) sit below the grid.
func (sd *SiteDetail) layoutEnvPanel(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	url := "https://" + site.Domain
	multisite := site.Multisite
	if multisite == "" {
		multisite = "single"
	}
	dbLabel := strings.ToTitle(site.DBEngine[:1]) + site.DBEngine[1:]
	if dbLabel == "" {
		dbLabel = "Database"
	}
	pairs := []envCell{
		{"PHP", site.PHPVersion},
		{dbLabel, site.DBVersion},
		{"Redis", site.RedisVersion},
		{"Web server", site.WebServer},
		{"URL", url},
		{"Public Dir", site.PublicDir},
		{"Files Dir", site.FilesDir},
		{"Multisite", multisite},
	}
	action := func(gtx layout.Context) layout.Dimensions {
		return material.Clickable(gtx, &sd.envSettingsBtn, func(gtx layout.Context) layout.Dimensions {
			return RoundedFill(gtx, th.Color.Bg1, th.Radii.R2, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(4)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return IconCog(gtx, th, unit.Dp(14), th.Color.Fg3)
				})
			})
		})
	}
	return panelWithAction(gtx, th, "Environment", action, func(gtx layout.Context) layout.Dimensions {
		return envGrid(gtx, th, pairs, 4)
	})
}

func (sd *SiteDetail) layoutSecondaryActions(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	exporting := sd.state.IsExportLoading()
	// Stop lives in the header bar at the same slot Start occupies, so the
	// primary toggle does not visually relocate when the user clicks Start.
	// Secondary actions here are file-level workflows (clone, export,
	// delete) and never include the lifecycle toggle.
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return iconLabelButton(gtx, th, &sd.cloneBtn, nil, "Clone", btnSecondary)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !site.Started {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				if exporting {
					return Loader(gtx, th, th.Dims.LoaderSizeSM)
				}
				return iconLabelButton(gtx, th, &sd.exportBtn, nil, "Export", btnSecondary)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Min.X}}
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return iconLabelButton(gtx, th, &sd.deleteBtn, nil, "Delete", btnDanger)
		}),
	)
}

// handleOverviewClicks processes URL/files-dir/save-public-dir clicks on
// the Overview tab.
func (sd *SiteDetail) handleOverviewClicks(gtx layout.Context, site *types.Site) {
	if sd.savePublicDir.Clicked(gtx) && !site.Started {
		id := site.ID
		newDir := sd.publicDirEditor.Text()
		if newDir == site.PublicDir {
			return
		}
		go func() {
			if err := sd.sm.UpdatePublicDir(context.Background(), id, newDir); err != nil {
				sd.state.ShowError("Failed to update public dir: " + err.Error())
			}
		}()
	}
}

// ─── Other tabs ─────────────────────────────────────────────────────────────

func (sd *SiteDetail) layoutDatabaseTab(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return panel(gtx, th, "Credentials", func(gtx layout.Context) layout.Dimensions {
				return sd.dbCreds.Layout(gtx, th, site)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Spacer{Height: th.Spacing.MD}.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return panel(gtx, th, "Snapshots", func(gtx layout.Context) layout.Dimensions {
				return sd.snapshotsPanel.Layout(gtx, th, site)
			})
		}),
	)
}

func (sd *SiteDetail) layoutUtilitiesTab(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	if !site.Started {
		return tabPlaceholder(gtx, th, "Start the site to access utilities.")
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return panel(gtx, th, "WP-CLI", func(gtx layout.Context) layout.Dimensions {
				return sd.wpcliPanel.Layout(gtx, th, site.ID)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Spacer{Height: th.Spacing.MD}.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return panel(gtx, th, "Link checker", func(gtx layout.Context) layout.Dimensions {
				return sd.linkChecker.Layout(gtx, th, site.ID)
			})
		}),
	)
}

func (sd *SiteDetail) layoutLogsTab(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	if !site.Started {
		return tabPlaceholder(gtx, th, "Start the site to view container logs.")
	}
	return panel(gtx, th, "Container logs", func(gtx layout.Context) layout.Dimensions {
		return sd.logViewer.Layout(gtx, th, site.ID)
	})
}

func (sd *SiteDetail) layoutMailTab(gtx layout.Context, th *Theme) layout.Dimensions {
	if sd.mailOpenBtn.Clicked(gtx) {
		go func() {
			if err := openInBrowser("https://mail.localhost"); err != nil {
				sd.state.ShowError("Failed to open mail UI: " + err.Error())
			}
		}()
	}
	return panel(gtx, th, "Mail", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, "Locorum captures all outgoing mail in MailHog. The catch-all UI is reachable at https://mail.localhost while the platform is running.")
				lbl.Color = th.Color.Fg2
				lbl.TextSize = th.Sizes.Body
				return lbl.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Spacer{Height: th.Spacing.SM}.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return iconLabelButton(gtx, th, &sd.mailOpenBtn, IconMail, "Open MailHog", btnPrimary)
			}),
		)
	})
}

func (sd *SiteDetail) layoutInitError(gtx layout.Context, th *Theme, errMsg string) layout.Dimensions {
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, "Docker Unavailable")
				lbl.Color = th.Color.Err
				lbl.TextSize = th.Sizes.Section
				lbl.Font.Weight = font.SemiBold
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, errMsg)
				lbl.Color = th.Color.Fg2
				lbl.TextSize = th.Sizes.Body
				return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return PrimaryButton(gtx, th, &sd.retryInitBtn, "Retry")
			}),
		)
	})
}
