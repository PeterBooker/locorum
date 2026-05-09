package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ActivityKind classifies an activity row by lifecycle method. Keeping the
// set bounded lets the UI map kinds to icons / colours / labels without
// hand-coded fallbacks.
type ActivityKind string

const (
	ActivityKindStart     ActivityKind = "start"
	ActivityKindStop      ActivityKind = "stop"
	ActivityKindDelete    ActivityKind = "delete"
	ActivityKindClone     ActivityKind = "clone"
	ActivityKindVersions  ActivityKind = "versions"
	ActivityKindMultisite ActivityKind = "multisite"
	ActivityKindExport    ActivityKind = "export"
	ActivityKindImportDB  ActivityKind = "import-db"
	ActivityKindSnapshot  ActivityKind = "snapshot"
	ActivityKindRestore   ActivityKind = "restore-snapshot"
	ActivityKindOther     ActivityKind = "other"
)

// Valid reports whether k is a recognised kind. "other" is valid as a
// deliberate catch-all, used when the plan name does not match a known
// lifecycle method.
func (k ActivityKind) Valid() bool {
	switch k {
	case ActivityKindStart,
		ActivityKindStop,
		ActivityKindDelete,
		ActivityKindClone,
		ActivityKindVersions,
		ActivityKindMultisite,
		ActivityKindExport,
		ActivityKindImportDB,
		ActivityKindSnapshot,
		ActivityKindRestore,
		ActivityKindOther:
		return true
	}
	return false
}

// ActivityStatus is the outcome of the underlying Plan.
type ActivityStatus string

const (
	ActivityStatusSucceeded  ActivityStatus = "succeeded"
	ActivityStatusFailed     ActivityStatus = "failed"
	ActivityStatusRolledBack ActivityStatus = "rolled-back"
)

// Valid reports whether s is a recognised status.
func (s ActivityStatus) Valid() bool {
	switch s {
	case ActivityStatusSucceeded, ActivityStatusFailed, ActivityStatusRolledBack:
		return true
	}
	return false
}

// ActivityRetentionDefault is the per-site cap enforced on every insert.
// Plan rows are small (a few hundred bytes including the JSON details
// blob), so 200 keeps a useful history while bounding worst-case storage
// at a few hundred KiB per site.
const ActivityRetentionDefault = 200

// activityTimeLayout is a fixed-width UTC RFC3339 layout with always-9
// digits of fractional seconds. Fixed width is load-bearing: the index
// `idx_activity_events_site_time` orders by the TEXT column, and SQLite's
// TEXT comparison is lexicographic. RFC3339Nano (Go's default) trims
// trailing zeros, which would make "2026-04-29T10:00:00Z" sort *after*
// "2026-04-29T10:00:00.5Z" — wrong. Always emitting 9 digits + 'Z'
// guarantees lex order == chronological order for every comparable pair.
const activityTimeLayout = "2006-01-02T15:04:05.000000000Z"

// ActivityEvent is one row in the audit timeline. One row per orch.Plan
// outcome — see internal/sites/activity.go for the emission rules.
//
// Time is stored as RFC3339Nano UTC. Details is an opaque JSON blob written
// at insert time so the Activity tab's expander has everything it needs
// without a follow-up query; format is owned by the emitter, not by this
// package, and survives across schema changes as a free-form payload.
type ActivityEvent struct {
	ID         int64
	SiteID     string
	Time       time.Time
	Plan       string
	Kind       ActivityKind
	Status     ActivityStatus
	DurationMS int64
	Message    string
	Details    json.RawMessage
}

// activityColumns is the canonical column order for every SELECT/INSERT
// against activity_events. Keep aligned with scanActivity below.
const activityColumns = "id, site_id, time, plan, kind, status, duration_ms, message, details"

// ErrActivityInvalid is returned by AppendActivity when the row is
// rejected before insert. Callers can errors.Is against it.
var ErrActivityInvalid = errors.New("activity event is invalid")

// AppendActivity inserts ev for siteID and atomically trims the per-site
// row count to keep at most ActivityRetentionDefault rows. ev.ID and ev.Time
// are populated on success.
//
// A non-nil ev.Time is preserved verbatim; otherwise the current UTC time
// is recorded. Empty Details serialises as a JSON null literal so the
// column always holds valid JSON; readers must tolerate that case.
//
// Insert + trim run in a single transaction: a partially-trimmed table is
// never observable to other readers. The trim's NOT IN (newest 200)
// sub-select uses idx_activity_events_site_time, so it costs O(log N) per
// site even with thousands of historical rows.
func (s *Storage) AppendActivity(ev *ActivityEvent) error {
	if ev == nil {
		return fmt.Errorf("%w: nil event", ErrActivityInvalid)
	}
	if ev.SiteID == "" {
		return fmt.Errorf("%w: empty site id", ErrActivityInvalid)
	}
	if !ev.Kind.Valid() {
		return fmt.Errorf("%w: unknown kind %q", ErrActivityInvalid, ev.Kind)
	}
	if !ev.Status.Valid() {
		return fmt.Errorf("%w: unknown status %q", ErrActivityInvalid, ev.Status)
	}
	if ev.DurationMS < 0 {
		ev.DurationMS = 0
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	} else {
		ev.Time = ev.Time.UTC()
	}

	// Encode an empty Details as a JSON null so the column is always a
	// well-formed JSON document. Readers that want a struct can still
	// branch on (len(blob) == 0 || string(blob) == "null").
	details := ev.Details
	if len(details) == 0 {
		details = json.RawMessage("null")
	} else if !json.Valid(details) {
		return fmt.Errorf("%w: details is not valid JSON", ErrActivityInvalid)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("AppendActivity: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(
		"INSERT INTO activity_events (site_id, time, plan, kind, status, duration_ms, message, details)"+
			" VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		ev.SiteID,
		ev.Time.Format(activityTimeLayout),
		ev.Plan,
		string(ev.Kind),
		string(ev.Status),
		ev.DurationMS,
		ev.Message,
		string(details),
	)
	if err != nil {
		return fmt.Errorf("AppendActivity: insert: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("AppendActivity: last insert id: %w", err)
	}
	ev.ID = id

	if err := trimActivityTx(tx, ev.SiteID, ActivityRetentionDefault); err != nil {
		return fmt.Errorf("AppendActivity: trim: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("AppendActivity: commit: %w", err)
	}
	return nil
}

// GetActivity returns up to limit newest-first activity events for siteID.
// A non-positive limit is treated as ActivityRetentionDefault — callers that
// want everything can pass that constant explicitly. The slice is empty,
// not nil, when there are no rows; callers can range over it without a nil
// check.
func (s *Storage) GetActivity(siteID string, limit int) ([]ActivityEvent, error) {
	if siteID == "" {
		return nil, errors.New("GetActivity: empty site id")
	}
	if limit <= 0 {
		limit = ActivityRetentionDefault
	}

	rows, err := s.db.Query(
		"SELECT "+activityColumns+" FROM activity_events"+
			" WHERE site_id = ? ORDER BY time DESC, id DESC LIMIT ?",
		siteID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("GetActivity: %w", err)
	}
	defer rows.Close()

	out := make([]ActivityEvent, 0, limit)
	for rows.Next() {
		ev, err := scanActivity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GetActivity: iterate: %w", err)
	}
	return out, nil
}

// TrimActivity keeps at most max newest rows for siteID and deletes the
// rest. A non-positive max is treated as ActivityRetentionDefault.
//
// This is exposed for the defensive startup sweep in app.Initialize; the
// per-insert trim happens inside AppendActivity, so steady-state callers
// do not need to invoke this explicitly.
func (s *Storage) TrimActivity(siteID string, limit int) error {
	if siteID == "" {
		return errors.New("TrimActivity: empty site id")
	}
	if limit <= 0 {
		limit = ActivityRetentionDefault
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("TrimActivity: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := trimActivityTx(tx, siteID, limit); err != nil {
		return fmt.Errorf("TrimActivity: %w", err)
	}
	return tx.Commit()
}

// trimActivityTx is the in-transaction body of the trim. The DELETE keeps
// the newest `limit` rows by (time DESC, id DESC) — id breaks ties when two
// rows share a timestamp (sub-millisecond plans are rare but possible in
// tests).
func trimActivityTx(tx *sql.Tx, siteID string, limit int) error {
	_, err := tx.Exec(
		"DELETE FROM activity_events"+
			" WHERE site_id = ?"+
			" AND id NOT IN ("+
			"   SELECT id FROM activity_events"+
			"   WHERE site_id = ?"+
			"   ORDER BY time DESC, id DESC"+
			"   LIMIT ?"+
			" )",
		siteID, siteID, limit,
	)
	return err
}

// scanActivity hydrates an ActivityEvent from a row scanner. Centralised so
// GetActivity and any future single-row reader stay in lockstep with
// activityColumns.
func scanActivity(scan interface{ Scan(...any) error }) (ActivityEvent, error) {
	var (
		ev      ActivityEvent
		ts      string
		kind    string
		status  string
		details string
	)
	if err := scan.Scan(
		&ev.ID, &ev.SiteID, &ts, &ev.Plan, &kind, &status,
		&ev.DurationMS, &ev.Message, &details,
	); err != nil {
		return ActivityEvent{}, fmt.Errorf("activity scan: %w", err)
	}
	// RFC3339Nano accepts any fractional precision (including zero), so it
	// parses both the fixed-width layout we write and any pre-existing rows
	// that ever used trimmed precision.
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ActivityEvent{}, fmt.Errorf("activity scan: parse time %q: %w", ts, err)
	}
	ev.Time = t
	ev.Kind = ActivityKind(kind)
	ev.Status = ActivityStatus(status)
	if details != "" {
		ev.Details = json.RawMessage(details)
	}
	return ev, nil
}
