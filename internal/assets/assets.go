// Package assets reconciles bundled (embedded) configuration files
// against the user's on-disk copy at ~/.locorum/config/.
//
// It distinguishes four states per file by hash-matching against
// (a) the file's current bundled SHA-256 and (b) the SHA-256 last
// observed on disk after the previous extract. Each combination is
// classified by Verdict so callers know whether to overwrite, leave
// alone, or surface a merge-needed warning to the GUI.
//
// State is persisted at ~/.locorum/state/asset_hashes.json and is
// itself a managed file (overwritten on every Reconcile). Missing
// state on first run is treated as "every disk file matches the
// previous embed" so first-launch users do not see merge warnings
// for untouched bundled defaults.
//
// This package depends only on stdlib + genmark. Do not import any
// site-manager or storage code here.
package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PeterBooker/locorum/internal/genmark"
)

// Verdict classifies one file's reconcile outcome.
type Verdict int

const (
	// VerdictNoOp: disk already matches the bundled file (either
	// content, or the user has explicitly opted out via genmark
	// signature removal).
	VerdictNoOp Verdict = iota

	// VerdictWrote: file was missing or matched the previous
	// embed; we wrote the new bundled bytes to disk.
	VerdictWrote

	// VerdictUserOwned: disk file is marked-managed but the
	// genmark.WriteAtomic short-circuit found the bytes already
	// equal — same outcome as NoOp from the user's perspective.
	// Reserved for telemetry; callers can fold it into NoOp.
	VerdictUserOwned

	// VerdictMergeNeeded: disk file diverged from both the
	// previous embed and the new embed. The user has hand-edited
	// AND the bundled defaults changed.  We did NOT write — the
	// user must reconcile manually.
	VerdictMergeNeeded
)

// String renders Verdict for log lines.
func (v Verdict) String() string {
	switch v {
	case VerdictNoOp:
		return "no-op"
	case VerdictWrote:
		return "wrote"
	case VerdictUserOwned:
		return "user-owned"
	case VerdictMergeNeeded:
		return "merge-needed"
	}
	return "unknown"
}

// FileResult is the per-file output of Reconcile.
type FileResult struct {
	// Path is the slash-separated path relative to diskRoot.
	Path string
	// Verdict is the reconcile outcome.
	Verdict Verdict
	// Err is non-nil only when an unexpected IO error short-
	// circuited a single file. Reconcile keeps going so one bad
	// file does not block the rest.
	Err error
}

// Report aggregates a Reconcile pass.
type Report struct {
	Files []FileResult
}

// MergeNeeded returns the subset of Files whose Verdict is
// VerdictMergeNeeded. Surfaced in the System Health panel so the
// user knows their tweaks no longer match the new defaults.
func (r Report) MergeNeeded() []FileResult {
	var out []FileResult
	for _, f := range r.Files {
		if f.Verdict == VerdictMergeNeeded {
			out = append(out, f)
		}
	}
	return out
}

// State is the on-disk hash record. Versioned so a future schema
// bump can be detected and the file rewritten cleanly.
type State struct {
	Version int               `json:"version"`
	Hashes  map[string]string `json:"hashes"` // slash-relative path → hex SHA-256
}

// stateVersion is the only persisted layout we currently understand.
// Newer files are treated as "no prior state" so we don't crash on
// downgrade — we just lose merge-detection accuracy for one cycle.
const stateVersion = 1

// LoadState reads State from disk. Missing file or version mismatch
// returns an empty State without error so first-run / downgrade is
// not a fatal condition.
func LoadState(path string) (State, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return State{Version: stateVersion, Hashes: map[string]string{}}, nil
		}
		return State{}, fmt.Errorf("assets: read state %q: %w", path, err)
	}
	var s State
	if err := json.Unmarshal(body, &s); err != nil {
		// Corrupt JSON: log-and-continue is the only sensible
		// behaviour. Returning an error would brick startup.
		return State{Version: stateVersion, Hashes: map[string]string{}}, nil
	}
	if s.Version != stateVersion {
		return State{Version: stateVersion, Hashes: map[string]string{}}, nil
	}
	if s.Hashes == nil {
		s.Hashes = map[string]string{}
	}
	return s, nil
}

// SaveState writes State to path atomically (via genmark.WriteAtomic).
// The file is purely Locorum-managed; users have no reason to touch
// it.
func SaveState(path string, s State) error {
	if s.Version == 0 {
		s.Version = stateVersion
	}
	if s.Hashes == nil {
		s.Hashes = map[string]string{}
	}
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("assets: marshal state: %w", err)
	}
	body = append(body, '\n')
	return genmark.WriteAtomic(path, body, 0o644)
}

// Reconcile walks every file under embedRoot in the embedded FS and
// applies the four-cell verdict matrix:
//
//	|               | disk == prev | disk != prev |
//	|---------------|--------------|--------------|
//	| embed == disk |    no-op     |    no-op     |
//	|               |              | (user edit ↔ |
//	|               |              |  bundled
//	|               |              |  unchanged)  |
//	| embed != disk |   overwrite  |   merge      |
//	|               |              |   needed     |
//
// Plus a "missing on disk" cell that always writes.
//
// onWrite is called per file the function actually wrote — useful
// for telemetry / tests. Pass nil to ignore.
//
// Reconcile returns a Report and, on any unexpected IO during the
// walk, an error. Per-file errors stay in Report.Files[i].Err so
// the caller can decide how strict to be.
func Reconcile(embedFS fs.FS, embedRoot, diskRoot string, prev State, onWrite func(path string)) (Report, State, error) {
	if prev.Hashes == nil {
		prev.Hashes = map[string]string{}
	}
	next := State{Version: stateVersion, Hashes: map[string]string{}}
	var report Report

	walkErr := fs.WalkDir(embedFS, embedRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(embedRoot, p)
		if err != nil {
			return err
		}
		// Normalise to slash for the State key — matches how
		// embed.FS exposes paths regardless of host OS.
		key := filepath.ToSlash(rel)

		embedBytes, err := fs.ReadFile(embedFS, p)
		if err != nil {
			report.Files = append(report.Files, FileResult{Path: key, Err: err})
			return nil
		}
		embedHash := hashHex(embedBytes)
		next.Hashes[key] = embedHash

		diskPath := filepath.Join(diskRoot, filepath.FromSlash(rel))
		diskHash, diskExists, err := hashFile(diskPath)
		if err != nil {
			report.Files = append(report.Files, FileResult{Path: key, Err: err})
			return nil
		}

		switch {
		case !diskExists:
			// First run for this file, or the user removed it
			// — write the bundled default.
			if err := genmark.WriteAtomic(diskPath, embedBytes, 0o644); err != nil {
				report.Files = append(report.Files, FileResult{Path: key, Err: err})
				return nil
			}
			report.Files = append(report.Files, FileResult{Path: key, Verdict: VerdictWrote})
			if onWrite != nil {
				onWrite(key)
			}
		case diskHash == embedHash:
			report.Files = append(report.Files, FileResult{Path: key, Verdict: VerdictNoOp})
		case diskHash == prev.Hashes[key]:
			// Bundled changed; user did not edit. Overwrite.
			if err := genmark.WriteAtomic(diskPath, embedBytes, 0o644); err != nil {
				report.Files = append(report.Files, FileResult{Path: key, Err: err})
				return nil
			}
			report.Files = append(report.Files, FileResult{Path: key, Verdict: VerdictWrote})
			if onWrite != nil {
				onWrite(key)
			}
		default:
			// Both sides moved — user edited AND bundled
			// changed. Don't touch; surface a warning.
			report.Files = append(report.Files, FileResult{Path: key, Verdict: VerdictMergeNeeded})
		}
		return nil
	})
	if walkErr != nil {
		return report, prev, fmt.Errorf("assets: walk %q: %w", embedRoot, walkErr)
	}

	// Stable order so the report is reproducible (and tests stay
	// deterministic).
	sort.Slice(report.Files, func(i, j int) bool {
		return report.Files[i].Path < report.Files[j].Path
	})
	return report, next, nil
}

// hashHex returns the hex-encoded SHA-256 of body.
func hashHex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// hashFile reads path and returns its SHA-256 hex digest. Missing
// file returns ("", false, nil) — the caller decides what to do.
func hashFile(path string) (string, bool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("assets: read %q: %w", path, err)
	}
	return hashHex(body), true, nil
}

// DefaultStatePath returns the canonical state-file location under
// homeDir. Centralised so callers never re-derive this and drift.
func DefaultStatePath(homeDir string) string {
	return filepath.Join(homeDir, ".locorum", "state", "asset_hashes.json")
}

// IsConfigPath reports whether key (a slash-relative asset path)
// belongs to a subsection callers might want to filter on.  Useful
// for surfacing "your nginx tweaks need attention" without flooding
// the System Health panel with unrelated paths.
func IsConfigPath(key, prefix string) bool {
	return strings.HasPrefix(key, prefix+"/") || key == prefix
}
