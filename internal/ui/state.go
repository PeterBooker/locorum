package ui

import (
	"sync"

	"gioui.org/app"

	"github.com/PeterBooker/locorum/internal/types"
)

type UIState struct {
	mu sync.Mutex

	// Site data
	Sites        []types.Site
	SelectedID   string
	SearchTerm   string

	// Modal state
	ShowNewSiteModal bool

	// Loading state
	SiteToggling map[string]bool // site ID -> whether start/stop is in progress

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
