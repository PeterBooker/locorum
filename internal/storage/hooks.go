package storage

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/PeterBooker/locorum/internal/hooks"
)

// hookColumns lists the persisted columns in their canonical order. Used by
// SELECT statements to keep the Scan() arg list aligned with the schema.
const hookColumns = "id, site_id, event, position, task_type, command, service, run_as_user, enabled, created_at, updated_at"

// ErrHookNotFound is returned by GetHook when the row does not exist.
var ErrHookNotFound = errors.New("hook not found")

// ListHooks returns every hook for siteID, ordered by event then position.
func (s *Storage) ListHooks(siteID string) ([]hooks.Hook, error) {
	rows, err := s.db.Query(
		"SELECT "+hookColumns+" FROM site_hooks WHERE site_id = ? ORDER BY event, position",
		siteID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing hooks: %w", err)
	}
	defer rows.Close()
	return scanHooks(rows)
}

// ListHooksByEvent returns the position-ordered hooks for siteID at ev.
// The result is ordered ascending by position; storage assigns positions
// monotonically on insert, so the result reflects user-defined run order.
func (s *Storage) ListHooksByEvent(siteID string, ev hooks.Event) ([]hooks.Hook, error) {
	rows, err := s.db.Query(
		"SELECT "+hookColumns+" FROM site_hooks WHERE site_id = ? AND event = ? ORDER BY position",
		siteID, string(ev),
	)
	if err != nil {
		return nil, fmt.Errorf("listing hooks by event: %w", err)
	}
	defer rows.Close()
	return scanHooks(rows)
}

// GetHook returns a single hook by row id, or ErrHookNotFound.
func (s *Storage) GetHook(id int64) (*hooks.Hook, error) {
	row := s.db.QueryRow(
		"SELECT "+hookColumns+" FROM site_hooks WHERE id = ?", id,
	)
	h, err := scanHook(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrHookNotFound
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// AddHook validates h, assigns the next free position for its (site, event)
// pair, and inserts the row. h.ID and timestamp fields are populated on
// success.
func (s *Storage) AddHook(h *hooks.Hook) error {
	if h == nil {
		return errors.New("AddHook: nil hook")
	}
	if err := h.Validate(); err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("AddHook: begin: %w", err)
	}
	defer tx.Rollback()

	var maxPos sql.NullInt64
	if err := tx.QueryRow(
		"SELECT MAX(position) FROM site_hooks WHERE site_id = ? AND event = ?",
		h.SiteID, string(h.Event),
	).Scan(&maxPos); err != nil {
		return fmt.Errorf("AddHook: max position: %w", err)
	}
	nextPos := 0
	if maxPos.Valid {
		nextPos = int(maxPos.Int64) + 1
	}
	h.Position = nextPos

	ts := now()
	h.CreatedAt = ts
	h.UpdatedAt = ts

	res, err := tx.Exec(
		"INSERT INTO site_hooks (site_id, event, position, task_type, command, service, run_as_user, enabled, created_at, updated_at)"+
			" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		h.SiteID, string(h.Event), h.Position, string(h.TaskType), h.Command, h.Service, h.RunAsUser, boolToInt(h.Enabled), h.CreatedAt, h.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("AddHook: insert: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("AddHook: last insert id: %w", err)
	}
	h.ID = id
	return tx.Commit()
}

// UpdateHook persists an existing hook. The row is identified by ID; SiteID
// and Event must match the existing row (callers cannot move a hook between
// sites or events through Update — use ReorderHooks for in-event reordering
// or delete+re-add for cross-event moves).
func (s *Storage) UpdateHook(h *hooks.Hook) error {
	if h == nil {
		return errors.New("UpdateHook: nil hook")
	}
	if h.ID == 0 {
		return errors.New("UpdateHook: missing ID")
	}
	if err := h.Validate(); err != nil {
		return err
	}
	h.UpdatedAt = now()

	res, err := s.db.Exec(
		"UPDATE site_hooks SET task_type = ?, command = ?, service = ?, run_as_user = ?, enabled = ?, updated_at = ?"+
			" WHERE id = ? AND site_id = ? AND event = ?",
		string(h.TaskType), h.Command, h.Service, h.RunAsUser, boolToInt(h.Enabled), h.UpdatedAt,
		h.ID, h.SiteID, string(h.Event),
	)
	if err != nil {
		return fmt.Errorf("UpdateHook: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("UpdateHook: rows affected: %w", err)
	}
	if n == 0 {
		return ErrHookNotFound
	}
	return nil
}

// DeleteHook removes the hook with the given id. Missing rows are not an
// error; callers can safely call this without a prior existence check.
//
// Positions of remaining hooks for the same (site, event) are NOT
// renumbered: gaps are harmless because run order is by ascending position.
// Use ReorderHooks if you want a contiguous sequence.
func (s *Storage) DeleteHook(id int64) error {
	_, err := s.db.Exec("DELETE FROM site_hooks WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("DeleteHook: %w", err)
	}
	return nil
}

// DeleteHooksForSite removes every hook attached to siteID. Redundant with
// the FK ON DELETE CASCADE, but explicit so callers don't have to know.
func (s *Storage) DeleteHooksForSite(siteID string) error {
	_, err := s.db.Exec("DELETE FROM site_hooks WHERE site_id = ?", siteID)
	if err != nil {
		return fmt.Errorf("DeleteHooksForSite: %w", err)
	}
	return nil
}

// ReorderHooks atomically rewrites positions for the given event so they
// match the supplied id order (first id → position 0, second → 1, …).
// Hooks with ids not in the list are left untouched, but their positions
// will collide with the rewritten range — callers must therefore pass the
// COMPLETE id set for the (site, event).
//
// All ids must already belong to siteID and ev; mismatches abort the
// transaction without partial writes.
func (s *Storage) ReorderHooks(siteID string, ev hooks.Event, orderedIDs []int64) error {
	if len(orderedIDs) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("ReorderHooks: begin: %w", err)
	}
	defer tx.Rollback()

	// Verify every id belongs to (siteID, ev). Cheaper than per-row UPDATE
	// guards because the existence check happens in one round trip.
	rows, err := tx.Query(
		"SELECT id FROM site_hooks WHERE site_id = ? AND event = ?",
		siteID, string(ev),
	)
	if err != nil {
		return fmt.Errorf("ReorderHooks: verify: %w", err)
	}
	existing := make(map[int64]struct{})
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("ReorderHooks: scan id: %w", err)
		}
		existing[id] = struct{}{}
	}
	rows.Close()

	if len(existing) != len(orderedIDs) {
		return fmt.Errorf("ReorderHooks: id count mismatch (got %d, have %d)", len(orderedIDs), len(existing))
	}
	for _, id := range orderedIDs {
		if _, ok := existing[id]; !ok {
			return fmt.Errorf("ReorderHooks: id %d not in (site=%s, event=%s)", id, siteID, ev)
		}
	}

	// Two-pass rewrite to dodge the UNIQUE(site_id, event, position) check:
	// first park every row at -1 .. -N, then write the final positions.
	// Otherwise swapping two adjacent rows would collide mid-transaction.
	ts := now()
	for i, id := range orderedIDs {
		if _, err := tx.Exec(
			"UPDATE site_hooks SET position = ?, updated_at = ? WHERE id = ?",
			-(i + 1), ts, id,
		); err != nil {
			return fmt.Errorf("ReorderHooks: park: %w", err)
		}
	}
	for i, id := range orderedIDs {
		if _, err := tx.Exec(
			"UPDATE site_hooks SET position = ? WHERE id = ?",
			i, id,
		); err != nil {
			return fmt.Errorf("ReorderHooks: assign: %w", err)
		}
	}
	return tx.Commit()
}

// ─── Helpers ────────────────────────────────────────────────────────────────

type hookScanner interface {
	Scan(dest ...any) error
}

func scanHook(s hookScanner) (hooks.Hook, error) {
	var (
		h        hooks.Hook
		event    string
		taskType string
		enabled  int
	)
	if err := s.Scan(
		&h.ID, &h.SiteID, &event, &h.Position, &taskType, &h.Command,
		&h.Service, &h.RunAsUser, &enabled, &h.CreatedAt, &h.UpdatedAt,
	); err != nil {
		return hooks.Hook{}, err
	}
	h.Event = hooks.Event(event)
	h.TaskType = hooks.TaskType(taskType)
	h.Enabled = enabled != 0
	return h, nil
}

func scanHooks(rows *sql.Rows) ([]hooks.Hook, error) {
	var out []hooks.Hook
	for rows.Next() {
		h, err := scanHook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
