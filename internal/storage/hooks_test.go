package storage

import (
	"errors"
	"testing"

	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/types"
)

func newHook(siteID string, ev hooks.Event, cmd string) *hooks.Hook {
	return &hooks.Hook{
		SiteID:   siteID,
		Event:    ev,
		TaskType: hooks.TaskExecHost,
		Command:  cmd,
		Enabled:  true,
	}
}

func seedSite(t *testing.T, st *Storage, id string) {
	t.Helper()
	site := &types.Site{
		ID: id, Name: id, Slug: id, Domain: id + ".localhost",
		FilesDir: "/tmp/" + id, PublicDir: "/", DBPassword: "pw",
	}
	if err := st.AddSite(site); err != nil {
		t.Fatalf("seed site: %v", err)
	}
}

func TestAddHook_AssignsMonotonicPosition(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "site-a")

	for i, cmd := range []string{"first", "second", "third"} {
		h := newHook("site-a", hooks.PostStart, cmd)
		if err := st.AddHook(h); err != nil {
			t.Fatalf("AddHook[%d]: %v", i, err)
		}
		if h.ID == 0 {
			t.Fatalf("AddHook[%d]: ID not set", i)
		}
		if h.Position != i {
			t.Errorf("AddHook[%d]: position = %d, want %d", i, h.Position, i)
		}
	}
}

func TestAddHook_PositionIsPerEvent(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "site-a")

	h1 := newHook("site-a", hooks.PostStart, "post-a")
	if err := st.AddHook(h1); err != nil {
		t.Fatal(err)
	}
	h2 := newHook("site-a", hooks.PreStart, "pre-a")
	h2.TaskType = hooks.TaskExecHost
	if err := st.AddHook(h2); err != nil {
		t.Fatal(err)
	}

	if h1.Position != 0 || h2.Position != 0 {
		t.Errorf("positions per event: post-start=%d pre-start=%d, both want 0", h1.Position, h2.Position)
	}
}

func TestAddHook_RejectsInvalid(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "site-a")

	cases := []struct {
		name string
		mut  func(*hooks.Hook)
	}{
		{"empty command", func(h *hooks.Hook) { h.Command = "" }},
		{"unknown task type", func(h *hooks.Hook) { h.TaskType = "weird" }},
		{"unknown event", func(h *hooks.Hook) { h.Event = "what-event" }},
		{"exec on pre-start", func(h *hooks.Hook) { h.Event = hooks.PreStart; h.TaskType = hooks.TaskExec }},
		{"wp-cli on pre-start", func(h *hooks.Hook) { h.Event = hooks.PreStart; h.TaskType = hooks.TaskWPCLI }},
		{"service on host task", func(h *hooks.Hook) { h.TaskType = hooks.TaskExecHost; h.Service = "php" }},
		{"unknown service", func(h *hooks.Hook) { h.TaskType = hooks.TaskExec; h.Service = "mystery" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHook("site-a", hooks.PostStart, "echo")
			tc.mut(h)
			if err := st.AddHook(h); err == nil {
				t.Error("expected error, got nil")
			} else if !errors.Is(err, hooks.ErrHookInvalid) && !errors.Is(err, hooks.ErrEmptyCommand) {
				t.Errorf("expected ErrHookInvalid family, got: %v", err)
			}
		})
	}
}

func TestListHooksByEvent_OrderedByPosition(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "site-a")

	for _, cmd := range []string{"a", "b", "c"} {
		if err := st.AddHook(newHook("site-a", hooks.PostStart, cmd)); err != nil {
			t.Fatal(err)
		}
	}

	got, err := st.ListHooksByEvent("site-a", hooks.PostStart)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, want := range []string{"a", "b", "c"} {
		if got[i].Command != want {
			t.Errorf("got[%d] = %q, want %q", i, got[i].Command, want)
		}
		if got[i].Position != i {
			t.Errorf("got[%d].Position = %d, want %d", i, got[i].Position, i)
		}
	}
}

func TestUpdateHook_ChangesPersisted(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "s")

	h := newHook("s", hooks.PostStart, "old")
	if err := st.AddHook(h); err != nil {
		t.Fatal(err)
	}

	h.Command = "new"
	h.Enabled = false
	if err := st.UpdateHook(h); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetHook(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Command != "new" {
		t.Errorf("Command = %q, want new", got.Command)
	}
	if got.Enabled {
		t.Error("Enabled = true, want false")
	}
}

func TestUpdateHook_RejectsCrossSiteOrEventMove(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "s")
	seedSite(t, st, "other")

	h := newHook("s", hooks.PostStart, "x")
	if err := st.AddHook(h); err != nil {
		t.Fatal(err)
	}

	// Mutating SiteID does not move the row — it makes the WHERE clause miss.
	h.SiteID = "other"
	if err := st.UpdateHook(h); !errors.Is(err, ErrHookNotFound) {
		t.Errorf("cross-site update: got %v, want ErrHookNotFound", err)
	}
}

func TestDeleteHook_IsIdempotent(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "s")

	h := newHook("s", hooks.PostStart, "x")
	if err := st.AddHook(h); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteHook(h.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteHook(h.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteHook(99999); err != nil {
		t.Fatal(err)
	}
}

func TestSiteDelete_CascadesToHooks(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "doomed")

	for _, cmd := range []string{"a", "b"} {
		if err := st.AddHook(newHook("doomed", hooks.PostStart, cmd)); err != nil {
			t.Fatal(err)
		}
	}

	if err := st.DeleteSite("doomed"); err != nil {
		t.Fatal(err)
	}

	got, err := st.ListHooks("doomed")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 (FK cascade should have wiped them)", len(got))
	}
}

func TestReorderHooks_RewritesPositions(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "s")

	var ids []int64
	for _, cmd := range []string{"a", "b", "c"} {
		h := newHook("s", hooks.PostStart, cmd)
		if err := st.AddHook(h); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, h.ID)
	}

	// Reverse order: c, b, a
	reversed := []int64{ids[2], ids[1], ids[0]}
	if err := st.ReorderHooks("s", hooks.PostStart, reversed); err != nil {
		t.Fatal(err)
	}

	got, err := st.ListHooksByEvent("s", hooks.PostStart)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"c", "b", "a"} {
		if got[i].Command != want {
			t.Errorf("got[%d] = %q, want %q", i, got[i].Command, want)
		}
		if got[i].Position != i {
			t.Errorf("got[%d].Position = %d, want %d", i, got[i].Position, i)
		}
	}
}

func TestReorderHooks_RejectsForeignID(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "s")
	seedSite(t, st, "t")

	hs := newHook("s", hooks.PostStart, "x")
	if err := st.AddHook(hs); err != nil {
		t.Fatal(err)
	}
	ht := newHook("t", hooks.PostStart, "y")
	if err := st.AddHook(ht); err != nil {
		t.Fatal(err)
	}

	// Try to reorder s with t's id mixed in.
	if err := st.ReorderHooks("s", hooks.PostStart, []int64{hs.ID, ht.ID}); err == nil {
		t.Error("expected error mixing ids across sites, got nil")
	}
}

func TestGetHook_NotFound(t *testing.T) {
	st := newStorage(t)
	_, err := st.GetHook(404)
	if !errors.Is(err, ErrHookNotFound) {
		t.Errorf("err = %v, want ErrHookNotFound", err)
	}
}
