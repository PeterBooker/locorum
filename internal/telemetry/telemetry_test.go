package telemetry

import (
	"context"
	"sync"
	"testing"
)

func TestDefaultIsNoopUntilSet(t *testing.T) {
	t.Parallel()
	// Don't reset the global — other tests may run in parallel. Just
	// verify that Default() returns a Sink whose Track is a no-op.
	s := Default()
	if s == nil {
		t.Fatalf("Default() returned nil")
	}
	s.Track("test.event", map[string]any{"k": "v"})
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("Noop.Flush returned %v", err)
	}
}

type recordingSink struct {
	mu     sync.Mutex
	events []string
}

func (r *recordingSink) Track(name string, _ map[string]any) {
	r.mu.Lock()
	r.events = append(r.events, name)
	r.mu.Unlock()
}

func (r *recordingSink) Flush(context.Context) error { return nil }

func TestSetDefaultThenTrack(t *testing.T) {
	// Sequential: this test mutates the global. Don't t.Parallel.
	prev := Default()
	defer SetDefault(prev)

	rs := &recordingSink{}
	SetDefault(rs)
	Track("evt.one", nil)
	Track("evt.two", map[string]any{"a": 1})

	rs.mu.Lock()
	defer rs.mu.Unlock()
	if len(rs.events) != 2 || rs.events[0] != "evt.one" || rs.events[1] != "evt.two" {
		t.Fatalf("recorded %+v, want [evt.one evt.two]", rs.events)
	}
}

func TestSetDefaultNilFallsBackToNoop(t *testing.T) {
	prev := Default()
	defer SetDefault(prev)
	SetDefault(nil)
	if _, ok := Default().(Noop); !ok {
		t.Fatalf("Default() after SetDefault(nil) = %T, want Noop", Default())
	}
}
