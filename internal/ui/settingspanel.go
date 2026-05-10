package ui

import (
	"strconv"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/dbengine"
	"github.com/PeterBooker/locorum/internal/sites"
)

// SettingsPanel takes over columns 2+3 when the nav rail's Settings item
// is active. Sections (top to bottom):
//
//   - System Health:    runner findings + re-check.
//   - Appearance:       theme picker (System / Light / Dark).
//   - New site defaults: pre-fill values for the new-site modal.
//   - Network & TLS:    router HTTP/HTTPS host ports + mkcert path.
//
// Each section reads from sm.Config() at construction time and pushes
// validated changes back through the typed setters. Validation errors
// surface as a transient toast — never an uncaught panic.
type SettingsPanel struct {
	state           *UIState
	sm              *sites.SiteManager
	onThemeChange   func(ThemeMode)
	healthPanel     *HealthPanel
	diagnosticPanel *DiagnosticsPanel

	themeEnum widget.Enum
	syncedTo  string // remembers which value we last seeded from settings

	// Section: New site defaults — dropdowns. Live in this struct so
	// they keep widget state across frames.
	defaultPHP    *Dropdown
	defaultEngine *Dropdown
	defaultDBVer  *Dropdown
	defaultRedis  *Dropdown
	defaultWeb    *Dropdown
	publishDBPort widget.Bool
	defaultDBList []string // matches defaultDBVer.Options each frame

	// Section: Network & TLS — text inputs + an explicit Save button.
	// Persisting these on every keystroke would briefly accept an
	// invalid intermediate (e.g. port "8" while typing "80"); the
	// button lets the user finish typing first.
	httpPortEditor   widget.Editor
	httpsPortEditor  widget.Editor
	mkcertPathEditor widget.Editor
	networkSaveBtn   widget.Clickable

	// Last-applied values — used to detect a real change before
	// hitting storage on every frame.
	lastPHP, lastEngine, lastDBVer string
	lastRedis, lastWeb             string
	lastPublishDBPort              bool
}

// NewSettingsPanel constructs a SettingsPanel. onThemeChange is invoked
// whenever the user changes the theme mode (e.g. to apply + persist).
func NewSettingsPanel(state *UIState, sm *sites.SiteManager, onThemeChange func(ThemeMode)) *SettingsPanel {
	s := &SettingsPanel{state: state, sm: sm, onThemeChange: onThemeChange}

	cfg := sm.Config()
	if cfg != nil {
		s.themeEnum.Value = cfg.ThemeMode()
	} else {
		s.themeEnum.Value = ThemeSystem.String()
	}
	s.syncedTo = s.themeEnum.Value

	// New-site defaults: same option lists as the new-site modal so
	// changes here pre-select the same values.
	s.defaultPHP = NewDropdown(phpVersions)
	s.defaultEngine = NewDropdown(dbEngineOptions)
	engineKind := dbengine.MySQL
	dbVerOptions := dbengine.MustFor(engineKind).KnownVersions()
	s.defaultDBVer = NewDropdown(dbVerOptions)
	s.defaultDBList = dbVerOptions
	s.defaultRedis = NewDropdown(redisVersions)
	s.defaultWeb = NewDropdown([]string{"nginx", "apache"})

	if cfg != nil {
		s.defaultPHP.Selected = indexOfOr(phpVersions, cfg.PHPVersionDefault(), 0)
		s.defaultEngine.Selected = indexOfDBEngine(cfg.DBEngineDefault(), dbEngineKinds)
		dbVerOptions = dbengine.MustFor(dbEngineKinds[s.defaultEngine.Selected]).KnownVersions()
		s.defaultDBVer = NewDropdown(dbVerOptions)
		s.defaultDBList = dbVerOptions
		s.defaultDBVer.Selected = indexOfOr(dbVerOptions, cfg.DBVersionDefault(), 0)
		s.defaultRedis.Selected = indexOfOr(redisVersions, cfg.RedisVersionDefault(), 0)
		s.defaultWeb.Selected = indexOfOr([]string{"nginx", "apache"}, cfg.WebServerDefault(), 0)
		s.publishDBPort.Value = cfg.PublishDBPortDefault()

		s.httpPortEditor.SingleLine = true
		s.httpsPortEditor.SingleLine = true
		s.mkcertPathEditor.SingleLine = true
		s.httpPortEditor.Filter = "0123456789"
		s.httpsPortEditor.Filter = "0123456789"
		s.httpPortEditor.SetText(strconv.Itoa(cfg.RouterHTTPPort()))
		s.httpsPortEditor.SetText(strconv.Itoa(cfg.RouterHTTPSPort()))
		s.mkcertPathEditor.SetText(cfg.MkcertPath())

		// Seed last-applied so we don't fire spurious Set calls on the
		// first frame.
		s.lastPHP = cfg.PHPVersionDefault()
		s.lastEngine = cfg.DBEngineDefault()
		s.lastDBVer = cfg.DBVersionDefault()
		s.lastRedis = cfg.RedisVersionDefault()
		s.lastWeb = cfg.WebServerDefault()
		s.lastPublishDBPort = cfg.PublishDBPortDefault()
	}

	return s
}

// SetHealthPanel attaches the system-health panel renderer. Optional —
// callers that don't wire a runner (early startup, tests) can leave it
// nil and the section is omitted from the layout.
func (s *SettingsPanel) SetHealthPanel(hp *HealthPanel) { s.healthPanel = hp }

// SetDiagnosticsPanel wires the Diagnostics card. Optional — when nil
// the card is omitted from the layout.
func (s *SettingsPanel) SetDiagnosticsPanel(dp *DiagnosticsPanel) { s.diagnosticPanel = dp }

// DiagnosticsPanel returns the wired panel (or nil). Used by main.go
// to fill in the §7.4 and §7.6 sub-cards once their subsystems exist.
func (s *SettingsPanel) DiagnosticsPanel() *DiagnosticsPanel { return s.diagnosticPanel }

// HandleUserInteractions watches the theme picker for selection
// changes, syncs the engine→version dropdown, and persists any changed
// defaults.
func (s *SettingsPanel) HandleUserInteractions(gtx layout.Context) {
	if s.themeEnum.Update(gtx) {
		if s.themeEnum.Value != s.syncedTo {
			s.syncedTo = s.themeEnum.Value
			if s.onThemeChange != nil {
				s.onThemeChange(ParseThemeMode(s.themeEnum.Value))
			}
		}
	}
	s.syncEngineVersionList()
	s.persistDefaults(gtx)
	if s.healthPanel != nil {
		s.healthPanel.HandleUserInteractions(gtx)
	}
	if s.diagnosticPanel != nil {
		s.diagnosticPanel.HandleUserInteractions(gtx)
	}
}

// syncEngineVersionList updates the DB-version dropdown options when
// the engine selection changes. Done in one place so the version index
// resets to 0 (a valid pick on every engine) on engine change.
func (s *SettingsPanel) syncEngineVersionList() {
	if s.defaultEngine == nil {
		return
	}
	want := dbengine.MustFor(dbEngineKinds[s.defaultEngine.Selected]).KnownVersions()
	if !slicesEqual(want, s.defaultDBList) {
		s.defaultDBList = want
		s.defaultDBVer = NewDropdown(want)
	}
}

// persistDefaults writes any changed value to cfg. Called every frame
// — but only takes the cache-write path when a value actually moved
// since the last frame. Validation errors surface as a transient
// toast.  The shape here is "I noticed you changed X; persist it"
// rather than an explicit "Save" button — matches the rest of
// Locorum's settings story (theme, fail-on-error switches) which save
// on edit.
func (s *SettingsPanel) persistDefaults(gtx layout.Context) {
	cfg := s.sm.Config()
	if cfg == nil {
		return
	}

	// Dropdown selections.
	if php := phpVersions[s.defaultPHP.Selected]; php != s.lastPHP {
		s.lastPHP = php
		if err := cfg.SetPHPVersionDefault(php); err != nil {
			s.state.ShowError("PHP default: " + err.Error())
		}
	}
	engKind := string(dbEngineKinds[s.defaultEngine.Selected])
	if engKind != s.lastEngine {
		s.lastEngine = engKind
		if err := cfg.SetDBEngineDefault(engKind); err != nil {
			s.state.ShowError("DB engine default: " + err.Error())
		}
	}
	if len(s.defaultDBList) > 0 {
		dbVer := s.defaultDBList[s.defaultDBVer.Selected]
		if dbVer != s.lastDBVer {
			s.lastDBVer = dbVer
			if err := cfg.SetDBVersionDefault(dbVer); err != nil {
				s.state.ShowError("DB version default: " + err.Error())
			}
		}
	}
	if redis := redisVersions[s.defaultRedis.Selected]; redis != s.lastRedis {
		s.lastRedis = redis
		if err := cfg.SetRedisVersionDefault(redis); err != nil {
			s.state.ShowError("Redis default: " + err.Error())
		}
	}
	web := []string{"nginx", "apache"}[s.defaultWeb.Selected]
	if web != s.lastWeb {
		s.lastWeb = web
		if err := cfg.SetWebServerDefault(web); err != nil {
			s.state.ShowError("Web server default: " + err.Error())
		}
	}
	if s.publishDBPort.Update(gtx) && s.publishDBPort.Value != s.lastPublishDBPort {
		s.lastPublishDBPort = s.publishDBPort.Value
		if err := cfg.SetPublishDBPortDefault(s.publishDBPort.Value); err != nil {
			s.state.ShowError("Publish DB port default: " + err.Error())
		}
	}

	// Text inputs commit on the explicit Save button — see
	// applyNetworkSettings.
	if s.networkSaveBtn.Clicked(gtx) {
		s.applyNetworkSettings(cfg)
	}
}

// applyNetworkSettings parses, validates, and persists the Network &
// TLS section. Errors surface as toasts; valid changes are written
// through the typed setters.  Idempotent — unchanged values do not hit
// the underlying store.
func (s *SettingsPanel) applyNetworkSettings(cfg networkSettingsWriter) {
	httpPort, err := strconv.Atoi(s.httpPortEditor.Text())
	if err != nil {
		s.state.ShowError("HTTP port: must be an integer")
		return
	}
	httpsPort, err := strconv.Atoi(s.httpsPortEditor.Text())
	if err != nil {
		s.state.ShowError("HTTPS port: must be an integer")
		return
	}
	if httpPort == httpsPort {
		s.state.ShowError("HTTP and HTTPS ports must differ")
		return
	}
	if err := cfg.SetRouterHTTPPort(httpPort); err != nil {
		s.state.ShowError("HTTP port: " + err.Error())
		return
	}
	if err := cfg.SetRouterHTTPSPort(httpsPort); err != nil {
		s.state.ShowError("HTTPS port: " + err.Error())
		return
	}
	if err := cfg.SetMkcertPath(s.mkcertPathEditor.Text()); err != nil {
		s.state.ShowError("mkcert path: " + err.Error())
		return
	}
	// Note: changing router ports here updates intent only; the
	// running router container keeps its current bindings until the
	// app is restarted. We deliberately don't restart the router
	// in-process because dropping :80/:443 mid-session would break
	// every running site at once.
}

// networkSettingsWriter is the subset of *config.Config the network
// section uses. Splitting it into an interface keeps applyNetworkSettings
// trivially testable.
type networkSettingsWriter interface {
	SetRouterHTTPPort(int) error
	SetRouterHTTPSPort(int) error
	SetMkcertPath(string) error
}

func (s *SettingsPanel) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	return FillBackground(gtx, th.Color.Bg, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(28)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Bottom: th.Spacing.LG}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th.Theme, "Settings")
						lbl.Color = th.Color.Fg
						lbl.TextSize = th.Sizes.H1
						lbl.Font.Weight = font.SemiBold
						return lbl.Layout(gtx)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if s.healthPanel == nil {
						return layout.Dimensions{}
					}
					return s.healthPanel.Layout(gtx, th)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return s.layoutAppearance(gtx, th)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return s.layoutDefaults(gtx, th)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return s.layoutNetworkAndTLS(gtx, th)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if s.diagnosticPanel == nil {
						return layout.Dimensions{}
					}
					return s.diagnosticPanel.Layout(gtx, th)
				}),
			)
		})
	})
}

// layoutDefaults renders the "New site defaults" card. Each dropdown
// drives the same option list the new-site modal shows, so changes
// here pre-select the same values when the user opens that modal.
func (s *SettingsPanel) layoutDefaults(gtx layout.Context, th *Theme) layout.Dimensions {
	return panel(gtx, th, "New site defaults", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, "Pre-fill new sites with these versions and options.")
				lbl.Color = th.Color.Fg2
				lbl.TextSize = th.Sizes.Body
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return s.defaultPHP.Layout(gtx, th, "PHP Version")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return s.defaultEngine.Layout(gtx, th, "Database Engine")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return s.defaultDBVer.Layout(gtx, th, "Database Version")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return s.defaultRedis.Layout(gtx, th, "Redis Version")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return s.defaultWeb.Layout(gtx, th, "Web Server")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				cb := material.CheckBox(th.Theme, &s.publishDBPort, "Publish DB host port by default")
				cb.Color = th.Color.Fg
				cb.IconColor = th.Color.Accent
				cb.Size = unit.Dp(20)
				cb.TextSize = th.Sizes.Body
				return cb.Layout(gtx)
			}),
		)
	})
}

// layoutNetworkAndTLS renders the "Network & TLS" card. Three text
// inputs and a Save button. Port edits do NOT take effect until the
// next app restart (see applyNetworkSettings).
func (s *SettingsPanel) layoutNetworkAndTLS(gtx layout.Context, th *Theme) layout.Dimensions {
	return panel(gtx, th, "Network & TLS", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, "Router host ports and the path to your mkcert binary. Port changes take effect on next launch.")
				lbl.Color = th.Color.Fg2
				lbl.TextSize = th.Sizes.Body
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return LabeledInput(gtx, th, "HTTP port", &s.httpPortEditor, "80")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return LabeledInput(gtx, th, "HTTPS port", &s.httpsPortEditor, "443")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return LabeledInput(gtx, th, "mkcert path (leave blank to autodetect)", &s.mkcertPathEditor, "/usr/local/bin/mkcert")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return PrimaryButton(gtx, th, &s.networkSaveBtn, "Save")
			}),
		)
	})
}

func (s *SettingsPanel) layoutAppearance(gtx layout.Context, th *Theme) layout.Dimensions {
	return panel(gtx, th, "Appearance", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, "Choose how Locorum looks to you.")
				lbl.Color = th.Color.Fg2
				lbl.TextSize = th.Sizes.Body
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return s.layoutThemeOption(gtx, th, ThemeSystem.String(), "Follow system", "Match your OS appearance setting.")
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return s.layoutThemeOption(gtx, th, ThemeLight.String(), "Light", "Bright surfaces with high contrast.")
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return s.layoutThemeOption(gtx, th, ThemeDark.String(), "Dark", "Deep neutral grays for low-light work.")
			}),
		)
	})
}

func (s *SettingsPanel) layoutThemeOption(gtx layout.Context, th *Theme, key, title, desc string) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		rb := material.RadioButton(th.Theme, &s.themeEnum, key, title)
		rb.Color = th.Color.Fg
		rb.IconColor = th.Color.Accent
		rb.TextSize = th.Sizes.Body
		rb.Size = unit.Dp(20)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(rb.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, desc)
				lbl.Color = th.Color.Fg3
				lbl.TextSize = th.Sizes.Mono
				return layout.Inset{Top: unit.Dp(2), Left: unit.Dp(28)}.Layout(gtx, lbl.Layout)
			}),
		)
	})
}
