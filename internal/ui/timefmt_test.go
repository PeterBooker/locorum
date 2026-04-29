package ui

import (
	"testing"
	"time"
)

func TestFormatActivityTime(t *testing.T) {
	loc := time.FixedZone("Test", 0)
	now := time.Date(2026, 4, 29, 14, 30, 0, 0, loc)

	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero", time.Time{}, ""},
		{"30 seconds ago", now.Add(-30 * time.Second), "just now"},
		{"59 seconds ago", now.Add(-59 * time.Second), "just now"},
		{"60 seconds ago", now.Add(-60 * time.Second), "1m ago"},
		{"5 minutes ago", now.Add(-5 * time.Minute), "5m ago"},
		{"59 minutes ago", now.Add(-59 * time.Minute), "59m ago"},
		// Same calendar day, but >= 1h: absolute clock time.
		{"earlier today", time.Date(2026, 4, 29, 9, 5, 0, 0, loc), "09:05"},
		{"midnight today", time.Date(2026, 4, 29, 0, 0, 0, 0, loc), "00:00"},
		// Yesterday: branch on wall date, not 24-hour offset.
		{"yesterday late", time.Date(2026, 4, 28, 23, 59, 0, 0, loc), "Yesterday 23:59"},
		{"yesterday early", time.Date(2026, 4, 28, 0, 0, 0, 0, loc), "Yesterday 00:00"},
		// Same year, > 1 day ago.
		{"a week ago", time.Date(2026, 4, 22, 12, 0, 0, 0, loc), "Apr 22 12:00"},
		{"earlier this year", time.Date(2026, 1, 1, 9, 0, 0, 0, loc), "Jan 1 09:00"},
		// Different year.
		{"last year", time.Date(2025, 12, 31, 23, 59, 0, 0, loc), "2025-12-31"},
		{"two years ago", time.Date(2024, 6, 1, 0, 0, 0, 0, loc), "2024-06-01"},
		// Future / clock skew falls through to same-day clock format.
		{"5 minutes future", now.Add(5 * time.Minute), "14:35"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatActivityTime(tc.t, now)
			if got != tc.want {
				t.Errorf("FormatActivityTime(%v, %v) = %q, want %q", tc.t, now, got, tc.want)
			}
		})
	}
}

func TestFormatActivityTime_RespectsCallerLocation(t *testing.T) {
	// A timestamp at 23:30 UTC, viewed from a UTC+2 caller, is 01:30 the
	// next day — should render against the caller's wall clock.
	utc := time.Date(2026, 4, 29, 23, 30, 0, 0, time.UTC)
	plus2 := time.FixedZone("Test+2", 2*60*60)
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, plus2) // local time

	got := FormatActivityTime(utc, now)
	if got != "01:30" {
		t.Errorf("got %q, want 01:30 (same calendar day in local zone)", got)
	}
}
