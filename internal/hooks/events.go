package hooks

import "sort"

// Event identifies a lifecycle moment at which hooks fire. Events are paired
// (pre-X / post-X) but each is independent; pre-X failure does not prevent
// post-X from running on a different invocation.
type Event string

// Active events — fire today.
const (
	PreStart  Event = "pre-start"
	PostStart Event = "post-start"

	PreStop  Event = "pre-stop"
	PostStop Event = "post-stop"

	PreDelete  Event = "pre-delete"
	PostDelete Event = "post-delete"

	PreClone  Event = "pre-clone"
	PostClone Event = "post-clone"

	PreVersionsChange  Event = "pre-versions-change"
	PostVersionsChange Event = "post-versions-change"

	PreMultisite  Event = "pre-multisite"
	PostMultisite Event = "post-multisite"

	PreExport  Event = "pre-export"
	PostExport Event = "post-export"

	PreImportDB  Event = "pre-import-db"
	PostImportDB Event = "post-import-db"

	PreSnapshot  Event = "pre-snapshot"
	PostSnapshot Event = "post-snapshot"
)

// Reserved events — declared but not yet fired by any lifecycle method.
// Adding the firing site is a one-line runner.Run(...) addition once the
// underlying feature lands.
const (
	PreImportFiles  Event = "pre-import-files"
	PostImportFiles Event = "post-import-files"

	PreRestoreSnapshot  Event = "pre-restore-snapshot"
	PostRestoreSnapshot Event = "post-restore-snapshot"

	PreImportSite  Event = "pre-import-site"
	PostImportSite Event = "post-import-site"
)

// allEvents is the canonical registry. Build the lookup map once at package
// init so Valid() is O(1).
var allEvents = []Event{
	PreStart, PostStart,
	PreStop, PostStop,
	PreDelete, PostDelete,
	PreClone, PostClone,
	PreVersionsChange, PostVersionsChange,
	PreMultisite, PostMultisite,
	PreExport, PostExport,

	PreImportDB, PostImportDB,
	PreImportFiles, PostImportFiles,
	PreSnapshot, PostSnapshot,
	PreRestoreSnapshot, PostRestoreSnapshot,
	PreImportSite, PostImportSite,
}

// activeEvents lists the events that the SiteManager fires today. Used by
// the GUI to enumerate the visible event sections — reserved events do not
// appear in the editor until their lifecycle method exists.
var activeEvents = []Event{
	PreStart, PostStart,
	PreStop, PostStop,
	PreDelete, PostDelete,
	PreClone, PostClone,
	PreVersionsChange, PostVersionsChange,
	PreMultisite, PostMultisite,
	PreExport, PostExport,
	PreImportDB, PostImportDB,
	PreSnapshot, PostSnapshot,
}

var eventSet = func() map[Event]struct{} {
	m := make(map[Event]struct{}, len(allEvents))
	for _, e := range allEvents {
		m[e] = struct{}{}
	}
	return m
}()

// AllEvents returns every recognised event, including reserved ones.
// The slice is a copy so callers may sort/filter freely.
func AllEvents() []Event {
	out := make([]Event, len(allEvents))
	copy(out, allEvents)
	return out
}

// ActiveEvents returns the events currently fired by the SiteManager.
func ActiveEvents() []Event {
	out := make([]Event, len(activeEvents))
	copy(out, activeEvents)
	return out
}

// SortedActiveEvents is ActiveEvents() sorted lexically — convenient when
// the UI wants a stable display order.
func SortedActiveEvents() []Event {
	out := ActiveEvents()
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Valid reports whether e is a known event.
func (e Event) Valid() bool {
	_, ok := eventSet[e]
	return ok
}

// AllowsContainerTasks reports whether the event fires while the site's
// containers are running (and so exec / wp-cli tasks are sensible).
//
// The "pre-X" events for lifecycle methods that bring containers up are
// rejected: the containers don't exist yet. Similarly, post-stop and
// pre/post-delete fire when containers are down or being torn down.
func (e Event) AllowsContainerTasks() bool {
	switch e {
	case PreStart,
		// post-stop fires after containers are stopped — exec into a
		// stopped container fails. Force exec-host here.
		PostStop,
		PreDelete,
		PostDelete,
		// pre-snapshot / pre-restore-snapshot run while the site is
		// stopped (snapshots require quiesced state).
		PreSnapshot,
		PostSnapshot,
		PreRestoreSnapshot,
		PostRestoreSnapshot,
		PreImportSite,
		PostImportSite:
		return false
	}
	return true
}
