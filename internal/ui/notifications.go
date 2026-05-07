package ui

import (
	"image"
	"image/color"
	"strconv"
	"sync"
	"time"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// NotifyAction is an optional button rendered next to an error/info banner.
// ID is a stable identifier used by tests and slog. Label is the button text.
// Run is invoked from HandleUserInteractions (the UI render goroutine) — it
// MUST spawn its own goroutine for any blocking work, and any UIState
// mutation must go through the existing UIState helpers (which lock and
// invalidate internally). Mirrors the convention every other click handler
// uses, and the field shape of health.Action so a future merge is mechanical.
type NotifyAction struct {
	ID    string
	Label string
	Run   func()
}

// HasRun reports whether the action carries a non-nil runner. The zero
// value of NotifyAction is "no action" — callers pass it to ShowError
// equivalents to mean "render plain banner."
func (a NotifyAction) HasRun() bool { return a.Run != nil }

// NotificationType determines the visual style of a notification.
type NotificationType int

const (
	NotificationError NotificationType = iota
	NotificationSuccess
	NotificationInfo
)

// Notification is a single message with a type, creation time, and TTL after
// which the floating banner auto-docks into the history panel.
type Notification struct {
	ID        int64
	Type      NotificationType
	Message   string
	CreatedAt time.Time
	Duration  time.Duration

	dismiss widget.Clickable
}

// expired returns true once the floating duration has elapsed; the entry
// then docks into the archive.
func (n *Notification) expired() bool {
	return time.Since(n.CreatedAt) > n.Duration
}

// Notifications is a notification center that floats new entries
// transiently in the bottom-right corner and docks expired entries into a
// history panel toggled by a bell icon. Archive entries persist until the
// user dismisses them individually or clears all.
type Notifications struct {
	mu        sync.Mutex
	state     *UIState
	active    []*Notification
	archive   []*Notification
	nextID    int64
	showPanel bool

	bellClick     widget.Clickable
	clearAllClick widget.Clickable
}

func NewNotifications(state *UIState) *Notifications {
	return &Notifications{state: state}
}

// Add inserts a new notification of the given type and starts its floating timer.
func (n *Notifications) Add(msg string, t NotificationType) {
	d := 5 * time.Second
	if t == NotificationError {
		d = 8 * time.Second
	}

	n.mu.Lock()
	n.nextID++
	entry := &Notification{
		ID:        n.nextID,
		Type:      t,
		Message:   msg,
		CreatedAt: time.Now(),
		Duration:  d,
	}
	n.active = append(n.active, entry)
	n.mu.Unlock()
	n.state.Invalidate()

	go func(at time.Duration) {
		time.Sleep(at)
		n.state.Invalidate()
	}(d)
}

// Error / Success / Info are sugar over Add.
func (n *Notifications) Error(msg string)   { n.Add(msg, NotificationError) }
func (n *Notifications) Success(msg string) { n.Add(msg, NotificationSuccess) }
func (n *Notifications) Info(msg string)    { n.Add(msg, NotificationInfo) }

// ShowError / ShowSuccess preserve the previous toast API.
func (n *Notifications) ShowError(msg string)   { n.Error(msg) }
func (n *Notifications) ShowSuccess(msg string) { n.Success(msg) }
func (n *Notifications) ShowInfo(msg string)    { n.Info(msg) }

// promoteExpired moves any active notification past its TTL into the archive.
// Caller must hold n.mu.
func (n *Notifications) promoteExpired() {
	kept := n.active[:0]
	for _, entry := range n.active {
		if entry.expired() {
			n.archive = append([]*Notification{entry}, n.archive...)
		} else {
			kept = append(kept, entry)
		}
	}
	n.active = kept
}

// HandleUserInteractions processes the bell-toggle and archive-dismiss clicks.
// Call from the root UI before Layout each frame.
func (n *Notifications) HandleUserInteractions(gtx layout.Context) {
	if n.bellClick.Clicked(gtx) {
		n.mu.Lock()
		n.showPanel = !n.showPanel
		n.mu.Unlock()
	}
	if n.clearAllClick.Clicked(gtx) {
		n.mu.Lock()
		n.archive = nil
		n.mu.Unlock()
	}
	n.mu.Lock()
	for i := 0; i < len(n.archive); i++ {
		if n.archive[i].dismiss.Clicked(gtx) {
			n.archive = append(n.archive[:i], n.archive[i+1:]...)
			i--
		}
	}
	n.mu.Unlock()
}

// Layout draws floating notifications in the bottom-right and, when the bell
// is open, the history panel above them.
func (n *Notifications) Layout(gtx layout.Context, th *Theme) layout.Dimensions {
	n.mu.Lock()
	n.promoteExpired()
	active := append([]*Notification(nil), n.active...)
	archive := append([]*Notification(nil), n.archive...)
	open := n.showPanel
	n.mu.Unlock()

	if len(active) == 0 && !open {
		return layout.Dimensions{}
	}

	return layout.SE.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Bottom: th.Spacing.LG, Right: th.Spacing.LG}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical, Alignment: layout.End}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !open {
						return layout.Dimensions{}
					}
					return n.layoutHistoryPanel(gtx, th, archive)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if len(active) == 0 {
						return layout.Dimensions{}
					}
					return n.layoutFloating(gtx, th, active)
				}),
			)
		})
	})
}

// LayoutBell draws the bell icon with an unread-count badge. Call from the
// sidebar so the bell sits beside the search field or app title.
func (n *Notifications) LayoutBell(gtx layout.Context, th *Theme) layout.Dimensions {
	n.mu.Lock()
	count := len(n.archive) + len(n.active)
	n.mu.Unlock()

	return material.Clickable(gtx, &n.bellClick, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top: th.Spacing.XS, Bottom: th.Spacing.XS,
			Left: th.Spacing.SM, Right: th.Spacing.SM,
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return drawBell(gtx, th, unit.Dp(20))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if count == 0 {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: th.Spacing.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return badge(gtx, th, strconv.Itoa(count))
					})
				}),
			)
		})
	})
}

func (n *Notifications) layoutFloating(gtx layout.Context, th *Theme, items []*Notification) layout.Dimensions {
	children := make([]layout.FlexChild, len(items))
	for i, item := range items {
		item := item
		children[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layoutNotificationCard(gtx, th, item, nil)
			})
		})
	}
	return layout.Flex{Axis: layout.Vertical, Alignment: layout.End}.Layout(gtx, children...)
}

func (n *Notifications) layoutHistoryPanel(gtx layout.Context, th *Theme, items []*Notification) layout.Dimensions {
	gtx.Constraints.Max.X = gtx.Dp(unit.Dp(380))
	gtx.Constraints.Min.X = gtx.Dp(unit.Dp(280))

	return FillBackground(gtx, th.Color.SurfaceElevated, func(gtx layout.Context) layout.Dimensions {
		rr := gtx.Dp(th.Radii.MD)
		defer clip.RRect{
			Rect: image.Rectangle{Max: gtx.Constraints.Min},
			NE:   rr, NW: rr, SE: rr, SW: rr,
		}.Push(gtx.Ops).Pop()

		return layout.UniformInset(th.Spacing.MD).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							lbl := material.H6(th.Theme, "History")
							lbl.Color = th.Color.TextPrimary
							return lbl.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if len(items) == 0 {
								return layout.Dimensions{}
							}
							return th.Small(gtx, &n.clearAllClick, "Clear all")
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Spacer{Height: th.Spacing.SM}.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if len(items) == 0 {
						lbl := material.Body2(th.Theme, "No notifications")
						lbl.Color = th.Color.TextMuted
						return lbl.Layout(gtx)
					}
					children := make([]layout.FlexChild, len(items))
					for i, item := range items {
						item := item
						children[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Bottom: th.Spacing.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return layoutNotificationCard(gtx, th, item, &item.dismiss)
							})
						})
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
				}),
			)
		})
	})
}

func layoutNotificationCard(gtx layout.Context, th *Theme, item *Notification, dismissBtn *widget.Clickable) layout.Dimensions {
	bg, fg := notificationColors(th, item.Type)

	gtx.Constraints.Max.X = gtx.Dp(unit.Dp(360))
	gtx.Constraints.Min.X = gtx.Dp(unit.Dp(240))

	return FillBackground(gtx, bg, func(gtx layout.Context) layout.Dimensions {
		rr := gtx.Dp(th.Radii.MD)
		defer clip.RRect{
			Rect: image.Rectangle{Max: gtx.Constraints.Min},
			NE:   rr, NW: rr, SE: rr, SW: rr,
		}.Push(gtx.Ops).Pop()

		return layout.Inset{
			Top: th.Spacing.SM, Bottom: th.Spacing.SM,
			Left: th.Spacing.MD, Right: th.Spacing.MD,
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body2(th.Theme, item.Message)
					lbl.Color = fg
					lbl.TextSize = th.Sizes.Base
					return lbl.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if dismissBtn == nil {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: th.Spacing.SM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return th.Small(gtx, dismissBtn, "✕")
					})
				}),
			)
		})
	})
}

// drawBell renders a small vector bell icon coloured with the theme's text
// primary colour. Used in place of an emoji glyph to avoid a system-font
// fallback (which on Linux fires GLib-GIO-CRITICAL warnings every frame).
func drawBell(gtx layout.Context, th *Theme, size unit.Dp) layout.Dimensions {
	s := float32(gtx.Dp(size))
	px := int(s)
	col := th.Color.TextPrimary

	// Body: rounded dome (top arc + flared base). Approximated as a circle for
	// the head plus a trapezoidal base, drawn as a stroked outline.
	headR := s * 0.32
	cx := s / 2
	cy := s * 0.46

	// Head (filled circle)
	defer clip.Ellipse{
		Min: image.Pt(int(cx-headR), int(cy-headR)),
		Max: image.Pt(int(cx+headR), int(cy+headR)),
	}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, col)

	// Base bar
	barW := s * 0.55
	barH := s * 0.10
	barTop := cy + headR*0.55
	barStack := clip.Rect{
		Min: image.Pt(int(cx-barW/2), int(barTop)),
		Max: image.Pt(int(cx+barW/2), int(barTop+barH)),
	}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, col)
	barStack.Pop()

	// Clapper (small circle below the base)
	clapperR := s * 0.08
	clapperCy := barTop + barH + clapperR
	clapperStack := clip.Ellipse{
		Min: image.Pt(int(cx-clapperR), int(clapperCy-clapperR)),
		Max: image.Pt(int(cx+clapperR), int(clapperCy+clapperR)),
	}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, col)
	clapperStack.Pop()

	return layout.Dimensions{Size: image.Pt(px, px)}
}

func notificationColors(th *Theme, t NotificationType) (color.NRGBA, color.NRGBA) {
	switch t {
	case NotificationError:
		return th.Color.DangerBg, th.Color.DangerFg
	case NotificationSuccess:
		return th.Color.SuccessBg, th.Color.SuccessFg
	default:
		return th.Color.InfoBg, th.Color.InfoFg
	}
}

func badge(gtx layout.Context, th *Theme, text string) layout.Dimensions {
	return FillBackground(gtx, th.Color.Primary, func(gtx layout.Context) layout.Dimensions {
		dims := layout.Inset{
			Top: unit.Dp(2), Bottom: unit.Dp(2),
			Left: unit.Dp(6), Right: unit.Dp(6),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(th.Theme, text)
			lbl.Color = th.Color.OnPrimary
			lbl.TextSize = th.Sizes.XS
			return lbl.Layout(gtx)
		})
		rr := gtx.Dp(th.Radii.SM)
		defer clip.RRect{
			Rect: image.Rectangle{Max: dims.Size},
			NE:   rr, NW: rr, SE: rr, SW: rr,
		}.Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, th.Color.Primary)
		return dims
	})
}
