//go:build !windows

package sites

import "syscall"

// extraOpenFlags returns platform-specific OpenFile flags for tar extraction.
// On Unix we add O_NOFOLLOW so a pre-existing symlink at the target path is
// rejected with ELOOP rather than silently followed (which would let a
// malicious tarball with a symlink in an earlier entry redirect later
// regular-file writes outside the docroot).
func extraOpenFlags() int { return syscall.O_NOFOLLOW }
