package ui

import (
	"image"
	"image/color"
	"sync"
	"time"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// ToastType determines the visual style of a toast notification.
type ToastType int

const (
	ToastError   ToastType = iota
	ToastSuccess
	ToastInfo
)

// Toast represents a single auto-dismissing notification.
type Toast struct {
	Message   string
	Type      ToastType
	CreatedAt time.Time
	Duration  time.Duration
}

// IsExpired returns true if the toast has exceeded its display duration.
func (t Toast) IsExpired() bool {
	return time.Since(t.CreatedAt) > t.Duration
}

// ToastManager manages a list of active toast notifications.
type ToastManager struct {
	mu     sync.Mutex
	toasts []Toast
	state  *UIState
}

func NewToastManager(state *UIState) *ToastManager {
	return &ToastManager{state: state}
}

// Show adds a toast notification and schedules its auto-dismissal.
func (tm *ToastManager) Show(msg string, tt ToastType) {
	duration := 5 * time.Second
	if tt == ToastError {
		duration = 8 * time.Second
	}

	tm.mu.Lock()
	tm.toasts = append(tm.toasts, Toast{
		Message:   msg,
		Type:      tt,
		CreatedAt: time.Now(),
		Duration:  duration,
	})
	tm.mu.Unlock()
	tm.state.Invalidate()

	go func() {
		time.Sleep(duration)
		tm.state.Invalidate()
	}()
}

// ShowError is a convenience method for error toasts.
func (tm *ToastManager) ShowError(msg string) {
	tm.Show(msg, ToastError)
}

// ShowSuccess is a convenience method for success toasts.
func (tm *ToastManager) ShowSuccess(msg string) {
	tm.Show(msg, ToastSuccess)
}

// activeToasts returns non-expired toasts and cleans up expired ones.
func (tm *ToastManager) activeToasts() []Toast {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	active := tm.toasts[:0]
	for _, t := range tm.toasts {
		if !t.IsExpired() {
			active = append(active, t)
		}
	}
	tm.toasts = active

	out := make([]Toast, len(active))
	copy(out, active)
	return out
}

// Layout renders all active toast notifications in the top-right corner.
func (tm *ToastManager) Layout(gtx layout.Context, th *material.Theme) layout.Dimensions {
	toasts := tm.activeToasts()
	if len(toasts) == 0 {
		return layout.Dimensions{}
	}

	// Position in top-right corner
	return layout.NE.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: SpaceLG, Right: SpaceLG}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			children := make([]layout.FlexChild, len(toasts))
			for i, toast := range toasts {
				toast := toast
				children[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Bottom: SpaceSM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layoutToast(gtx, th, toast)
					})
				})
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		})
	})
}

func layoutToast(gtx layout.Context, th *material.Theme, toast Toast) layout.Dimensions {
	var bg, fg color.NRGBA
	switch toast.Type {
	case ToastError:
		bg = ColorRed100
		fg = ColorRed800
	case ToastSuccess:
		bg = ColorGreen100
		fg = ColorGreen800
	case ToastInfo:
		bg = ColorBlue100
		fg = ColorBlue800
	}

	// Constrain width
	gtx.Constraints.Max.X = gtx.Dp(unit.Dp(360))
	gtx.Constraints.Min.X = gtx.Dp(unit.Dp(240))

	return FillBackground(gtx, bg, func(gtx layout.Context) layout.Dimensions {
		rr := gtx.Dp(RadiusMD)
		defer clip.RRect{
			Rect: image.Rectangle{Max: gtx.Constraints.Min},
			NE:   rr, NW: rr, SE: rr, SW: rr,
		}.Push(gtx.Ops).Pop()

		return layout.Inset{
			Top: SpaceMD, Bottom: SpaceMD,
			Left: SpaceLG, Right: SpaceLG,
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th, toast.Message)
			lbl.Color = fg
			lbl.TextSize = TextBase
			return lbl.Layout(gtx)
		})
	})
}
