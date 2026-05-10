package ui

import (
	"context"
	"image"
	"image/color"
	"sync"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/types"
	"github.com/PeterBooker/locorum/internal/utils"
)

// AccessPanel is the per-site Access tab. It owns the LAN-access toggle,
// the URL row, two QR-code cards (the site URL and the "install root CA"
// URL served by capairing), and the WSL/IP-detection notices.
//
// Long-running operations (toggle, refresh IP, start CA pairing server)
// always run in goroutines per the load-bearing background-ops pattern;
// Layout never blocks.
type AccessPanel struct {
	state  *UIState
	sm     *sites.SiteManager
	toasts *Notifications

	enableBtn   widget.Clickable
	disableBtn  widget.Clickable
	refreshBtn  widget.Clickable
	copyURLBtn  widget.Clickable
	pairCABtn   widget.Clickable
	stopPairBtn widget.Clickable

	urlSel widget.Selectable

	// CA pairing server lifecycle. Owned by this panel because it is
	// strictly UI-driven — the server's URL is rendered as a QR code
	// and the auto-shutdown timer is tied to user interaction. Not
	// shared between sites: switching sites stops any pending server.
	mu         sync.Mutex
	pairing    *caPairingServer
	pairingURL string
	pairingErr string
	lastSiteID string
	qrCacheKey string
	qrSiteImg  paint.ImageOp
	qrPairImg  paint.ImageOp
}

// NewAccessPanel constructs an AccessPanel. State + SiteManager are
// required; toasts may be nil.
func NewAccessPanel(state *UIState, sm *sites.SiteManager, toasts *Notifications) *AccessPanel {
	return &AccessPanel{state: state, sm: sm, toasts: toasts}
}

// HandleUserInteractions processes button clicks. Must be called once
// per frame, before Layout.
func (ap *AccessPanel) HandleUserInteractions(gtx layout.Context, site *types.Site) {
	if site == nil {
		return
	}
	if utils.IsWSL() {
		// LAN access on WSL2 is gated; ignore button clicks defensively.
		return
	}

	if ap.enableBtn.Clicked(gtx) && !ap.state.IsSiteLanToggling(site.ID) {
		ap.toggleLAN(site.ID, true)
	}
	if ap.disableBtn.Clicked(gtx) && !ap.state.IsSiteLanToggling(site.ID) {
		ap.toggleLAN(site.ID, false)
	}
	if ap.refreshBtn.Clicked(gtx) && !ap.state.IsSiteLanToggling(site.ID) {
		ap.refresh(site.ID)
	}
	if ap.copyURLBtn.Clicked(gtx) {
		if url := ap.siteURL(site); url != "" {
			CopyToClipboard(gtx, url)
			if ap.toasts != nil {
				ap.toasts.ShowInfo("LAN URL copied to clipboard")
			}
		}
	}
	if ap.pairCABtn.Clicked(gtx) {
		ap.startPairing()
	}
	if ap.stopPairBtn.Clicked(gtx) {
		ap.stopPairing()
	}
}

// Layout renders the panel.
func (ap *AccessPanel) Layout(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	if site == nil {
		return layout.Dimensions{}
	}
	// Stop any in-flight pairing server when the user navigates away.
	ap.mu.Lock()
	if ap.lastSiteID != "" && ap.lastSiteID != site.ID && ap.pairing != nil {
		ap.pairing.Stop()
		ap.pairing = nil
		ap.pairingURL = ""
		ap.pairingErr = ""
	}
	ap.lastSiteID = site.ID
	ap.mu.Unlock()

	if utils.IsWSL() {
		return ap.layoutWSLNotice(gtx, th)
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return ap.layoutToggleCard(gtx, th, site)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if site.Multisite != "subdomain" {
				return layout.Dimensions{}
			}
			return ap.layoutMultisiteSubdomainNotice(gtx, th)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !site.LanEnabled {
				return layout.Dimensions{}
			}
			return ap.layoutAccessCard(gtx, th, site)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !site.LanEnabled {
				return layout.Dimensions{}
			}
			return ap.layoutCAPairingCard(gtx, th)
		}),
	)
}

// ─── Toggle card ────────────────────────────────────────────────────────────

func (ap *AccessPanel) layoutToggleCard(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	toggling := ap.state.IsSiteLanToggling(site.ID)
	hint := "Enable LAN access to open this site on phones, tablets, and other devices on the same Wi-Fi. Locorum derives a public-DNS hostname from the host's LAN IP and adds it to the site's TLS certificate — see ACCESS.md."
	if site.LanEnabled {
		ip := ap.sm.LanIP()
		if ip != nil {
			hint = "LAN access enabled. Devices on the same Wi-Fi can reach this site at the URL below; first-time visitors should also install the root CA via the QR code further down."
		} else {
			hint = "LAN access is enabled but no LAN IPv4 was detected. Click \"Refresh\" after moving networks, or set lan.ip_override in Settings if your network has a non-standard layout."
		}
	}

	return panel(gtx, th, "LAN access", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, hint)
				lbl.Color = th.Color.Fg2
				lbl.TextSize = th.Sizes.Body
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return ap.layoutStatusRow(gtx, th, site)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if toggling {
								return Loader(gtx, th, th.Dims.LoaderSizeSM)
							}
							if site.LanEnabled {
								return th.Small(gtx, &ap.disableBtn, "Disable LAN access")
							}
							return th.Primary(gtx, &ap.enableBtn, "Enable LAN access")
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if !site.LanEnabled {
								return layout.Dimensions{}
							}
							return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return th.Small(gtx, &ap.refreshBtn, "Refresh LAN IP")
							})
						}),
					)
				})
			}),
		)
	})
}

func (ap *AccessPanel) layoutStatusRow(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	statusKey := StatusErr
	statusLabel := "LAN disabled"
	if site.LanEnabled {
		statusKey = StatusOk
		statusLabel = "LAN enabled"
	}
	ipText := "No LAN IP detected"
	if ip := ap.sm.LanIP(); ip != nil {
		ipText = "LAN IP: " + ip.String()
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return spxStatusPill(gtx, th, statusKey, statusLabel)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, ipText)
				lbl.Color = th.Color.Fg3
				lbl.TextSize = th.Sizes.Base
				return lbl.Layout(gtx)
			})
		}),
	)
}

// ─── Access (URL + QR) card ────────────────────────────────────────────────

func (ap *AccessPanel) layoutAccessCard(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	url := ap.siteURL(site)
	if url == "" {
		return panel(gtx, th, "URL", func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, "No LAN URL is currently available — Locorum could not detect a usable IPv4 address.")
			lbl.Color = th.Color.Fg3
			lbl.TextSize = th.Sizes.Body
			return lbl.Layout(gtx)
		})
	}

	return panel(gtx, th, "URL & QR code", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return ap.copyableRow(gtx, th, "URL", url, &ap.urlSel, &ap.copyURLBtn)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return ap.qrCodeBox(gtx, th, "Scan with phone camera", url, ap.cachedSiteQR(url))
				})
			}),
		)
	})
}

// ─── CA pairing card ───────────────────────────────────────────────────────

func (ap *AccessPanel) layoutCAPairingCard(gtx layout.Context, th *Theme) layout.Dimensions {
	ap.mu.Lock()
	pairURL := ap.pairingURL
	pairErr := ap.pairingErr
	running := ap.pairing != nil
	ap.mu.Unlock()

	return panel(gtx, th, "Trust the certificate", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				body := "First-time visitors will see an HTTPS warning until they install Locorum's local certificate authority on the device. Click below to start a temporary HTTP server on your LAN IP that serves the root CA. Scan the resulting QR code on the device, follow the OS prompts to install, then trust the cert in your settings."
				lbl := material.Body2(th.Theme, body)
				lbl.Color = th.Color.Fg2
				lbl.TextSize = th.Sizes.Body
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				warning := "Only install this CA on devices you control. It signs only certificates issued by your local mkcert installation, but anyone with this file can issue trusted certs for any hostname on this device."
				lbl := material.Body2(th.Theme, warning)
				lbl.Color = th.Color.Err
				lbl.TextSize = th.Sizes.XS
				lbl.Font.Weight = font.Medium
				return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if pairErr != "" {
					lbl := material.Body2(th.Theme, "Could not start CA server: "+pairErr)
					lbl.Color = th.Color.Err
					lbl.TextSize = th.Sizes.Body
					return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
				}
				return layout.Dimensions{}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if running {
							return th.Small(gtx, &ap.stopPairBtn, "Stop CA server")
						}
						return th.Primary(gtx, &ap.pairCABtn, "Start CA pairing server")
					}),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !running || pairURL == "" {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return ap.qrCodeBox(gtx, th, "Install CA on this device", pairURL, ap.cachedPairQR(pairURL))
				})
			}),
		)
	})
}

// ─── Multisite-subdomain notice ────────────────────────────────────────────

// layoutMultisiteSubdomainNotice surfaces the v1 limitation around
// subdomain multisite + LAN: only the main site's URL works on the LAN
// hostname because subsite hostnames are stored in `wp_blogs.domain`
// rather than derived from `WP_HOME`/`WP_SITEURL`. The user can still
// enable LAN access — Locorum just can't redirect subsite URLs.
func (ap *AccessPanel) layoutMultisiteSubdomainNotice(gtx layout.Context, th *Theme) layout.Dimensions {
	return panel(gtx, th, "Multisite limitation", func(gtx layout.Context) layout.Dimensions {
		body := "This is a subdomain multisite. LAN access works for the main site, but subsite URLs (e.g. https://sub1.<slug>.localhost) will not redirect to the LAN hostname — WordPress stores subsite domains in the database rather than reading them from the URL constants. Track this with a search-replace on the wp_blogs table, or use subdirectory multisite if cross-device subsite access is important."
		lbl := material.Body2(th.Theme, body)
		lbl.Color = th.Color.Fg2
		lbl.TextSize = th.Sizes.Body
		return lbl.Layout(gtx)
	})
}

// ─── WSL notice ────────────────────────────────────────────────────────────

func (ap *AccessPanel) layoutWSLNotice(gtx layout.Context, th *Theme) layout.Dimensions {
	return panel(gtx, th, "LAN access", func(gtx layout.Context) layout.Dimensions {
		body := "LAN access is not supported on WSL2 yet — the Windows host's LAN IP isn't visible from inside WSL. See ACCESS.md for the future plan."
		lbl := material.Body2(th.Theme, body)
		lbl.Color = th.Color.Fg2
		lbl.TextSize = th.Sizes.Body
		return lbl.Layout(gtx)
	})
}

// ─── Helpers ───────────────────────────────────────────────────────────────

// siteURL renders the canonical https LAN URL for a site, or "" when LAN
// access is off / no IP could be detected.
func (ap *AccessPanel) siteURL(site *types.Site) string {
	host := ap.sm.LanHostFor(site)
	if host == "" {
		return ""
	}
	return "https://" + host + "/"
}

func (ap *AccessPanel) toggleLAN(siteID string, on bool) {
	ap.state.SetSiteLanToggling(siteID, true)
	go func() {
		defer ap.state.SetSiteLanToggling(siteID, false)
		var err error
		if on {
			err = ap.sm.EnableLAN(context.Background(), siteID)
		} else {
			err = ap.sm.DisableLAN(context.Background(), siteID)
		}
		if err != nil {
			ap.state.ShowError(err.Error())
		}
	}()
}

// refresh re-runs LAN detection, regenerates the on-disk wp-config
// host whitelist, and re-issues the router route. Mirrors the
// background-ops pattern used by toggleLAN; the LAN-toggling flag
// gates against double-clicks while the request is in flight.
func (ap *AccessPanel) refresh(siteID string) {
	ap.state.SetSiteLanToggling(siteID, true)
	go func() {
		defer ap.state.SetSiteLanToggling(siteID, false)
		if err := ap.sm.RefreshLAN(context.Background(), siteID); err != nil {
			ap.state.ShowError("Refresh LAN: " + err.Error())
			return
		}
		if ap.toasts != nil {
			ap.toasts.ShowInfo("LAN access refreshed")
		}
	}()
}

func (ap *AccessPanel) startPairing() {
	ip := ap.sm.LanIP()
	if ip == nil {
		ap.state.ShowError("Cannot start CA server: no LAN IP detected")
		return
	}
	go func() {
		ap.mu.Lock()
		if ap.pairing != nil {
			// Already running; no-op (the UI button would normally be
			// hidden, but the goroutine race window can still race).
			ap.mu.Unlock()
			return
		}
		ap.mu.Unlock()

		srv, err := newCAPairingServer(context.Background(), ap.sm, ip)
		if err != nil {
			ap.mu.Lock()
			ap.pairingErr = err.Error()
			ap.mu.Unlock()
			ap.state.Invalidate()
			return
		}
		ap.mu.Lock()
		ap.pairing = srv
		ap.pairingURL = srv.URL()
		ap.pairingErr = ""
		ap.mu.Unlock()
		ap.state.Invalidate()

		// Watch for shutdown so we can clear the URL when the server
		// auto-stops.
		go func() {
			<-srv.Done()
			ap.mu.Lock()
			if ap.pairing == srv {
				ap.pairing = nil
				ap.pairingURL = ""
			}
			ap.mu.Unlock()
			ap.state.Invalidate()
		}()
	}()
}

func (ap *AccessPanel) stopPairing() {
	ap.mu.Lock()
	srv := ap.pairing
	ap.pairing = nil
	ap.pairingURL = ""
	ap.mu.Unlock()
	if srv != nil {
		srv.Stop()
	}
	ap.state.Invalidate()
}

// cachedSiteQR returns the rendered ImageOp for the site URL, encoding
// once per (site URL) and reusing the cached op on subsequent frames.
func (ap *AccessPanel) cachedSiteQR(url string) paint.ImageOp {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	key := "site:" + url
	if ap.qrCacheKey == key && ap.qrSiteImg.Size().X != 0 {
		return ap.qrSiteImg
	}
	ap.qrCacheKey = key
	ap.qrSiteImg = encodeQR(url)
	return ap.qrSiteImg
}

// cachedPairQR is the CA-pairing twin of cachedSiteQR.
func (ap *AccessPanel) cachedPairQR(url string) paint.ImageOp {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	if ap.qrPairImg.Size().X != 0 && ap.pairingURL == url {
		return ap.qrPairImg
	}
	ap.qrPairImg = encodeQR(url)
	return ap.qrPairImg
}

// encodeQR builds an ImageOp for url. On encode failure (input too long
// for the chosen recovery level — should not happen for our short
// hostnames) returns a 1×1 white pixel so callers don't panic on size.
func encodeQR(url string) paint.ImageOp {
	if url == "" {
		return paint.NewImageOp(blankPixel())
	}
	q, err := qrcode.New(url, qrcode.Medium)
	if err != nil {
		return paint.NewImageOp(blankPixel())
	}
	q.DisableBorder = false
	return paint.NewImageOp(q.Image(256))
}

func blankPixel() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.White)
	return img
}

// qrCodeBox renders a labelled QR code: the image, then a caption
// underneath, padded by a card-like inset. The image is sized to a
// fixed Dp box so it scales identically across DPI scales.
func (ap *AccessPanel) qrCodeBox(gtx layout.Context, th *Theme, caption, url string, op paint.ImageOp) layout.Dimensions {
	const qrDp = 220
	return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				size := gtx.Dp(unit.Dp(qrDp))
				gtx.Constraints.Min = image.Pt(size, size)
				gtx.Constraints.Max = image.Pt(size, size)
				return widget.Image{
					Src:   op,
					Fit:   widget.Contain,
					Scale: float32(size) / float32(op.Size().X) / gtx.Metric.PxPerDp,
				}.Layout(gtx)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Top: th.Spacing.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body2(th.Theme, caption)
					lbl.Color = th.Color.Fg3
					lbl.TextSize = th.Sizes.XS
					return lbl.Layout(gtx)
				})
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body2(th.Theme, url)
				lbl.Color = th.Color.Fg3
				lbl.TextSize = th.Sizes.XS
				lbl.Font = MonoFont
				lbl.MaxLines = 1
				lbl.Truncator = "…"
				return lbl.Layout(gtx)
			})
		}),
	)
}

// copyableRow is the same Label/value/Copy row shape as the profiling
// panel uses; replicated here so AccessPanel doesn't reach across files
// for one trivial helper.
func (ap *AccessPanel) copyableRow(gtx layout.Context, th *Theme, label, value string, sel *widget.Selectable, copyBtn *widget.Clickable) layout.Dimensions {
	return layout.Inset{Bottom: th.Spacing.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(th.Dims.LabelColWidth)
				lbl := material.Body2(th.Theme, label)
				lbl.Color = th.Color.TextSecondary
				lbl.TextSize = th.Sizes.Base
				return lbl.Layout(gtx)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return SelectableLabel(gtx, th, sel, value, th.Sizes.Base, th.Color.Fg, MonoFont)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return SmallButton(gtx, th, copyBtn, "Copy")
				})
			}),
		)
	})
}
