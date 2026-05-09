package ui

import (
	"fmt"
	"image"
	"image/color"
	"sync"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/health"
)

// HealthPanel renders the System Health card in the Settings panel. It
// reads the snapshot from UIState (atomic, lock-free) every frame and
// renders one row per Finding plus a "Re-check now" button.
//
// HealthPanel does NOT own the runner — it only reads. Action invocations
// route back through the runner via the optional submitAction callback,
// which the wiring layer (main.go) provides as a closure over
// runner.SubmitAction.
type HealthPanel struct {
	state        *UIState
	submitAction func(id string, a health.Action) error
	runNow       func()

	recheckBtn widget.Clickable

	// findingButtons keeps a stable widget.Clickable per finding key
	// across frames so click-state survives between renders. New keys
	// are allocated on first Layout pass; no eviction (the set is
	// bounded by the number of distinct findings, ~10).
	mu             sync.Mutex
	findingButtons map[string]*widget.Clickable
}

// NewHealthPanel constructs the panel. submitAction is the runner's
// SubmitAction wrapped in a closure; runNow is runner.RunNow + Snapshot
// publication. Both may be nil for tests.
func NewHealthPanel(state *UIState, submitAction func(id string, a health.Action) error, runNow func()) *HealthPanel {
	return &HealthPanel{
		state:          state,
		submitAction:   submitAction,
		runNow:         runNow,
		findingButtons: make(map[string]*widget.Clickable),
	}
}

// HandleUserInteractions reads the re-check button and any per-finding
// action button. Called by the parent panel each frame.
func (p *HealthPanel) HandleUserInteractions(gtx layout.Context) {
	if p.recheckBtn.Clicked(gtx) && p.runNow != nil {
		go p.runNow()
	}
	snap := p.state.HealthSnapshot()
	for i := range snap.Findings {
		f := &snap.Findings[i]
		if f.Action == nil {
			continue
		}
		btn := p.buttonFor(findingButtonKey(*f))
		if btn.Clicked(gtx) && p.submitAction != nil {
			act := *f.Action
			id := f.ID
			go func() {
				if err := p.submitAction(id, act); err != nil {
					p.state.ShowError("Action failed: " + err.Error())
				}
			}()
		}
	}
}

// Layout renders the System Health card.
func (p *HealthPanel) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	snap := p.state.HealthSnapshot()
	header := func(gtx layout.Context) layout.Dimensions {
		label := "Re-check now"
		if snap.Running {
			label = "Re-checking…"
		}
		return th.SmallGated(gtx, &p.recheckBtn, label, !snap.Running && p.runNow != nil)
	}
	return panelWithAction(gtx, th, "System Health", header, func(gtx layout.Context) layout.Dimensions {
		return p.layoutBody(gtx, th, snap)
	})
}

func (p *HealthPanel) layoutBody(gtx layout.Context, th *Theme, snap health.Snapshot) layout.Dimensions {
	if len(snap.Findings) == 0 {
		lbl := material.Body2(th.Theme, "All systems nominal.")
		lbl.Color = th.Color.Fg2
		lbl.TextSize = th.Sizes.Body
		return lbl.Layout(gtx)
	}
	children := make([]layout.FlexChild, 0, len(snap.Findings)+1)
	for i := range snap.Findings {
		f := snap.Findings[i]
		first := i == 0
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			top := th.Spacing.SM
			if first {
				top = 0
			}
			return layout.Inset{Top: top}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.layoutFinding(gtx, th, f)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func (p *HealthPanel) layoutFinding(gtx layout.Context, th *Theme, f health.Finding) layout.Dimensions {
	bg, fg := severityColors(th, f.Severity)
	return RoundedFill(gtx, bg, th.Radii.R2, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return severityDot(gtx, fg)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								title := f.Title
								if f.Stale {
									title = "↻ " + title
								}
								lbl := material.Body2(th.Theme, title)
								lbl.Color = th.Color.Fg
								lbl.TextSize = th.Sizes.Body
								lbl.Font.Weight = font.SemiBold
								return lbl.Layout(gtx)
							})
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if f.Detail == "" {
						return layout.Dimensions{}
					}
					lbl := material.Body2(th.Theme, f.Detail)
					lbl.Color = th.Color.Fg2
					lbl.TextSize = th.Sizes.Body
					return layout.Inset{Top: unit.Dp(4), Left: unit.Dp(20)}.Layout(gtx, lbl.Layout)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if f.Remediation == "" {
						return layout.Dimensions{}
					}
					lbl := material.Body2(th.Theme, "→ "+f.Remediation)
					lbl.Color = th.Color.Fg2
					lbl.TextSize = th.Sizes.Mono
					return layout.Inset{Top: unit.Dp(4), Left: unit.Dp(20)}.Layout(gtx, lbl.Layout)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if f.Action == nil {
						return layout.Dimensions{}
					}
					return layout.Inset{Top: unit.Dp(8), Left: unit.Dp(20)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						btn := p.buttonFor(findingButtonKey(f))
						return th.Small(gtx, btn, f.Action.Label)
					})
				}),
			)
		})
	})
}

func (p *HealthPanel) buttonFor(key string) *widget.Clickable {
	p.mu.Lock()
	defer p.mu.Unlock()
	if b, ok := p.findingButtons[key]; ok {
		return b
	}
	b := &widget.Clickable{}
	p.findingButtons[key] = b
	return b
}

// severityColors maps a finding severity onto (background, accent).
func severityColors(th *Theme, sev health.Severity) (color.NRGBA, color.NRGBA) {
	switch sev {
	case health.SeverityBlocker:
		return th.Color.ErrSoft, th.Color.Err
	case health.SeverityWarn:
		return th.Color.WarnSoft, th.Color.Warn
	case health.SeverityInfo:
		return th.Color.AccentSoft, th.Color.Accent
	}
	return th.Color.Bg2, th.Color.Fg3
}

func severityDot(gtx layout.Context, col color.NRGBA) layout.Dimensions {
	d := gtx.Dp(unit.Dp(10))
	r := gtx.Dp(unit.Dp(5))
	rect := image.Rectangle{Max: image.Point{X: d, Y: d}}
	defer clip.RRect{Rect: rect, NE: r, NW: r, SE: r, SW: r}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, col)
	return layout.Dimensions{Size: image.Point{X: d, Y: d}}
}

// findingButtonKey is the stable key for a finding's action button. Action
// labels can change between runs; only ID + DedupKey are stable enough.
func findingButtonKey(f health.Finding) string {
	return f.ID + "|" + f.DedupKey
}

// HealthBlockerModal renders the full-screen modal that fires when at
// least one Blocker finding is present. The user has two options:
// "Re-check" (calls runner.RunNow) and "Quit". There is deliberately no
// "Ignore" — DDEV's discipline.
type HealthBlockerModal struct {
	state   *UIState
	runNow  func()
	onQuit  func()
	recheck widget.Clickable
	quit    widget.Clickable
}

// NewHealthBlockerModal builds the modal. quit is invoked when the user
// clicks Quit; main.go wires it to a graceful shutdown.
func NewHealthBlockerModal(state *UIState, runNow func(), onQuit func()) *HealthBlockerModal {
	return &HealthBlockerModal{state: state, runNow: runNow, onQuit: onQuit}
}

// HasBlocker reports whether the current snapshot contains a blocker.
func (m *HealthBlockerModal) HasBlocker() bool {
	snap := m.state.HealthSnapshot()
	for _, f := range snap.Findings {
		if f.Severity == health.SeverityBlocker {
			return true
		}
	}
	return false
}

// HandleUserInteractions processes button clicks. Called by the root UI
// before Layout when the modal is visible.
func (m *HealthBlockerModal) HandleUserInteractions(gtx layout.Context) {
	if m.recheck.Clicked(gtx) && m.runNow != nil {
		go m.runNow()
	}
	if m.quit.Clicked(gtx) && m.onQuit != nil {
		m.onQuit()
	}
}

// Layout renders the blocker modal as a full-screen overlay.
func (m *HealthBlockerModal) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	snap := m.state.HealthSnapshot()
	var blockers []health.Finding
	for _, f := range snap.Findings {
		if f.Severity == health.SeverityBlocker {
			blockers = append(blockers, f)
		}
	}
	if len(blockers) == 0 {
		return layout.Dimensions{}
	}
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			return FillBackground(gtx, th.Color.Overlay, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: gtx.Constraints.Min}
			})
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = gtx.Constraints.Max
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.X = gtx.Dp(th.Dims.ModalWidth)
				return widget.Border{
					Color: th.Color.Err, Width: unit.Dp(2), CornerRadius: th.Radii.R3,
				}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return RoundedFill(gtx, th.Color.Bg1, th.Radii.R3, func(gtx layout.Context) layout.Dimensions {
						return layout.UniformInset(unit.Dp(20)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return m.layoutContent(gtx, th, blockers, snap.Running)
						})
					})
				})
			})
		}),
	)
}

func (m *HealthBlockerModal) layoutContent(gtx layout.Context, th *Theme, blockers []health.Finding, running bool) layout.Dimensions {
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, fmt.Sprintf("Locorum can't run (%d issue%s):", len(blockers), pluralS(len(blockers))))
			lbl.Color = th.Color.Err
			lbl.TextSize = th.Sizes.H1
			lbl.Font.Weight = font.SemiBold
			return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, lbl.Layout)
		}),
	}
	for i, f := range blockers {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			top := th.Spacing.SM
			if i == 0 {
				top = 0
			}
			return layout.Inset{Top: top}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(th.Theme, f.Title)
						lbl.Color = th.Color.Fg
						lbl.TextSize = th.Sizes.Body
						lbl.Font.Weight = font.SemiBold
						return lbl.Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if f.Detail == "" {
							return layout.Dimensions{}
						}
						lbl := material.Body2(th.Theme, f.Detail)
						lbl.Color = th.Color.Fg2
						lbl.TextSize = th.Sizes.Body
						return layout.Inset{Top: unit.Dp(2)}.Layout(gtx, lbl.Layout)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if f.Remediation == "" {
							return layout.Dimensions{}
						}
						lbl := material.Body2(th.Theme, "→ "+f.Remediation)
						lbl.Color = th.Color.Fg2
						lbl.TextSize = th.Sizes.Mono
						return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, lbl.Layout)
					}),
				)
			})
		}))
	}
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: th.Spacing.LG}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceStart}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return th.SmallGated(gtx, &m.recheck, "Re-check", !running)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return DangerButton(gtx, th, &m.quit, "Quit")
				}),
			)
		})
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// HealthBadgeKind is the styling key for the nav-rail badge.
type HealthBadgeKind int

const (
	HealthBadgeNone    HealthBadgeKind = iota
	HealthBadgeInfo                    // hidden in the rail; shown only in the panel
	HealthBadgeUpdate                  // accent — surfaced when an update is available
	HealthBadgeWarn                    // amber
	HealthBadgeBlocker                 // red
)

// HealthBadgeFor maps a snapshot onto the rail-badge styling. Returns
// HealthBadgeNone when the snapshot has no findings or only Info-level
// ones.
func HealthBadgeFor(snap health.Snapshot) HealthBadgeKind {
	hi := snap.HighestSeverity()
	switch hi {
	case health.SeverityBlocker:
		return HealthBadgeBlocker
	case health.SeverityWarn:
		return HealthBadgeWarn
	}
	return HealthBadgeNone
}

// LayoutHealthBadge paints a small dot in the given foreground colour.
// Used by the nav rail to flag the Settings entry. Caller is responsible
// for positioning.
func LayoutHealthBadge(gtx layout.Context, kind HealthBadgeKind, th *Theme) layout.Dimensions {
	switch kind {
	case HealthBadgeNone, HealthBadgeInfo:
		return layout.Dimensions{}
	case HealthBadgeUpdate:
		return severityDot(gtx, th.Color.Accent)
	case HealthBadgeBlocker:
		return severityDot(gtx, th.Color.Err)
	default:
		return severityDot(gtx, th.Color.Warn)
	}
}
