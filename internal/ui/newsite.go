package ui

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/types"
)

var (
	phpVersions   = []string{"8.4", "8.3", "8.2", "8.1", "8.0", "7.4"}
	mysqlVersions = []string{"8.4", "8.0"}
	redisVersions = []string{"8.0", "7.4", "7.2"}
)

type NewSiteModal struct {
	state  *UIState
	sm     *sites.SiteManager
	toasts *ToastManager

	// Form fields
	nameEditor   widget.Editor
	publicEditor widget.Editor
	filesDirVal  string

	// Dropdowns
	phpDropdown       *Dropdown
	mysqlDropdown     *Dropdown
	redisDropdown     *Dropdown
	webServerDropdown *Dropdown
	multisiteDropdown *Dropdown

	// Buttons
	browseDirBtn widget.Clickable
	createBtn    widget.Clickable
	cancelBtn    widget.Clickable
}

func NewNewSiteModal(state *UIState, sm *sites.SiteManager, toasts *ToastManager) *NewSiteModal {
	webServerOptions := []string{"nginx", "apache"}
	multisiteOptions := []string{"Single Site", "Multisite (Subdirectory)", "Multisite (Subdomain)"}

	m := &NewSiteModal{
		state:             state,
		sm:                sm,
		toasts:            toasts,
		phpDropdown:       NewDropdown(phpVersions),
		mysqlDropdown:     NewDropdown(mysqlVersions),
		redisDropdown:     NewDropdown(redisVersions),
		webServerDropdown: NewDropdown(webServerOptions),
		multisiteDropdown: NewDropdown(multisiteOptions),
	}
	m.nameEditor.SingleLine = true
	m.publicEditor.SingleLine = true
	m.publicEditor.SetText("/")
	return m
}

func (m *NewSiteModal) Layout(gtx layout.Context, th *material.Theme) layout.Dimensions {
	// Handle button clicks
	if m.cancelBtn.Clicked(gtx) {
		m.state.SetShowNewSiteModal(false)
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

	if m.createBtn.Clicked(gtx) {
		name := m.nameEditor.Text()
		filesDir := m.filesDirVal
		publicDir := m.publicEditor.Text()
		phpVer := phpVersions[m.phpDropdown.Selected]
		mysqlVer := mysqlVersions[m.mysqlDropdown.Selected]
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
					MySQLVersion: mysqlVer,
					RedisVersion: redisVer,
					WebServer:    webServer,
					Multisite:    multisite,
				}
				if err := m.sm.AddSite(site); err != nil {
					m.state.ShowError("Failed to create site: " + err.Error())
					return
				}

				m.state.SetShowNewSiteModal(false)

				// Reset form
				m.nameEditor.SetText("")
				m.filesDirVal = ""
				m.publicEditor.SetText("/")
				m.phpDropdown.Selected = 0
				m.mysqlDropdown.Selected = 0
				m.redisDropdown.Selected = 0
				m.webServerDropdown.Selected = 0
				m.multisiteDropdown.Selected = 0

				m.state.Invalidate()
			}()
		}
	}

	return ModalOverlay(gtx, func(gtx layout.Context) layout.Dimensions {
		return m.layoutForm(gtx, th)
	})
}

func (m *NewSiteModal) layoutForm(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// Title
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.H5(th, "Create New Site")
			return layout.Inset{Bottom: unit.Dp(20)}.Layout(gtx, lbl.Layout)
		}),
		// Site Name
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: SpaceMD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return LabeledInput(gtx, th, "Site Name", &m.nameEditor, "My WordPress Site")
			})
		}),
		// Files Dir
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: SpaceMD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return m.layoutDirPicker(gtx, th)
			})
		}),
		// Public Dir
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: SpaceMD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return LabeledInput(gtx, th, "Public Dir", &m.publicEditor, "/")
			})
		}),
		// Web Server
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: SpaceMD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return m.webServerDropdown.Layout(gtx, th, "Web Server")
			})
		}),
		// PHP Version
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: SpaceMD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return m.phpDropdown.Layout(gtx, th, "PHP Version")
			})
		}),
		// Database Version
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: SpaceMD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return m.mysqlDropdown.Layout(gtx, th, "Database Version")
			})
		}),
		// Redis Version
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: SpaceMD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
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
					return layout.Inset{Right: SpaceSM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
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

func (m *NewSiteModal) layoutDirPicker(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th, "Files Dir")
			lbl.Color = ColorGray700
			return layout.Inset{Bottom: SpaceXS}.Layout(gtx, lbl.Layout)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: SpaceSM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return SecondaryButton(gtx, th, &m.browseDirBtn, "Choose directory...")
					})
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					dirText := m.filesDirVal
					if dirText == "" {
						dirText = "No directory selected"
					}
					lbl := material.Body2(th, dirText)
					lbl.Color = ColorGray500
					lbl.TextSize = TextSM
					return lbl.Layout(gtx)
				}),
			)
		}),
	)
}
