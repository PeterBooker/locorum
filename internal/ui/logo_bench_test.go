package ui

import (
	"image"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

func BenchmarkLayoutLogo(b *testing.B) {
	th := NewTheme()
	var ops op.Ops
	gtx := layout.Context{
		Constraints: layout.Constraints{
			Min: image.Pt(20, 20),
			Max: image.Pt(20, 20),
		},
		Ops: &ops,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ops.Reset()
		_ = LayoutLogo(gtx, th, unit.Dp(20))
	}
}
