package orch

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeStep struct {
	name string
	desc string
	err  error
}

func (f *fakeStep) Name() string                               { return f.name }
func (f *fakeStep) Apply(_ context.Context) error              { return errors.New("dry-run must not Apply") }
func (f *fakeStep) Rollback(_ context.Context) error           { return nil }
func (f *fakeStep) Describe(_ context.Context) (string, error) { return f.desc, f.err }

type silentStep struct{ name string }

func (s *silentStep) Name() string                     { return s.name }
func (s *silentStep) Apply(_ context.Context) error    { return errors.New("must not run") }
func (s *silentStep) Rollback(_ context.Context) error { return nil }

func TestDry_DescribesEverySupportingStep(t *testing.T) {
	plan := Plan{
		Name: "test",
		Steps: []Step{
			&fakeStep{name: "first", desc: "do first"},
			&fakeStep{name: "second", desc: "do second"},
			&silentStep{name: "no-describer"},
		},
	}
	res, err := Dry(context.Background(), plan)
	if err != nil {
		t.Fatalf("Dry: %v", err)
	}
	if len(res.Steps) != 3 {
		t.Fatalf("expected 3 step results, got %d", len(res.Steps))
	}
	if res.Steps[0].Description != "do first" || !res.Steps[0].Implemented {
		t.Fatalf("step 0 wrong: %+v", res.Steps[0])
	}
	if res.Steps[2].Implemented {
		t.Fatalf("silentStep should report Implemented=false")
	}
	if !strings.Contains(res.Steps[2].Description, "no preview") {
		t.Fatalf("silentStep description: %q", res.Steps[2].Description)
	}
}

func TestDry_RecoversFromPanic(t *testing.T) {
	plan := Plan{
		Name: "panicky",
		Steps: []Step{
			&panicStep{name: "boom"},
		},
	}
	res, err := Dry(context.Background(), plan)
	if err != nil {
		t.Fatalf("Dry: %v", err)
	}
	if res.Steps[0].Error == nil {
		t.Fatalf("expected error from panicked describe")
	}
}

func TestDry_FormatHumanReadable(t *testing.T) {
	plan := Plan{
		Name:  "delete-site:foo",
		Steps: []Step{&fakeStep{name: "stop-containers", desc: "stop nginx, php"}},
	}
	res, _ := Dry(context.Background(), plan)
	out := res.Format()
	if !strings.Contains(out, "delete-site:foo") {
		t.Fatalf("format missing plan name: %s", out)
	}
	if !strings.Contains(out, "stop nginx, php") {
		t.Fatalf("format missing description: %s", out)
	}
}

type panicStep struct{ name string }

func (p *panicStep) Name() string                     { return p.name }
func (p *panicStep) Apply(_ context.Context) error    { return nil }
func (p *panicStep) Rollback(_ context.Context) error { return nil }
func (p *panicStep) Describe(_ context.Context) (string, error) {
	panic("describe boom")
}
