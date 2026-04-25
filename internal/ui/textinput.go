package ui

import (
	"gioui.org/layout"
	"gioui.org/widget"
)

// TextInput wraps widget.Editor with a self-resetting Changed() helper that
// returns true the first time it's called after the user has modified the
// text, then resets until the next change.
type TextInput struct {
	Editor widget.Editor

	prev    string
	primed  bool
	changed bool
}

// NewTextInput returns a single-line TextInput preloaded with the given value.
func NewTextInput(initial string) *TextInput {
	t := &TextInput{}
	t.Editor.SingleLine = true
	t.Editor.SetText(initial)
	t.prev = initial
	t.primed = true
	return t
}

// NewMultiline returns a multi-line TextInput.
func NewMultiline(initial string) *TextInput {
	t := &TextInput{}
	t.Editor.SetText(initial)
	t.prev = initial
	t.primed = true
	return t
}

// Text returns the current editor text.
func (t *TextInput) Text() string { return t.Editor.Text() }

// SetText replaces the editor text and resyncs the Changed baseline so a
// programmatic update doesn't read as user input.
func (t *TextInput) SetText(s string) {
	t.Editor.SetText(s)
	t.prev = s
	t.changed = false
	t.primed = true
}

// Update should be called once per frame (typically from
// HandleUserInteractions). It reads the editor's current text and flips the
// internal changed flag if it differs from the previous frame's value.
func (t *TextInput) Update(gtx layout.Context) {
	cur := t.Editor.Text()
	if !t.primed {
		t.prev = cur
		t.primed = true
		return
	}
	if cur != t.prev {
		t.prev = cur
		t.changed = true
	}
}

// Changed returns true if the text changed since the last call to Changed,
// then resets. Pair with Update() each frame.
func (t *TextInput) Changed() bool {
	c := t.changed
	t.changed = false
	return c
}
