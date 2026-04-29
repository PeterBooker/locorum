package ui

import (
	"encoding/json"
	"image/color"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/storage"
)

// activityStepDetail mirrors the JSON shape written by
// internal/sites/activity.go. Duplicated here so the UI can decode without
// a dependency on the sites package's internal types.
type activityStepDetail struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

type activityDetails struct {
	Steps []activityStepDetail `json:"steps,omitempty"`
	Error string               `json:"error,omitempty"`
}

func decodeActivityDetails(raw []byte) activityDetails {
	if len(raw) == 0 || string(raw) == "null" {
		return activityDetails{}
	}
	var d activityDetails
	if err := json.Unmarshal(raw, &d); err != nil {
		// Don't surface decode errors to the user — the row's Message and
		// status pill already carry the load-bearing information. Log
		// once for diagnostics.
		slog.Warn("activity details decode failed", "err", err.Error())
		return activityDetails{}
	}
	return d
}

// ActivityTab renders the per-site lifecycle audit feed. Each row is one
// orch.Plan outcome; clicking a row toggles an inline expander with the
// step list and final error captured at write time.
//
// State that needs to survive across frames lives on this struct: the
// scroll position, the per-row expand toggles, and the per-site set of
// loads we've already kicked off. Storage reads happen in goroutines —
// Layout() is purely a function of the cached snapshot.
type ActivityTab struct {
	state *UIState
	sm    *sites.SiteManager

	list widget.List

	// expanded tracks which rows are open. Keyed by ActivityEvent.ID
	// (stable across frames; sourced from the database).
	expandMu sync.Mutex
	expanded map[int64]bool

	// rowClicks holds one widget.Clickable per visible row, lazy-allocated
	// by ID so the click target persists across frames even as the visible
	// window scrolls. Bounded by the cache cap (ActivityFullMax) — we don't
	// expose more rows than that, so the map stays small in practice.
	rowClicks map[int64]*widget.Clickable

	// loadingFor tracks sites for which a full-cache load is already in
	// flight. Prevents Layout() from spawning a fresh goroutine on every
	// frame while one is still running.
	loadingMu  sync.Mutex
	loadingFor map[string]bool
}

func NewActivityTab(state *UIState, sm *sites.SiteManager) *ActivityTab {
	at := &ActivityTab{
		state:      state,
		sm:         sm,
		expanded:   make(map[int64]bool),
		rowClicks:  make(map[int64]*widget.Clickable),
		loadingFor: make(map[string]bool),
	}
	at.list.List.Axis = layout.Vertical
	return at
}

// Layout renders the activity tab body for siteID. Triggers a one-time
// background load of the full per-site history on first paint and on
// site-switch.
func (at *ActivityTab) Layout(gtx layout.Context, th *Theme, siteID string) layout.Dimensions {
	if siteID == "" {
		return layout.Dimensions{}
	}

	rows, loaded := at.state.ActivityFull(siteID)
	if !loaded {
		at.kickLoad(siteID)
		return at.layoutLoading(gtx, th)
	}
	if len(rows) == 0 {
		return at.layoutEmpty(gtx, th)
	}

	// Process clicks before laying out, so a click on this frame opens
	// the expander on this frame.
	for _, ev := range rows {
		c := at.clickableFor(ev.ID)
		if c.Clicked(gtx) {
			at.toggleExpanded(ev.ID)
		}
	}

	now := time.Now()
	return material.List(th.Theme, &at.list).Layout(gtx, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
		ev := rows[i]
		return at.layoutRow(gtx, th, ev, now)
	})
}

// HandleUserInteractions is a no-op today — Layout() consumes clicks as it
// renders so the matching row's expander opens within the same frame.
// Kept as a stub so the tab plugs into SiteDetail's HandleUserInteractions
// dispatch without an awkward conditional.
func (at *ActivityTab) HandleUserInteractions(layout.Context) {}

// kickLoad starts a one-shot goroutine that loads the full activity
// history for siteID into the UIState cache. Skips if a load is already in
// flight for that site, so a slow disk doesn't queue up duplicate reads.
func (at *ActivityTab) kickLoad(siteID string) {
	at.loadingMu.Lock()
	if at.loadingFor[siteID] {
		at.loadingMu.Unlock()
		return
	}
	at.loadingFor[siteID] = true
	at.loadingMu.Unlock()

	go func() {
		defer func() {
			at.loadingMu.Lock()
			delete(at.loadingFor, siteID)
			at.loadingMu.Unlock()
		}()
		evs, err := at.sm.GetActivity(siteID, ActivityFullMax)
		if err != nil {
			slog.Warn("activity load failed", "site", siteID, "err", err.Error())
			return
		}
		at.state.SetActivityFull(siteID, evs)
	}()
}

func (at *ActivityTab) layoutLoading(gtx layout.Context, th *Theme) layout.Dimensions {
	return panel(gtx, th, "Activity", func(gtx layout.Context) layout.Dimensions {
		return Loader(gtx, th, th.Dims.LoaderSizeSM)
	})
}

func (at *ActivityTab) layoutEmpty(gtx layout.Context, th *Theme) layout.Dimensions {
	return panel(gtx, th, "Activity", func(gtx layout.Context) layout.Dimensions {
		lbl := material.Body2(th.Theme, "No activity yet. Lifecycle actions (start, stop, delete, …) appear here as they happen.")
		lbl.Color = th.Color.Fg3
		lbl.TextSize = th.Sizes.Body
		return lbl.Layout(gtx)
	})
}

// layoutRow renders one row plus, when expanded, its detail body.
func (at *ActivityTab) layoutRow(gtx layout.Context, th *Theme, ev storage.ActivityEvent, now time.Time) layout.Dimensions {
	c := at.clickableFor(ev.ID)
	expanded := at.isExpanded(ev.ID)

	return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return drawCard(gtx, th, func(gtx layout.Context) layout.Dimensions {
			return material.Clickable(gtx, c, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return activityTabHeader(gtx, th, ev, now)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if !expanded {
								return layout.Dimensions{}
							}
							return layout.Inset{Top: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return activityTabDetail(gtx, th, ev)
							})
						}),
					)
				})
			})
		})
	})
}

func activityTabHeader(gtx layout.Context, th *Theme, ev storage.ActivityEvent, now time.Time) layout.Dimensions {
	dotCol := activityRowDotColor(th, rowKindForStatus(ev.Status))
	timeStr := FormatActivityTime(ev.Time, now)
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return activityStatusDot(gtx, dotCol, unit.Dp(8))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Spacer{Width: unit.Dp(10)}.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Dp(unit.Dp(120))
			gtx.Constraints.Max.X = gtx.Constraints.Min.X
			lbl := material.Body2(th.Theme, timeStr)
			lbl.Color = th.Color.Fg3
			lbl.TextSize = th.Sizes.Mono
			lbl.Font = MonoFont
			lbl.MaxLines = 1
			return lbl.Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, ev.Message)
			lbl.Color = activityMessageColor(th, ev.Status)
			lbl.TextSize = th.Sizes.Body
			lbl.MaxLines = 1
			lbl.Truncator = "…"
			return lbl.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, formatDuration(ev.DurationMS))
			lbl.Color = th.Color.Fg3
			lbl.TextSize = th.Sizes.Mono
			lbl.Font = MonoFont
			lbl.MaxLines = 1
			return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, lbl.Layout)
		}),
	)
}

// activityTabDetail renders the inline expander body: the step list and
// (if any) the final error captured in the JSON details blob.
func activityTabDetail(gtx layout.Context, th *Theme, ev storage.ActivityEvent) layout.Dimensions {
	d := decodeActivityDetails(ev.Details)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if len(d.Steps) == 0 {
				lbl := material.Body2(th.Theme, "No step detail available.")
				lbl.Color = th.Color.Fg3
				lbl.TextSize = th.Sizes.Mono
				lbl.Font = MonoFont
				return lbl.Layout(gtx)
			}
			children := make([]layout.FlexChild, 0, len(d.Steps))
			for i, step := range d.Steps {
				step := step
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					top := unit.Dp(0)
					if i > 0 {
						top = unit.Dp(4)
					}
					return layout.Inset{Top: top}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return activityStepRow(gtx, th, step)
					})
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if d.Error == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return activityErrorBlock(gtx, th, d.Error)
			})
		}),
	)
}

func activityStepRow(gtx layout.Context, th *Theme, step activityStepDetail) layout.Dimensions {
	statusCol := activityStepStatusColor(th, step.Status)
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return activityStatusDot(gtx, statusCol, unit.Dp(6))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Dp(unit.Dp(80))
			gtx.Constraints.Max.X = gtx.Constraints.Min.X
			lbl := material.Body2(th.Theme, step.Status)
			lbl.Color = statusCol
			lbl.TextSize = th.Sizes.Mono
			lbl.Font = MonoFont
			lbl.MaxLines = 1
			return lbl.Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, step.Name)
			lbl.Color = th.Color.Fg2
			lbl.TextSize = th.Sizes.Mono
			lbl.Font = MonoFont
			lbl.MaxLines = 1
			lbl.Truncator = "…"
			return lbl.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, formatDuration(step.DurationMS))
			lbl.Color = th.Color.Fg3
			lbl.TextSize = th.Sizes.Mono
			lbl.Font = MonoFont
			lbl.MaxLines = 1
			return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, lbl.Layout)
		}),
	)
}

func activityErrorBlock(gtx layout.Context, th *Theme, msg string) layout.Dimensions {
	return RoundedFill(gtx, th.Color.DangerBg, th.Radii.R2, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, msg)
			lbl.Color = th.Color.DangerFg
			lbl.TextSize = th.Sizes.Mono
			lbl.Font = MonoFont
			lbl.Font.Weight = font.Normal
			return lbl.Layout(gtx)
		})
	})
}

func (at *ActivityTab) clickableFor(id int64) *widget.Clickable {
	at.expandMu.Lock()
	defer at.expandMu.Unlock()
	if c, ok := at.rowClicks[id]; ok {
		return c
	}
	c := &widget.Clickable{}
	at.rowClicks[id] = c
	return c
}

func (at *ActivityTab) isExpanded(id int64) bool {
	at.expandMu.Lock()
	defer at.expandMu.Unlock()
	return at.expanded[id]
}

func (at *ActivityTab) toggleExpanded(id int64) {
	at.expandMu.Lock()
	defer at.expandMu.Unlock()
	if at.expanded[id] {
		delete(at.expanded, id)
	} else {
		at.expanded[id] = true
	}
}

// formatDuration renders a millisecond count as a short human string
// (e.g. "12ms", "1.2s", "2m 13s"). Kept inline because no other panel
// needs it yet; promote to a shared helper if a second caller appears.
func formatDuration(ms int64) string {
	if ms < 0 {
		return ""
	}
	if ms < 1000 {
		return strconv.FormatInt(ms, 10) + "ms"
	}
	if ms < 60_000 {
		whole := ms / 1000
		tenths := (ms % 1000) / 100
		if tenths == 0 {
			return strconv.FormatInt(whole, 10) + "s"
		}
		return strconv.FormatInt(whole, 10) + "." + strconv.FormatInt(tenths, 10) + "s"
	}
	mins := ms / 60_000
	rem := (ms % 60_000) / 1000
	if rem == 0 {
		return strconv.FormatInt(mins, 10) + "m"
	}
	return strconv.FormatInt(mins, 10) + "m " + strconv.FormatInt(rem, 10) + "s"
}

func rowKindForStatus(s storage.ActivityStatus) activityRowKind {
	switch s {
	case storage.ActivityStatusFailed:
		return activityRowFailed
	case storage.ActivityStatusRolledBack:
		return activityRowRolledBack
	}
	return activityRowOK
}

// activityMessageColor returns the row-message foreground colour based on
// status. Failed rows use the error colour; rolled-back stays neutral
// because the warn dot already carries the visual weight; succeeded uses
// Fg2 like ordinary body copy.
func activityMessageColor(th *Theme, s storage.ActivityStatus) color.NRGBA {
	if s == storage.ActivityStatusFailed {
		return th.Color.Err
	}
	return th.Color.Fg2
}

// activityStepStatusColor maps an orch.Status string to a visual colour
// for the step list inside the inline expander.
func activityStepStatusColor(th *Theme, status string) color.NRGBA {
	switch status {
	case "succeeded":
		return th.Color.Ok
	case "failed":
		return th.Color.Err
	case "rolled-back":
		return th.Color.Warn
	case "skipped":
		return th.Color.Fg3
	case "running", "pending":
		return th.Color.Accent
	}
	return th.Color.Fg3
}
