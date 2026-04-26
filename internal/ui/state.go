package ui

import (
	"sync"
	"time"

	"gioui.org/app"

	"github.com/PeterBooker/locorum/internal/types"
)

// UIState holds all mutable UI state, protected by a mutex for thread-safe
// access from background goroutines (Docker operations, site loading, etc.).
type UIState struct {
	mu sync.Mutex

	// Site data
	sites      []types.Site
	selectedID string
	searchTerm string

	// Modal state
	showNewSiteModal bool

	// Loading state
	siteToggling map[string]bool // site ID -> whether start/stop is in progress

	// Error banner state
	errorMessage string
	errorExpiry  time.Time

	// Delete confirmation modal
	showDeleteConfirm bool
	deleteTargetID    string
	deleteTargetName  string

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
	// from errorMessage, which is transient and red.
	notice string

	// Window reference for triggering invalidation from background goroutines.
	window *app.Window
}

func NewUIState() *UIState {
	return &UIState{
		siteToggling: make(map[string]bool),
	}
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

// ShowError sets the error banner message with an 8-second auto-dismiss.
func (s *UIState) ShowError(msg string) {
	s.mu.Lock()
	s.errorMessage = msg
	s.errorExpiry = time.Now().Add(8 * time.Second)
	s.mu.Unlock()
	s.Invalidate()

	go func() {
		time.Sleep(8 * time.Second)
		s.Invalidate()
	}()
}

// ActiveError returns the current error message if it hasn't expired, or "".
func (s *UIState) ActiveError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errorMessage == "" {
		return ""
	}
	if time.Now().After(s.errorExpiry) {
		s.errorMessage = ""
		return ""
	}
	return s.errorMessage
}

// ─── Delete Confirmation ────────────────────────────────────────────────────

// ShowDeleteConfirm opens the delete confirmation modal for a site.
func (s *UIState) ShowDeleteConfirm(id, name string) {
	s.mu.Lock()
	s.showDeleteConfirm = true
	s.deleteTargetID = id
	s.deleteTargetName = name
	s.mu.Unlock()
}

// ClearDeleteConfirm closes the delete confirmation modal and returns the
// target site ID (so the caller can proceed with deletion).
func (s *UIState) ClearDeleteConfirm() string {
	s.mu.Lock()
	id := s.deleteTargetID
	s.showDeleteConfirm = false
	s.deleteTargetID = ""
	s.deleteTargetName = ""
	s.mu.Unlock()
	return id
}

// DismissDeleteConfirm closes the delete confirmation modal without deleting.
func (s *UIState) DismissDeleteConfirm() {
	s.mu.Lock()
	s.showDeleteConfirm = false
	s.deleteTargetID = ""
	s.deleteTargetName = ""
	s.mu.Unlock()
}

// GetDeleteConfirmState returns the current delete confirmation state.
func (s *UIState) GetDeleteConfirmState() (show bool, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.showDeleteConfirm, s.deleteTargetName
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

// SetNotice sets a persistent informational banner (or clears it with "").
// Used for non-fatal status like "mkcert not installed — HTTPS will be
// untrusted".
func (s *UIState) SetNotice(msg string) {
	s.mu.Lock()
	s.notice = msg
	s.mu.Unlock()
	s.Invalidate()
}

func (s *UIState) GetNotice() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notice
}
