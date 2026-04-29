package ui

import (
	"fmt"
	"time"
)

// FormatActivityTime renders t as a short string suitable for the Activity
// feed's leading time chip. Uses relative form for recent events and
// absolute for older ones — the goal is "readable at a glance, unambiguous
// for old entries":
//
//	< 60 seconds      "just now"
//	< 60 minutes      "Nm ago"
//	same calendar day "15:04"
//	yesterday         "Yesterday 15:04"
//	same year         "Jan 2 15:04"
//	older             "2006-01-02"
//
// now is taken as a parameter so the caller can pin a clock for tests and
// so the function never depends on time.Now's monotonic clock semantics.
// All formatting uses now's location so a local-timezone "Yesterday" is
// computed against the user's wall clock rather than UTC.
func FormatActivityTime(t time.Time, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	t = t.In(now.Location())

	delta := now.Sub(t)
	if delta < 0 {
		// Future or clock skew. Render as same-day so the row still looks
		// reasonable instead of "in 5 minutes".
		return t.Format("15:04")
	}
	if delta < time.Minute {
		return "just now"
	}
	if delta < time.Hour {
		return fmt.Sprintf("%dm ago", int(delta/time.Minute))
	}

	if sameCalendarDay(t, now) {
		return t.Format("15:04")
	}
	if sameCalendarDay(t, now.AddDate(0, 0, -1)) {
		return "Yesterday " + t.Format("15:04")
	}
	if t.Year() == now.Year() {
		return t.Format("Jan 2 15:04")
	}
	return t.Format("2006-01-02")
}

func sameCalendarDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
