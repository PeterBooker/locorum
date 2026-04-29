package sites

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PeterBooker/locorum/internal/orch"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/types"
)

func TestClassifyActivityKind(t *testing.T) {
	cases := map[string]storage.ActivityKind{
		"start-site:demo":     storage.ActivityKindStart,
		"stop-site:demo":      storage.ActivityKindStop,
		"delete-site:demo":    storage.ActivityKindDelete,
		"clone-site:demo":     storage.ActivityKindClone,
		"versions-site:demo":  storage.ActivityKindVersions,
		"multisite-site:demo": storage.ActivityKindMultisite,
		"export-site:demo":    storage.ActivityKindExport,
		"import-db:demo":      storage.ActivityKindImportDB,
		"snapshot-site:demo":  storage.ActivityKindSnapshot,
		// Plan name doesn't match the convention → Other.
		"some-future-thing":         storage.ActivityKindOther,
		"":                          storage.ActivityKindOther,
		"random:thing":              storage.ActivityKindOther,
		"-site:nothing-before-dash": storage.ActivityKindOther,
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			if got := classifyActivityKind(name); got != want {
				t.Errorf("classify(%q) = %q, want %q", name, got, want)
			}
		})
	}
}

func TestClassifyActivityStatus(t *testing.T) {
	cases := []struct {
		name string
		res  orch.Result
		want storage.ActivityStatus
	}{
		{"clean", orch.Result{}, storage.ActivityStatusSucceeded},
		{"final error only", orch.Result{FinalError: errors.New("x")}, storage.ActivityStatusFailed},
		{"rolled back", orch.Result{FinalError: errors.New("x"), RolledBack: true}, storage.ActivityStatusRolledBack},
		// Defensive: rolled-back without a FinalError shouldn't happen, but
		// if it does the status takes priority.
		{"rolled back no err", orch.Result{RolledBack: true}, storage.ActivityStatusRolledBack},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyActivityStatus(tc.res); got != tc.want {
				t.Errorf("status = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderActivityMessage_Succeeded(t *testing.T) {
	site := &types.Site{ID: "s", Slug: "demo", PHPVersion: "8.3"}
	cases := map[storage.ActivityKind]string{
		storage.ActivityKindStart:     "Started · php 8.3",
		storage.ActivityKindStop:      "Stopped",
		storage.ActivityKindDelete:    "Deleted",
		storage.ActivityKindImportDB:  "Imported database",
		storage.ActivityKindSnapshot:  "Created snapshot",
		storage.ActivityKindClone:     "Cloned",
		storage.ActivityKindVersions:  "Updated versions",
		storage.ActivityKindMultisite: "Updated multisite configuration",
	}
	for kind, want := range cases {
		t.Run(string(kind), func(t *testing.T) {
			got := renderActivityMessage(kind, storage.ActivityStatusSucceeded, site, orch.Result{})
			if got != want {
				t.Errorf("kind=%s: got %q, want %q", kind, got, want)
			}
		})
	}
}

func TestRenderActivityMessage_StartWithoutVersion(t *testing.T) {
	site := &types.Site{ID: "s", Slug: "demo"}
	got := renderActivityMessage(storage.ActivityKindStart, storage.ActivityStatusSucceeded, site, orch.Result{})
	if got != "Started" {
		t.Errorf("got %q, want %q", got, "Started")
	}
}

func TestRenderActivityMessage_FailedNamesFirstFailedStep(t *testing.T) {
	res := orch.Result{
		FinalError: errors.New("boom"),
		Steps: []orch.StepResult{
			{Name: "ensure-network", Status: orch.StatusSucceeded},
			{Name: "pull-images", Status: orch.StatusFailed, Error: errors.New("boom")},
			{Name: "create-containers", Status: orch.StatusSkipped},
		},
	}
	got := renderActivityMessage(storage.ActivityKindStart, storage.ActivityStatusFailed,
		&types.Site{ID: "s", Slug: "demo"}, res)
	if got != "Start failed at pull-images" {
		t.Errorf("got %q", got)
	}

	gotRB := renderActivityMessage(storage.ActivityKindStart, storage.ActivityStatusRolledBack,
		&types.Site{ID: "s", Slug: "demo"}, res)
	if gotRB != "Start rolled back at pull-images" {
		t.Errorf("got %q", gotRB)
	}
}

func TestRenderActivityMessage_FailedFallsBackToErrorWhenNoFailedStep(t *testing.T) {
	res := orch.Result{FinalError: errors.New("ctx cancelled")}
	got := renderActivityMessage(storage.ActivityKindStart, storage.ActivityStatusFailed,
		&types.Site{ID: "s"}, res)
	if got != "Start failed: ctx cancelled" {
		t.Errorf("got %q", got)
	}
}

func TestBuildActivityDetails_RoundTrip(t *testing.T) {
	res := orch.Result{
		FinalError: errors.New("pull failed"),
		Steps: []orch.StepResult{
			{Name: "ensure-network", Status: orch.StatusSucceeded, Duration: 10 * time.Millisecond},
			{Name: "pull-images", Status: orch.StatusFailed, Duration: 5 * time.Second, Error: errors.New("pull failed")},
		},
	}
	raw := buildActivityDetails(res)
	if len(raw) == 0 {
		t.Fatal("empty details")
	}
	var d activityDetails
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(d.Steps) != 2 || d.Steps[0].Name != "ensure-network" || d.Steps[1].Name != "pull-images" {
		t.Errorf("steps = %+v", d.Steps)
	}
	if d.Steps[1].Error != "pull failed" {
		t.Errorf("step error = %q", d.Steps[1].Error)
	}
	if d.Error != "pull failed" {
		t.Errorf("final error = %q", d.Error)
	}
	if d.Steps[1].DurationMS != 5000 {
		t.Errorf("duration = %d, want 5000", d.Steps[1].DurationMS)
	}
}

func TestBuildActivityDetails_TruncatesLargeError(t *testing.T) {
	huge := strings.Repeat("x", activityErrorMaxBytes*2)
	res := orch.Result{FinalError: errors.New(huge)}
	raw := buildActivityDetails(res)
	var d activityDetails
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Must end with the ellipsis when truncation happened, and stay
	// well below 2× the cap (allowing for the ellipsis byte cost).
	if !strings.HasSuffix(d.Error, "…") {
		t.Errorf("expected ellipsis suffix; got %q", d.Error[len(d.Error)-10:])
	}
	if len(d.Error) > activityErrorMaxBytes {
		t.Errorf("len(error) = %d, want <= %d", len(d.Error), activityErrorMaxBytes)
	}
}

func TestTruncateRunes_AsciiAndMultibyte(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"shorter than max", "abc", 10, "abc"},
		{"exact", "abcd", 4, "abcd"},
		// max=5 budget, "…" eats 3 bytes → 2 ASCII bytes available.
		{"ascii truncate", "abcdefghij", 5, "ab…"},
		// 6 multibyte codepoints, each 3 bytes → 18 bytes total. Limit at
		// 9 bytes - len("…")=3 → 6 bytes available → 2 runes (6 bytes) +
		// "…" (3 bytes) = 9 bytes, exactly the budget.
		{"multibyte", "字字字字字字", 9, "字字…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateRunes(tc.in, tc.max)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if len(got) > tc.max && len(tc.in) > tc.max {
				t.Errorf("len(out)=%d > max=%d", len(got), tc.max)
			}
		})
	}
}

func TestRecordActivity_PersistsAndFiresCallback(t *testing.T) {
	st := storage.NewTestStorage(t)
	sm := &SiteManager{st: st}

	site := &types.Site{ID: "s1", Slug: "demo", PHPVersion: "8.3", DBPassword: "p"}
	if err := st.AddSite(site); err != nil {
		t.Fatal(err)
	}

	var receivedSiteID string
	var received storage.ActivityEvent
	sm.OnActivityAppended = func(siteID string, ev storage.ActivityEvent) {
		receivedSiteID = siteID
		received = ev
	}

	res := orch.Result{
		PlanName: "start-site:demo",
		Started:  time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC),
		Duration: 750 * time.Millisecond,
		Steps: []orch.StepResult{
			{Name: "ensure-network", Status: orch.StatusSucceeded, Duration: 10 * time.Millisecond},
		},
	}
	plan := orch.Plan{Name: "start-site:demo"}
	sm.recordActivity(site, plan, res)

	if receivedSiteID != "s1" {
		t.Errorf("callback siteID = %q, want s1", receivedSiteID)
	}
	if received.Kind != storage.ActivityKindStart {
		t.Errorf("kind = %q", received.Kind)
	}
	if received.Status != storage.ActivityStatusSucceeded {
		t.Errorf("status = %q", received.Status)
	}
	if received.Message != "Started · php 8.3" {
		t.Errorf("message = %q", received.Message)
	}
	if received.DurationMS != 750 {
		t.Errorf("duration_ms = %d", received.DurationMS)
	}

	got, err := st.GetActivity("s1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1", len(got))
	}
	if got[0].Plan != "start-site:demo" {
		t.Errorf("plan = %q", got[0].Plan)
	}
	// Time is the plan's completion instant.
	wantT := res.Started.Add(res.Duration).UTC()
	if !got[0].Time.Equal(wantT) {
		t.Errorf("time = %v, want %v", got[0].Time, wantT)
	}
}

func TestRecordActivity_RolledBackPlan(t *testing.T) {
	st := storage.NewTestStorage(t)
	sm := &SiteManager{st: st}

	site := &types.Site{ID: "s1", Slug: "demo", DBPassword: "p"}
	if err := st.AddSite(site); err != nil {
		t.Fatal(err)
	}

	res := orch.Result{
		PlanName:   "start-site:demo",
		Started:    time.Now().UTC(),
		Duration:   200 * time.Millisecond,
		FinalError: errors.New("pull-images: timeout"),
		RolledBack: true,
		Steps: []orch.StepResult{
			{Name: "ensure-network", Status: orch.StatusSucceeded, Duration: 10 * time.Millisecond},
			{Name: "pull-images", Status: orch.StatusFailed, Duration: 100 * time.Millisecond, Error: errors.New("timeout")},
		},
	}
	sm.recordActivity(site, orch.Plan{Name: "start-site:demo"}, res)

	got, err := st.GetActivity("s1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1", len(got))
	}
	if got[0].Status != storage.ActivityStatusRolledBack {
		t.Errorf("status = %q", got[0].Status)
	}
	if got[0].Message != "Start rolled back at pull-images" {
		t.Errorf("message = %q", got[0].Message)
	}
	// Details JSON should carry both steps and the final error.
	var d activityDetails
	if err := json.Unmarshal(got[0].Details, &d); err != nil {
		t.Fatalf("details: %v", err)
	}
	if len(d.Steps) != 2 || d.Steps[1].Status != string(orch.StatusFailed) {
		t.Errorf("steps = %+v", d.Steps)
	}
	if d.Error == "" {
		t.Error("expected non-empty error in details")
	}
}

func TestRecordActivity_NilSiteIsNoop(t *testing.T) {
	st := storage.NewTestStorage(t)
	sm := &SiteManager{st: st}
	// Should not panic, should not write a row.
	sm.recordActivity(nil, orch.Plan{Name: "start-site:x"}, orch.Result{})
}
