package ui

import (
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/hooks"
)

// hookServiceOptions is the dropdown list for task_type=exec. "" maps to
// "php" by default.
var hookServiceOptions = []string{"php", "web", "database", "redis"}

// hookTaskTypeOptions presents task types in a stable order.
var hookTaskTypeOptions = []string{"exec", "exec-host", "wp-cli"}

func hookTaskTypeAt(idx int) hooks.TaskType {
	switch idx {
	case 1:
		return hooks.TaskExecHost
	case 2:
		return hooks.TaskWPCLI
	default:
		return hooks.TaskExec
	}
}

func hookTaskTypeIndex(t hooks.TaskType) int {
	switch t {
	case hooks.TaskExecHost:
		return 1
	case hooks.TaskWPCLI:
		return 2
	default:
		return 0
	}
}

// HookEditor is the add/edit dialog for a single hook. It is created once
// per SiteDetail and mutated to represent either a new hook (Open with
// Hook.ID == 0) or an existing one (Open with Hook.ID != 0).
type HookEditor struct {
	visible bool
	editing hooks.Hook
	siteID  string

	taskTypeIdx      int
	taskTypeClicks   []widget.Clickable
	eventIdx         int
	allowedEvents    []hooks.Event
	eventDropdown    *Dropdown
	commandEditor    widget.Editor
	serviceDropdown  *Dropdown
	runAsUserEditor  widget.Editor
	enabledClickable widget.Clickable
	enabled          bool

	saveBtn   widget.Clickable
	cancelBtn widget.Clickable

	// onSubmit is invoked when Save is clicked with a valid form. The
	// supplied Hook is the assembled, validated draft — the caller
	// persists it and triggers a list refresh.
	onSubmit func(hooks.Hook)

	keys *ModalFocus
	anim *modalShowState
}

// NewHookEditor constructs a HookEditor with the active event list as
// dropdown options.
func NewHookEditor() *HookEditor {
	he := &HookEditor{
		serviceDropdown: NewDropdown(hookServiceOptions),
		taskTypeClicks:  make([]widget.Clickable, len(hookTaskTypeOptions)),
		keys:            NewModalFocus(),
		anim:            NewModalAnim(),
	}
	he.allowedEvents = hooks.ActiveEvents()
	labels := make([]string, len(he.allowedEvents))
	for i, ev := range he.allowedEvents {
		labels[i] = string(ev)
	}
	he.eventDropdown = NewDropdown(labels)
	he.runAsUserEditor.SingleLine = true
	return he
}

// IsVisible reports whether the editor is open.
func (he *HookEditor) IsVisible() bool { return he.visible }

// Open prepares the editor for adding (h.ID == 0) or editing (h.ID != 0).
// onSubmit fires when the user clicks Save with a valid form.
func (he *HookEditor) Open(siteID string, h hooks.Hook, onSubmit func(hooks.Hook)) {
	he.visible = true
	he.editing = h
	he.siteID = siteID
	he.onSubmit = onSubmit

	he.taskTypeIdx = hookTaskTypeIndex(h.TaskType)
	he.eventIdx = he.findEventIndex(h.Event)
	he.eventDropdown.Selected = he.eventIdx
	he.commandEditor.SetText(h.Command)
	he.runAsUserEditor.SetText(h.RunAsUser)
	// New hooks default to enabled; edits preserve the existing flag.
	he.enabled = h.Enabled || h.ID == 0

	he.serviceDropdown.Selected = 0
	for i, opt := range hookServiceOptions {
		if opt == h.Service {
			he.serviceDropdown.Selected = i
			break
		}
	}

	he.anim.Hide()
}

// Close hides the editor without saving.
func (he *HookEditor) Close() {
	he.visible = false
	he.onSubmit = nil
	he.keys.OnHide()
	he.anim.Hide()
}

func (he *HookEditor) findEventIndex(ev hooks.Event) int {
	for i, e := range he.allowedEvents {
		if e == ev {
			return i
		}
	}
	return 0
}

// HandleUserInteractions runs each frame the editor is visible.
func (he *HookEditor) HandleUserInteractions(gtx layout.Context, state *UIState) {
	if !he.visible {
		return
	}
	keys := ProcessModalKeys(gtx, he.keys.Tag)
	if he.cancelBtn.Clicked(gtx) || keys.Escape {
		he.Close()
		return
	}
	if he.enabledClickable.Clicked(gtx) {
		he.enabled = !he.enabled
	}

	he.eventIdx = he.eventDropdown.Selected
	containerOK := he.allowedEvents[he.eventIdx].AllowsContainerTasks()
	for i := range he.taskTypeClicks {
		if he.taskTypeClicks[i].Clicked(gtx) {
			next := hookTaskTypeAt(i)
			if (next == hooks.TaskExec || next == hooks.TaskWPCLI) && !containerOK {
				continue // illegal combination — refuse the click
			}
			he.taskTypeIdx = i
		}
	}
	// If the chosen event no longer allows the current task type, force
	// the user back to exec-host.
	current := hookTaskTypeAt(he.taskTypeIdx)
	if !containerOK && (current == hooks.TaskExec || current == hooks.TaskWPCLI) {
		he.taskTypeIdx = hookTaskTypeIndex(hooks.TaskExecHost)
	}

	if he.saveBtn.Clicked(gtx) || keys.Enter {
		draft, err := he.assemble()
		if err != nil {
			state.ShowError(err.Error())
			return
		}
		if he.onSubmit != nil {
			he.onSubmit(draft)
		}
		he.Close()
	}
}

func (he *HookEditor) assemble() (hooks.Hook, error) {
	if he.eventIdx < 0 || he.eventIdx >= len(he.allowedEvents) {
		return hooks.Hook{}, errInvalidEvent
	}
	taskType := hookTaskTypeAt(he.taskTypeIdx)
	command := strings.TrimSpace(he.commandEditor.Text())
	service := ""
	user := ""
	if taskType == hooks.TaskExec {
		if he.serviceDropdown.Selected >= 0 && he.serviceDropdown.Selected < len(hookServiceOptions) {
			service = hookServiceOptions[he.serviceDropdown.Selected]
		}
		user = strings.TrimSpace(he.runAsUserEditor.Text())
	}

	draft := he.editing
	draft.SiteID = he.siteID
	draft.Event = he.allowedEvents[he.eventIdx]
	draft.TaskType = taskType
	draft.Command = command
	draft.Service = service
	draft.RunAsUser = user
	draft.Enabled = he.enabled

	if err := draft.Validate(); err != nil {
		return hooks.Hook{}, err
	}
	return draft, nil
}

var errInvalidEvent = &editorErr{msg: "select a valid lifecycle event"}

type editorErr struct{ msg string }

func (e *editorErr) Error() string { return e.msg }

// Layout draws the modal. Caller is responsible for only invoking when
// IsVisible() is true.
func (he *HookEditor) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	he.anim.Show()
	return AnimatedModalOverlay(gtx, th, he.anim, func(gtx layout.Context) layout.Dimensions {
		he.keys.Layout(gtx)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				title := "Add hook"
				if he.editing.ID != 0 {
					title = "Edit hook"
				}
				lbl := material.H5(th.Theme, title)
				return layout.Inset{Bottom: th.Spacing.LG}.Layout(gtx, lbl.Layout)
			}),

			// Event selector
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							lbl := material.Body2(th.Theme, "Event")
							lbl.Color = th.Color.TextStrong
							return layout.Inset{Bottom: th.Spacing.XS}.Layout(gtx, lbl.Layout)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return he.eventDropdown.Layout(gtx, th, "")
						}),
					)
				})
			}),

			// Task type radio
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return he.layoutTaskTypeRow(gtx, th)
				})
			}),

			// Command
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							lbl := material.Body2(th.Theme, "Command")
							lbl.Color = th.Color.TextStrong
							return layout.Inset{Bottom: th.Spacing.XS}.Layout(gtx, lbl.Layout)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return BorderedMonoEditor(gtx, th, &he.commandEditor, he.commandHint())
						}),
					)
				})
			}),

			// Service + RunAsUser (only for task=exec)
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if hookTaskTypeAt(he.taskTypeIdx) != hooks.TaskExec {
					return layout.Dimensions{}
				}
				return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return he.serviceDropdown.Layout(gtx, th, "Service")
							})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return LabeledInput(gtx, th, "Run as user (optional)", &he.runAsUserEditor, "e.g. root or 1000:1000")
						}),
					)
				})
			}),

			// Enabled toggle
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: unit.Dp(20)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return material.Clickable(gtx, &he.enabledClickable, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layoutCheckbox(gtx, th, he.enabled)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								lbl := material.Body2(th.Theme, "Enabled")
								return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, lbl.Layout)
							}),
						)
					})
				})
			}),

			// Save / Cancel row
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceStart}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return SecondaryButton(gtx, th, &he.cancelBtn, "Cancel")
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return PrimaryButton(gtx, th, &he.saveBtn, "Save")
					}),
				)
			}),
		)
	})
}

func (he *HookEditor) commandHint() string {
	switch hookTaskTypeAt(he.taskTypeIdx) {
	case hooks.TaskExecHost:
		return `echo "Hello from ${LOCORUM_SITE_NAME}"`
	case hooks.TaskWPCLI:
		return "option get siteurl"
	}
	return "tail -n 50 wp-content/debug.log"
}

func (he *HookEditor) layoutTaskTypeRow(gtx layout.Context, th *Theme) layout.Dimensions {
	allowedEvent := he.allowedEvents[he.eventIdx]
	containerOK := allowedEvent.AllowsContainerTasks()

	children := make([]layout.FlexChild, len(hookTaskTypeOptions))
	for i, label := range hookTaskTypeOptions {
		idxIsContainer := hookTaskTypeAt(i) != hooks.TaskExecHost
		disabled := !containerOK && idxIsContainer
		children[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return he.layoutTaskTypeOption(gtx, th, label, i, disabled)
			})
		})
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, "Task type")
			lbl.Color = th.Color.TextStrong
			return layout.Inset{Bottom: th.Spacing.XS}.Layout(gtx, lbl.Layout)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
		}),
	)
}

func (he *HookEditor) layoutTaskTypeOption(gtx layout.Context, th *Theme, label string, idx int, disabled bool) layout.Dimensions {
	bg := th.Color.SurfaceAlt
	fg := th.Color.TextStrong
	if idx == he.taskTypeIdx {
		bg = th.Color.Primary
		fg = th.Color.OnPrimary
	}
	if disabled {
		bg = th.Disabled(bg)
		fg = th.Disabled(fg)
		gtx = gtx.Disabled()
	}
	b := material.Button(th.Theme, &he.taskTypeClicks[idx], label)
	b.Background = bg
	b.Color = fg
	b.CornerRadius = th.Radii.SM
	b.TextSize = th.Sizes.SM
	b.Inset = layout.Inset{
		Top:    unit.Dp(6),
		Bottom: unit.Dp(6),
		Left:   unit.Dp(12),
		Right:  unit.Dp(12),
	}
	return b.Layout(gtx)
}
