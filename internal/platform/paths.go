package platform

import (
	"path/filepath"
	"runtime"
	"strings"
)

// dockerSeparators is the canonical list of separator characters that must
// be normalised to '/'. We *cannot* delegate to filepath.ToSlash because on
// Linux that's a no-op for '\\' (Go's per-OS separator constant) — and the
// whole point of DockerPath is to translate paths regardless of the build
// host (e.g. a Linux test runner asserting on a Windows-shaped input).
const dockerSeparators = "\\"

// WPMaxPluginPathSuffix is the deepest path-suffix we expect under a site's
// FilesDir for a worst-case WordPress install. Used by IsLongPath to decide
// whether the fully-qualified path will breach Windows' 260-character
// MAX_PATH limit.
//
// Sourced from a scan of the wordpress.org top-100 plugins for the longest
// asset path; padded with a safety margin so a future wp-mu-plugin or
// nested block-asset import doesn't trip it. Length 180 = 158 of measured
// content + 22 of headroom.
const WPMaxPluginPathSuffix = 180

// WindowsMaxPath is the legacy MAX_PATH constant from <minwindef.h>. Modern
// Windows + the LongPathsEnabled registry key allows much longer paths, but
// many tools (and the user's text editor) still respect the historical
// limit. Detect-and-warn beats letting the user discover the breakage at
// composer-update time.
const WindowsMaxPath = 260

// DockerPath returns p in the slash form Docker's bind-mount API accepts on
// every supported platform. On Linux/macOS it's a no-op (paths are already
// slash-separated). On Windows native, it converts `C:\Users\foo\bar` →
// `/host_mnt/c/Users/foo/bar` style — wait, no, Docker's bind API accepts
// `C:\Users\foo\bar` natively on Windows; what it *won't* accept is mixed
// forward/back slashes, so we forward-slash everything via filepath.ToSlash.
//
// On WSL hosts pointing at a Windows-side Docker daemon, the caller must
// either:
//   - use Linux-side paths (everything under /home/<user>/) and rely on
//     wsl.exe + Docker Desktop's path-translation, OR
//   - hand-translate `/mnt/c/...` to `C:/...` first (we don't auto-translate
//     here because the caller knows whether it's talking to a host- or
//     Linux-side daemon, and we don't want to corrupt a Linux-side bind).
//
// In short: this helper guarantees forward-slash only. Anything more
// platform-specific is the router/spec-builder's call.
func DockerPath(p string) string {
	if p == "" {
		return ""
	}
	// First pass via filepath.ToSlash to handle the build host's own
	// separator (a Windows build's `\\` for paths Go itself produced).
	// Second pass replaces any *other* backslashes that survived — e.g.
	// a Windows-shaped input handed to a Linux build.
	out := filepath.ToSlash(p)
	if strings.ContainsAny(out, dockerSeparators) {
		out = strings.ReplaceAll(out, "\\", "/")
	}
	return out
}

// IsLongPath reports whether the *expected* longest descendant of p
// (`p + "/" + WPMaxPluginPathSuffix-chars-of-suffix`) would breach Windows'
// MAX_PATH limit.
//
// Returns false on non-Windows hosts — Linux/macOS path limits are far
// higher and IsLongPath would just produce noise. The check is informational;
// a true result does NOT block site creation, just adds a yellow note.
func IsLongPath(p string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	// Use len() on the original path because Windows paths are typically
	// drawn from a Windows-codepage (Latin-1-ish) charset where bytes ≈
	// chars. Even if the user has a CJK home directory, we'd over-count
	// on non-ASCII — which is the safe direction for a "warn if too long"
	// check.
	if p == "" {
		return false
	}
	return len(p)+1+WPMaxPluginPathSuffix > WindowsMaxPath
}

// IsMntC reports whether p is on Windows-host-mounted DrvFS under WSL2
// (`/mnt/c/...`, `/mnt/d/...`, etc.). Paths there are 10× slower than
// native ext4 — sites kept here will *work* but feel sluggish.
//
// Returns false on non-WSL hosts, since /mnt/c on a native Linux box is
// just a regular directory.
func IsMntC(p string) bool {
	if !Get().WSL.Active {
		return false
	}
	return isMntDrvFsPath(p)
}

// isMntDrvFsPath is the path-matching half of IsMntC, factored out so tests
// don't depend on the package-level WSL state.
func isMntDrvFsPath(p string) bool {
	clean := filepath.ToSlash(strings.ToLower(p))
	if !strings.HasPrefix(clean, "/mnt/") {
		return false
	}
	rest := clean[len("/mnt/"):]
	// Must have at least a single drive-letter segment: /mnt/c, /mnt/c/...
	if rest == "" {
		return false
	}
	// First segment is the drive letter. Strict: a single ascii letter.
	end := strings.IndexByte(rest, '/')
	letter := rest
	if end >= 0 {
		letter = rest[:end]
	}
	return len(letter) == 1 && letter[0] >= 'a' && letter[0] <= 'z'
}
