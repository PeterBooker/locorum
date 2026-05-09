// Package updatecheck queries GitHub Releases for newer versions of
// Locorum and persists the answer between launches. The result feeds the
// Settings → Diagnostics card and the small badge on the nav rail
// Settings entry.
//
// Design choices:
//   - One HTTP call per launch (gated by mtime throttle) — no chatty
//     background polling.
//   - No auto-update; the user clicks "Download" which opens the
//     release page in the default browser. Signed-binary distribution
//     is enough friction for v1.
//   - Channel = stable | beta. Stable picks the highest semver with no
//     prerelease tag; beta accepts any tag.
//   - Tiny semver compare lives here so we don't pull
//     golang.org/x/mod/semver for three-component compares.
package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultRepoOwner / DefaultRepoName point at the canonical Locorum
// release stream. Overridable via Options.RepoOwner/RepoName for test or
// for a future fork.
const (
	DefaultRepoOwner = "PeterBooker"
	DefaultRepoName  = "locorum"
)

// Channel is the user's desired release stream.
type Channel string

const (
	ChannelStable Channel = "stable"
	ChannelBeta   Channel = "beta"
)

// throttle caps how often we call the GitHub API. Four hours is more
// than long enough for "check on app start" UX while keeping us well
// under the 60 unauthenticated requests/hour budget.
const throttle = 4 * time.Hour

// httpTimeout caps a single fetch.
const httpTimeout = 10 * time.Second

// userAgent identifies the client. GitHub requires a User-Agent on
// every API request; sending the version helps debugging.
var userAgent = "locorum-updatecheck/1"

// Result is what Check returns: the latest version string (semver
// without a leading "v"), the URL to the release page, and the
// human-readable release notes. All fields are empty when no upgrade
// is available.
type Result struct {
	Latest string
	URL    string
	Notes  string
}

// Options controls Check. Zero values pick sensible defaults.
type Options struct {
	RepoOwner string
	RepoName  string
	Channel   Channel
	// HTTPClient overrides the default client (used by tests).
	HTTPClient *http.Client
	// StatePath is where the throttle mtime lives. Empty = no throttle.
	StatePath string
	// Now is the wall-clock used by the throttle; tests set it.
	Now func() time.Time
}

// Check returns the highest version on the configured channel that is
// strictly greater than `current`. When the throttle file says we
// checked recently, returns (Result{}, ErrThrottled, nil) — the caller
// reads the previously-cached available version from settings.
//
// Network errors propagate; a 4xx/5xx from GitHub returns a wrapped
// error so the caller can log and continue.
func Check(ctx context.Context, current string, opts Options) (Result, error) {
	opts = withDefaults(opts)
	if opts.StatePath != "" && !shouldRun(opts.StatePath, opts.Now()) {
		return Result{}, ErrThrottled
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", opts.RepoOwner, opts.RepoName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("updatecheck: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return Result{}, fmt.Errorf("updatecheck: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var releases []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return Result{}, fmt.Errorf("updatecheck: decode: %w", err)
	}

	// Touch the throttle file regardless of what we found — a successful
	// API hit "counts" against the budget.
	if opts.StatePath != "" {
		_ = touch(opts.StatePath, opts.Now())
	}

	pick := pickRelease(releases, opts.Channel)
	if pick == nil {
		return Result{}, nil
	}
	latest := strings.TrimPrefix(pick.TagName, "v")
	if !isStrictlyNewer(latest, current) {
		return Result{}, nil
	}
	return Result{
		Latest: latest,
		URL:    pick.HTMLURL,
		Notes:  pick.Body,
	}, nil
}

// IsStrictlyNewer reports whether `latest` is a strictly higher semver
// than `current`. Exposed for callers that want to compare the
// dismissed-version setting against a freshly-fetched latest without
// re-running the GitHub call.
func IsStrictlyNewer(latest, current string) bool { return isStrictlyNewer(latest, current) }

// ErrThrottled is returned by Check when the throttle file says we
// checked too recently. Not really an error — the caller logs and
// surfaces the previously-cached value.
var ErrThrottled = errors.New("updatecheck: throttled")

// ── internals ─────────────────────────────────────────────────────────

type githubRelease struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Name       string `json:"name"`
	Body       string `json:"body"`
	Prerelease bool   `json:"prerelease"`
	Draft      bool   `json:"draft"`
}

func withDefaults(opts Options) Options {
	if opts.RepoOwner == "" {
		opts.RepoOwner = DefaultRepoOwner
	}
	if opts.RepoName == "" {
		opts.RepoName = DefaultRepoName
	}
	if opts.Channel == "" {
		opts.Channel = ChannelStable
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: httpTimeout}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return opts
}

func pickRelease(rs []githubRelease, ch Channel) *githubRelease {
	var best *githubRelease
	bestSem := semver{}
	for i := range rs {
		r := &rs[i]
		if r.Draft {
			continue
		}
		if ch == ChannelStable && r.Prerelease {
			continue
		}
		s, ok := parseSemver(r.TagName)
		if !ok {
			continue
		}
		if best == nil || compareSemver(s, bestSem) > 0 {
			best = r
			bestSem = s
		}
	}
	return best
}

// shouldRun reports whether the throttle file is older than `throttle`.
// Missing / unreadable file = run.
func shouldRun(path string, now time.Time) bool {
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	return now.Sub(info.ModTime()) >= throttle
}

// touch creates / refreshes the throttle file's mtime to `now`.
func touch(path string, now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_ = f.Close()
	return os.Chtimes(path, now, now)
}

// ── tiny semver ──────────────────────────────────────────────────────

type semver struct {
	major, minor, patch int
	pre                 string
}

// parseSemver parses "v1.2.3" or "1.2.3" or "1.2.3-beta.1". Returns
// false on garbage; the caller skips those entries.
func parseSemver(tag string) (semver, bool) {
	tag = strings.TrimPrefix(tag, "v")
	pre := ""
	if i := strings.IndexByte(tag, '-'); i >= 0 {
		pre = tag[i+1:]
		tag = tag[:i]
	}
	if i := strings.IndexByte(tag, '+'); i >= 0 {
		tag = tag[:i]
	}
	parts := strings.SplitN(tag, ".", 3)
	if len(parts) < 2 {
		return semver{}, false
	}
	mj, err := strconv.Atoi(parts[0])
	if err != nil {
		return semver{}, false
	}
	mn, err := strconv.Atoi(parts[1])
	if err != nil {
		return semver{}, false
	}
	pt := 0
	if len(parts) == 3 {
		pt, err = strconv.Atoi(parts[2])
		if err != nil {
			return semver{}, false
		}
	}
	return semver{major: mj, minor: mn, patch: pt, pre: pre}, true
}

// compareSemver returns -1 / 0 / +1 in the usual ordering. A version
// without a prerelease tag is greater than the same triple WITH one
// (semver §11). Two prerelease strings compare lexicographically — good
// enough for "beta.1" < "beta.2"; documented limitation for exotic
// dotted suffixes.
func compareSemver(a, b semver) int {
	switch {
	case a.major != b.major:
		return cmpInt(a.major, b.major)
	case a.minor != b.minor:
		return cmpInt(a.minor, b.minor)
	case a.patch != b.patch:
		return cmpInt(a.patch, b.patch)
	}
	if a.pre == b.pre {
		return 0
	}
	if a.pre == "" {
		return 1
	}
	if b.pre == "" {
		return -1
	}
	return strings.Compare(a.pre, b.pre)
}

func cmpInt(a, b int) int {
	switch {
	case a > b:
		return 1
	case a < b:
		return -1
	}
	return 0
}

func isStrictlyNewer(latest, current string) bool {
	la, ok := parseSemver(latest)
	if !ok {
		return false
	}
	cu, ok := parseSemver(current)
	if !ok {
		// "dev" or other non-semver current strings: be conservative
		// and don't surface an upgrade banner — we don't know if the
		// user is running ahead of latest.
		return false
	}
	return compareSemver(la, cu) > 0
}
