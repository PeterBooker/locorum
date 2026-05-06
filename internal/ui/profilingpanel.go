package ui

import (
	"fmt"
	"net/url"
	"sort"
	"sync"
	"time"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/types"
	"github.com/PeterBooker/locorum/internal/utils"
)

// ProfilingPanel is the per-site SPX profiler tab. It owns the toggle,
// the URL/key card, and the recent-reports list. SPX itself runs
// inside the existing PHP-FPM container — this panel never spawns or
// touches Docker; it strictly drives SiteManager and reads the
// profile-data directory via the SiteManager helpers.
type ProfilingPanel struct {
	state  *UIState
	sm     *sites.SiteManager
	toasts *Notifications

	enableBtn  widget.Clickable
	disableBtn widget.Clickable
	rotateBtn  widget.Clickable
	openBtn    widget.Clickable
	copyURL    widget.Clickable
	copyKey    widget.Clickable
	refreshBtn widget.Clickable
	clearBtn   widget.Clickable

	urlSel widget.Selectable
	keySel widget.Selectable

	// Cached report list. Refreshed on tab activation (loadedFor change)
	// and on manual Refresh. Mutex-protected because the load runs in a
	// goroutine and Layout reads on the UI thread.
	mu           sync.Mutex
	reports      []sites.SPXReport
	loadedFor    string
	loadInFlight bool
	loadErr      string
}

// NewProfilingPanel constructs a ProfilingPanel. State + SiteManager
// are required; toasts may be nil (errors then route only through the
// global error banner via state.ShowError).
func NewProfilingPanel(state *UIState, sm *sites.SiteManager, toasts *Notifications) *ProfilingPanel {
	return &ProfilingPanel{state: state, sm: sm, toasts: toasts}
}

// HandleUserInteractions processes button clicks. Must be called once
// per frame, before Layout.
func (pp *ProfilingPanel) HandleUserInteractions(gtx layout.Context, site *types.Site) {
	if site == nil {
		return
	}

	if pp.enableBtn.Clicked(gtx) && !site.Started {
		pp.toggle(site.ID, true)
	}
	if pp.disableBtn.Clicked(gtx) && !site.Started {
		pp.toggle(site.ID, false)
	}
	if pp.rotateBtn.Clicked(gtx) && !site.Started {
		pp.rotate(site.ID)
	}
	if pp.openBtn.Clicked(gtx) && site.SPXEnabled && site.Started {
		go func() {
			if err := utils.OpenURL(spxUIURL(site)); err != nil {
				pp.state.ShowError("Could not open SPX UI: " + err.Error())
			}
		}()
	}
	if pp.copyURL.Clicked(gtx) && site.SPXEnabled {
		CopyToClipboard(gtx, spxUIURL(site))
		if pp.toasts != nil {
			pp.toasts.ShowInfo("SPX URL copied to clipboard")
		}
	}
	if pp.copyKey.Clicked(gtx) && site.SPXEnabled {
		CopyToClipboard(gtx, site.SPXKey)
		if pp.toasts != nil {
			pp.toasts.ShowInfo("SPX key copied to clipboard")
		}
	}
	if pp.refreshBtn.Clicked(gtx) {
		pp.kickLoad(site.ID, true)
	}
	if pp.clearBtn.Clicked(gtx) {
		pp.clearAll(site.ID)
	}
}

// Layout renders the panel.
func (pp *ProfilingPanel) Layout(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	if site == nil {
		return layout.Dimensions{}
	}
	pp.kickLoad(site.ID, false)

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return pp.layoutToggle(gtx, th, site)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !site.SPXEnabled {
				return layout.Dimensions{}
			}
			return pp.layoutAccessCard(gtx, th, site)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !site.SPXEnabled {
				return layout.Dimensions{}
			}
			return pp.layoutReports(gtx, th, site)
		}),
	)
}

// ─── Sections ────────────────────────────────────────────────────────────────

func (pp *ProfilingPanel) layoutToggle(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	return panel(gtx, th, "Profiling (SPX)", func(gtx layout.Context) layout.Dimensions {
		hint := "SPX is a low-overhead PHP profiler with a built-in flamegraph viewer. Open the SPX UI, click the Control Panel dropdown, tick \"Enabled\", then reload any page on this site to capture a profile."
		if site.Started && !site.SPXEnabled {
			hint = "SPX is disabled — stop the site, enable it here, then start the site again."
		} else if site.Started && site.SPXEnabled {
			hint = "Open the SPX UI, click the Control Panel dropdown, tick \"Enabled\", then reload any page on this site to capture a profile. Stop the site first if you need to disable SPX or rotate the key."
		} else if !site.SPXEnabled {
			hint = "Enable SPX to profile requests, WP-CLI, and cron jobs. Reports land under .locorum/spx/ inside the site directory."
		} else {
			hint = "SPX is enabled. Start the site, then capture profiles via the SPX UI's Control Panel."
		}

		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, hint)
				lbl.Color = th.Color.Fg2
				lbl.TextSize = th.Sizes.Body
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						key := StatusErr
						label := "SPX disabled"
						if site.SPXEnabled {
							key = StatusOk
							label = "SPX enabled"
						}
						return spxStatusPill(gtx, th, key, label)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Spacer{Width: th.Spacing.MD}.Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if site.SPXEnabled {
							return th.SmallGated(gtx, &pp.disableBtn, "Disable", !site.Started)
						}
						return th.PrimaryGated(gtx, &pp.enableBtn, "Enable SPX", !site.Started)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !site.SPXEnabled {
							return layout.Dimensions{}
						}
						return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return th.SmallGated(gtx, &pp.rotateBtn, "Rotate key", !site.Started)
						})
					}),
				)
			}),
		)
	})
}

func (pp *ProfilingPanel) layoutAccessCard(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	url := spxUIURL(site)
	openHint := ""
	if !site.Started {
		openHint = " — start the site to use"
	}
	return panel(gtx, th, "Access", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return pp.copyableRow(gtx, th, "URL", url, &pp.urlSel, &pp.copyURL)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return pp.copyableRow(gtx, th, "Key", site.SPXKey, &pp.keySel, &pp.copyKey)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return th.PrimaryGated(gtx, &pp.openBtn, "Open SPX UI"+openHint, site.Started)
						}),
					)
				})
			}),
		)
	})
}

func (pp *ProfilingPanel) layoutReports(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	pp.mu.Lock()
	reports := append([]sites.SPXReport(nil), pp.reports...)
	loading := pp.loadInFlight
	loadErr := pp.loadErr
	pp.mu.Unlock()

	titleAction := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return th.Small(gtx, &pp.refreshBtn, "Refresh")
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if len(reports) == 0 {
					return layout.Dimensions{}
				}
				return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return th.Small(gtx, &pp.clearBtn, "Clear all")
				})
			}),
		)
	}

	body := func(gtx layout.Context) layout.Dimensions {
		switch {
		case loadErr != "":
			lbl := material.Body2(th.Theme, "Could not list reports: "+loadErr)
			lbl.Color = th.Color.Err
			lbl.TextSize = th.Sizes.Body
			return lbl.Layout(gtx)
		case loading && len(reports) == 0:
			lbl := material.Body2(th.Theme, "Loading…")
			lbl.Color = th.Color.Fg3
			lbl.TextSize = th.Sizes.Body
			return lbl.Layout(gtx)
		case len(reports) == 0:
			lbl := material.Body2(th.Theme, "No reports yet. To capture one: open the SPX UI, click Profiling → enable, then reload a page on this site (or run a WP-CLI command). The new report will appear here on next refresh.")
			lbl.Color = th.Color.Fg3
			lbl.TextSize = th.Sizes.Body
			return lbl.Layout(gtx)
		default:
			return pp.layoutReportRows(gtx, th, reports, site)
		}
	}

	return panelWithAction(gtx, th, "Reports", titleAction, body)
}

// layoutReportRows renders a flat, informational list of profile-data
// files. SPX's web UI is the canonical place to open and analyse a
// single report — its SPA does not expose stable deep-links — so the
// rows show metadata only. Use the "Open SPX UI" button at the top
// of the panel to navigate.
func (pp *ProfilingPanel) layoutReportRows(gtx layout.Context, th *Theme, reports []sites.SPXReport, _ *types.Site) layout.Dimensions {
	now := time.Now()
	children := make([]layout.FlexChild, 0, len(reports))
	for _, r := range reports {
		r := r
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Spacing.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th.Theme, r.Name)
						lbl.TextSize = th.Sizes.Base
						lbl.Font = MonoFont
						lbl.MaxLines = 1
						lbl.Truncator = "…"
						return lbl.Layout(gtx)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Spacer{}.Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th.Theme, formatReportMeta(r, now))
						lbl.Color = th.Color.Fg3
						lbl.TextSize = th.Sizes.XS
						return lbl.Layout(gtx)
					}),
				)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// copyableRow renders a Label / selectable mono value / Copy button row.
func (pp *ProfilingPanel) copyableRow(gtx layout.Context, th *Theme, label, value string, sel *widget.Selectable, copy *widget.Clickable) layout.Dimensions {
	return layout.Inset{Bottom: th.Spacing.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(th.Dims.LabelColWidth)
				lbl := material.Body2(th.Theme, label)
				lbl.Color = th.Color.TextSecondary
				lbl.TextSize = th.Sizes.Base
				return lbl.Layout(gtx)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return SelectableLabel(gtx, th, sel, value, th.Sizes.Base, th.Color.Fg, MonoFont)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return SmallButton(gtx, th, copy, "Copy")
				})
			}),
		)
	})
}

// spxStatusPill draws a compact pill describing whether SPX is on or
// off for the current site. Local rather than reusing StatusPill so
// the label is the SPX-specific copy ("SPX enabled" / "SPX disabled")
// rather than the running-state phrasing.
func spxStatusPill(gtx layout.Context, th *Theme, key, label string) layout.Dimensions {
	pal := statusPalette(th, key)
	return RoundedFill(gtx, pal.bg, th.Radii.R2, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top: unit.Dp(4), Bottom: unit.Dp(4),
			Left: th.Spacing.SM, Right: th.Spacing.SM,
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return LiveStatusDot(gtx, th, key, false)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th.Theme, label)
						lbl.Color = pal.fg
						lbl.TextSize = th.Sizes.Micro
						lbl.Font = MonoFont
						return lbl.Layout(gtx)
					})
				}),
			)
		})
	})
}

// formatReportMeta renders "<size> · <relative time>" for a report row.
func formatReportMeta(r sites.SPXReport, now time.Time) string {
	return fmt.Sprintf("%s · %s", humanBytesShort(r.Size), humanizeTimeAgo(now.Sub(r.Time)))
}

// humanBytesShort is the same compact decimal formatter the top-bar
// disk segment uses, kept locally to avoid widening the public surface
// of the formatBytes helper in ui.go.
func humanBytesShort(n int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case n >= GB:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(GB))
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(MB))
	case n >= KB:
		return fmt.Sprintf("%.0f KB", float64(n)/float64(KB))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func humanizeTimeAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}

// spxUIURL returns the canonical "open the SPX viewer" URL.
func spxUIURL(site *types.Site) string {
	q := url.Values{}
	q.Set("SPX_UI_URI", "/")
	q.Set("SPX_KEY", site.SPXKey)
	return "https://" + site.Domain + "/?" + q.Encode()
}

// ─── Background ops ──────────────────────────────────────────────────────────

func (pp *ProfilingPanel) toggle(siteID string, on bool) {
	go func() {
		if err := pp.sm.SetSPXEnabled(siteID, on); err != nil {
			pp.state.ShowError("SPX toggle failed: " + err.Error())
			return
		}
		if pp.toasts != nil {
			if on {
				pp.toasts.ShowSuccess("SPX enabled — start the site to apply.")
			} else {
				pp.toasts.ShowSuccess("SPX disabled — start the site to apply.")
			}
		}
		// Trigger a redraw via Invalidate so the UI picks up the new flag
		// even when the state subscription cannot dedupe the change.
		pp.state.Invalidate()
	}()
}

func (pp *ProfilingPanel) rotate(siteID string) {
	go func() {
		if err := pp.sm.RotateSPXKey(siteID); err != nil {
			pp.state.ShowError("SPX key rotation failed: " + err.Error())
			return
		}
		if pp.toasts != nil {
			pp.toasts.ShowSuccess("SPX key rotated.")
		}
		pp.state.Invalidate()
	}()
}

func (pp *ProfilingPanel) clearAll(siteID string) {
	go func() {
		if err := pp.sm.ClearSPXReports(siteID); err != nil {
			pp.state.ShowError("Could not clear reports: " + err.Error())
		}
		pp.kickLoad(siteID, true)
	}()
}

// kickLoad ensures the report list is loaded for siteID. Skips when a
// load is already in flight, or when force is false and the cache is
// already for siteID. Runs the read in a goroutine so Layout() never
// blocks.
func (pp *ProfilingPanel) kickLoad(siteID string, force bool) {
	pp.mu.Lock()
	if pp.loadInFlight {
		pp.mu.Unlock()
		return
	}
	if !force && pp.loadedFor == siteID {
		pp.mu.Unlock()
		return
	}
	pp.loadInFlight = true
	pp.mu.Unlock()

	go func() {
		reports, err := pp.sm.ListSPXReports(siteID)
		// Stable secondary order if mtimes collide.
		sort.SliceStable(reports, func(i, j int) bool {
			if !reports[i].Time.Equal(reports[j].Time) {
				return reports[i].Time.After(reports[j].Time)
			}
			return reports[i].Name < reports[j].Name
		})

		pp.mu.Lock()
		pp.reports = reports
		pp.loadedFor = siteID
		pp.loadInFlight = false
		if err != nil {
			pp.loadErr = err.Error()
		} else {
			pp.loadErr = ""
		}
		pp.mu.Unlock()
		pp.state.Invalidate()
	}()
}
