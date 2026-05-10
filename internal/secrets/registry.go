// Package secrets provides a process-wide secret registry used to redact
// known-sensitive substrings (DB passwords, auth tokens, salts) from any
// string that is about to be persisted, logged, or returned to a user-
// facing surface.
//
// The single global registry is populated on startup from the storage
// layer (every site's DBPassword + salts) and kept in sync as sites are
// added, cloned, or deleted. Boundaries that handle backend errors —
// the audit log writer, the activity recorder, the daemon RPCError
// builder, the MCP error formatter — all funnel error strings through
// Redact before externalising them.
//
// The registry is intentionally tiny on purpose: a global list of opaque
// strings, redacted by exact-substring match. We deliberately do NOT
// attempt regex-based "looks like a secret" detection; false positives
// in error messages would obscure debugging more than they would help.
package secrets

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// redactionMarker is the literal substituted in for matched secrets.
const redactionMarker = "[REDACTED]"

// minSecretLen guards against a vacuous registration that would replace
// every "ab" in every error message. We never persist anything shorter
// than 8 characters as a "secret" — DB passwords are 32 hex chars,
// tokens are 64+, salts are 64. A 1-char "secret" almost certainly is
// a programmer error and refusing it loudly is safer than redacting
// the word "if" out of every panic.
const minSecretLen = 8

// Registry holds the set of secret values to redact. Safe for concurrent
// use. The zero value is unusable; obtain one via NewRegistry or use the
// process-wide Default.
type Registry struct {
	mu     sync.RWMutex
	values map[string]struct{}
}

// NewRegistry returns an empty registry. Most callers should use Default.
func NewRegistry() *Registry {
	return &Registry{values: make(map[string]struct{})}
}

// Default is the process-wide registry. Populated at app startup (see
// internal/app.Initialize) and kept in sync by SiteManager.
var Default = NewRegistry()

// Add registers s as a secret to redact. Strings shorter than minSecretLen
// are silently ignored — see the const for rationale. Empty strings are
// always ignored. Idempotent: re-adding the same value is a no-op.
func (r *Registry) Add(s string) {
	if len(s) < minSecretLen {
		return
	}
	r.mu.Lock()
	r.values[s] = struct{}{}
	r.mu.Unlock()
}

// Remove unregisters s. Idempotent: removing a value that was never
// registered is a no-op.
func (r *Registry) Remove(s string) {
	if s == "" {
		return
	}
	r.mu.Lock()
	delete(r.values, s)
	r.mu.Unlock()
}

// Reset clears every entry. Used by tests; not used by app code.
func (r *Registry) Reset() {
	r.mu.Lock()
	r.values = make(map[string]struct{})
	r.mu.Unlock()
}

// RedactString returns s with every registered secret substring replaced
// by the redaction marker. The empty input returns empty.
//
// We sort by length (descending) before substituting so that overlapping
// secrets — e.g. a password that happens to be a prefix of a token —
// don't leave a half-redacted suffix behind. With small registries
// (O(N) sites) the sort cost is negligible.
func (r *Registry) RedactString(s string) string {
	if s == "" {
		return s
	}
	r.mu.RLock()
	if len(r.values) == 0 {
		r.mu.RUnlock()
		return s
	}
	values := make([]string, 0, len(r.values))
	for v := range r.values {
		values = append(values, v)
	}
	r.mu.RUnlock()

	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, v := range values {
		if strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, redactionMarker)
		}
	}
	return s
}

// Redact returns err with its message redacted. The wrapped chain is
// flattened: the returned error is a fresh errors.New(redacted-msg). We
// deliberately drop wrap/unwrap relationships at this boundary because
// the caller is by definition about to externalise the error — no further
// errors.Is/As checks are expected. nil in → nil out.
func (r *Registry) Redact(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	red := r.RedactString(msg)
	if red == msg {
		return err
	}
	return errors.New(red)
}

// Add is a convenience for Default.Add.
func Add(s string) { Default.Add(s) }

// Remove is a convenience for Default.Remove.
func Remove(s string) { Default.Remove(s) }

// RedactString is a convenience for Default.RedactString.
func RedactString(s string) string { return Default.RedactString(s) }

// Redact is a convenience for Default.Redact.
func Redact(err error) error { return Default.Redact(err) }
