package ui

import (
	"fmt"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/utils"
)

// HookOutput renders the live output panel for the most recent hook run on
// the selected site. It tracks no per-output state of its own — everything
// it draws comes from UIState.HookSnapshot.
type HookOutput struct {
	state *UIState
	sm    *sites.SiteManager

	output      *OutputView
	openLogBtn  widget.Clickable
	clearBtn    widget.Clickable
	lastLogPath string
}

// NewHookOutput builds a fresh output panel. `sm` is unused at present but
// reserved for future cancel-run / re-run integration.
func NewHookOutput(state *UIState, sm *sites.SiteManager) *HookOutput {
	return &HookOutput{
		state:  state,
		sm:     sm,
		output: NewOutputView(),
	}
}

// HandleUserInteractions handles open-log and clear-output clicks.
func (ho *HookOutput) HandleUserInteractions(gtx layout.Context, siteID string) {
	if ho.openLogBtn.Clicked(gtx) && ho.lastLogPath != "" {
		path := ho.lastLogPath
		go func() {
			if err := utils.OpenPath(path); err != nil {
				ho.state.ShowError("Failed to open log: " + err.Error())
			}
		}()
	}
	if ho.clearBtn.Clicked(gtx) {
		ho.state.ClearHookOutput(siteID)
	}
}

// Layout renders the panel for the given site.
func (ho *HookOutput) Layout(gtx layout.Context, th *Theme, siteID string) layout.Dimensions {
	snap := ho.state.HookSnapshot(siteID)
	if !snap.HasActivity() {
		return layout.Dimensions{}
	}

	// Track the latest log path across frames so the open-log button works
	// after the run ends.
	if snap.Last != nil && snap.Last.LogPath != "" {
		ho.lastLogPath = snap.Last.LogPath
	} else if snap.Summary != nil && snap.Summary.LogPath != "" {
		ho.lastLogPath = snap.Summary.LogPath
	}

	body := buildOutputBody(snap)

	return Section(gtx, th, "Hook output", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return ho.layoutHeader(gtx, th, snap)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return ho.output.Layout(gtx, th, body, "Hook output will appear here while a run is in progress.", th.Dims.OutputAreaMax)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceEnd}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if ho.lastLogPath == "" {
								return layout.Dimensions{}
							}
							return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return SmallButton(gtx, th, &ho.openLogBtn, "Open full log")
							})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return SmallButton(gtx, th, &ho.clearBtn, "Clear")
						}),
					)
				})
			}),
		)
	})
}

// layoutHeader shows what's happening right now: spinner + task name while
// running, or a "Run summary" badge once the run completes.
func (ho *HookOutput) layoutHeader(gtx layout.Context, th *Theme, snap HookSnapshot) layout.Dimensions {
	if snap.Running != nil {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return Loader(gtx, th, th.Dims.LoaderSizeSM)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, "Running: "+formatHookTitle(*snap.Running))
				lbl.Color = th.Color.TextStrong
				return lbl.Layout(gtx)
			}),
		)
	}

	if snap.Summary != nil {
		s := *snap.Summary
		text := fmt.Sprintf("Done — %d ok, %d failed, %d skipped (%s)",
			s.Succeeded, s.Failed, s.Skipped, s.Duration.Truncate(time.Millisecond))
		col := th.Color.Success
		if s.Failed > 0 {
			col = th.Color.Danger
		}
		lbl := material.Body2(th.Theme, text)
		lbl.Color = col
		return lbl.Layout(gtx)
	}

	if snap.Last != nil {
		text := "Last task exited " + fmt.Sprint(snap.Last.ExitCode)
		col := th.Color.TextSecondary
		if !snap.Last.Succeeded() {
			col = th.Color.Danger
		}
		lbl := material.Body2(th.Theme, text)
		lbl.Color = col
		return lbl.Layout(gtx)
	}

	return layout.Dimensions{}
}

// buildOutputBody concatenates the captured lines into a single text block.
// stderr lines are prefixed with "! " so they are visually distinct in the
// monospace output area.
func buildOutputBody(snap HookSnapshot) string {
	if len(snap.Lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(len(snap.Lines) * 32)
	for _, line := range snap.Lines {
		if line.Stderr {
			b.WriteString("! ")
		}
		b.WriteString(line.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

// formatHookTitle returns a short label for the running hook.
func formatHookTitle(h hooks.Hook) string {
	cmd := h.Command
	if len(cmd) > 60 {
		cmd = cmd[:60] + "…"
	}
	return string(h.TaskType) + " " + cmd
}
