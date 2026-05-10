package sites

import (
	"github.com/PeterBooker/locorum/internal/platform"
)

// PathNoteSeverity classifies a [PathNote] as either a soft warning the
// user can dismiss or a hard error that should block site creation /
// start. The zero value (Warn) is the historical default — every existing
// caller that constructs PathNote without setting Severity keeps the same
// behaviour.
type PathNoteSeverity int8

const (
	// PathSeverityWarn is informational. The new-site modal renders an
	// amber banner; the user may submit anyway.
	PathSeverityWarn PathNoteSeverity = 0

	// PathSeverityBlock is a hard error. The form's submit button is
	// disabled and [SiteManager.AddSite] / [SiteManager.StartSite]
	// refuse with [ErrPathTooLong].
	PathSeverityBlock PathNoteSeverity = 1
)

// PathNote is a single warning produced by [ValidateSitePath]. The
// modal allows submission only when [Severity] is [PathSeverityWarn] for
// every note in the slice; a single [PathSeverityBlock] disables the
// Create button and the create call refuses regardless.
//
// We don't reuse health.Finding here because the lifecycle is different:
// these are surfaced inline next to the path field, not in the System Health
// panel. Sharing a struct would couple the new-site modal to the runner
// machinery for no benefit.
type PathNote struct {
	// Severity selects between soft warn (default) and hard block.
	Severity PathNoteSeverity

	// Title is the short heading. ~40 chars.
	Title string

	// Detail is the single-sentence explanation.
	Detail string

	// Remediation is an imperative one-liner ("Use a shorter path or
	// enable LongPathsEnabled"). Only set for blocking notes; warn
	// notes lean on Detail to convey the same.
	Remediation string

	// HelpURL is an optional documentation link.
	HelpURL string
}

// ValidateSitePath returns 0..n notes for path p on a host described by
// info. Pure: no I/O, no globals, no clock. Safe to call on every
// keystroke (the caller debounces before re-rendering, but the function
// itself is microsecond-cheap).
//
// Cases checked:
//
//   - WSL host with site under /mnt/c (or any /mnt/<letter>/) — DrvFS is
//     much slower than native ext4 for the WordPress workload. Warn.
//   - Windows host where the path's expected longest descendant exceeds
//     MAX_PATH. Severity depends on [platform.Info.LongPathsEnabled]:
//     when the OS has the long-path opt-in set, this is a Warn (the OS
//     handles it, but some legacy plugins still won't). When the flag is
//     off (or the registry value couldn't be read), it's a Block — the
//     create call refuses to proceed.
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
		switch {
		case info.LongPathsEnabled:
			// The OS's long-path opt-in is set. Most things will work,
			// but some plugins / tooling don't honour the manifest opt-in
			// and will still hit MAX_PATH. Keep as a soft warning so the
			// user can choose to use a shorter path anyway.
			notes = append(notes, PathNote{
				Severity: PathSeverityWarn,
				Title:    "Path may exceed Windows 260-character limit",
				Detail:   "LongPathsEnabled is set, but some WordPress plugins still don't opt in to long paths and may fail at install time.",
				HelpURL:  "https://learn.microsoft.com/en-us/windows/win32/fileio/maximum-file-path-limitation",
			})
		default:
			notes = append(notes, PathNote{
				Severity:    PathSeverityBlock,
				Title:       "Path exceeds Windows 260-character limit",
				Detail:      "Windows MAX_PATH is 260 characters; this path's deepest WordPress asset would breach it.",
				Remediation: "Use a shorter path (≤ 200 characters), or enable LongPathsEnabled in the registry (admin) and restart Locorum.",
				HelpURL:     "https://learn.microsoft.com/en-us/windows/win32/fileio/maximum-file-path-limitation",
			})
		}
	}

	return notes
}

// HasBlockingNote reports whether any note in notes carries
// [PathSeverityBlock]. Convenience for the UI form's submit-gating logic
// and for [SiteManager.AddSite] / [SiteManager.StartSite] preflight.
func HasBlockingNote(notes []PathNote) bool {
	for _, n := range notes {
		if n.Severity == PathSeverityBlock {
			return true
		}
	}
	return false
}
