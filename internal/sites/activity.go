package sites

import (
	"encoding/json"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PeterBooker/locorum/internal/orch"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/types"
)

// activityMessageMaxBytes caps the rendered Message column to keep one row
// short enough to render in the overview feed without wrapping. The Activity
// tab's expander surfaces the full error text from the Details blob.
const activityMessageMaxBytes = 200

// activityErrorMaxBytes caps the error captured in the JSON Details blob.
// Container error strings can be large (multi-line stack traces, redacted
// JSON dumps); we keep the first kibibyte, which is enough for triage and
// keeps the row well under SQLite's default page size.
const activityErrorMaxBytes = 1024

// activityDetails is the JSON shape we serialise into ActivityEvent.Details.
// Format is owned by this file — readers (the Activity tab) decode the same
// shape. Keep field names short: the column is per-row, so payload size adds
// up across hundreds of rows.
type activityDetails struct {
	Steps []activityStepDetail `json:"steps,omitempty"`
	Error string               `json:"error,omitempty"`
}

type activityStepDetail struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// recordActivity persists one row summarising a Plan outcome. Best-effort:
// any storage error is logged and swallowed — the lifecycle method that
// invoked us has already finished its real work, and an audit-write
// failure must never propagate.
//
// Called from runPlan's OnPlanDone callback alongside writeAuditLog. The
// two recorders serve different audiences: lifecycle.log is the engineer
// artefact (raw, full step list, rotated), while activity_events feeds the
// in-app UI with a bounded, queryable timeline.
//
// Successful "delete-site" plans wipe their own activity row a moment
// later: DeleteSite runs sm.st.DeleteSite after runPlan returns, and the
// FK cascade on activity_events.site_id removes every row for the site.
// That's intentional — there is no per-site UI for a site that no longer
// exists, and lifecycle.log preserves the engineering record across
// deletes. Failed delete plans keep their row (no SQL DELETE runs).
func (sm *SiteManager) recordActivity(site *types.Site, plan orch.Plan, res orch.Result) {
	if sm.st == nil || site == nil {
		return
	}

	kind := classifyActivityKind(plan.Name)
	status := classifyActivityStatus(res)
	message := renderActivityMessage(kind, status, site, res)
	details := buildActivityDetails(res)

	// Use the plan's completion instant so the row's chronological
	// position matches when the work actually finished. Falls back to
	// "now" if the Plan didn't carry a Started timestamp.
	t := time.Now().UTC()
	if !res.Started.IsZero() {
		t = res.Started.Add(res.Duration).UTC()
	}

	ev := &storage.ActivityEvent{
		SiteID:     site.ID,
		Time:       t,
		Plan:       plan.Name,
		Kind:       kind,
		Status:     status,
		DurationMS: res.Duration.Milliseconds(),
		Message:    message,
		Details:    details,
	}
	if err := sm.st.AppendActivity(ev); err != nil {
		slog.Warn("activity append failed",
			"plan", plan.Name, "site", site.Slug, "err", err.Error())
		return
	}
	if sm.OnActivityAppended != nil {
		sm.OnActivityAppended(site.ID, *ev)
	}
}

// classifyActivityKind maps plan.Name (e.g. "start-site:demo", "import-db:demo")
// to a known ActivityKind, defaulting to ActivityKindOther. Plan name format
// is owned by the lifecycle methods in sites.go / import.go — keep this
// helper aligned with that convention.
func classifyActivityKind(planName string) storage.ActivityKind {
	prefix := planName
	if i := strings.IndexByte(prefix, ':'); i >= 0 {
		prefix = prefix[:i]
	}
	prefix = strings.TrimSuffix(prefix, "-site")

	switch storage.ActivityKind(prefix) {
	case storage.ActivityKindStart,
		storage.ActivityKindStop,
		storage.ActivityKindDelete,
		storage.ActivityKindClone,
		storage.ActivityKindVersions,
		storage.ActivityKindMultisite,
		storage.ActivityKindExport,
		storage.ActivityKindImportDB,
		storage.ActivityKindSnapshot,
		storage.ActivityKindRestore:
		return storage.ActivityKind(prefix)
	}
	return storage.ActivityKindOther
}

// classifyActivityStatus collapses orch.Result's three outcome fields
// (FinalError, RolledBack) into the three ActivityStatus values the UI
// renders.
//
// RolledBack takes priority over FinalError — a rollback always implies a
// failure, but the user-visible distinction is whether prior work was
// reverted. "failed" therefore means "errored without rolling back",
// typically a Plan whose first step failed.
func classifyActivityStatus(res orch.Result) storage.ActivityStatus {
	switch {
	case res.RolledBack:
		return storage.ActivityStatusRolledBack
	case res.FinalError != nil:
		return storage.ActivityStatusFailed
	default:
		return storage.ActivityStatusSucceeded
	}
}

// renderActivityMessage produces the human-readable string shown in the
// activity row. Kept tight (single line, capped) — the inline expander on
// the Activity tab surfaces the full step list and error.
func renderActivityMessage(kind storage.ActivityKind, status storage.ActivityStatus, site *types.Site, res orch.Result) string {
	var msg string
	switch status {
	case storage.ActivityStatusSucceeded:
		msg = renderSucceededMessage(kind, site)
	case storage.ActivityStatusFailed, storage.ActivityStatusRolledBack:
		msg = renderFailedMessage(kind, status, res)
	default:
		msg = string(kind)
	}
	return truncateRunes(msg, activityMessageMaxBytes)
}

func renderSucceededMessage(kind storage.ActivityKind, site *types.Site) string {
	switch kind {
	case storage.ActivityKindStart:
		if site != nil && site.PHPVersion != "" {
			return "Started · php " + site.PHPVersion
		}
		return "Started"
	case storage.ActivityKindStop:
		return "Stopped"
	case storage.ActivityKindDelete:
		return "Deleted"
	case storage.ActivityKindClone:
		return "Cloned"
	case storage.ActivityKindVersions:
		return "Updated versions"
	case storage.ActivityKindMultisite:
		return "Updated multisite configuration"
	case storage.ActivityKindExport:
		return "Exported"
	case storage.ActivityKindImportDB:
		return "Imported database"
	case storage.ActivityKindSnapshot:
		return "Created snapshot"
	case storage.ActivityKindRestore:
		return "Restored snapshot"
	}
	return string(kind)
}

func renderFailedMessage(kind storage.ActivityKind, status storage.ActivityStatus, res orch.Result) string {
	verb := verbForKind(kind)
	suffix := "failed"
	if status == storage.ActivityStatusRolledBack {
		suffix = "rolled back"
	}

	if step := firstFailedStepName(res); step != "" {
		return verb + " " + suffix + " at " + step
	}
	if res.FinalError != nil {
		// No specific step failed (e.g. context cancelled before any step
		// started) — fall back to the error string.
		return verb + " " + suffix + ": " + res.FinalError.Error()
	}
	return verb + " " + suffix
}

func verbForKind(kind storage.ActivityKind) string {
	switch kind {
	case storage.ActivityKindStart:
		return "Start"
	case storage.ActivityKindStop:
		return "Stop"
	case storage.ActivityKindDelete:
		return "Delete"
	case storage.ActivityKindClone:
		return "Clone"
	case storage.ActivityKindVersions:
		return "Version change"
	case storage.ActivityKindMultisite:
		return "Multisite change"
	case storage.ActivityKindExport:
		return "Export"
	case storage.ActivityKindImportDB:
		return "Database import"
	case storage.ActivityKindSnapshot:
		return "Snapshot"
	case storage.ActivityKindRestore:
		return "Restore"
	}
	return "Plan"
}

// firstFailedStepName returns the Name of the first step in res whose
// Status is StatusFailed, or "" if none failed (typical for "skipped"
// outcomes after context cancellation).
func firstFailedStepName(res orch.Result) string {
	for _, s := range res.Steps {
		if s.Status == orch.StatusFailed {
			return s.Name
		}
	}
	return ""
}

// buildActivityDetails serialises the per-step breakdown and final error
// into the JSON blob stored in activity_events.details. A marshalling
// failure is logged but never aborts emission — the row is more useful
// without details than not at all.
func buildActivityDetails(res orch.Result) json.RawMessage {
	d := activityDetails{
		Steps: make([]activityStepDetail, 0, len(res.Steps)),
	}
	for _, s := range res.Steps {
		entry := activityStepDetail{
			Name:       s.Name,
			Status:     string(s.Status),
			DurationMS: s.Duration.Milliseconds(),
		}
		if s.Error != nil {
			entry.Error = truncateRunes(s.Error.Error(), activityErrorMaxBytes)
		}
		d.Steps = append(d.Steps, entry)
	}
	if res.FinalError != nil {
		d.Error = truncateRunes(res.FinalError.Error(), activityErrorMaxBytes)
	}
	buf, err := json.Marshal(d)
	if err != nil {
		slog.Warn("activity details marshal failed", "err", err.Error())
		return nil
	}
	return buf
}

// truncateRunes shortens s to at most maxBytes bytes, never splitting a UTF-8
// rune. Used to bound row size before it hits SQLite. The "…" suffix is
// added when truncation actually occurs so the UI signals elision without
// the caller having to know.
func truncateRunes(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	const ellipsis = "…"
	limit := maxBytes - len(ellipsis)
	if limit <= 0 {
		// Pathologically small budget — return a hard byte slice on a
		// rune boundary. Walk back until we land on a UTF-8 start byte.
		i := maxBytes
		for i > 0 && (s[i-1]&0xC0) == 0x80 {
			i--
		}
		return s[:i]
	}
	// `range` over a string yields (byteIndex, rune); when i+size(rune)
	// would exceed limit, stop. This guarantees the slice ends on a rune
	// boundary regardless of multi-byte content.
	end := 0
	for i, r := range s {
		size := utf8.RuneLen(r)
		if size < 0 {
			size = utf8.RuneLen(utf8.RuneError)
		}
		if i+size > limit {
			break
		}
		end = i + size
	}
	return s[:end] + ellipsis
}
