package ui

import (
	"context"
	"sync/atomic"

	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
)

// NewResetInfraCard builds the Diagnostics → "Reset Locorum
// Infrastructure" card.
//
// resetFn does the actual wipe + bring-up. Wired from main.go to
// app.App.ResetInfrastructure so the UI doesn't import internal/app.
// reconcileFn marks every site as stopped after the wipe — the UI
// passes sm.ReconcileState here so the site-list reflects reality.
//
// confirmation goes through a generic confirm dialog. While the action
// is in flight the card disables the button (atomic guard) so a double-
// click can't dispatch two resets at once. Errors surface as a toast
// (via state.ShowError); success surfaces as a toast (via toasts.ShowSuccess).
func NewResetInfraCard(state *UIState, sm *sites.SiteManager, toasts *Notifications, resetFn func(context.Context) error) *ResetInfraCard {
	if resetFn == nil {
		// A safety net: a nil resetFn means main.go forgot to wire the
		// closure. Render the card disabled rather than crashing on click.
		resetFn = func(context.Context) error { return errResetNotWired }
	}

	r := &resetInfraCardImpl{
		state:   state,
		sm:      sm,
		toasts:  toasts,
		resetFn: resetFn,
	}
	return &ResetInfraCard{
		HandleUserInteractionsFn: r.HandleUserInteractions,
		LayoutFn:                 r.Layout,
	}
}

type resetInfraCardImpl struct {
	state  *UIState
	sm     *sites.SiteManager
	toasts *Notifications

	resetFn func(context.Context) error

	openBtn     widget.Clickable
	dialog      ConfirmDialog
	confirmOpen bool
	running     atomic.Bool
}

func (r *resetInfraCardImpl) HandleUserInteractions(gtx layout.Context) {
	if r.openBtn.Clicked(gtx) && !r.running.Load() {
		r.confirmOpen = true
	}
	if r.confirmOpen {
		confirmed, cancelled := r.dialog.HandleUserInteractions(gtx)
		if cancelled {
			r.confirmOpen = false
		}
		if confirmed {
			r.confirmOpen = false
			r.run()
		}
	}
}

func (r *resetInfraCardImpl) run() {
	if !r.running.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer r.running.Store(false)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := r.resetFn(ctx); err != nil {
			r.state.ShowError("Reset failed: " + err.Error())
			return
		}
		// Reflect the post-wipe truth in storage so the site list
		// shows every site as stopped.
		if r.sm != nil {
			if err := r.sm.ReconcileState(); err != nil {
				r.state.ShowError("Reset succeeded, but reconcile failed: " + err.Error())
				return
			}
		}
		r.toasts.ShowSuccess("Locorum infrastructure reset")
		r.state.Invalidate()
	}()
}

func (r *resetInfraCardImpl) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	return panel(gtx, th, "Reset Locorum Infrastructure", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, "Stop and recreate the global router, mail, and adminer containers. All running sites will be stopped. DB volumes are kept — site data is safe.")
				lbl.Color = th.Color.Fg2
				lbl.TextSize = th.Sizes.Body
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if r.running.Load() {
					return Loader(gtx, th, th.Dims.LoaderSizeSM)
				}
				return DangerButton(gtx, th, &r.openBtn, "Reset Locorum Infrastructure")
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !r.confirmOpen {
					return layout.Dimensions{}
				}
				return r.dialog.Layout(gtx, th, ConfirmDialogStyle{
					Title:        "Reset infrastructure?",
					Message:      "All running sites will be stopped. The router, mail, and adminer containers will be recreated. DB volumes are kept — site data is safe. Proceed?",
					ConfirmLabel: "Reset",
					ConfirmColor: th.Color.Err,
				})
			}),
		)
	})
}

// errResetNotWired is the safety-net error returned by NewResetInfraCard
// when no resetFn was provided. Should never reach the user in a
// correctly-wired build.
var errResetNotWired = errResetNotWiredError{}

type errResetNotWiredError struct{}

func (errResetNotWiredError) Error() string { return "reset: not wired (developer error)" }
