package platform

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// MaxSanitisedUsernameLen caps the output of [SanitiseUsername]. 32 bytes
// is enough for any container-name suffix and keeps log lines tidy. Docker
// label-value limits are far larger, so this is policy, not protocol.
const MaxSanitisedUsernameLen = 32

// SanitiseUsername returns a deterministic, container-safe form of the
// host login name. The output:
//
//   - matches the regex `^[a-z][a-z0-9._-]*$` after normalisation
//   - is between 1 and [MaxSanitisedUsernameLen] runes long
//   - is byte-identical for byte-identical input across Go versions
//     (locked in by golden tests)
//
// Steps (in order):
//
//  1. NFKC compatibility composition. Collapses look-alikes that NFD
//     leaves alone — e.g. "ﬃ" → "ffi", "Ⅳ" → "IV", "①" → "1". Catches
//     fullwidth/halfwidth quirks common in Asian-locale Windows installs.
//  2. NFD decomposition + drop combining marks. Strips diacritics:
//     "André" → "Andre", "Müller" → "Muller". This is the DDEV bug
//     ("André Kraus" breaks containers) where the fix lives.
//  3. Replace `\` (Windows DOMAIN\user format), whitespace, and `/` with
//     `-`. Path separators are dangerous in container paths regardless of
//     where they end up.
//  4. Lowercase. Docker container names are case-sensitive but the
//     ecosystem (Compose, Kubernetes) treats them as lowercase; uniform
//     output sidesteps "USER vs user" ambiguity.
//  5. Drop characters outside `[a-z0-9._-]`. Anything that survives this
//     point is something a shell can mangle.
//  6. Collapse runs of `-` into one and trim leading/trailing `-`/`.`.
//  7. Truncate to [MaxSanitisedUsernameLen] (post-step-6, byte length).
//  8. If the result is empty or starts with a digit, prefix `u-`. If it
//     is *still* empty (impossible after the prefix, but defensive),
//     fall through to the literal "locorum".
//
// Properties asserted by tests:
//   - Idempotent: SanitiseUsername(SanitiseUsername(x)) == SanitiseUsername(x).
//   - Output is in [1, MaxSanitisedUsernameLen] bytes.
//   - Output regex `^[a-z][a-z0-9._-]*$` always holds.
func SanitiseUsername(in string) string {
	// 1. NFKC normalise.
	s := norm.NFKC.String(in)

	// 2. NFD decompose; drop combining marks; recompose to a flat string.
	s = stripCombining(s)

	// 3. Replace backslashes, slashes, and whitespace with `-`.
	var bld strings.Builder
	bld.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\\', r == '/':
			bld.WriteByte('-')
		case unicode.IsSpace(r):
			bld.WriteByte('-')
		default:
			bld.WriteRune(r)
		}
	}
	s = bld.String()

	// 4. Lowercase.
	s = strings.ToLower(s)

	// 5. Drop characters outside the allowed set.
	bld.Reset()
	bld.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			bld.WriteRune(r)
		case r >= '0' && r <= '9':
			bld.WriteRune(r)
		case r == '.' || r == '-' || r == '_':
			bld.WriteRune(r)
		}
	}
	s = bld.String()

	// 6. Collapse runs of `-`, trim leading/trailing punctuation.
	s = collapseDashes(s)
	s = strings.Trim(s, "-.")

	// 7. Truncate.
	if len(s) > MaxSanitisedUsernameLen {
		s = s[:MaxSanitisedUsernameLen]
		// A truncate may now end in a `.` or `-` we've already trimmed
		// once — re-trim.
		s = strings.TrimRight(s, "-.")
	}

	// 8. Empty or doesn't start with [a-z]? Prefix. The regex contract
	//    is `^[a-z][a-z0-9._-]*$`, so any leading char other than a
	//    lowercase letter — digit, underscore, etc. — must be moved
	//    behind the `u-` prefix to keep the first byte alphabetic.
	if s == "" || !(s[0] >= 'a' && s[0] <= 'z') {
		s = "u-" + s
		if len(s) > MaxSanitisedUsernameLen {
			s = s[:MaxSanitisedUsernameLen]
			s = strings.TrimRight(s, "-.")
		}
	}
	if s == "" || s == "u-" {
		return "locorum"
	}
	return s
}

// stripCombining decomposes via NFD and filters out characters in the Mn
// (Mark, Nonspacing) class — i.e. combining diacritics. The remaining
// runes are the "base" letters.
func stripCombining(s string) string {
	dec := norm.NFD.String(s)
	var bld strings.Builder
	bld.Grow(len(dec))
	for _, r := range dec {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		bld.WriteRune(r)
	}
	return bld.String()
}

// collapseDashes replaces runs of `-` with a single `-`. ASCII-only —
// runs entirely on byte boundaries because `-` is ASCII.
func collapseDashes(s string) string {
	if !strings.Contains(s, "--") {
		return s
	}
	var bld strings.Builder
	bld.Grow(len(s))
	prevDash := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' {
			if prevDash {
				continue
			}
			prevDash = true
			bld.WriteByte('-')
			continue
		}
		prevDash = false
		bld.WriteByte(c)
	}
	return bld.String()
}
