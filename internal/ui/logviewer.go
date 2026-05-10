package ui

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/PeterBooker/locorum/internal/applog"
	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/sites"
)

// logViewerRingSize caps the number of lines held in memory per
// service stream. Older lines are dropped when the ring fills — the
// on-disk container log retains everything.
const logViewerRingSize = 5000

// errorLineRegex marks lines worth highlighting in red. Cheap; defer
// full syntax-highlighting indefinitely (the §7.7 plan calls this out).
var errorLineRegex = regexp.MustCompile(`(?i)\b(error|fatal|panic|exception|fail(?:ed)?)\b`)

// LogViewer is the Logs tab. Replaces the prior batched-Refresh viewer
// with a streaming follow that:
//
//   - holds the last logViewerRingSize lines in memory
//   - re-attaches automatically on container restart (driven by
//     SiteManager.StreamLogs)
//   - lets the user pause / resume the visible feed without losing the
//     in-flight stream (incoming lines still fill the ring while paused;
//     resume snaps to the latest)
//   - highlights any line matching errorLineRegex in red
//   - exports the ring to <datadir>/logs/<slug>-<service>-<ts>.log
//
// The streaming context is owned by the viewer; switching service or
// switching site cancels the current stream and starts a new one.
type LogViewer struct {
	state *UIState
	sm    *sites.SiteManager

	serviceDropdown *Dropdown
	pauseBtn        widget.Clickable
	resumeBtn       widget.Clickable
	clearBtn        widget.Clickable
	exportBtn       widget.Clickable
	highlightBox    widget.Bool
	output          *OutputView

	mu        sync.Mutex
	siteID    string
	siteSlug  string
	service   string
	cancel    context.CancelFunc
	ring      []logLineCached
	ringHead  int
	ringFill  int
	paused    bool
	highlight bool
	frozen    string // last-rendered visible buffer; updated when not paused
}

type logLineCached struct {
	t       time.Time
	stderr  bool
	text    string
	isError bool
}

func NewLogViewer(state *UIState, sm *sites.SiteManager) *LogViewer {
	lv := &LogViewer{
		state:           state,
		sm:              sm,
		serviceDropdown: NewDropdown([]string{"web", "php", "database", "redis"}),
		output:          NewOutputView(),
		ring:            make([]logLineCached, logViewerRingSize),
		highlight:       true,
	}
	lv.highlightBox.Value = true
	return lv
}

// Layout draws the controls + scrollback. Called every frame; the
// streamed lines arrive on a background goroutine and are read here under
// the mutex.
func (lv *LogViewer) Layout(gtx layout.Context, th *Theme, siteID string) layout.Dimensions {
	body := lv.snapshot()

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.H6(th.Theme, "Container Logs")
			return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, lbl.Layout)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Max.X = gtx.Dp(unit.Dp(160))
						return lv.serviceDropdown.Layout(gtx, th, "Service")
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							if lv.paused {
								return SecondaryButton(gtx, th, &lv.resumeBtn, "Resume")
							}
							return SecondaryButton(gtx, th, &lv.pauseBtn, "Pause")
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return SecondaryButton(gtx, th, &lv.clearBtn, "Clear")
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return SecondaryButton(gtx, th, &lv.exportBtn, "Export")
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: th.Spacing.MD}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							cb := material.CheckBox(th.Theme, &lv.highlightBox, "Highlight errors")
							cb.Color = th.Color.Fg
							cb.IconColor = th.Color.Accent
							cb.Size = unit.Dp(18)
							cb.TextSize = th.Sizes.SM
							cb.Font.Weight = font.Normal
							return cb.Layout(gtx)
						})
					}),
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(20)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				placeholder := "Streaming logs… (start the site if nothing appears)"
				return lv.output.Layout(gtx, th, body, placeholder, th.Dims.OutputAreaMax)
			})
		}),
	)
}

// HandleUserInteractions reacts to control clicks AND ensures the
// streaming goroutine is bound to the current site / service. Idempotent
// — when the user has not switched anything, no work happens.
func (lv *LogViewer) HandleUserInteractions(gtx layout.Context, siteID string) {
	service := lv.serviceDropdown.Options[lv.serviceDropdown.Selected]

	lv.mu.Lock()
	needsRestart := lv.siteID != siteID || lv.service != service
	lv.mu.Unlock()
	if needsRestart {
		lv.startStream(siteID, service)
	}

	if lv.pauseBtn.Clicked(gtx) {
		lv.mu.Lock()
		lv.paused = true
		lv.frozen = lv.renderLocked()
		lv.mu.Unlock()
	}
	if lv.resumeBtn.Clicked(gtx) {
		lv.mu.Lock()
		lv.paused = false
		lv.frozen = ""
		lv.mu.Unlock()
		lv.state.Invalidate()
	}
	if lv.clearBtn.Clicked(gtx) {
		lv.mu.Lock()
		lv.ringHead = 0
		lv.ringFill = 0
		lv.frozen = ""
		lv.mu.Unlock()
		lv.state.Invalidate()
	}
	if lv.highlightBox.Update(gtx) {
		lv.mu.Lock()
		lv.highlight = lv.highlightBox.Value
		if lv.paused {
			lv.frozen = lv.renderLocked()
		}
		lv.mu.Unlock()
		lv.state.Invalidate()
	}
	if lv.exportBtn.Clicked(gtx) {
		lv.exportToFile()
	}
}

// startStream cancels any in-flight stream, resets the ring, and kicks
// off a new SiteManager.StreamLogs goroutine.
func (lv *LogViewer) startStream(siteID, service string) {
	lv.mu.Lock()
	if lv.cancel != nil {
		lv.cancel()
		lv.cancel = nil
	}
	lv.siteID = siteID
	lv.service = service
	lv.ringHead = 0
	lv.ringFill = 0
	lv.frozen = ""
	site, _ := lv.sm.GetSite(siteID)
	if site != nil {
		lv.siteSlug = site.Slug
	}
	lv.mu.Unlock()

	if siteID == "" {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	lv.mu.Lock()
	lv.cancel = cancel
	lv.mu.Unlock()

	go func() {
		ch, err := lv.sm.StreamLogs(ctx, siteID, service)
		if err != nil {
			lv.append(time.Now(), true, "[stream error: "+err.Error()+"]")
			return
		}
		for line := range ch {
			lv.append(line.Time, line.Stream == docker.LogStreamStderr, line.Text)
		}
	}()
}

func (lv *LogViewer) append(t time.Time, stderr bool, text string) {
	lv.mu.Lock()
	entry := logLineCached{
		t:       t,
		stderr:  stderr,
		text:    text,
		isError: errorLineRegex.MatchString(text),
	}
	lv.ring[lv.ringHead] = entry
	lv.ringHead = (lv.ringHead + 1) % len(lv.ring)
	if lv.ringFill < len(lv.ring) {
		lv.ringFill++
	}
	lv.mu.Unlock()
	lv.state.Invalidate()
}

// snapshot returns the visible buffer body. When paused, returns the
// frozen body captured at pause time so the user can scroll a stable
// view; otherwise re-renders from the live ring.
func (lv *LogViewer) snapshot() string {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if lv.paused && lv.frozen != "" {
		return lv.frozen
	}
	return lv.renderLocked()
}

// renderLocked formats the ring into a single string. Caller must hold mu.
//
// Highlighting is *prefix-based* — we prepend "!! " to error lines when
// the toggle is on. Gio's OutputView is a plain text widget; per-rune
// styling would require a richer renderer. The marker is ugly but
// honest: a future LayoutLogPanel could parse it back.
func (lv *LogViewer) renderLocked() string {
	if lv.ringFill == 0 {
		return ""
	}
	var b strings.Builder
	start := lv.ringHead - lv.ringFill
	if start < 0 {
		start += len(lv.ring)
	}
	for i := 0; i < lv.ringFill; i++ {
		entry := lv.ring[(start+i)%len(lv.ring)]
		if lv.highlight && entry.isError {
			b.WriteString("!! ")
		}
		b.WriteString(entry.text)
		if i < lv.ringFill-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// exportToFile writes the current ring to ~/.locorum/logs/<slug>-<svc>-<ts>.log.
func (lv *LogViewer) exportToFile() {
	lv.mu.Lock()
	body := lv.renderLocked()
	slug := lv.siteSlug
	service := lv.service
	lv.mu.Unlock()

	if body == "" {
		lv.state.ShowError("Nothing to export — log buffer is empty")
		return
	}
	dir := applog.LogDir()
	if dir == "" {
		// Fall back to the user's home dir + .locorum/logs to maintain
		// at least one canonical location even when applog.Init failed.
		dir = filepath.Join(os.TempDir(), "locorum-logs")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			lv.state.ShowError("Export failed: " + err.Error())
			return
		}
	}
	ts := time.Now().Format("20060102-150405")
	name := fmt.Sprintf("%s-%s-%s.log", slug, service, ts)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		lv.state.ShowError("Export failed: " + err.Error())
		return
	}
	lv.state.ShowError("Exported to " + path)
}

// Stop cancels any in-flight stream. Called when the SiteDetail is being
// torn down (e.g. a future "close site" affordance).
func (lv *LogViewer) Stop() {
	lv.mu.Lock()
	if lv.cancel != nil {
		lv.cancel()
		lv.cancel = nil
	}
	lv.mu.Unlock()
}

// LineRender is the colour helper used by callers that want to apply
// the ringed entries directly (kept for future per-line styling).
func LineRender(stderr, isError bool, th *Theme) color.NRGBA {
	switch {
	case isError:
		return th.Color.Err
	case stderr:
		return th.Color.Fg2
	default:
		return th.Color.Fg
	}
}
