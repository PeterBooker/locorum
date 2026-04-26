package ui

import (
	"context"
	"errors"
	"strings"
	"sync"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/storage"
)

// hookListProvider is the narrow port the panel uses to load hooks from
// storage. *sites.SiteManager satisfies it via pass-through methods —
// keeping the interface here avoids importing *storage.Storage into the UI
// directly.
type hookListProvider interface {
	ListSiteHooks(siteID string) ([]hooks.Hook, error)
	AddSiteHook(*hooks.Hook) error
	UpdateSiteHook(*hooks.Hook) error
	DeleteSiteHook(id int64) error
	ReorderSiteHooks(siteID string, ev hooks.Event, ids []int64) error
}

// HooksPanel is the per-site hooks tab. It loads hooks lazily on display,
// caches them in memory, and refreshes whenever the user makes a change.
type HooksPanel struct {
	state    *UIState
	sm       *sites.SiteManager
	provider hookListProvider
	toasts   *Notifications

	editor        *HookEditor
	output        *HookOutput
	confirm       ConfirmDialog
	confirmShown  bool
	confirmTarget int64

	// per-site cache: id -> hooks bucket. Reloaded when the user opens a
	// different site or after a write.
	mu       sync.Mutex
	loadedID string
	groups   map[hooks.Event][]hooks.Hook
	loadErr  string

	// Per-row interactive state. The slice is rebuilt on every reload so
	// indexes remain stable across frames within a single load.
	rows []hookRow

	// Per-event "Add hook" buttons. We allocate one per active event and
	// keep them alive across frames.
	addBtns map[hooks.Event]*widget.Clickable

	// Per-event "Run all" buttons.
	runAllBtns map[hooks.Event]*widget.Clickable

	// Fail-on-error toggle (per-site).
	failClickable widget.Clickable
	failOn        bool
	failLoaded    bool

	scroll widget.List
}

// hookRow tracks the per-row click and toggle state for a single hook.
type hookRow struct {
	hook hooks.Hook

	enabledClick widget.Clickable
	editClick    widget.Clickable
	deleteClick  widget.Clickable
	runNowClick  widget.Clickable
	moveUpClick  widget.Clickable
	moveDnClick  widget.Clickable

	// Position within its event group at load time. Updated on reorder.
	groupIndex int
	groupSize  int
}

// NewHooksPanel builds a HooksPanel.
func NewHooksPanel(state *UIState, sm *sites.SiteManager, provider hookListProvider, toasts *Notifications) *HooksPanel {
	hp := &HooksPanel{
		state:      state,
		sm:         sm,
		provider:   provider,
		toasts:     toasts,
		editor:     NewHookEditor(),
		output:     NewHookOutput(state, sm),
		groups:     map[hooks.Event][]hooks.Hook{},
		addBtns:    map[hooks.Event]*widget.Clickable{},
		runAllBtns: map[hooks.Event]*widget.Clickable{},
	}
	hp.scroll.Axis = layout.Vertical
	return hp
}

// HandleUserInteractions processes clicks for the hooks tab. It delegates
// to the editor / confirm dialog as needed.
func (hp *HooksPanel) HandleUserInteractions(gtx layout.Context, siteID string) {
	hp.ensureLoaded(siteID)

	hp.editor.HandleUserInteractions(gtx, hp.state)
	hp.output.HandleUserInteractions(gtx, siteID)

	if hp.confirmShown {
		confirmed, cancelled := hp.confirm.HandleUserInteractions(gtx)
		if cancelled {
			hp.confirmShown = false
			hp.confirmTarget = 0
		}
		if confirmed {
			id := hp.confirmTarget
			hp.confirmShown = false
			hp.confirmTarget = 0
			go hp.runDelete(siteID, id)
		}
	}

	if hp.failClickable.Clicked(gtx) {
		hp.failOn = !hp.failOn
		hp.persistFailFlag(siteID)
	}

	for ev, btn := range hp.addBtns {
		if btn.Clicked(gtx) {
			ev := ev
			hp.editor.Open(siteID, hooks.Hook{Event: ev, TaskType: defaultTaskTypeForEvent(ev)}, func(h hooks.Hook) {
				go hp.runAdd(siteID, h)
			})
		}
	}
	for ev, btn := range hp.runAllBtns {
		if btn.Clicked(gtx) {
			hp.runEvent(ev)
		}
	}

	hp.mu.Lock()
	rows := hp.rows
	hp.mu.Unlock()
	for i := range rows {
		row := &rows[i]
		if row.enabledClick.Clicked(gtx) {
			h := row.hook
			h.Enabled = !h.Enabled
			go hp.runUpdate(siteID, h)
		}
		if row.editClick.Clicked(gtx) {
			h := row.hook
			hp.editor.Open(siteID, h, func(updated hooks.Hook) {
				go hp.runUpdate(siteID, updated)
			})
		}
		if row.deleteClick.Clicked(gtx) {
			hp.confirmShown = true
			hp.confirmTarget = row.hook.ID
		}
		if row.runNowClick.Clicked(gtx) {
			h := row.hook
			go hp.runOne(h)
		}
		if row.moveUpClick.Clicked(gtx) {
			go hp.runMove(siteID, row.hook, -1)
		}
		if row.moveDnClick.Clicked(gtx) {
			go hp.runMove(siteID, row.hook, +1)
		}
	}
}

// Layout renders the hooks tab body for the given site.
func (hp *HooksPanel) Layout(gtx layout.Context, th *Theme, siteID string) layout.Dimensions {
	hp.ensureLoaded(siteID)

	if hp.loadErr != "" {
		lbl := material.Body1(th.Theme, "Failed to load hooks: "+hp.loadErr)
		lbl.Color = th.Color.Danger
		return layout.Inset{Top: th.Spacing.MD}.Layout(gtx, lbl.Layout)
	}

	return layout.Inset{Top: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			// Live output
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return hp.output.Layout(gtx, th, siteID)
			}),
			// Per-event sections
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return material.List(th.Theme, &hp.scroll).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
					return hp.layoutEvents(gtx, th, siteID)
				})
			}),
			// Fail-on-error toggle
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return hp.layoutFailToggle(gtx, th)
				})
			}),
		)
	})
}

// LayoutModalLayer is called from the root UI's modal stack so the hook
// editor and delete-confirm dialogs sit above the rest of the chrome.
func (hp *HooksPanel) LayoutModalLayer(gtx layout.Context, th *Theme) layout.Dimensions {
	if hp.editor.IsVisible() {
		return hp.editor.Layout(gtx, th)
	}
	if hp.confirmShown {
		return hp.confirm.Layout(gtx, th, ConfirmDialogStyle{
			Title:        "Delete hook",
			Message:      "Remove this hook? This cannot be undone.",
			ConfirmLabel: "Delete",
			ConfirmColor: th.Color.Danger,
		})
	}
	return layout.Dimensions{}
}

// HasActiveModal reports whether the panel is showing the editor or the
// delete-confirm modal.
func (hp *HooksPanel) HasActiveModal() bool {
	return hp.editor.IsVisible() || hp.confirmShown
}

func (hp *HooksPanel) layoutEvents(gtx layout.Context, th *Theme, siteID string) layout.Dimensions {
	events := hooks.SortedActiveEvents()
	children := make([]layout.FlexChild, 0, len(events)*2)
	hp.mu.Lock()
	groups := hp.groups
	hp.mu.Unlock()
	for _, ev := range events {
		ev := ev
		bucket := groups[ev]
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return hp.layoutEvent(gtx, th, siteID, ev, bucket)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func (hp *HooksPanel) layoutEvent(gtx layout.Context, th *Theme, siteID string, ev hooks.Event, bucket []hooks.Hook) layout.Dimensions {
	addBtn := hp.addBtns[ev]
	if addBtn == nil {
		addBtn = &widget.Clickable{}
		hp.addBtns[ev] = addBtn
	}
	runAllBtn := hp.runAllBtns[ev]
	if runAllBtn == nil {
		runAllBtn = &widget.Clickable{}
		hp.runAllBtns[ev] = runAllBtn
	}

	return layout.Inset{Bottom: th.Spacing.LG}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			// Header
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							lbl := material.H6(th.Theme, string(ev))
							return lbl.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if len(bucket) == 0 {
								return layout.Dimensions{}
							}
							return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return SmallButton(gtx, th, runAllBtn, "Run all")
							})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return SmallButton(gtx, th, addBtn, "Add hook")
						}),
					)
				})
			}),
			// Empty-state line OR the rows
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if len(bucket) == 0 {
					lbl := material.Body2(th.Theme, "No hooks for this event.")
					lbl.Color = th.Color.TextMuted
					return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
				}
				return hp.layoutBucket(gtx, th, ev, bucket)
			}),
		)
	})
}

func (hp *HooksPanel) layoutBucket(gtx layout.Context, th *Theme, ev hooks.Event, bucket []hooks.Hook) layout.Dimensions {
	hp.mu.Lock()
	rowState := hp.rowsForEvent(ev)
	hp.mu.Unlock()

	children := make([]layout.FlexChild, len(bucket))
	for i, h := range bucket {
		i, h := i, h
		row := rowState[i]
		row.groupIndex = i
		row.groupSize = len(bucket)
		children[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return hp.layoutRow(gtx, th, h, row)
		})
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func (hp *HooksPanel) layoutRow(gtx layout.Context, th *Theme, h hooks.Hook, row *hookRow) layout.Dimensions {
	return layout.Inset{Bottom: th.Spacing.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return FillBackground(gtx, th.Color.Surface, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(th.Spacing.SM).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					// Reorder arrows
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return hp.layoutReorderControls(gtx, th, row)
					}),
					// Enabled toggle
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return material.Clickable(gtx, &row.enabledClick, func(gtx layout.Context) layout.Dimensions {
								return layoutCheckbox(gtx, th, h.Enabled)
							})
						})
					}),
					// Type badge
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return hookTypeBadge(gtx, th, h)
						})
					}),
					// Command (truncated, flexed)
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th.Theme, TruncateWords(h.Command, 80))
						lbl.MaxLines = 1
						lbl.Truncator = "…"
						lbl.Font = MonoFont
						lbl.TextSize = th.Sizes.SM
						if !h.Enabled {
							lbl.Color = th.Color.TextMuted
						}
						return lbl.Layout(gtx)
					}),
					// Run-now / Edit / Delete
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return SmallButton(gtx, th, &row.runNowClick, "Run")
								})
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: th.Spacing.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return SmallButton(gtx, th, &row.editClick, "Edit")
								})
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: th.Spacing.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									b := material.Button(th.Theme, &row.deleteClick, "Delete")
									b.Background = th.Color.SurfaceAlt
									b.Color = th.Color.Danger
									b.CornerRadius = th.Radii.SM
									b.TextSize = th.Sizes.XS
									b.Inset = layout.Inset{
										Top:    unit.Dp(4),
										Bottom: unit.Dp(4),
										Left:   unit.Dp(10),
										Right:  unit.Dp(10),
									}
									return b.Layout(gtx)
								})
							}),
						)
					}),
				)
			})
		})
	})
}

func (hp *HooksPanel) layoutReorderControls(gtx layout.Context, th *Theme, row *hookRow) layout.Dimensions {
	return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				up := th.SmallGated(gtx, &row.moveUpClick, "▲", row.groupIndex > 0)
				return up
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				dn := th.SmallGated(gtx, &row.moveDnClick, "▼", row.groupIndex+1 < row.groupSize)
				return dn
			}),
		)
	})
}

func (hp *HooksPanel) layoutFailToggle(gtx layout.Context, th *Theme) layout.Dimensions {
	return material.Clickable(gtx, &hp.failClickable, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layoutCheckbox(gtx, th, hp.failOn)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, "Fail the lifecycle method when a hook errors")
				lbl.Color = th.Color.TextSecondary
				return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, lbl.Layout)
			}),
		)
	})
}

// hookTypeBadge draws a tinted pill showing the task type.
func hookTypeBadge(gtx layout.Context, th *Theme, h hooks.Hook) layout.Dimensions {
	label := string(h.TaskType)
	if h.TaskType == hooks.TaskExec && h.Service != "" {
		label = "exec/" + h.Service
	}
	lbl := material.Body2(th.Theme, label)
	lbl.Color = th.Color.TextStrong
	lbl.TextSize = th.Sizes.XS
	return layout.UniformInset(unit.Dp(2)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return FillBackground(gtx, th.Color.SurfaceAlt, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{
				Top: unit.Dp(2), Bottom: unit.Dp(2),
				Left: unit.Dp(6), Right: unit.Dp(6),
			}.Layout(gtx, lbl.Layout)
		})
	})
}

// ─── Loading and persistence ────────────────────────────────────────────────

func (hp *HooksPanel) ensureLoaded(siteID string) {
	hp.mu.Lock()
	needsLoad := hp.loadedID != siteID
	hp.mu.Unlock()
	if needsLoad {
		hp.reload(siteID)
		hp.loadFailFlag(siteID)
	}
}

func (hp *HooksPanel) reload(siteID string) {
	list, err := hp.provider.ListSiteHooks(siteID)
	hp.mu.Lock()
	hp.loadedID = siteID
	hp.loadErr = ""
	if err != nil {
		hp.loadErr = err.Error()
		hp.groups = map[hooks.Event][]hooks.Hook{}
		hp.rows = nil
		hp.mu.Unlock()
		return
	}
	groups := map[hooks.Event][]hooks.Hook{}
	for _, h := range list {
		groups[h.Event] = append(groups[h.Event], h)
	}
	hp.groups = groups
	// Rebuild stable per-row state. We allocate one entry per hook so the
	// click state survives across frames. Reorders/edits trigger a reload,
	// so the slice is always fresh.
	hp.rows = make([]hookRow, len(list))
	for i, h := range list {
		hp.rows[i].hook = h
	}
	hp.mu.Unlock()
}

// rowsForEvent returns the row pointers belonging to ev in the order the
// rows were loaded. The caller already holds hp.mu.
func (hp *HooksPanel) rowsForEvent(ev hooks.Event) []*hookRow {
	var out []*hookRow
	for i := range hp.rows {
		if hp.rows[i].hook.Event == ev {
			out = append(out, &hp.rows[i])
		}
	}
	return out
}

func (hp *HooksPanel) loadFailFlag(siteID string) {
	if hp.failLoaded && hp.loadedID == siteID {
		return
	}
	val, _ := hp.sm.GetSetting(hooks.SettingKeyFailPrefix + siteID)
	if val == "" {
		val, _ = hp.sm.GetSetting(hooks.SettingKeyFailGlobal)
	}
	hp.failOn = val == "true" || val == "1"
	hp.failLoaded = true
}

func (hp *HooksPanel) persistFailFlag(siteID string) {
	val := "false"
	if hp.failOn {
		val = "true"
	}
	if err := hp.sm.SetSetting(hooks.SettingKeyFailPrefix+siteID, val); err != nil {
		hp.state.ShowError("Failed to save hooks setting: " + err.Error())
		return
	}
}

func (hp *HooksPanel) runAdd(siteID string, h hooks.Hook) {
	if err := hp.provider.AddSiteHook(&h); err != nil {
		hp.state.ShowError(formatHookErr("add", err))
		return
	}
	hp.toasts.ShowSuccess("Hook added")
	hp.reload(siteID)
	hp.state.Invalidate()
}

func (hp *HooksPanel) runUpdate(siteID string, h hooks.Hook) {
	if err := hp.provider.UpdateSiteHook(&h); err != nil {
		hp.state.ShowError(formatHookErr("save", err))
		return
	}
	hp.reload(siteID)
	hp.state.Invalidate()
}

func (hp *HooksPanel) runDelete(siteID string, id int64) {
	if err := hp.provider.DeleteSiteHook(id); err != nil {
		hp.state.ShowError(formatHookErr("delete", err))
		return
	}
	hp.toasts.ShowSuccess("Hook deleted")
	hp.reload(siteID)
	hp.state.Invalidate()
}

func (hp *HooksPanel) runMove(siteID string, h hooks.Hook, delta int) {
	hp.mu.Lock()
	bucket := append([]hooks.Hook(nil), hp.groups[h.Event]...)
	hp.mu.Unlock()

	idx := -1
	for i := range bucket {
		if bucket[i].ID == h.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	target := idx + delta
	if target < 0 || target >= len(bucket) {
		return
	}
	bucket[idx], bucket[target] = bucket[target], bucket[idx]
	ids := make([]int64, len(bucket))
	for i, b := range bucket {
		ids[i] = b.ID
	}
	if err := hp.provider.ReorderSiteHooks(siteID, h.Event, ids); err != nil {
		hp.state.ShowError(formatHookErr("reorder", err))
		return
	}
	hp.reload(siteID)
	hp.state.Invalidate()
}

func (hp *HooksPanel) runOne(h hooks.Hook) {
	hp.state.HookTaskStarted(h.SiteID, h)
	go func() {
		if _, err := hp.sm.RunHookNow(context.Background(), h); err != nil {
			hp.state.ShowError("Run failed: " + err.Error())
		}
	}()
}

func (hp *HooksPanel) runEvent(ev hooks.Event) {
	hp.mu.Lock()
	bucket := append([]hooks.Hook(nil), hp.groups[ev]...)
	hp.mu.Unlock()
	if len(bucket) == 0 {
		hp.toasts.ShowInfo("No hooks for " + string(ev))
		return
	}
	go func() {
		for _, h := range bucket {
			if !h.Enabled {
				continue
			}
			hp.state.HookTaskStarted(h.SiteID, h)
			if _, err := hp.sm.RunHookNow(context.Background(), h); err != nil {
				hp.state.ShowError("Hook failed: " + err.Error())
				return
			}
		}
		hp.toasts.ShowSuccess("Ran all hooks for " + string(ev))
	}()
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// defaultTaskTypeForEvent picks a sensible default for the editor's
// initial task-type selection — exec-host for events that fire while
// containers are down, exec for everything else.
func defaultTaskTypeForEvent(ev hooks.Event) hooks.TaskType {
	if ev.AllowsContainerTasks() {
		return hooks.TaskExec
	}
	return hooks.TaskExecHost
}

// formatHookErr converts a storage / runner error into a user-readable
// message. Validation errors expose their own message; storage errors are
// prefixed with the verb.
func formatHookErr(verb string, err error) string {
	if errors.Is(err, hooks.ErrHookInvalid) || errors.Is(err, hooks.ErrEmptyCommand) {
		return strings.ToUpper(verb[:1]) + verb[1:] + ": " + err.Error()
	}
	if errors.Is(err, storage.ErrHookNotFound) {
		return "Hook no longer exists"
	}
	return "Failed to " + verb + " hook: " + err.Error()
}
