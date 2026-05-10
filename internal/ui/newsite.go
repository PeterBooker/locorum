package ui

import (
	"sync"
	"time"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/dbengine"
	"github.com/PeterBooker/locorum/internal/platform"
	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/types"
)

var (
	phpVersions   = []string{"8.4", "8.3", "8.2", "8.1", "8.0", "7.4"}
	redisVersions = []string{"8.0", "7.4", "7.2"}
)

// dbEngineOptions / dbEngineKinds are the parallel slices the engine
// dropdown reads. Display names are user-facing; kinds are persisted.
var (
	dbEngineOptions = []string{"MySQL", "MariaDB"}
	dbEngineKinds   = []dbengine.Kind{dbengine.MySQL, dbengine.MariaDB}
)

// dbVersionsFor returns the version dropdown options for an engine kind.
// New engines drop in by extending dbEngineOptions / dbEngineKinds — the
// version list comes straight from the engine.
func dbVersionsFor(k dbengine.Kind) []string {
	return dbengine.MustFor(k).KnownVersions()
}

type NewSiteModal struct {
	state  *UIState
	sm     *sites.SiteManager
	toasts *Notifications

	// Form fields
	nameEditor   widget.Editor
	publicEditor widget.Editor
	filesDirVal  string

	// Dropdowns
	phpDropdown       *Dropdown
	dbEngineDropdown  *Dropdown
	dbVersionDropdown *Dropdown
	redisDropdown     *Dropdown
	webServerDropdown *Dropdown
	multisiteDropdown *Dropdown

	// dbVersions tracks the version list currently shown in the
	// dbVersionDropdown — it changes when the user picks a different
	// engine, so we cache the visible slice for the click handler.
	dbVersions []string

	// Buttons
	browseDirBtn widget.Clickable
	createBtn    widget.Clickable
	cancelBtn    widget.Clickable
	closeBtn     widget.Clickable

	keys *ModalFocus
	anim *modalShowState

	// Path-validation cache. The Layout pass reads notes; HandleUserInteractions
	// debounces on the FilesDir value and re-runs ValidateSitePath off
	// the goroutine started by the debounce timer. The mutex covers both.
	pathMu      sync.Mutex
	pathNotes   []sites.PathNote
	pathTarget  string      // last value we ran validation against
	pathTimer   *time.Timer // active debounce timer; nil between runs
	pathPending string      // value to validate when the timer fires
}

func NewNewSiteModal(state *UIState, sm *sites.SiteManager, toasts *Notifications) *NewSiteModal {
	webServerOptions := []string{"nginx", "apache"}
	multisiteOptions := []string{"Single Site", "Multisite (Subdirectory)", "Multisite (Subdomain)"}

	// Pull global defaults so the form opens pre-filled with the
	// user's last saved preferences. Falling back to the index 0 of
	// each list keeps behaviour identical for first-run users.
	cfg := sm.Config()
	engineIdx := 0
	if cfg != nil {
		engineIdx = indexOfDBEngine(cfg.DBEngineDefault(), dbEngineKinds)
	}
	versions := dbVersionsFor(dbEngineKinds[engineIdx])

	m := &NewSiteModal{
		state:             state,
		sm:                sm,
		toasts:            toasts,
		phpDropdown:       NewDropdown(phpVersions),
		dbEngineDropdown:  NewDropdown(dbEngineOptions),
		dbVersionDropdown: NewDropdown(versions),
		dbVersions:        versions,
		redisDropdown:     NewDropdown(redisVersions),
		webServerDropdown: NewDropdown(webServerOptions),
		multisiteDropdown: NewDropdown(multisiteOptions),
		keys:              NewModalFocus(),
		anim:              NewModalAnim(),
	}
	m.dbEngineDropdown.Selected = engineIdx
	if cfg != nil {
		m.phpDropdown.Selected = indexOfOr(phpVersions, cfg.PHPVersionDefault(), 0)
		m.dbVersionDropdown.Selected = indexOfOr(versions, cfg.DBVersionDefault(), 0)
		m.redisDropdown.Selected = indexOfOr(redisVersions, cfg.RedisVersionDefault(), 0)
		m.webServerDropdown.Selected = indexOfOr(webServerOptions, cfg.WebServerDefault(), 0)
	}

	m.nameEditor.SingleLine = true
	m.publicEditor.SingleLine = true
	m.publicEditor.SetText("/")
	return m
}

// indexOfOr returns the index of value in options, or fallback if value
// is empty or not present.
func indexOfOr(options []string, value string, fallback int) int {
	if value == "" {
		return fallback
	}
	for i, o := range options {
		if o == value {
			return i
		}
	}
	return fallback
}

// indexOfDBEngine maps a persisted engine name onto the index in
// kinds. Returns 0 when the name is unknown — keeps the dropdown valid
// even after a future engine is removed.
func indexOfDBEngine(name string, kinds []dbengine.Kind) int {
	for i, k := range kinds {
		if string(k) == name {
			return i
		}
	}
	return 0
}

// HandleUserInteractions processes Cancel / Browse / Create button clicks.
// Called by the root UI before Layout when the modal is visible.
func (m *NewSiteModal) HandleUserInteractions(gtx layout.Context) {
	keys := ProcessModalKeys(gtx, m.keys.Tag)

	// Debounced path validation. Re-arm the timer whenever the field
	// value moves; the validator runs at most once per 250 ms regardless
	// of typing speed.
	m.scheduleValidation(m.filesDirVal)

	if m.cancelBtn.Clicked(gtx) || m.closeBtn.Clicked(gtx) || keys.Escape {
		m.state.SetShowNewSiteModal(false)
		m.keys.OnHide()
		m.anim.Hide()
		return
	}

	if m.browseDirBtn.Clicked(gtx) {
		go func() {
			dir, err := m.sm.PickDirectory()
			if err == nil && dir != "" {
				m.state.mu.Lock()
				m.filesDirVal = dir
				m.state.mu.Unlock()
				m.state.Invalidate()
			}
		}()
	}

	// Sync the version dropdown to the selected engine. Re-running this
	// each frame is cheap and means the user sees the right options
	// without an explicit "engine changed" event.
	wantVersions := dbVersionsFor(dbEngineKinds[m.dbEngineDropdown.Selected])
	if !slicesEqual(wantVersions, m.dbVersions) {
		m.dbVersions = wantVersions
		m.dbVersionDropdown = NewDropdown(wantVersions)
	}

	if m.createBtn.Clicked(gtx) || keys.Enter {
		name := m.nameEditor.Text()
		filesDir := m.filesDirVal
		publicDir := m.publicEditor.Text()
		phpVer := phpVersions[m.phpDropdown.Selected]
		dbEngine := dbEngineKinds[m.dbEngineDropdown.Selected]
		dbVer := m.dbVersions[m.dbVersionDropdown.Selected]
		redisVer := redisVersions[m.redisDropdown.Selected]
		webServer := []string{"nginx", "apache"}[m.webServerDropdown.Selected]
		multisiteMap := []string{"", "subdirectory", "subdomain"}
		multisite := multisiteMap[m.multisiteDropdown.Selected]

		// Refuse submit when the path picked up a hard-blocking
		// note (today: Windows MAX_PATH without LongPathsEnabled).
		// AddSite enforces the same rule for scripted callers; the
		// in-form check is a UX aid so the user gets immediate
		// feedback instead of an error toast after the goroutine
		// fires.
		notes := m.pathNotesSnapshot()

		switch {
		case name == "":
			m.state.ShowError("Site name is required")
		case filesDir == "":
			m.state.ShowError("Files directory is required")
		case sites.HasBlockingNote(notes):
			m.state.ShowError("Choose a shorter path; this one exceeds Windows' 260-character limit.")
		default:
			go func() {
				site := types.Site{
					Name:         name,
					FilesDir:     filesDir,
					PublicDir:    publicDir,
					PHPVersion:   phpVer,
					DBEngine:     string(dbEngine),
					DBVersion:    dbVer,
					RedisVersion: redisVer,
					WebServer:    webServer,
					Multisite:    multisite,
				}
				if err := m.sm.AddSite(site); err != nil {
					m.state.ShowError("Failed to create site: " + err.Error())
					return
				}

				// Remember these choices as the new defaults for
				// next time. Errors are logged via the ShowError
				// path elsewhere; failure here is non-fatal — the
				// site was already created.
				if cfg := m.sm.Config(); cfg != nil {
					_ = cfg.SetPHPVersionDefault(phpVer)
					_ = cfg.SetDBEngineDefault(string(dbEngine))
					_ = cfg.SetDBVersionDefault(dbVer)
					_ = cfg.SetRedisVersionDefault(redisVer)
					_ = cfg.SetWebServerDefault(webServer)
				}

				m.state.SetShowNewSiteModal(false)
				m.keys.OnHide()
				m.anim.Hide()

				// Reset form
				m.nameEditor.SetText("")
				m.filesDirVal = ""
				m.publicEditor.SetText("/")
				m.phpDropdown.Selected = 0
				m.dbEngineDropdown.Selected = 0
				m.dbVersions = dbVersionsFor(dbEngineKinds[0])
				m.dbVersionDropdown = NewDropdown(m.dbVersions)
				m.redisDropdown.Selected = 0
				m.webServerDropdown.Selected = 0
				m.multisiteDropdown.Selected = 0

				m.state.Invalidate()
			}()
		}
	}
}

// scheduleValidation arms (or re-arms) the path-debounce timer. Pure-Go
// validation runs in microseconds, but we still debounce so the UI doesn't
// feel chatty during a paste. After 250 ms of quiescence the timer fires;
// the goroutine recomputes notes off the UI loop and stores them, then
// invalidates so Layout picks up the new content.
func (m *NewSiteModal) scheduleValidation(target string) {
	m.pathMu.Lock()
	defer m.pathMu.Unlock()

	if target == m.pathTarget {
		return
	}
	m.pathPending = target
	if m.pathTimer != nil {
		m.pathTimer.Reset(250 * time.Millisecond)
		return
	}
	m.pathTimer = time.AfterFunc(250*time.Millisecond, func() {
		m.pathMu.Lock()
		val := m.pathPending
		m.pathTimer = nil
		m.pathMu.Unlock()

		var info *platform.Info
		if platform.IsInitialized() {
			info = platform.Get()
		}
		notes := sites.ValidateSitePath(val, info)

		m.pathMu.Lock()
		m.pathNotes = notes
		m.pathTarget = val
		m.pathMu.Unlock()

		m.state.Invalidate()
	})
}

// pathNotesSnapshot returns the current notes for the layout pass.
func (m *NewSiteModal) pathNotesSnapshot() []sites.PathNote {
	m.pathMu.Lock()
	defer m.pathMu.Unlock()
	out := make([]sites.PathNote, len(m.pathNotes))
	copy(out, m.pathNotes)
	return out
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (m *NewSiteModal) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	m.anim.Show()
	return AnimatedModalOverlay(gtx, th, m.anim, func(gtx layout.Context) layout.Dimensions {
		m.keys.Layout(gtx)
		return m.layoutForm(gtx, th)
	})
}

func (m *NewSiteModal) layoutForm(gtx layout.Context, th *Theme) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// Title + close
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(20)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(material.H5(th.Theme, "Create New Site").Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Dimensions{Size: gtx.Constraints.Min}
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return material.Clickable(gtx, &m.closeBtn, func(gtx layout.Context) layout.Dimensions {
							return RoundedFill(gtx, th.Color.Bg1, th.Radii.R2, func(gtx layout.Context) layout.Dimensions {
								return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return IconClose(gtx, th, unit.Dp(16), th.Color.Fg2)
								})
							})
						})
					}),
				)
			})
		}),
		// Site Name
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return LabeledInput(gtx, th, "Site Name", &m.nameEditor, "My WordPress Site")
			})
		}),
		// Files Dir
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return m.layoutDirPicker(gtx, th)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return m.layoutPathNotes(gtx, th)
					}),
				)
			})
		}),
		// Public Dir
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return LabeledInput(gtx, th, "Public Dir", &m.publicEditor, "/")
			})
		}),
		// Web Server
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return m.webServerDropdown.Layout(gtx, th, "Web Server")
			})
		}),
		// PHP Version
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return m.phpDropdown.Layout(gtx, th, "PHP Version")
			})
		}),
		// Database Engine
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return m.dbEngineDropdown.Layout(gtx, th, "Database Engine")
			})
		}),
		// Database Version
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return m.dbVersionDropdown.Layout(gtx, th, "Database Version")
			})
		}),
		// Redis Version
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return m.redisDropdown.Layout(gtx, th, "Redis Version")
			})
		}),
		// Multisite
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(20)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return m.multisiteDropdown.Layout(gtx, th, "Multisite")
			})
		}),
		// Buttons row
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceStart}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return SecondaryButton(gtx, th, &m.cancelBtn, "Cancel")
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return PrimaryButton(gtx, th, &m.createBtn, "Create")
				}),
			)
		}),
	)
}

// layoutPathNotes renders the inline validation notes beneath the
// directory picker. Empty when there's nothing to warn about. Blocker
// notes render with the error palette and an extra remediation line so
// the user knows the form's Create button won't fire.
func (m *NewSiteModal) layoutPathNotes(gtx layout.Context, th *Theme) layout.Dimensions {
	notes := m.pathNotesSnapshot()
	if len(notes) == 0 {
		return layout.Dimensions{}
	}
	children := make([]layout.FlexChild, 0, len(notes))
	for _, n := range notes {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			bg, fg := th.Color.WarnSoft, th.Color.Warn
			if n.Severity == sites.PathSeverityBlock {
				bg, fg = th.Color.ErrSoft, th.Color.Err
			}
			return layout.Inset{Top: th.Spacing.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return RoundedFill(gtx, bg, th.Radii.R2, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								lbl := material.Body2(th.Theme, n.Title)
								lbl.Color = fg
								lbl.TextSize = th.Sizes.Body
								lbl.Font.Weight = font.SemiBold
								return lbl.Layout(gtx)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if n.Detail == "" {
									return layout.Dimensions{}
								}
								lbl := material.Body2(th.Theme, n.Detail)
								lbl.Color = th.Color.Fg2
								lbl.TextSize = th.Sizes.Body
								return layout.Inset{Top: unit.Dp(2)}.Layout(gtx, lbl.Layout)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if n.Remediation == "" {
									return layout.Dimensions{}
								}
								lbl := material.Body2(th.Theme, "→ "+n.Remediation)
								lbl.Color = th.Color.Fg2
								lbl.TextSize = th.Sizes.Mono
								return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, lbl.Layout)
							}),
						)
					})
				})
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func (m *NewSiteModal) layoutDirPicker(gtx layout.Context, th *Theme) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, "Files Dir")
			lbl.Color = th.Color.TextStrong
			return layout.Inset{Bottom: th.Spacing.XS}.Layout(gtx, lbl.Layout)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return SecondaryButton(gtx, th, &m.browseDirBtn, "Choose directory...")
					})
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					dirText := m.filesDirVal
					if dirText == "" {
						dirText = "No directory selected"
					}
					lbl := material.Body2(th.Theme, dirText)
					lbl.Color = th.Color.TextSecondary
					lbl.TextSize = th.Sizes.SM
					return lbl.Layout(gtx)
				}),
			)
		}),
	)
}
