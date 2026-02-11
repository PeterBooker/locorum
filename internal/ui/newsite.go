package ui

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/types"
)

var (
	phpVersions   = []string{"8.4", "8.3", "8.2", "8.1", "8.0", "7.4"}
	mysqlVersions = []string{"8.4", "8.0"}
	redisVersions = []string{"8.0", "7.4", "7.2"}
)

type NewSiteModal struct {
	ui *UI

	// Form fields
	nameEditor   widget.Editor
	publicEditor widget.Editor
	filesDirVal  string

	// Dropdowns
	phpDropdown   *Dropdown
	mysqlDropdown *Dropdown
	redisDropdown *Dropdown

	// Buttons
	browseDirBtn widget.Clickable
	createBtn    widget.Clickable
	cancelBtn    widget.Clickable
}

func NewNewSiteModal(ui *UI) *NewSiteModal {
	m := &NewSiteModal{
		ui:            ui,
		phpDropdown:   NewDropdown(phpVersions),
		mysqlDropdown: NewDropdown(mysqlVersions),
		redisDropdown: NewDropdown(redisVersions),
	}
	m.nameEditor.SingleLine = true
	m.publicEditor.SingleLine = true
	m.publicEditor.SetText("public")
	return m
}

func (m *NewSiteModal) Layout(gtx layout.Context, th *material.Theme) layout.Dimensions {
	// Handle button clicks
	if m.cancelBtn.Clicked(gtx) {
		m.ui.State.mu.Lock()
		m.ui.State.ShowNewSiteModal = false
		m.ui.State.mu.Unlock()
	}

	if m.browseDirBtn.Clicked(gtx) {
		go func() {
			dir, err := m.ui.SM.PickDirectory()
			if err == nil && dir != "" {
				m.ui.State.mu.Lock()
				m.filesDirVal = dir
				m.ui.State.mu.Unlock()
				m.ui.State.Invalidate()
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

		go func() {
			site := types.Site{
				Name:         name,
				FilesDir:     filesDir,
				PublicDir:    publicDir,
				PHPVersion:   phpVer,
				MySQLVersion: mysqlVer,
				RedisVersion: redisVer,
			}
			_ = m.ui.SM.AddSite(site)

			m.ui.State.mu.Lock()
			m.ui.State.ShowNewSiteModal = false
			m.ui.State.mu.Unlock()

			// Reset form
			m.nameEditor.SetText("")
			m.filesDirVal = ""
			m.publicEditor.SetText("public")
			m.phpDropdown.Selected = 0
			m.mysqlDropdown.Selected = 0
			m.redisDropdown.Selected = 0

			m.ui.State.Invalidate()
		}()
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
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return LabeledInput(gtx, th, "Site Name", &m.nameEditor, "My WordPress Site")
			})
		}),
		// Files Dir
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return m.layoutDirPicker(gtx, th)
			})
		}),
		// Public Dir
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return LabeledInput(gtx, th, "Public Dir", &m.publicEditor, "public")
			})
		}),
		// PHP Version
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return m.phpDropdown.Layout(gtx, th, "PHP Version")
			})
		}),
		// Database Version
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return m.mysqlDropdown.Layout(gtx, th, "Database Version")
			})
		}),
		// Redis Version
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(20)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return m.redisDropdown.Layout(gtx, th, "Redis Version")
			})
		}),
		// Buttons row
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceStart}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
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
			return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, lbl.Layout)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
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
					lbl.TextSize = unit.Sp(13)
					return lbl.Layout(gtx)
				}),
			)
		}),
	)
}
