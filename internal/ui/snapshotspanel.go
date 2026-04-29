package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/types"
)

// SnapshotsPanel lists snapshots for a site, lets the user create a new
// one, and offers per-row Restore + Delete actions. Lives in the site
// detail panel as a tab next to Logs / WP-CLI / Hooks.
type SnapshotsPanel struct {
	state  *UIState
	sm     *sites.SiteManager
	toasts *Notifications

	createBtn   widget.Clickable
	refreshBtn  widget.Clickable
	labelEditor widget.Editor

	rows []snapshotRow

	// Cached snapshot list for the current site. Refreshed on first
	// render and after each create/delete/restore.
	loadedFor string
	loaded    []sites.SnapshotInfo
	loading   bool
}

type snapshotRow struct {
	restore widget.Clickable
	del     widget.Clickable
}

func NewSnapshotsPanel(state *UIState, sm *sites.SiteManager, toasts *Notifications) *SnapshotsPanel {
	p := &SnapshotsPanel{state: state, sm: sm, toasts: toasts}
	p.labelEditor.SingleLine = true
	p.labelEditor.SetText("manual")
	return p
}

// HandleUserInteractions processes button clicks. Called by the parent
// before Layout when this panel is visible.
func (p *SnapshotsPanel) HandleUserInteractions(gtx layout.Context, site *types.Site) {
	if site == nil {
		return
	}
	if p.loadedFor != site.ID && !p.loading {
		p.refresh(site)
	}

	if p.refreshBtn.Clicked(gtx) {
		p.refresh(site)
	}

	if p.createBtn.Clicked(gtx) {
		label := strings.TrimSpace(p.labelEditor.Text())
		if label == "" {
			label = "manual"
		}
		siteCopy := *site
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			path, err := p.sm.Snapshot(ctx, siteCopy.ID, label)
			if err != nil {
				p.state.ShowError("Snapshot failed: " + err.Error())
				return
			}
			p.toasts.ShowSuccess("Snapshot saved: " + shortPath(path))
			p.refresh(&siteCopy)
		}()
	}

	// Per-row actions.
	for i := range p.rows {
		if i >= len(p.loaded) {
			break
		}
		snap := p.loaded[i]
		if p.rows[i].restore.Clicked(gtx) {
			siteID := site.ID
			path := snap.HostPath
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				defer cancel()
				if err := p.sm.RestoreSnapshot(ctx, siteID, path, sites.RestoreSnapshotOptions{}); err != nil {
					p.state.ShowError("Restore failed: " + err.Error())
					return
				}
				p.toasts.ShowSuccess("Restored from " + shortPath(path))
			}()
		}
		if p.rows[i].del.Clicked(gtx) {
			siteCopy := *site
			path := snap.HostPath
			go func() {
				if err := p.sm.DeleteSnapshot(path); err != nil {
					p.state.ShowError("Delete snapshot failed: " + err.Error())
					return
				}
				p.toasts.ShowSuccess("Snapshot deleted")
				p.refresh(&siteCopy)
			}()
		}
	}
}

// refresh re-reads the snapshot list for site. ListSnapshots filters by
// slug (because slug is what's encoded in the filename); the cache key
// is the site ID so we can detect "different site selected" cheaply
// without comparing every field.
func (p *SnapshotsPanel) refresh(site *types.Site) {
	p.loading = true
	go func() {
		list, err := p.sm.ListSnapshots(site.Slug)
		if err != nil {
			p.state.ShowError("Listing snapshots failed: " + err.Error())
		}
		p.state.mu.Lock()
		p.loaded = list
		p.loadedFor = site.ID
		p.loading = false
		// Keep rows aligned with the loaded list.
		p.rows = make([]snapshotRow, len(list))
		p.state.mu.Unlock()
		p.state.Invalidate()
	}()
}

func (p *SnapshotsPanel) Layout(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	if site == nil {
		return layout.Dimensions{}
	}

	return Section(gtx, th, "Snapshots", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return p.layoutControls(gtx, th, site)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return p.layoutList(gtx, th)
				})
			}),
		)
	})
}

func (p *SnapshotsPanel) layoutControls(gtx layout.Context, th *Theme, site *types.Site) layout.Dimensions {
	canCreate := site.Started
	createLabel := "Create snapshot"
	if !canCreate {
		createLabel = "Start site to snapshot"
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return LabeledInput(gtx, th, "Label", &p.labelEditor, "manual")
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return th.PrimaryGated(gtx, &p.createBtn, createLabel, canCreate)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return SecondaryButton(gtx, th, &p.refreshBtn, "Refresh")
			})
		}),
	)
}

func (p *SnapshotsPanel) layoutList(gtx layout.Context, th *Theme) layout.Dimensions {
	if p.loading {
		lbl := material.Body2(th.Theme, "Loading snapshots…")
		lbl.Color = th.Color.TextSecondary
		return lbl.Layout(gtx)
	}
	if len(p.loaded) == 0 {
		lbl := material.Body2(th.Theme, "No snapshots yet. Click Create to make one.")
		lbl.Color = th.Color.TextSecondary
		return lbl.Layout(gtx)
	}

	children := make([]layout.FlexChild, 0, len(p.loaded))
	for i, snap := range p.loaded {
		idx := i
		s := snap
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.layoutRow(gtx, th, s, idx)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func (p *SnapshotsPanel) layoutRow(gtx layout.Context, th *Theme, snap sites.SnapshotInfo, idx int) layout.Dimensions {
	titleText := fmt.Sprintf("%s · %s · %s/%s",
		snap.Label,
		snap.CreatedAt.Local().Format("2006-01-02 15:04"),
		snap.Engine, snap.Version,
	)
	subText := fmt.Sprintf("%s · %s · %s",
		humanBytes(snap.SizeBytes),
		snap.Compression,
		checksumLabel(snap.HasChecksum),
	)

	return RoundedFill(gtx, th.Color.Bg1, th.Radii.R2, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(th.Spacing.SM).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							lbl := material.Body1(th.Theme, titleText)
							lbl.Color = th.Color.TextStrong
							return lbl.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							lbl := material.Body2(th.Theme, subText)
							lbl.Color = th.Color.TextSecondary
							return lbl.Layout(gtx)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return SmallButton(gtx, th, &p.rows[idx].restore, "Restore")
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return SmallButton(gtx, th, &p.rows[idx].del, "Delete")
					})
				}),
			)
		})
	})
}

// humanBytes formats a byte count as a human-readable string (KiB / MiB).
func humanBytes(n int64) string {
	const k = 1024
	switch {
	case n < k:
		return fmt.Sprintf("%dB", n)
	case n < k*k:
		return fmt.Sprintf("%.1fKiB", float64(n)/k)
	case n < k*k*k:
		return fmt.Sprintf("%.1fMiB", float64(n)/(k*k))
	default:
		return fmt.Sprintf("%.2fGiB", float64(n)/(k*k*k))
	}
}

func checksumLabel(has bool) string {
	if has {
		return "checksum verified"
	}
	return "no checksum"
}

func shortPath(p string) string {
	idx := strings.LastIndex(p, "/")
	if idx < 0 {
		return p
	}
	return p[idx+1:]
}
