package sites

import "errors"

// ErrSiteNotRunning is returned by lifecycle methods that require the
// site's containers to be up — wp-cli, link-check, snapshot, db import,
// etc. The UI branches on errors.Is to render a banner with a "Start
// site" action that kicks off StartSite (it does NOT auto-retry the
// original action; the user re-clicks once the site is healthy).
var ErrSiteNotRunning = errors.New("site is not running")
