package ui

import (
	"errors"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/router"
	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/tls"
	"github.com/PeterBooker/locorum/internal/utils"
)

// External documentation links surfaced by the typed-error banner
// actions. URLs live here (not scattered through call sites) so a future
// rebrand or doc move is one search-and-replace.
const (
	dockerStartHelpURL   = "https://docs.docker.com/desktop/"
	mkcertInstallHelpURL = "https://github.com/FiloSottile/mkcert#installation"
)

// SurfaceError translates a backend error into the appropriate banner
// presentation. Recognised sentinels get an action button that points
// the user at the next step (start the daemon, install mkcert, free up
// the port, start the site). Anything unrecognised falls back to the
// plain "<prefix>: <err>" toast.
//
// `prefix` is the user-facing context for the unrecognised path ("Failed
// to start site"). For sentinel branches the prefix is ignored and the
// banner shows the canonical message tied to that sentinel.
//
// The starter argument is invoked by the ErrSiteNotRunning branch's
// "Start site" button. Pass nil if the calling context does not have a
// site in hand (e.g. settings-page errors); the action is then omitted.
//
// CLAUDE.md invariant 1 keeps the UI on a strict diet of internal/sites,
// internal/types, orch.StepResult, and docker.PullProgress. The added
// imports here (docker, router, tls) are intentionally limited to
// sentinel-value comparison via errors.Is — no calls into those
// packages' methods. Do not generalise this file into "the UI knows
// docker"; if it grows past sentinels, refactor through SiteManager.
func SurfaceError(state *UIState, prefix string, err error, starter func()) {
	if err == nil {
		return
	}
	switch {
	case errors.Is(err, docker.ErrDaemonUnreachable):
		state.ShowErrorWithAction(
			"Docker isn't running. Start Docker Desktop (or your daemon) and try again.",
			NotifyAction{
				ID:    "open-docker-help",
				Label: "How to start Docker",
				Run: func() {
					_ = utils.OpenURL(dockerStartHelpURL)
					state.ClearErrorBanner()
				},
			},
		)
	case errors.Is(err, tls.ErrMkcertMissing):
		state.ShowErrorWithAction(
			"mkcert is not installed — HTTPS certificates can't be issued.",
			NotifyAction{
				ID:    "open-mkcert-help",
				Label: "Show me how",
				Run: func() {
					_ = utils.OpenURL(mkcertInstallHelpURL)
					state.ClearErrorBanner()
				},
			},
		)
	case errors.Is(err, router.ErrPortInUse):
		state.ShowErrorWithAction(
			"A required network port is already in use. Choose a different port in Settings.",
			NotifyAction{
				ID:    "open-network-settings",
				Label: "Open Settings",
				Run: func() {
					state.SetNavView(NavViewSettings)
					state.ClearErrorBanner()
				},
			},
		)
	case errors.Is(err, sites.ErrSiteNotRunning):
		if starter == nil {
			state.ShowError(prefix + ": " + err.Error())
			return
		}
		state.ShowErrorWithAction(
			"This action needs the site to be running.",
			NotifyAction{
				ID:    "start-site",
				Label: "Start site",
				Run: func() {
					go starter()
					state.ClearErrorBanner()
				},
			},
		)
	default:
		state.ShowError(prefix + ": " + err.Error())
	}
}
