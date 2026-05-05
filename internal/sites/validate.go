package sites

import (
	"github.com/PeterBooker/locorum/internal/platform"
)

// PathNote is a single warning produced by [ValidateSitePath]. Severity is
// always "warn" — the modal allows submission either way; the note exists
// to set expectations ("this site is going to feel slow" / "Windows MAX_PATH
// could bite later").
//
// We don't reuse health.Finding here because the lifecycle is different:
// these are surfaced inline next to the path field, not in the System Health
// panel. Sharing a struct would couple the new-site modal to the runner
// machinery for no benefit.
type PathNote struct {
	// Title is the short heading. ~40 chars.
	Title string

	// Detail is the single-sentence explanation.
	Detail string

	// HelpURL is an optional documentation link.
	HelpURL string
}

// ValidateSitePath returns 0..n warning notes for path p on a host described
// by info. Pure: no I/O, no globals, no clock. Safe to call on every
// keystroke (the caller debounces before re-rendering, but the function
// itself is microsecond-cheap).
//
// Cases checked:
//
//   - WSL host with site under /mnt/c (or any /mnt/<letter>/) — DrvFS is
//     much slower than native ext4 for the WordPress workload.
//   - Windows host where the path's expected longest descendant exceeds
//     MAX_PATH — some plugin assets will fail to resolve.
//
// Empty path → no notes (the form's "required" check handles emptiness;
// we don't double up).
func ValidateSitePath(p string, info *platform.Info) []PathNote {
	if p == "" || info == nil {
		return nil
	}

	var notes []PathNote

	if info.WSL.Active && platform.IsMntC(p) {
		notes = append(notes, PathNote{
			Title:   "Slow filesystem",
			Detail:  "Sites under /mnt/c (or other Windows-mounted drives) run roughly 10× slower than under /home/<user>/.",
			HelpURL: "https://docs.locorum.dev/wsl-performance",
		})
	}

	if info.OS == "windows" && platform.IsLongPath(p) {
		notes = append(notes, PathNote{
			Title:   "Path may exceed Windows 260-character limit",
			Detail:  "Some WordPress plugin assets may fail to resolve. Use a shorter root path, or enable LongPathsEnabled in the registry.",
			HelpURL: "https://learn.microsoft.com/en-us/windows/win32/fileio/maximum-file-path-limitation",
		})
	}

	return notes
}
