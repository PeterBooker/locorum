package ui

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
)

// CloneModal is a dialog for cloning a site with a new name.
type CloneModal struct {
	state  *UIState
	sm     *sites.SiteManager
	toasts *Notifications

	nameEditor widget.Editor
	cloneBtn   widget.Clickable
	cancelBtn  widget.Clickable

	keys *ModalFocus
	anim *modalShowState

	lastTargetID string
}

func NewCloneModal(state *UIState, sm *sites.SiteManager, toasts *Notifications) *CloneModal {
	m := &CloneModal{state: state, sm: sm, toasts: toasts, keys: NewModalFocus(), anim: NewModalAnim()}
	m.nameEditor.SingleLine = true
	return m
}

// HandleUserInteractions processes Cancel / Clone button clicks.
// Called by the root UI before Layout when the modal is visible.
func (cm *CloneModal) HandleUserInteractions(gtx layout.Context) {
	show, targetID, _ := cm.state.GetCloneModalState()
	if !show {
		return
	}

	keys := ProcessModalKeys(gtx, cm.keys.Tag)

	if cm.cancelBtn.Clicked(gtx) || keys.Escape {
		cm.state.DismissCloneModal()
		cm.lastTargetID = ""
		cm.keys.OnHide()
		cm.anim.Hide()
		return
	}

	if (cm.cloneBtn.Clicked(gtx) || keys.Enter) && !cm.state.IsCloneLoading() {
		newName := cm.nameEditor.Text()
		if newName == "" {
			cm.state.ShowError("Site name is required")
		} else {
			siteID := targetID
			cm.state.SetCloneLoading(true)
			go func() {
				defer cm.state.SetCloneLoading(false)
				if err := cm.sm.CloneSite(siteID, newName); err != nil {
					cm.state.ShowError("Clone failed: " + err.Error())
					return
				}
				cm.toasts.ShowSuccess("Site cloned successfully")
				cm.state.DismissCloneModal()
				cm.lastTargetID = ""
				cm.keys.OnHide()
				cm.anim.Hide()
			}()
		}
	}
}

func (cm *CloneModal) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	show, targetID, targetName := cm.state.GetCloneModalState()
	if !show {
		return layout.Dimensions{}
	}

	// Pre-fill name when modal first appears for a target.
	if cm.lastTargetID != targetID {
		cm.lastTargetID = targetID
		cm.nameEditor.SetText(targetName + " (Copy)")
	}

	cm.anim.Show()
	return AnimatedModalOverlay(gtx, th, cm.anim, func(gtx layout.Context) layout.Dimensions {
		cm.keys.Layout(gtx)
		cloning := cm.state.IsCloneLoading()

		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.H5(th.Theme, "Clone Site")
				return layout.Inset{Bottom: unit.Dp(20)}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return LabeledInput(gtx, th, "New Site Name", &cm.nameEditor, "My Site (Copy)")
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if cloning {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return Loader(gtx, th, th.Dims.LoaderSizeSM)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							lbl := material.Body2(th.Theme, "  Cloning site...")
							lbl.Color = th.Color.TextSecondary
							return lbl.Layout(gtx)
						}),
					)
				}
				return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceStart}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return SecondaryButton(gtx, th, &cm.cancelBtn, "Cancel")
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return PrimaryButton(gtx, th, &cm.cloneBtn, "Clone")
					}),
				)
			}),
		)
	})
}
