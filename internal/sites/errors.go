package sites

import "errors"

// ErrSiteNotRunning is returned by lifecycle methods that require the
// site's containers to be up — wp-cli, link-check, snapshot, db import,
// etc. The UI branches on errors.Is to render a banner with a "Start
// site" action that kicks off StartSite (it does NOT auto-retry the
// original action; the user re-clicks once the site is healthy).
var ErrSiteNotRunning = errors.New("site is not running")

// ErrPathTooLong is returned by [SiteManager.AddSite] and the start-site
// lifecycle when the site's root path would breach Windows' MAX_PATH and
// the OS does not have LongPathsEnabled. UI code branches on errors.Is
// to render a precise remediation dialog. Linux/macOS hosts never see
// this error — the underlying check returns false on non-Windows.
var ErrPathTooLong = errors.New("site path exceeds Windows MAX_PATH and LongPathsEnabled is not set")
