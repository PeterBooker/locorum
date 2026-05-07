package ui

import (
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/app"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/health"
	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/orch"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/types"
)

// ActivityRecentMax caps the per-site row count cached for the overview
// panel. Mirrors sites.activityRecentLimit; declared here so the UI layer
// is self-contained.
const ActivityRecentMax = 5

// ActivityFullMax caps the per-site row count cached for the Activity tab.
// The DB enforces the same retention via storage.ActivityRetentionDefault;
// duplicating the constant here means the UI never holds more than the
// designed maximum even if a future schema change widens retention.
const ActivityFullMax = 200

// MaxHookOutputLinesPerSite caps how many output lines we keep in memory
// for a site's live-output panel. Older lines are dropped (the on-disk run
// log retains the full output).
const MaxHookOutputLinesPerSite = 200

// NavView identifies which root area is shown in columns 2+3. The nav rail
// in column 1 toggles this; the chrome adapts accordingly.
type NavView string

const (
	NavViewSites    NavView = "sites"
	NavViewSettings NavView = "settings"
)

// UIState holds all mutable UI state, protected by a mutex for thread-safe
// access from background goroutines (Docker operations, site loading, etc.).
type UIState struct {
	mu sync.Mutex

	// Site data
	sites      []types.Site
	selectedID string
	searchTerm string

	// Navigation
	navView      NavView
	navCollapsed bool

	// Modal state
	showNewSiteModal bool

	// Loading state
	siteToggling map[string]bool // site ID -> whether start/stop is in progress

	// Error banner state
	errorMessage     string
	errorExpiry      time.Time
	errorActionID    string
	errorActionLabel string
	errorActionRun   func()
	errorActionBusy  bool

	// Delete confirmation modal
	showDeleteConfirm bool
	deleteTargetID    string
	deleteTargetName  string
	deletePurgeVolume bool

	// Container log viewer
	logService string
	logOutput  string
	logLoading bool

	// WP-CLI
	wpcliOutput  string
	wpcliLoading bool

	// Site export
	exportLoading bool

	// Initialization state
	initDone    bool
	initError   string
	onRetryInit func()

	// Clone modal
	showCloneModal  bool
	cloneTargetID   string
	cloneTargetName string
	cloneLoading    bool

	// Link checker
	linkCheckOutput  string
	linkCheckLoading bool

	// Persistent informational notice (e.g. "install mkcert"). Distinct
	// from errorMessage, which is transient and red. When noticeAction is
	// non-nil, the banner renders an action button; noticeBusy mutes that
	// button while the action is in flight.
	notice            string
	noticeAction      func()
	noticeActionLabel string
	noticeBusy        bool

	// Window reference for triggering invalidation from background goroutines.
	window *app.Window

	// Hook live state — keyed by siteID. The siteID is the SiteManager's
	// canonical site UUID; entries are created lazily on the first hook
	// callback for a site and reset when the user opens a fresh Run Now.
	hookState map[string]*hookSiteState

	// Lifecycle plan progress — keyed by siteID. Created lazily on the
	// first OnStepStart callback; reset by ResetLifecycleProgress at the
	// start of a new lifecycle method (StartSite, StopSite, etc).
	lifecycleState map[string]*lifecycleSiteState

	// Activity feed cache — keyed by siteID. Populated by background
	// loaders and OnActivityAppended; the UI reads via the snapshot
	// helpers below so Layout() never holds the mutex while iterating.
	activityState map[string]*activitySiteCache

	// Aggregate health of the global services (router, mail, adminer).
	// Polled from the main goroutine; written via SetServicesHealth.
	servicesHealth ServicesHealth

	// healthSnapshot is the latest snapshot from the health.Runner.
	// Stored via atomic.Pointer so reads are lock-free; the UI Layout
	// pass reads on every frame without contending with the runner.
	healthSnapshot atomic.Pointer[health.Snapshot]

	// healthSeen tracks the (per-finding-key) last-seen generation so
	// we can decide whether to toast a freshly-published finding. Two
	// concerns: cross-restart persistence (delegated to the config
	// store via the syncHealthSeen method) and per-frame toast
	// throttling. Guarded by healthSeenMu.
	healthSeen      map[string]bool
	healthSeenMu    sync.Mutex
	healthFirstFire bool // true until the first snapshot publishes; suppresses
	// the initial flood of toasts on app start so an upgrade doesn't bury
	// the user in pre-existing-but-newly-detected findings.

	// diskFreeBytes is the most recent host-filesystem free-byte reading
	// surfaced by the disk-low check. Zero before the first reading.
	// Guarded by mu (top-level UIState mutex) — same as servicesHealth.
	diskFreeBytes int64

	// updateAvailable / updateURL / updateNotes / updateDismissed
	// snapshot the latest update-check result for the Settings card and
	// the nav rail dot. Empty updateAvailable = no banner; non-empty
	// + matches updateDismissed = also no banner (user clicked "Dismiss
	// this version"). Compared as semver via the updatecheck package.
	updateAvailable string
	updateURL       string
	updateNotes     string
	updateDismissed string
}

// activitySiteCache mirrors a slice of recent activity rows for one site.
// The recent slice is bounded to ActivityRecentMax for the overview panel;
// the full slice is bounded to ActivityFullMax for the Activity tab and is
// only populated when the user opens that tab — fullLoaded distinguishes
// "loaded, empty" from "not yet loaded".
type activitySiteCache struct {
	recent     []storage.ActivityEvent
	full       []storage.ActivityEvent
	fullLoaded bool
}

// ServicesHealth captures the rolled-up health of Locorum's global
// services. Status is the worst observed state across the three; Detail
// is a human-readable suffix shown in the top status bar.
type ServicesHealth struct {
	Status ServicesHealthStatus
	Detail string
}

// ServicesHealthStatus is a tri-state for the global-services bar.
type ServicesHealthStatus int

const (
	ServicesHealthUnknown ServicesHealthStatus = iota
	ServicesHealthHealthy
	ServicesHealthDegraded
	ServicesHealthDown
)

// lifecycleSiteState captures the progress of a site lifecycle Plan.
type lifecycleSiteState struct {
	planName    string
	steps       []orch.StepResult
	pullByImage map[string]docker.PullProgress
	pullOrder   []string
	finalErr    error
	rolledBack  bool
	done        bool
}

// hookSiteState captures the live output and progress for a site's hook run.
type hookSiteState struct {
	// runningHook is set while a task is in flight; nil between tasks.
	runningHook *hooks.Hook
	// lastResult is the most recently completed task; nil before the first
	// completion.
	lastResult *hooks.Result
	// lines is a ring of recent output lines (capped at
	// MaxHookOutputLinesPerSite). Older lines spill to the on-disk log.
	lines []hookLine
	// summary is set when OnAllDone fires; cleared on the next Run.
	summary *hooks.Summary
}

// hookLine pairs an output line with its stream origin.
type hookLine struct {
	Text   string
	Stderr bool
}

func NewUIState() *UIState {
	s := &UIState{
		navView:         NavViewSites,
		siteToggling:    make(map[string]bool),
		hookState:       make(map[string]*hookSiteState),
		lifecycleState:  make(map[string]*lifecycleSiteState),
		activityState:   make(map[string]*activitySiteCache),
		healthSeen:      make(map[string]bool),
		healthFirstFire: true,
	}
	// Publish an empty snapshot so the Layout pass never reads nil.
	empty := health.Snapshot{}
	s.healthSnapshot.Store(&empty)
	return s
}

// ─── Navigation ─────────────────────────────────────────────────────────────

// NavView returns the active root view ("sites" or "settings").
func (s *UIState) NavView() NavView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.navView
}

// SetNavView switches the active root view. Triggers a redraw so the chrome
// can swap columns 2 and 3 immediately.
func (s *UIState) SetNavView(v NavView) {
	s.mu.Lock()
	s.navView = v
	s.mu.Unlock()
	s.Invalidate()
}

// NavCollapsed reports whether the nav rail is in icon-only collapsed mode.
func (s *UIState) NavCollapsed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.navCollapsed
}

// SetNavCollapsed toggles the rail's collapsed state.
func (s *UIState) SetNavCollapsed(c bool) {
	s.mu.Lock()
	s.navCollapsed = c
	s.mu.Unlock()
	s.Invalidate()
}

// ─── Window ─────────────────────────────────────────────────────────────────

// SetWindow stores the app window reference for invalidation.
func (s *UIState) SetWindow(w *app.Window) {
	s.mu.Lock()
	s.window = w
	s.mu.Unlock()
}

// Invalidate triggers a redraw from any goroutine safely.
func (s *UIState) Invalidate() {
	s.mu.Lock()
	w := s.window
	s.mu.Unlock()
	if w != nil {
		w.Invalidate()
	}
}

// ─── System health ──────────────────────────────────────────────────────────

// SetHealthSnapshot publishes a new snapshot from the runner. Lock-free
// (atomic.Pointer). Triggers a window invalidation so the Layout pass
// picks up the new content on the next frame.
//
// Toast bookkeeping is delegated to the caller, which knows when first-fire
// suppression should apply (see UIState.HealthShouldToast).
func (s *UIState) SetHealthSnapshot(snap health.Snapshot) {
	cp := snap
	s.healthSnapshot.Store(&cp)
	s.Invalidate()
}

// HealthSnapshot returns the most recently stored snapshot. Never nil
// because NewUIState publishes an empty snapshot at construction time.
func (s *UIState) HealthSnapshot() health.Snapshot {
	v := s.healthSnapshot.Load()
	if v == nil {
		return health.Snapshot{}
	}
	return *v
}

// HealthShouldToast reports whether the given finding key has been seen
// before in this process. The first call for any key returns true (the
// caller should fire a toast); subsequent calls return false.
//
// Returns false during the first-fire window — the runner publishes the
// initial snapshot but the UI suppresses toasts so a fresh install isn't
// flooded.
func (s *UIState) HealthShouldToast(key string) bool {
	s.healthSeenMu.Lock()
	defer s.healthSeenMu.Unlock()
	if s.healthFirstFire {
		s.healthSeen[key] = true
		return false
	}
	if s.healthSeen[key] {
		return false
	}
	s.healthSeen[key] = true
	return true
}

// HealthClearFirstFire ends the first-fire suppression window. Called
// once after the first snapshot finishes. Subsequent toasts behave normally.
func (s *UIState) HealthClearFirstFire() {
	s.healthSeenMu.Lock()
	s.healthFirstFire = false
	s.healthSeenMu.Unlock()
}

// HealthSeenKeys returns a snapshot of the seen-keys set, suitable for
// JSON serialisation into the persistent settings table.
func (s *UIState) HealthSeenKeys() []string {
	s.healthSeenMu.Lock()
	defer s.healthSeenMu.Unlock()
	out := make([]string, 0, len(s.healthSeen))
	for k := range s.healthSeen {
		out = append(out, k)
	}
	return out
}

// HealthHydrateSeen seeds the seen-keys set from the persisted JSON. Idempotent.
func (s *UIState) HealthHydrateSeen(keys []string) {
	s.healthSeenMu.Lock()
	for _, k := range keys {
		s.healthSeen[k] = true
	}
	s.healthSeenMu.Unlock()
}

// ─── Update-check banner ────────────────────────────────────────────────────

// UpdateBannerSnapshot is a frame-stable copy of the update-check state
// for the Settings → Diagnostics card and the nav rail dot.
type UpdateBannerSnapshot struct {
	Available string // version string (empty = no upgrade)
	URL       string
	Notes     string
	Dismissed string
}

// HasUnreadUpdate reports whether there's an upgrade available that the
// user has not already dismissed. Comparison is delegated to the caller
// (updatecheck.IsStrictlyNewer) so this package stays semver-blind.
// Cheap; safe to call from the nav rail every frame.
func (u UpdateBannerSnapshot) HasUnreadUpdate(newer func(latest, dismissed string) bool) bool {
	if u.Available == "" {
		return false
	}
	if u.Dismissed == "" {
		return true
	}
	return newer(u.Available, u.Dismissed)
}

// SetUpdateAvailable publishes the latest update-check answer.
func (s *UIState) SetUpdateAvailable(version, url, notes string) {
	s.mu.Lock()
	s.updateAvailable = version
	s.updateURL = url
	s.updateNotes = notes
	s.mu.Unlock()
	s.Invalidate()
}

// SetUpdateDismissed records the version the user has clicked
// "Dismiss this version" on. Call after the persisted setting has
// been written, so the in-memory snapshot stays consistent.
func (s *UIState) SetUpdateDismissed(version string) {
	s.mu.Lock()
	s.updateDismissed = version
	s.mu.Unlock()
	s.Invalidate()
}

// UpdateBannerSnapshot returns the current update-check state.
func (s *UIState) UpdateBannerSnapshot() UpdateBannerSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return UpdateBannerSnapshot{
		Available: s.updateAvailable,
		URL:       s.updateURL,
		Notes:     s.updateNotes,
		Dismissed: s.updateDismissed,
	}
}

// SetDiskFreeBytes stores the most recent host-filesystem free-byte reading
// for the top status bar. The runner publishes this via the disk-low check;
// main.go pulls it out and pushes here on the same cadence the services-
// health bar polls so the UI updates feel synchronous to the user.
func (s *UIState) SetDiskFreeBytes(n int64) {
	s.mu.Lock()
	changed := s.diskFreeBytes != n
	s.diskFreeBytes = n
	s.mu.Unlock()
	if changed {
		s.Invalidate()
	}
}

// DiskFreeBytes returns the cached host-filesystem free-byte reading.
// Zero means "unknown" (typical until the first health cycle completes).
func (s *UIState) DiskFreeBytes() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.diskFreeBytes
}

// ─── Services health ────────────────────────────────────────────────────────

// SetServicesHealth replaces the rolled-up global-services health snapshot
// shown in the top status bar.
func (s *UIState) SetServicesHealth(h ServicesHealth) {
	s.mu.Lock()
	changed := s.servicesHealth != h
	s.servicesHealth = h
	s.mu.Unlock()
	if changed {
		s.Invalidate()
	}
}

// ServicesHealthSnapshot returns the current global-services health.
func (s *UIState) ServicesHealthSnapshot() ServicesHealth {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.servicesHealth
}

// ─── Sites ──────────────────────────────────────────────────────────────────

// SetSites replaces the site list.
func (s *UIState) SetSites(sites []types.Site) {
	s.mu.Lock()
	s.sites = sites
	s.mu.Unlock()
	s.Invalidate()
}

// GetSites returns a copy of the current site list.
func (s *UIState) GetSites() []types.Site {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]types.Site, len(s.sites))
	copy(out, s.sites)
	return out
}

// UpdateSite replaces a single site in the list by ID.
func (s *UIState) UpdateSite(site types.Site) {
	s.mu.Lock()
	for i, existing := range s.sites {
		if existing.ID == site.ID {
			s.sites[i] = site
			break
		}
	}
	s.mu.Unlock()
	s.Invalidate()
}

// SelectedSite returns the currently selected site, or nil.
func (s *UIState) SelectedSite() *types.Site {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.sites {
		if s.sites[i].ID == s.selectedID {
			return &s.sites[i]
		}
	}
	return nil
}

// SetSelectedID sets the selected site ID.
func (s *UIState) SetSelectedID(id string) {
	s.mu.Lock()
	s.selectedID = id
	s.mu.Unlock()
}

// GetSelectedID returns the selected site ID.
func (s *UIState) GetSelectedID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selectedID
}

// ─── Search ─────────────────────────────────────────────────────────────────

// SetSearchTerm updates the sidebar search filter.
func (s *UIState) SetSearchTerm(term string) {
	s.mu.Lock()
	s.searchTerm = term
	s.mu.Unlock()
}

// GetSearchTerm returns the current search filter.
func (s *UIState) GetSearchTerm() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.searchTerm
}

// ─── New Site Modal ─────────────────────────────────────────────────────────

// SetShowNewSiteModal controls visibility of the new site modal.
func (s *UIState) SetShowNewSiteModal(show bool) {
	s.mu.Lock()
	s.showNewSiteModal = show
	s.mu.Unlock()
}

// IsShowNewSiteModal returns whether the new site modal is visible.
func (s *UIState) IsShowNewSiteModal() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.showNewSiteModal
}

// ─── Site Toggling (Start/Stop Loading) ─────────────────────────────────────

// SetSiteToggling sets the loading state for a site's start/stop operation.
func (s *UIState) SetSiteToggling(id string, toggling bool) {
	s.mu.Lock()
	s.siteToggling[id] = toggling
	s.mu.Unlock()
	s.Invalidate()
}

// IsSiteToggling returns whether a site is currently starting or stopping.
func (s *UIState) IsSiteToggling(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.siteToggling[id]
}

// ─── Error Banner ───────────────────────────────────────────────────────────

// errorBannerTTL caps how long an error banner stays visible before
// auto-dismiss. Banners with an action button stay longer because the user
// has to read, understand, and click — eight seconds isn't enough.
const (
	errorBannerTTL       = 8 * time.Second
	errorBannerActionTTL = 30 * time.Second
)

// ErrorBannerSnapshot is a frame-stable copy of the error banner state.
// The Layout pass reads it once per frame so it can render the message and
// (optionally) an action button without holding the state mutex.
type ErrorBannerSnapshot struct {
	Message     string
	ActionID    string
	ActionLabel string
	HasAction   bool
	Busy        bool
}

// ShowError sets the error banner message with an 8-second auto-dismiss.
// Any prior action button is cleared — call ShowErrorWithAction if you
// want a button.
func (s *UIState) ShowError(msg string) {
	s.setError(msg, NotifyAction{}, errorBannerTTL)
}

// ShowErrorWithAction sets the error banner with an optional action
// button. The TTL is 30 s (vs 8 s for plain errors) to give the user time
// to act. If a.Run is nil the call behaves identically to ShowError.
func (s *UIState) ShowErrorWithAction(msg string, a NotifyAction) {
	ttl := errorBannerTTL
	if a.HasRun() {
		ttl = errorBannerActionTTL
	}
	s.setError(msg, a, ttl)
}

func (s *UIState) setError(msg string, a NotifyAction, ttl time.Duration) {
	s.mu.Lock()
	s.errorMessage = msg
	s.errorExpiry = time.Now().Add(ttl)
	if a.HasRun() {
		s.errorActionID = a.ID
		s.errorActionLabel = a.Label
		s.errorActionRun = a.Run
	} else {
		s.errorActionID = ""
		s.errorActionLabel = ""
		s.errorActionRun = nil
	}
	s.errorActionBusy = false
	s.mu.Unlock()
	s.Invalidate()

	go func() {
		time.Sleep(ttl)
		s.Invalidate()
	}()
}

// ActiveError returns the current error message if it hasn't expired, or "".
// Preserved for callers that don't care about the action button. New code
// should prefer ErrorBannerSnapshot.
func (s *UIState) ActiveError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errorMessage == "" {
		return ""
	}
	if time.Now().After(s.errorExpiry) {
		s.errorMessage = ""
		s.errorActionID = ""
		s.errorActionLabel = ""
		s.errorActionRun = nil
		s.errorActionBusy = false
		return ""
	}
	return s.errorMessage
}

// ErrorBannerSnapshot returns a frame-stable snapshot of the banner state.
// The Run callback is intentionally not exposed — callers wire clicks
// through TriggerErrorAction so the busy-flag handshake stays atomic.
func (s *UIState) ErrorBannerSnapshot() ErrorBannerSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errorMessage == "" {
		return ErrorBannerSnapshot{}
	}
	if time.Now().After(s.errorExpiry) {
		s.errorMessage = ""
		s.errorActionID = ""
		s.errorActionLabel = ""
		s.errorActionRun = nil
		s.errorActionBusy = false
		return ErrorBannerSnapshot{}
	}
	return ErrorBannerSnapshot{
		Message:     s.errorMessage,
		ActionID:    s.errorActionID,
		ActionLabel: s.errorActionLabel,
		HasAction:   s.errorActionRun != nil,
		Busy:        s.errorActionBusy,
	}
}

// TriggerErrorAction atomically claims the busy slot and invokes the
// banner's action callback. Returns true if an action was started, false
// when no callback is registered or one is already in flight. The action
// is responsible for clearing its own busy state via ClearErrorBanner
// when it completes (success) or by leaving the banner visible until
// auto-dismiss (failure).
func (s *UIState) TriggerErrorAction() bool {
	s.mu.Lock()
	if s.errorActionRun == nil || s.errorActionBusy {
		s.mu.Unlock()
		return false
	}
	s.errorActionBusy = true
	action := s.errorActionRun
	s.mu.Unlock()
	s.Invalidate()
	action()
	return true
}

// ClearErrorBanner immediately dismisses the error banner. Called from
// action callbacks after they've kicked off the requested follow-up work
// (e.g. starting a site) so the banner doesn't hang around.
func (s *UIState) ClearErrorBanner() {
	s.mu.Lock()
	s.errorMessage = ""
	s.errorActionID = ""
	s.errorActionLabel = ""
	s.errorActionRun = nil
	s.errorActionBusy = false
	s.mu.Unlock()
	s.Invalidate()
}

// ─── Delete Confirmation ────────────────────────────────────────────────────

// ShowDeleteConfirm opens the delete confirmation modal for a site.
func (s *UIState) ShowDeleteConfirm(id, name string) {
	s.mu.Lock()
	s.showDeleteConfirm = true
	s.deleteTargetID = id
	s.deleteTargetName = name
	s.deletePurgeVolume = false
	s.mu.Unlock()
}

// ClearDeleteConfirm closes the delete confirmation modal and returns the
// target site ID and the user's purge choice.
func (s *UIState) ClearDeleteConfirm() (id string, purgeVolume bool) {
	s.mu.Lock()
	id = s.deleteTargetID
	purgeVolume = s.deletePurgeVolume
	s.showDeleteConfirm = false
	s.deleteTargetID = ""
	s.deleteTargetName = ""
	s.deletePurgeVolume = false
	s.mu.Unlock()
	return id, purgeVolume
}

// DismissDeleteConfirm closes the delete confirmation modal without deleting.
func (s *UIState) DismissDeleteConfirm() {
	s.mu.Lock()
	s.showDeleteConfirm = false
	s.deleteTargetID = ""
	s.deleteTargetName = ""
	s.deletePurgeVolume = false
	s.mu.Unlock()
}

// SetDeletePurgeVolume toggles the "also delete database volume" choice in
// the delete-confirm modal.
func (s *UIState) SetDeletePurgeVolume(purge bool) {
	s.mu.Lock()
	s.deletePurgeVolume = purge
	s.mu.Unlock()
	s.Invalidate()
}

// GetDeleteConfirmState returns the current delete confirmation state.
func (s *UIState) GetDeleteConfirmState() (show bool, name string, purgeVolume bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.showDeleteConfirm, s.deleteTargetName, s.deletePurgeVolume
}

// ─── Container Logs ─────────────────────────────────────────────────────────

// SetLogOutput updates the log viewer content.
func (s *UIState) SetLogOutput(service, output string) {
	s.mu.Lock()
	s.logService = service
	s.logOutput = output
	s.mu.Unlock()
	s.Invalidate()
}

// SetLogLoading sets the log viewer loading state.
func (s *UIState) SetLogLoading(loading bool) {
	s.mu.Lock()
	s.logLoading = loading
	s.mu.Unlock()
	s.Invalidate()
}

// GetLogState returns the current log viewer state.
func (s *UIState) GetLogState() (output, service string, loading bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logOutput, s.logService, s.logLoading
}

// ─── WP-CLI ─────────────────────────────────────────────────────────────────

// SetWPCLIOutput updates the WP-CLI output content.
func (s *UIState) SetWPCLIOutput(output string) {
	s.mu.Lock()
	s.wpcliOutput = output
	s.mu.Unlock()
	s.Invalidate()
}

// SetWPCLILoading sets the WP-CLI loading state.
func (s *UIState) SetWPCLILoading(loading bool) {
	s.mu.Lock()
	s.wpcliLoading = loading
	s.mu.Unlock()
	s.Invalidate()
}

// GetWPCLIState returns the current WP-CLI state.
func (s *UIState) GetWPCLIState() (output string, loading bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wpcliOutput, s.wpcliLoading
}

// ─── Export ─────────────────────────────────────────────────────────────────

// SetExportLoading sets the export loading state.
func (s *UIState) SetExportLoading(loading bool) {
	s.mu.Lock()
	s.exportLoading = loading
	s.mu.Unlock()
	s.Invalidate()
}

// IsExportLoading returns whether an export is in progress.
func (s *UIState) IsExportLoading() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exportLoading
}

// ─── Initialization ─────────────────────────────────────────────────────────

// SetInitError records an initialization failure.
func (s *UIState) SetInitError(msg string) {
	s.mu.Lock()
	s.initError = msg
	s.initDone = false
	s.mu.Unlock()
	s.Invalidate()
}

// SetInitDone marks initialization as complete.
func (s *UIState) SetInitDone() {
	s.mu.Lock()
	s.initDone = true
	s.initError = ""
	s.mu.Unlock()
	s.Invalidate()
}

// ClearInitError clears the init error.
func (s *UIState) ClearInitError() {
	s.mu.Lock()
	s.initError = ""
	s.mu.Unlock()
	s.Invalidate()
}

// GetInitError returns the current initialization error, or "".
func (s *UIState) GetInitError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initError
}

// SetRetryInit sets the retry callback.
func (s *UIState) SetRetryInit(fn func()) {
	s.mu.Lock()
	s.onRetryInit = fn
	s.mu.Unlock()
}

// GetRetryInit returns the retry callback.
func (s *UIState) GetRetryInit() func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.onRetryInit
}

// ─── Clone Modal ────────────────────────────────────────────────────────────

func (s *UIState) ShowCloneModal(id, name string) {
	s.mu.Lock()
	s.showCloneModal = true
	s.cloneTargetID = id
	s.cloneTargetName = name
	s.mu.Unlock()
	s.Invalidate()
}

func (s *UIState) DismissCloneModal() {
	s.mu.Lock()
	s.showCloneModal = false
	s.cloneTargetID = ""
	s.cloneTargetName = ""
	s.mu.Unlock()
	s.Invalidate()
}

func (s *UIState) GetCloneModalState() (show bool, id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.showCloneModal, s.cloneTargetID, s.cloneTargetName
}

func (s *UIState) SetCloneLoading(loading bool) {
	s.mu.Lock()
	s.cloneLoading = loading
	s.mu.Unlock()
	s.Invalidate()
}

func (s *UIState) IsCloneLoading() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cloneLoading
}

// ─── Link Checker ───────────────────────────────────────────────────────────

func (s *UIState) SetLinkCheckOutput(output string) {
	s.mu.Lock()
	s.linkCheckOutput = output
	s.mu.Unlock()
	s.Invalidate()
}

func (s *UIState) AppendLinkCheckOutput(line string) {
	s.mu.Lock()
	if s.linkCheckOutput != "" {
		s.linkCheckOutput += "\n"
	}
	s.linkCheckOutput += line
	s.mu.Unlock()
	s.Invalidate()
}

func (s *UIState) SetLinkCheckLoading(loading bool) {
	s.mu.Lock()
	s.linkCheckLoading = loading
	s.mu.Unlock()
	s.Invalidate()
}

func (s *UIState) GetLinkCheckState() (output string, loading bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.linkCheckOutput, s.linkCheckLoading
}

// ─── Notice ─────────────────────────────────────────────────────────────────

// NoticeSnapshot is a frame-stable copy of the notice banner state. The
// Layout pass reads this once per frame so it can render the message and
// (optionally) an action button without holding the state mutex.
type NoticeSnapshot struct {
	Message     string
	ActionLabel string
	HasAction   bool
	Busy        bool
}

// SetNotice sets a persistent informational banner with no action button
// (or clears it with ""). Used for non-fatal status like "HTTPS will be
// untrusted". Always clears any prior action callback.
func (s *UIState) SetNotice(msg string) {
	s.mu.Lock()
	s.notice = msg
	s.noticeAction = nil
	s.noticeActionLabel = ""
	s.noticeBusy = false
	s.mu.Unlock()
	s.Invalidate()
}

// SetNoticeWithAction sets a persistent banner with an action button. The
// callback fires when the user clicks the button; it should kick off any
// long-running work in its own goroutine and call SetNoticeBusy to gate
// double-clicks.
func (s *UIState) SetNoticeWithAction(msg, label string, action func()) {
	s.mu.Lock()
	s.notice = msg
	s.noticeActionLabel = label
	s.noticeAction = action
	s.noticeBusy = false
	s.mu.Unlock()
	s.Invalidate()
}

// SetNoticeBusy gates the banner action button while a triggered task is
// in flight, swapping the label for an in-progress hint.
func (s *UIState) SetNoticeBusy(busy bool) {
	s.mu.Lock()
	s.noticeBusy = busy
	s.mu.Unlock()
	s.Invalidate()
}

// NoticeSnapshot returns the current banner state as a frame-stable copy.
func (s *UIState) NoticeSnapshot() NoticeSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return NoticeSnapshot{
		Message:     s.notice,
		ActionLabel: s.noticeActionLabel,
		HasAction:   s.noticeAction != nil,
		Busy:        s.noticeBusy,
	}
}

// TriggerNoticeAction atomically claims the busy slot and invokes the
// banner's action callback. Returns true if an action was started, false
// when no callback is registered or one is already in flight. The action
// is responsible for clearing the busy state when it completes.
func (s *UIState) TriggerNoticeAction() bool {
	s.mu.Lock()
	if s.noticeAction == nil || s.noticeBusy {
		s.mu.Unlock()
		return false
	}
	s.noticeBusy = true
	action := s.noticeAction
	s.mu.Unlock()
	s.Invalidate()
	action()
	return true
}

// ─── Hook live state ───────────────────────────────────────────────────────

// HookSnapshot is a frame-stable copy of the per-site hook output / progress.
// Renderers receive a snapshot so the layout pass never holds the state
// mutex while iterating.
type HookSnapshot struct {
	Running *hooks.Hook
	Last    *hooks.Result
	Summary *hooks.Summary
	Lines   []hookLine
}

// HasActivity reports whether there is anything worth displaying.
func (h HookSnapshot) HasActivity() bool {
	return h.Running != nil || h.Last != nil || len(h.Lines) > 0 || h.Summary != nil
}

func (s *UIState) hookStateFor(siteID string) *hookSiteState {
	st, ok := s.hookState[siteID]
	if !ok {
		st = &hookSiteState{}
		s.hookState[siteID] = st
	}
	return st
}

// HookTaskStarted clears any prior summary and records that h is running.
func (s *UIState) HookTaskStarted(siteID string, h hooks.Hook) {
	s.mu.Lock()
	st := s.hookStateFor(siteID)
	hCopy := h
	st.runningHook = &hCopy
	st.summary = nil
	s.mu.Unlock()
	s.Invalidate()
}

// HookTaskOutput appends a line to the site's output ring.
func (s *UIState) HookTaskOutput(siteID string, line string, stderr bool) {
	s.mu.Lock()
	st := s.hookStateFor(siteID)
	st.lines = append(st.lines, hookLine{Text: line, Stderr: stderr})
	if overflow := len(st.lines) - MaxHookOutputLinesPerSite; overflow > 0 {
		// Drop the oldest entries; the on-disk log preserves full history.
		st.lines = append(st.lines[:0], st.lines[overflow:]...)
	}
	s.mu.Unlock()
	s.Invalidate()
}

// HookTaskDone records the result and clears the running marker.
func (s *UIState) HookTaskDone(siteID string, r hooks.Result) {
	s.mu.Lock()
	st := s.hookStateFor(siteID)
	rc := r
	st.lastResult = &rc
	st.runningHook = nil
	s.mu.Unlock()
	s.Invalidate()
}

// HookAllDone records the run summary.
func (s *UIState) HookAllDone(siteID string, summary hooks.Summary) {
	s.mu.Lock()
	st := s.hookStateFor(siteID)
	sc := summary
	st.summary = &sc
	st.runningHook = nil
	s.mu.Unlock()
	s.Invalidate()
}

// ClearHookOutput discards the cached output for a site (e.g. when the user
// switches sites or the panel resets).
func (s *UIState) ClearHookOutput(siteID string) {
	s.mu.Lock()
	delete(s.hookState, siteID)
	s.mu.Unlock()
	s.Invalidate()
}

// ─── Lifecycle plan progress ───────────────────────────────────────────────

// LifecycleSnapshot is a frame-stable copy of a site's running lifecycle
// Plan. Renderers receive a snapshot so the Layout pass never holds the
// state mutex while iterating.
type LifecycleSnapshot struct {
	PlanName   string
	Steps      []orch.StepResult
	Pulls      []docker.PullProgress
	FinalError error
	RolledBack bool
	Done       bool
}

// HasActivity reports whether there is anything worth displaying.
func (l LifecycleSnapshot) HasActivity() bool {
	return l.PlanName != "" || len(l.Steps) > 0
}

func (s *UIState) lifecycleStateFor(siteID string) *lifecycleSiteState {
	st, ok := s.lifecycleState[siteID]
	if !ok {
		st = &lifecycleSiteState{pullByImage: map[string]docker.PullProgress{}}
		s.lifecycleState[siteID] = st
	}
	return st
}

// ResetLifecycleProgress clears the cached Plan progress for a site, in
// preparation for a fresh lifecycle method run.
func (s *UIState) ResetLifecycleProgress(siteID string) {
	s.mu.Lock()
	delete(s.lifecycleState, siteID)
	s.mu.Unlock()
	s.Invalidate()
}

// LifecycleStepStarted records an in-flight step. Earlier completed steps
// are preserved so the GUI shows the full trail.
func (s *UIState) LifecycleStepStarted(siteID string, step orch.StepResult) {
	s.mu.Lock()
	st := s.lifecycleStateFor(siteID)
	idx := indexOfStep(st.steps, step.Name)
	if idx >= 0 {
		st.steps[idx] = step
	} else {
		st.steps = append(st.steps, step)
	}
	s.mu.Unlock()
	s.Invalidate()
}

// LifecycleStepDone updates the recorded step (matched by name) with the
// final outcome.
func (s *UIState) LifecycleStepDone(siteID string, step orch.StepResult) {
	s.mu.Lock()
	st := s.lifecycleStateFor(siteID)
	idx := indexOfStep(st.steps, step.Name)
	if idx >= 0 {
		st.steps[idx] = step
	} else {
		st.steps = append(st.steps, step)
	}
	s.mu.Unlock()
	s.Invalidate()
}

// LifecyclePlanDone records the final Result.
func (s *UIState) LifecyclePlanDone(siteID string, res orch.Result) {
	s.mu.Lock()
	st := s.lifecycleStateFor(siteID)
	st.planName = res.PlanName
	st.steps = append([]orch.StepResult(nil), res.Steps...)
	st.finalErr = res.FinalError
	st.rolledBack = res.RolledBack
	st.done = true
	s.mu.Unlock()
	s.Invalidate()
}

// LifecyclePullProgress records the latest pull progress for an image.
// Per-image: aggregated across layers by the engine before it reaches us.
func (s *UIState) LifecyclePullProgress(siteID string, p docker.PullProgress) {
	s.mu.Lock()
	st := s.lifecycleStateFor(siteID)
	if _, ok := st.pullByImage[p.Image]; !ok {
		st.pullOrder = append(st.pullOrder, p.Image)
	}
	st.pullByImage[p.Image] = p
	s.mu.Unlock()
	s.Invalidate()
}

// LifecycleSnapshot returns a frame-stable copy of the lifecycle state.
func (s *UIState) LifecycleSnapshot(siteID string) LifecycleSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.lifecycleState[siteID]
	if !ok {
		return LifecycleSnapshot{}
	}
	out := LifecycleSnapshot{
		PlanName:   st.planName,
		Steps:      append([]orch.StepResult(nil), st.steps...),
		FinalError: st.finalErr,
		RolledBack: st.rolledBack,
		Done:       st.done,
	}
	for _, img := range st.pullOrder {
		out.Pulls = append(out.Pulls, st.pullByImage[img])
	}
	return out
}

func indexOfStep(steps []orch.StepResult, name string) int {
	for i, s := range steps {
		if s.Name == name {
			return i
		}
	}
	return -1
}

// HookSnapshot returns a copy of the per-site hook output for safe reading
// from a Layout pass.
func (s *UIState) HookSnapshot(siteID string) HookSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.hookState[siteID]
	if !ok {
		return HookSnapshot{}
	}
	out := HookSnapshot{}
	if st.runningHook != nil {
		h := *st.runningHook
		out.Running = &h
	}
	if st.lastResult != nil {
		r := *st.lastResult
		out.Last = &r
	}
	if st.summary != nil {
		sm := *st.summary
		out.Summary = &sm
	}
	out.Lines = make([]hookLine, len(st.lines))
	copy(out.Lines, st.lines)
	return out
}

// ─── Activity feed cache ───────────────────────────────────────────────────

// activityCacheFor returns the per-site cache, creating it on first
// access. Caller must hold s.mu.
func (s *UIState) activityCacheFor(siteID string) *activitySiteCache {
	c, ok := s.activityState[siteID]
	if !ok {
		c = &activitySiteCache{}
		s.activityState[siteID] = c
	}
	return c
}

// SetActivityRecent replaces the recent-rows cache for siteID. Caller is
// responsible for fetching newest-first; this method does not re-sort.
//
// The slice is copied (not retained) so the caller can recycle the input.
// If evs is longer than ActivityRecentMax, only the first N entries are
// kept — the recent panel never renders more than that.
func (s *UIState) SetActivityRecent(siteID string, evs []storage.ActivityEvent) {
	if siteID == "" {
		return
	}
	n := len(evs)
	if n > ActivityRecentMax {
		n = ActivityRecentMax
	}
	cp := make([]storage.ActivityEvent, n)
	copy(cp, evs[:n])

	s.mu.Lock()
	c := s.activityCacheFor(siteID)
	c.recent = cp
	s.mu.Unlock()
	s.Invalidate()
}

// SetActivityFull replaces the full-rows cache for siteID and marks the
// full cache as loaded. Mirrors SetActivityRecent's copy semantics.
//
// If evs is longer than ActivityFullMax, only the newest N are kept.
func (s *UIState) SetActivityFull(siteID string, evs []storage.ActivityEvent) {
	if siteID == "" {
		return
	}
	n := len(evs)
	if n > ActivityFullMax {
		n = ActivityFullMax
	}
	cp := make([]storage.ActivityEvent, n)
	copy(cp, evs[:n])

	s.mu.Lock()
	c := s.activityCacheFor(siteID)
	c.full = cp
	c.fullLoaded = true
	s.mu.Unlock()
	s.Invalidate()
}

// AppendActivity prepends ev to both the recent and (if loaded) full cache
// for siteID, trimming each to its respective cap. Used from the
// OnActivityAppended callback.
//
// The full cache is only mutated if it was previously loaded — otherwise
// the row is on disk and will appear next time the Activity tab is opened.
// This avoids growing an unbounded cache for sites the user never visits.
func (s *UIState) AppendActivity(siteID string, ev storage.ActivityEvent) {
	if siteID == "" {
		return
	}
	s.mu.Lock()
	c := s.activityCacheFor(siteID)
	c.recent = prependCap(c.recent, ev, ActivityRecentMax)
	if c.fullLoaded {
		c.full = prependCap(c.full, ev, ActivityFullMax)
	}
	s.mu.Unlock()
	s.Invalidate()
}

// ActivityRecent returns a snapshot copy of the cached recent rows for
// siteID. Returns an empty slice (not nil) if no cache entry exists.
func (s *UIState) ActivityRecent(siteID string) []storage.ActivityEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.activityState[siteID]
	if !ok {
		return nil
	}
	out := make([]storage.ActivityEvent, len(c.recent))
	copy(out, c.recent)
	return out
}

// ActivityFull returns a snapshot copy of the cached full rows for siteID
// and a flag indicating whether the full cache has been populated. The
// caller uses the flag to decide whether to kick off a load.
func (s *UIState) ActivityFull(siteID string) ([]storage.ActivityEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.activityState[siteID]
	if !ok || !c.fullLoaded {
		return nil, false
	}
	out := make([]storage.ActivityEvent, len(c.full))
	copy(out, c.full)
	return out, true
}

// ClearActivity drops the cache entry for siteID. Called when a site is
// removed so we don't leak per-site state for the lifetime of the process.
func (s *UIState) ClearActivity(siteID string) {
	s.mu.Lock()
	delete(s.activityState, siteID)
	s.mu.Unlock()
}

// prependCap returns a new slice with v at the head, dst tail-truncated
// to keep len <= max. dst is not retained; the result is a fresh backing
// array, so callers may safely write to either.
func prependCap(dst []storage.ActivityEvent, v storage.ActivityEvent, max int) []storage.ActivityEvent {
	if max <= 0 {
		return dst[:0]
	}
	keep := len(dst)
	if keep >= max {
		keep = max - 1
	}
	out := make([]storage.ActivityEvent, 0, keep+1)
	out = append(out, v)
	out = append(out, dst[:keep]...)
	return out
}
