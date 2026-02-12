package ui

import (
	"sync"
	"time"

	"gioui.org/app"

	"github.com/PeterBooker/locorum/internal/types"
)

type UIState struct {
	mu sync.Mutex

	// Site data
	Sites      []types.Site
	SelectedID string
	SearchTerm string

	// Modal state
	ShowNewSiteModal bool

	// Loading state
	SiteToggling map[string]bool // site ID -> whether start/stop is in progress

	// Error banner state
	ErrorMessage string
	ErrorExpiry  time.Time

	// Delete confirmation modal
	ShowDeleteConfirmModal bool
	DeleteTargetID         string
	DeleteTargetName       string

	// Container log viewer
	LogService string
	LogOutput  string
	LogLoading bool

	// WP-CLI
	WPCLIOutput  string
	WPCLILoading bool

	// Site export
	ExportLoading bool

	// Initialization state
	InitDone    bool
	InitError   string
	OnRetryInit func()

	// Window reference for triggering invalidation from background goroutines
	Window *app.Window
}

func NewUIState() *UIState {
	return &UIState{
		SiteToggling: make(map[string]bool),
	}
}

// SelectedSite returns the currently selected site, or nil.
func (s *UIState) SelectedSite() *types.Site {
	for i := range s.Sites {
		if s.Sites[i].ID == s.SelectedID {
			return &s.Sites[i]
		}
	}
	return nil
}

// Invalidate triggers a redraw from any goroutine safely.
func (s *UIState) Invalidate() {
	if s.Window != nil {
		s.Window.Invalidate()
	}
}

// ShowError sets the error banner message with a timeout.
// A background goroutine triggers a redraw after expiry to auto-dismiss.
func (s *UIState) ShowError(msg string) {
	s.mu.Lock()
	s.ErrorMessage = msg
	s.ErrorExpiry = time.Now().Add(8 * time.Second)
	s.mu.Unlock()
	s.Invalidate()

	go func() {
		time.Sleep(8 * time.Second)
		s.Invalidate()
	}()
}

// ActiveError returns the current error message if it hasn't expired, or "".
func (s *UIState) ActiveError() string {
	if s.ErrorMessage == "" {
		return ""
	}
	if time.Now().After(s.ErrorExpiry) {
		s.ErrorMessage = ""
		return ""
	}
	return s.ErrorMessage
}

// SetInitError records an initialization failure (thread-safe, for use from main).
func (s *UIState) SetInitError(msg string) {
	s.mu.Lock()
	s.InitError = msg
	s.InitDone = false
	s.mu.Unlock()
	s.Invalidate()
}

// SetInitDone marks initialization as complete (thread-safe, for use from main).
func (s *UIState) SetInitDone() {
	s.mu.Lock()
	s.InitDone = true
	s.InitError = ""
	s.mu.Unlock()
	s.Invalidate()
}

// ClearInitError clears the init error (thread-safe, for use from main).
func (s *UIState) ClearInitError() {
	s.mu.Lock()
	s.InitError = ""
	s.mu.Unlock()
	s.Invalidate()
}

// SetRetryInit sets the retry callback (thread-safe, for use from main).
func (s *UIState) SetRetryInit(fn func()) {
	s.mu.Lock()
	s.OnRetryInit = fn
	s.mu.Unlock()
}

// SetSites replaces the site list (thread-safe, for use from main).
func (s *UIState) SetSites(sites []types.Site) {
	s.mu.Lock()
	s.Sites = sites
	s.mu.Unlock()
	s.Invalidate()
}
