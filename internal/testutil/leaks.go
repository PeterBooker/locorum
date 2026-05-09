package testutil

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// LeakCheckMain wraps testing.M.Run and reports goroutines still
// running after the suite finishes. Use as the package's TestMain:
//
//	func TestMain(m *testing.M) { testutil.LeakCheckMain(m) }
//
// extraIgnore is package-specific stack-frame substrings to add on top
// of DefaultIgnoredFrames (e.g. an HTTP server the suite intentionally
// leaves running for the whole run).
//
// Frames with no stack body — typically the runner's own snapshotting
// goroutine — are skipped to avoid self-reporting a leak.
func LeakCheckMain(m *testing.M, extraIgnore ...string) {
	code := m.Run()
	if code != 0 {
		os.Exit(code)
	}

	ignore := append(append([]string{}, DefaultIgnoredFrames...), extraIgnore...)
	deadline := time.Now().Add(2 * time.Second)
	var leaks []string
	for time.Now().Before(deadline) {
		runtime.GC()
		leaks = unexpectedGoroutines(ignore)
		if len(leaks) == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(leaks) > 0 {
		fmt.Fprintln(os.Stderr, "leakcheck: goroutine leak after suite:")
		for _, l := range leaks {
			fmt.Fprintln(os.Stderr, "  "+l)
		}
		os.Exit(1)
	}
}

// DefaultIgnoredFrames covers goroutines that legitimately outlive a
// test suite: SQLite/libc workers, runtime internals, Docker SDK
// long-polls, the test runner itself.
var DefaultIgnoredFrames = []string{
	"modernc.org/sqlite",
	"modernc.org/libc",
	"runtime.gopark",
	"runtime.goexit",
	"runtime.notetsleepg",
	"runtime.main",
	"runfinq",
	"signal.signal_recv",
	"created by os/signal.Notify",
	"github.com/docker/docker/client",
	"net/http.(*http2Server)",
	"net/http.(*persistConn)",
	"sql.(*DB).connectionOpener",
	"sql.(*DB).connectionResetter",
	"testing.(*M).Run",
	"testing.(*T).Run",
	"testing.tRunner",
	"main.main",
	"main.init",
	// runtime.Stack snapshots itself; ignore self-frames.
	"runtime.Stack",
}

func unexpectedGoroutines(ignore []string) []string {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	stacks := string(buf[:n])
	frames := strings.Split(stacks, "\n\ngoroutine ")
	var unexpected []string
	for _, fr := range frames {
		// A frame with only the header carries no executing code; the
		// runner's snapshotting goroutine looks like that.
		if !strings.Contains(fr, "\n\t") {
			continue
		}
		matched := false
		for _, sub := range ignore {
			if strings.Contains(fr, sub) {
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		head := strings.TrimSpace(strings.SplitN(fr, "\n", 2)[0])
		if head != "" {
			unexpected = append(unexpected, head)
		}
	}
	return unexpected
}
