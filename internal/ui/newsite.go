package ui

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/dbengine"
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
}

func NewNewSiteModal(state *UIState, sm *sites.SiteManager, toasts *Notifications) *NewSiteModal {
	webServerOptions := []string{"nginx", "apache"}
	multisiteOptions := []string{"Single Site", "Multisite (Subdirectory)", "Multisite (Subdomain)"}

	defaultVersions := dbVersionsFor(dbEngineKinds[0])
	m := &NewSiteModal{
		state:             state,
		sm:                sm,
		toasts:            toasts,
		phpDropdown:       NewDropdown(phpVersions),
		dbEngineDropdown:  NewDropdown(dbEngineOptions),
		dbVersionDropdown: NewDropdown(defaultVersions),
		dbVersions:        defaultVersions,
		redisDropdown:     NewDropdown(redisVersions),
		webServerDropdown: NewDropdown(webServerOptions),
		multisiteDropdown: NewDropdown(multisiteOptions),
		keys:              NewModalFocus(),
		anim:              NewModalAnim(),
	}
	m.nameEditor.SingleLine = true
	m.publicEditor.SingleLine = true
	m.publicEditor.SetText("/")
	return m
}

// HandleUserInteractions processes Cancel / Browse / Create button clicks.
// Called by the root UI before Layout when the modal is visible.
func (m *NewSiteModal) HandleUserInteractions(gtx layout.Context) {
	keys := ProcessModalKeys(gtx, m.keys.Tag)

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

		if name == "" {
			m.state.ShowError("Site name is required")
		} else if filesDir == "" {
			m.state.ShowError("Files directory is required")
		} else {
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
				return m.layoutDirPicker(gtx, th)
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
