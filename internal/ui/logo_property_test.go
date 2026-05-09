package ui

import (
	"image"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// Catches the recurring "layout.Center + Min=0" bug class documented
// in CLAUDE.md.
func TestLayoutLogo_ConstraintRobustness(t *testing.T) {
	cases := []struct {
		name string
		cons layout.Constraints
		size unit.Dp
	}{
		{"normal-20", layout.Constraints{Min: image.Pt(20, 20), Max: image.Pt(20, 20)}, unit.Dp(20)},
		{"zero-min", layout.Constraints{Max: image.Pt(40, 40)}, unit.Dp(20)},
		{"tiny", layout.Constraints{Max: image.Pt(8, 8)}, unit.Dp(6)},
		{"huge", layout.Constraints{Max: image.Pt(8000, 8000)}, unit.Dp(64)},
		{"zero-size-input", layout.Constraints{Min: image.Pt(20, 20), Max: image.Pt(20, 20)}, unit.Dp(0)},
	}
	th := NewTheme()
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			var ops op.Ops
			gtx := layout.Context{Constraints: c.cons, Ops: &ops}
			dims := LayoutLogo(gtx, th, c.size)
			if dims.Size.X < 0 || dims.Size.Y < 0 {
				t.Fatalf("negative dims: %+v", dims)
			}
			if c.cons.Max.X > 0 && dims.Size.X > c.cons.Max.X {
				t.Errorf("X exceeds Max: %d > %d", dims.Size.X, c.cons.Max.X)
			}
			if c.cons.Max.Y > 0 && dims.Size.Y > c.cons.Max.Y {
				t.Errorf("Y exceeds Max: %d > %d", dims.Size.Y, c.cons.Max.Y)
			}
		})
	}
}

// 1ms is generous against the 16.6ms 60 fps budget — a real regression
// dwarfs the threshold; cushion absorbs CI noise.
func TestLayoutLogo_FrameBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("frame-budget assertion is timing-sensitive")
	}
	th := NewTheme()
	var ops op.Ops
	gtx := layout.Context{
		Constraints: layout.Constraints{
			Min: image.Pt(20, 20),
			Max: image.Pt(20, 20),
		},
		Ops: &ops,
	}
	res := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ops.Reset()
			LayoutLogo(gtx, th, unit.Dp(20))
		}
	})
	if nsPer := res.NsPerOp(); nsPer > 1_000_000 {
		t.Errorf("LayoutLogo budget exceeded: %dns/op (limit 1ms)", nsPer)
	}
}
