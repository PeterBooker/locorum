package ui

import (
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/sqweek/dialog"

	"github.com/PeterBooker/locorum/internal/types"
)

type SiteDetail struct {
	ui        *UI
	startBtn  widget.Clickable
	stopBtn   widget.Clickable
	openFiles widget.Clickable
	exportBtn widget.Clickable
	list      widget.List

	// Log viewer
	logServiceDropdown *Dropdown
	logRefreshBtn      widget.Clickable
	logList            widget.List

	// WP-CLI
	wpcliEditor widget.Editor
	wpcliRunBtn widget.Clickable
	wpcliList   widget.List

	// Docker unavailable
	retryInitBtn widget.Clickable
}

func NewSiteDetail(ui *UI) *SiteDetail {
	sd := &SiteDetail{
		ui:                 ui,
		logServiceDropdown: NewDropdown([]string{"web", "php", "database", "redis"}),
	}
	sd.list.List.Axis = layout.Vertical
	sd.logList.List.Axis = layout.Vertical
	sd.wpcliList.List.Axis = layout.Vertical
	sd.wpcliEditor.SingleLine = true
	return sd
}

func (sd *SiteDetail) Layout(gtx layout.Context, th *material.Theme) layout.Dimensions {
	// Check for Docker initialization error.
	sd.ui.State.mu.Lock()
	initError := sd.ui.State.InitError
	sd.ui.State.mu.Unlock()

	if initError != "" {
		return sd.layoutInitError(gtx, th, initError)
	}

	sd.ui.State.mu.Lock()
	site := sd.ui.State.SelectedSite()
	sd.ui.State.mu.Unlock()

	if site == nil {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			lbl := material.H6(th, "Select a site from the sidebar")
			lbl.Color = ColorGray400
			return lbl.Layout(gtx)
		})
	}

	// Handle button clicks
	sd.handleClicks(gtx, site)

	return layout.Inset{
		Top:    unit.Dp(24),
		Bottom: unit.Dp(24),
		Left:   unit.Dp(32),
		Right:  unit.Dp(32),
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return material.List(th, &sd.list).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
			return sd.layoutContent(gtx, th, site)
		})
	})
}

func (sd *SiteDetail) layoutInitError(gtx layout.Context, th *material.Theme, errMsg string) layout.Dimensions {
	if sd.retryInitBtn.Clicked(gtx) {
		sd.ui.State.mu.Lock()
		retryFn := sd.ui.State.OnRetryInit
		sd.ui.State.mu.Unlock()
		if retryFn != nil {
			go retryFn()
		}
	}

	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.H5(th, "Docker Unavailable")
				lbl.Color = ColorRed600
				return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th, errMsg)
				lbl.Color = ColorGray500
				return layout.Inset{Bottom: unit.Dp(20)}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return PrimaryButton(gtx, th, &sd.retryInitBtn, "Retry")
			}),
		)
	})
}

func (sd *SiteDetail) handleClicks(gtx layout.Context, site *types.Site) {
	if sd.startBtn.Clicked(gtx) {
		siteID := site.ID
		sd.ui.State.mu.Lock()
		sd.ui.State.SiteToggling[siteID] = true
		sd.ui.State.mu.Unlock()
		sd.ui.State.Invalidate()

		go func() {
			if err := sd.ui.SM.StartSite(siteID); err != nil {
				sd.ui.State.ShowError("Failed to start site: " + err.Error())
			}
			sd.ui.State.mu.Lock()
			sd.ui.State.SiteToggling[siteID] = false
			sd.ui.State.mu.Unlock()
			sd.ui.State.Invalidate()
		}()
	}

	if sd.stopBtn.Clicked(gtx) {
		siteID := site.ID
		sd.ui.State.mu.Lock()
		sd.ui.State.SiteToggling[siteID] = true
		sd.ui.State.mu.Unlock()
		sd.ui.State.Invalidate()

		go func() {
			if err := sd.ui.SM.StopSite(siteID); err != nil {
				sd.ui.State.ShowError("Failed to stop site: " + err.Error())
			}
			sd.ui.State.mu.Lock()
			sd.ui.State.SiteToggling[siteID] = false
			sd.ui.State.mu.Unlock()
			sd.ui.State.Invalidate()
		}()
	}

	if sd.openFiles.Clicked(gtx) {
		siteID := site.ID
		go func() {
			if err := sd.ui.SM.OpenSiteFilesDir(siteID); err != nil {
				sd.ui.State.ShowError("Failed to open files directory: " + err.Error())
			}
		}()
	}

	if sd.exportBtn.Clicked(gtx) && site.Started {
		siteID := site.ID
		sd.ui.State.mu.Lock()
		sd.ui.State.ExportLoading = true
		sd.ui.State.mu.Unlock()
		sd.ui.State.Invalidate()

		go func() {
			defer func() {
				sd.ui.State.mu.Lock()
				sd.ui.State.ExportLoading = false
				sd.ui.State.mu.Unlock()
				sd.ui.State.Invalidate()
			}()

			dest, err := dialog.File().Filter("Tar archive", "tar.gz").Title("Export site").Save()
			if err != nil {
				if err.Error() != "Cancelled" {
					sd.ui.State.ShowError("Export cancelled: " + err.Error())
				}
				return
			}
			if !strings.HasSuffix(dest, ".tar.gz") {
				dest += ".tar.gz"
			}

			if err := sd.ui.SM.ExportSite(siteID, dest); err != nil {
				sd.ui.State.ShowError("Export failed: " + err.Error())
				return
			}
		}()
	}

	// Log viewer refresh
	if sd.logRefreshBtn.Clicked(gtx) && site.Started {
		siteID := site.ID
		service := sd.logServiceDropdown.Options[sd.logServiceDropdown.Selected]
		sd.ui.State.mu.Lock()
		sd.ui.State.LogLoading = true
		sd.ui.State.mu.Unlock()
		sd.ui.State.Invalidate()

		go func() {
			output, err := sd.ui.SM.GetContainerLogs(siteID, service, 100)
			sd.ui.State.mu.Lock()
			if err != nil {
				sd.ui.State.LogOutput = "Error: " + err.Error()
			} else {
				sd.ui.State.LogOutput = output
			}
			sd.ui.State.LogService = service
			sd.ui.State.LogLoading = false
			sd.ui.State.mu.Unlock()
			sd.ui.State.Invalidate()
		}()
	}

	// WP-CLI run
	if sd.wpcliRunBtn.Clicked(gtx) && site.Started {
		cmdText := sd.wpcliEditor.Text()
		if cmdText != "" {
			siteID := site.ID
			args := strings.Fields(cmdText)
			sd.ui.State.mu.Lock()
			sd.ui.State.WPCLILoading = true
			sd.ui.State.mu.Unlock()
			sd.ui.State.Invalidate()

			go func() {
				output, err := sd.ui.SM.ExecWPCLI(siteID, args)
				sd.ui.State.mu.Lock()
				if err != nil {
					if output != "" {
						sd.ui.State.WPCLIOutput = output + "\n\nError: " + err.Error()
					} else {
						sd.ui.State.WPCLIOutput = "Error: " + err.Error()
					}
				} else {
					sd.ui.State.WPCLIOutput = output
				}
				sd.ui.State.WPCLILoading = false
				sd.ui.State.mu.Unlock()
				sd.ui.State.Invalidate()
			}()
		}
	}
}

func (sd *SiteDetail) layoutContent(gtx layout.Context, th *material.Theme, site *types.Site) layout.Dimensions {
	sd.ui.State.mu.Lock()
	toggling := sd.ui.State.SiteToggling[site.ID]
	exporting := sd.ui.State.ExportLoading
	sd.ui.State.mu.Unlock()

	children := []layout.FlexChild{
		// Site name heading
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.H4(th, site.Name)
			return layout.Inset{Bottom: unit.Dp(16)}.Layout(gtx, lbl.Layout)
		}),
		// Controls row: start/stop + open files + export
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(24)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return sd.layoutControls(gtx, th, site, toggling, exporting)
			})
		}),
		// Site section
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sd.layoutSection(gtx, th, "Site", []kv{
				{"ID", site.ID},
				{"Slug", site.Slug},
				{"URL", "https://" + site.Domain},
				{"Files Dir", site.FilesDir},
				{"Public Dir", site.PublicDir},
				{"Created", site.CreatedAt},
				{"Updated", site.UpdatedAt},
			})
		}),
		// Versions section
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sd.layoutSection(gtx, th, "Versions", []kv{
				{"PHP", site.PHPVersion},
				{"MySQL", site.MySQLVersion},
				{"Redis", site.RedisVersion},
			})
		}),
		// Database section
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sd.layoutSection(gtx, th, "Database", []kv{
				{"Hostname", "locorum-" + site.Slug + "-database"},
				{"Database", "wordpress"},
				{"User", "wordpress"},
				{"Password", site.DBPassword},
			})
		}),
	}

	// Log viewer (only when started)
	if site.Started {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sd.layoutLogViewer(gtx, th)
		}))
	}

	// WP-CLI (only when started)
	if site.Started {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sd.layoutWPCLI(gtx, th)
		}))
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func (sd *SiteDetail) layoutControls(gtx layout.Context, th *material.Theme, site *types.Site, toggling, exporting bool) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceEnd}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if toggling {
				return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					loader := material.Loader(th)
					gtx.Constraints.Max.X = gtx.Dp(unit.Dp(36))
					gtx.Constraints.Max.Y = gtx.Dp(unit.Dp(36))
					return loader.Layout(gtx)
				})
			}
			if site.Started {
				b := material.Button(th, &sd.stopBtn, "Stop")
				b.Background = ColorRed600
				b.Color = ColorWhite
				b.CornerRadius = unit.Dp(6)
				b.TextSize = unit.Sp(14)
				return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, b.Layout)
			}
			b := material.Button(th, &sd.startBtn, "Start")
			b.Background = ColorGreen600
			b.Color = ColorWhite
			b.CornerRadius = unit.Dp(6)
			b.TextSize = unit.Sp(14)
			return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, b.Layout)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return SecondaryButton(gtx, th, &sd.openFiles, "View Site Files")
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !site.Started {
				return layout.Dimensions{}
			}
			if exporting {
				loader := material.Loader(th)
				gtx.Constraints.Max.X = gtx.Dp(unit.Dp(36))
				gtx.Constraints.Max.Y = gtx.Dp(unit.Dp(36))
				return loader.Layout(gtx)
			}
			return SecondaryButton(gtx, th, &sd.exportBtn, "Export")
		}),
	)
}

func (sd *SiteDetail) layoutLogViewer(gtx layout.Context, th *material.Theme) layout.Dimensions {
	sd.ui.State.mu.Lock()
	logOutput := sd.ui.State.LogOutput
	logLoading := sd.ui.State.LogLoading
	sd.ui.State.mu.Unlock()

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// Section title
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.H6(th, "Container Logs")
			return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, lbl.Layout)
		}),
		// Controls: dropdown + refresh
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Max.X = gtx.Dp(unit.Dp(160))
						return sd.logServiceDropdown.Layout(gtx, th, "Service")
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							if logLoading {
								loader := material.Loader(th)
								gtx.Constraints.Max.X = gtx.Dp(unit.Dp(28))
								gtx.Constraints.Max.Y = gtx.Dp(unit.Dp(28))
								return loader.Layout(gtx)
							}
							return SecondaryButton(gtx, th, &sd.logRefreshBtn, "Refresh")
						})
					}),
				)
			})
		}),
		// Log output area
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(20)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				// Gray background box
				maxHeight := gtx.Dp(unit.Dp(300))
				gtx.Constraints.Max.Y = maxHeight
				gtx.Constraints.Min.X = gtx.Constraints.Max.X

				return FillBackground(gtx, ColorGray100, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						if logOutput == "" {
							lbl := material.Body2(th, "Click Refresh to load logs")
							lbl.Color = ColorGray400
							lbl.TextSize = unit.Sp(12)
							return lbl.Layout(gtx)
						}
						lines := strings.Split(logOutput, "\n")
						return material.List(th, &sd.logList).Layout(gtx, len(lines), func(gtx layout.Context, i int) layout.Dimensions {
							lbl := material.Body2(th, lines[i])
							lbl.TextSize = unit.Sp(11)
							return lbl.Layout(gtx)
						})
					})
				})
			})
		}),
	)
}

func (sd *SiteDetail) layoutWPCLI(gtx layout.Context, th *material.Theme) layout.Dimensions {
	sd.ui.State.mu.Lock()
	wpcliOutput := sd.ui.State.WPCLIOutput
	wpcliLoading := sd.ui.State.WPCLILoading
	sd.ui.State.mu.Unlock()

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// Section title
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.H6(th, "WP-CLI")
			return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, lbl.Layout)
		}),
		// Input row: "wp " label + editor + run button
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th, "wp ")
						lbl.TextSize = unit.Sp(14)
						return lbl.Layout(gtx)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return BorderedEditor(gtx, th, &sd.wpcliEditor, "plugin list --status=active")
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							if wpcliLoading {
								loader := material.Loader(th)
								gtx.Constraints.Max.X = gtx.Dp(unit.Dp(28))
								gtx.Constraints.Max.Y = gtx.Dp(unit.Dp(28))
								return loader.Layout(gtx)
							}
							return PrimaryButton(gtx, th, &sd.wpcliRunBtn, "Run")
						})
					}),
				)
			})
		}),
		// Output area
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(20)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				maxHeight := gtx.Dp(unit.Dp(250))
				gtx.Constraints.Max.Y = maxHeight
				gtx.Constraints.Min.X = gtx.Constraints.Max.X

				return FillBackground(gtx, ColorGray100, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						if wpcliOutput == "" {
							lbl := material.Body2(th, "Run a WP-CLI command to see output")
							lbl.Color = ColorGray400
							lbl.TextSize = unit.Sp(12)
							return lbl.Layout(gtx)
						}
						lines := strings.Split(wpcliOutput, "\n")
						return material.List(th, &sd.wpcliList).Layout(gtx, len(lines), func(gtx layout.Context, i int) layout.Dimensions {
							lbl := material.Body2(th, lines[i])
							lbl.TextSize = unit.Sp(11)
							return lbl.Layout(gtx)
						})
					})
				})
			})
		}),
	)
}

type kv struct {
	Key   string
	Value string
}

func (sd *SiteDetail) layoutSection(gtx layout.Context, th *material.Theme, title string, items []kv) layout.Dimensions {
	children := make([]layout.FlexChild, 0, len(items)+1)

	// Section title
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		lbl := material.H6(th, title)
		return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, lbl.Layout)
	}))

	// Key-value rows
	for _, item := range items {
		item := item
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Dp(unit.Dp(100))
						lbl := material.Body2(th, item.Key)
						lbl.Color = ColorGray500
						lbl.TextSize = unit.Sp(14)
						return lbl.Layout(gtx)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th, item.Value)
						lbl.TextSize = unit.Sp(14)
						return lbl.Layout(gtx)
					}),
				)
			})
		}))
	}

	// Bottom margin for section
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Spacer{Height: unit.Dp(20)}.Layout(gtx)
	}))

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

