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
