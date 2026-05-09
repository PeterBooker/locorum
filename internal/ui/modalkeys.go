package ui

import (
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
)

// ModalKeyResult reports which key terminated a frame's key processing.
type ModalKeyResult struct {
	Escape bool
	Enter  bool
}

// ProcessModalKeys consumes Escape/Enter key events delivered to the given
// tag and returns which one fired (if any). Call once per frame from
// HandleUserInteractions while the modal is visible.
//
// The caller is responsible for declaring the tag in the layout pass via
// FocusModalKeys, which also requests focus the first time it is shown.
func ProcessModalKeys(gtx layout.Context, tag event.Tag) ModalKeyResult {
	var r ModalKeyResult
	for {
		ev, ok := gtx.Event(
			key.Filter{Focus: tag, Name: key.NameEscape},
			key.Filter{Focus: tag, Name: key.NameReturn},
			key.Filter{Focus: tag, Name: key.NameEnter},
		)
		if !ok {
			break
		}
		ke, ok := ev.(key.Event)
		if !ok || ke.State != key.Press {
			continue
		}
		switch ke.Name {
		case key.NameEscape:
			r.Escape = true
		case key.NameReturn, key.NameEnter:
			r.Enter = true
		}
	}
	return r
}

// ModalFocus declares tag as a key event receiver and requests focus on
// the first call after the modal becomes visible. Call from the modal's
// Layout() before Process is called for the next frame.
type ModalFocus struct {
	Tag     event.Tag
	focused bool
}

// NewModalFocus creates a focus tracker for a modal. Tag should be a stable
// pointer (e.g. &ModalFocus{} stored on the modal struct).
func NewModalFocus() *ModalFocus {
	mf := &ModalFocus{}
	mf.Tag = mf
	return mf
}

// Layout registers the focus tag and requests keyboard focus the first frame
// it is shown. Call OnHide when the modal closes to re-arm focus for next time.
func (mf *ModalFocus) Layout(gtx layout.Context) {
	event.Op(gtx.Ops, mf.Tag)
	if !mf.focused {
		gtx.Execute(key.FocusCmd{Tag: mf.Tag})
		mf.focused = true
	}
}

// OnHide resets the focus tracker so the next time the modal is shown,
// focus is requested again.
func (mf *ModalFocus) OnHide() { mf.focused = false }
