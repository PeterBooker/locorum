package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func newActivity(siteID string, kind ActivityKind, status ActivityStatus, msg string) *ActivityEvent {
	return &ActivityEvent{
		SiteID:     siteID,
		Plan:       string(kind) + "-site:" + siteID,
		Kind:       kind,
		Status:     status,
		DurationMS: 1234,
		Message:    msg,
	}
}

func TestAppendActivity_AssignsIDAndDefaultsTime(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "site-a")

	before := time.Now().UTC()
	ev := newActivity("site-a", ActivityKindStart, ActivityStatusSucceeded, "Started")
	if err := st.AppendActivity(ev); err != nil {
		t.Fatalf("AppendActivity: %v", err)
	}
	after := time.Now().UTC()

	if ev.ID == 0 {
		t.Fatal("expected ID to be set")
	}
	if ev.Time.IsZero() {
		t.Fatal("expected Time to be set")
	}
	// AppendActivity rounds to UTC; allow a small slack on either side
	// for clock granularity.
	if ev.Time.Before(before.Add(-time.Second)) || ev.Time.After(after.Add(time.Second)) {
		t.Errorf("Time = %v, expected in [%v, %v]", ev.Time, before, after)
	}
}

func TestAppendActivity_PreservesExplicitTime(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "s")

	t0 := time.Date(2026, 4, 29, 10, 30, 0, 123456789, time.UTC)
	ev := newActivity("s", ActivityKindStop, ActivityStatusSucceeded, "Stopped")
	ev.Time = t0
	if err := st.AppendActivity(ev); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetActivity("s", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	if !got[0].Time.Equal(t0) {
		t.Errorf("Time = %v, want %v", got[0].Time, t0)
	}
}

func TestAppendActivity_RejectsInvalid(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "s")

	cases := map[string]*ActivityEvent{
		"nil":            nil,
		"empty site":     {Kind: ActivityKindStart, Status: ActivityStatusSucceeded},
		"unknown kind":   {SiteID: "s", Kind: "weird", Status: ActivityStatusSucceeded},
		"unknown status": {SiteID: "s", Kind: ActivityKindStart, Status: "weird"},
		"bad details": {
			SiteID: "s", Kind: ActivityKindStart, Status: ActivityStatusSucceeded,
			Details: json.RawMessage("{not json"),
		},
	}
	for name, ev := range cases {
		t.Run(name, func(t *testing.T) {
			err := st.AppendActivity(ev)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, ErrActivityInvalid) {
				t.Errorf("expected ErrActivityInvalid, got %v", err)
			}
		})
	}
}

func TestAppendActivity_RoundTripsDetailsBlob(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "s")

	type payload struct {
		Steps []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"steps"`
		Error string `json:"error"`
	}
	original := payload{}
	original.Steps = append(original.Steps, struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}{Name: "ensure-network", Status: "succeeded"})
	original.Error = "boom"
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	ev := newActivity("s", ActivityKindStart, ActivityStatusFailed, "Failed")
	ev.Details = raw
	if err := st.AppendActivity(ev); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetActivity("s", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	var decoded payload
	if err := json.Unmarshal(got[0].Details, &decoded); err != nil {
		t.Fatalf("unmarshal details: %v (raw=%q)", err, got[0].Details)
	}
	if decoded.Error != "boom" || len(decoded.Steps) != 1 || decoded.Steps[0].Name != "ensure-network" {
		t.Errorf("decoded = %+v", decoded)
	}
}

func TestAppendActivity_EmptyDetailsStoresJSONNull(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "s")

	ev := newActivity("s", ActivityKindStart, ActivityStatusSucceeded, "Started")
	if err := st.AppendActivity(ev); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetActivity("s", 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got[0].Details) != "null" {
		t.Errorf("Details = %q, want null", got[0].Details)
	}
}

func TestGetActivity_OrdersNewestFirst(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "s")

	base := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		ev := newActivity("s", ActivityKindStart, ActivityStatusSucceeded, fmt.Sprintf("msg-%d", i))
		ev.Time = base.Add(time.Duration(i) * time.Second)
		if err := st.AppendActivity(ev); err != nil {
			t.Fatal(err)
		}
	}

	got, err := st.GetActivity("s", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5", len(got))
	}
	for i, want := range []string{"msg-4", "msg-3", "msg-2", "msg-1", "msg-0"} {
		if got[i].Message != want {
			t.Errorf("got[%d].Message = %q, want %q", i, got[i].Message, want)
		}
	}
}

func TestGetActivity_LimitsResults(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "s")

	for i := 0; i < 10; i++ {
		if err := st.AppendActivity(newActivity("s", ActivityKindStart, ActivityStatusSucceeded, fmt.Sprintf("m%d", i))); err != nil {
			t.Fatal(err)
		}
	}

	got, err := st.GetActivity("s", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("len = %d, want 3", len(got))
	}
}

func TestGetActivity_FiltersBySite(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "a")
	seedSite(t, st, "b")

	if err := st.AppendActivity(newActivity("a", ActivityKindStart, ActivityStatusSucceeded, "from-a")); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendActivity(newActivity("b", ActivityKindStart, ActivityStatusSucceeded, "from-b")); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetActivity("a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Message != "from-a" {
		t.Errorf("site-a results = %+v", got)
	}
}

func TestAppendActivity_TrimsToRetention(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "s")

	// Insert one more than the retention cap. The oldest row should be
	// trimmed atomically inside AppendActivity.
	base := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)
	total := ActivityRetentionDefault + 5
	for i := 0; i < total; i++ {
		ev := newActivity("s", ActivityKindStart, ActivityStatusSucceeded, fmt.Sprintf("m%d", i))
		// Strictly increasing timestamps so ordering is deterministic.
		ev.Time = base.Add(time.Duration(i) * time.Millisecond)
		if err := st.AppendActivity(ev); err != nil {
			t.Fatal(err)
		}
	}

	got, err := st.GetActivity("s", total*2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != ActivityRetentionDefault {
		t.Fatalf("len = %d, want %d", len(got), ActivityRetentionDefault)
	}
	// Newest first: head should be m{total-1}, tail should be m{total - retention}.
	wantHead := fmt.Sprintf("m%d", total-1)
	wantTail := fmt.Sprintf("m%d", total-ActivityRetentionDefault)
	if got[0].Message != wantHead {
		t.Errorf("head = %q, want %q", got[0].Message, wantHead)
	}
	if got[len(got)-1].Message != wantTail {
		t.Errorf("tail = %q, want %q", got[len(got)-1].Message, wantTail)
	}
}

func TestTrimActivity_ManualSweep(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "s")

	for i := 0; i < 20; i++ {
		if err := st.AppendActivity(newActivity("s", ActivityKindStart, ActivityStatusSucceeded, fmt.Sprintf("m%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.TrimActivity("s", 5); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetActivity("s", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Errorf("len after trim = %d, want 5", len(got))
	}
}

func TestActivity_FKCascadeOnSiteDelete(t *testing.T) {
	st := newStorage(t)
	seedSite(t, st, "s")

	for i := 0; i < 3; i++ {
		if err := st.AppendActivity(newActivity("s", ActivityKindStart, ActivityStatusSucceeded, "m")); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.DeleteSite("s"); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetActivity("s", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("len after site delete = %d, want 0 (cascade failed)", len(got))
	}
}

func TestActivityTimeLayout_LexSortMatchesChronological(t *testing.T) {
	// Regression: the index orders TEXT lexicographically. The fixed-width
	// layout must therefore preserve chronological order across whole
	// seconds, fractional seconds of any precision, and the day boundary.
	a := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	b := time.Date(2026, 4, 29, 10, 0, 0, 500_000_000, time.UTC) // a + 0.5s
	c := time.Date(2026, 4, 29, 10, 0, 0, 500_000_001, time.UTC) // b + 1ns
	d := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)            // next day

	cases := []struct {
		name string
		x, y time.Time
	}{
		{"second vs subsecond", a, b},
		{"adjacent fractionals", b, c},
		{"day boundary", c, d},
	}
	for _, tc := range cases {
		sx := tc.x.Format(activityTimeLayout)
		sy := tc.y.Format(activityTimeLayout)
		if !(sx < sy) {
			t.Errorf("%s: %q !< %q (chrono says %v < %v)", tc.name, sx, sy, tc.x, tc.y)
		}
		if len(sx) != len(sy) {
			t.Errorf("%s: width mismatch %d vs %d (%q, %q)", tc.name, len(sx), len(sy), sx, sy)
		}
	}
}
