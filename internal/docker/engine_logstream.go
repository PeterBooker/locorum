package docker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/containerd/errdefs"
	"github.com/docker/docker/pkg/stdcopy"
)

// LogStream identifies which container stream a LogLine came from. Knowing
// stdout vs stderr lets the UI colour stderr lines red without applying a
// fragile regex to the text.
type LogStream uint8

const (
	LogStreamStdout LogStream = iota
	LogStreamStderr
)

// LogLine is one line of streamed output. Time is the container-emitted
// timestamp (truncated to millisecond resolution); Stream identifies the
// origin; Text is the line contents *without* trailing newline.
type LogLine struct {
	Time   time.Time
	Stream LogStream
	Text   string
}

// streamChannelBuf caps the in-flight LogLine queue. A consumer that
// can't keep up will see the oldest lines dropped silently — preferring
// freshness to completeness for an interactive viewer. The on-disk
// container log retains everything, so nothing is permanently lost.
const streamChannelBuf = 1024

// StreamContainerLogs follows the named container's combined stdout +
// stderr. The since argument selects the starting timestamp: pass the
// zero time.Time to stream from the live tail (with the last 100 lines
// of historical context); pass a real time to resume after a reconnect.
//
// The returned channel closes when ctx is cancelled, the container exits,
// or an unrecoverable read error occurs. Callers ranging over the channel
// MUST cancel the supplied context before returning to release the
// underlying SDK connection.
//
// The first error (if any) is reported through the optional second
// channel; nil means "channel closed cleanly." Returns ErrNotFound when
// the container does not exist at start; transient mid-stream read
// failures are logged via slog and result in the channel closing.
func (d *Docker) StreamContainerLogs(ctx context.Context, name string, since time.Time) (<-chan LogLine, error) {
	if d.cli == nil {
		return nil, fmt.Errorf("%w: docker client not initialised", ErrDaemonUnreachable)
	}

	info, err := d.cli.ContainerInspect(ctx, name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("%w: container %q", ErrNotFound, name)
		}
		return nil, fmt.Errorf("inspect %q: %w", name, err)
	}
	tty := info.Config != nil && info.Config.Tty

	opts := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Timestamps: true,
		Tail:       "100",
	}
	if !since.IsZero() {
		// Docker's API expects unix-seconds with optional nanos as a
		// string; UnixNano keeps the resolution we receive back.
		opts.Since = strconv.FormatInt(since.Unix(), 10)
		// Don't replay the historical tail when resuming — the caller
		// already has it.
		opts.Tail = "0"
	}

	rc, err := d.cli.ContainerLogs(ctx, name, opts)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("%w: container %q", ErrNotFound, name)
		}
		return nil, fmt.Errorf("stream logs %q: %w", name, err)
	}

	out := make(chan LogLine, streamChannelBuf)
	go func() {
		defer close(out)
		defer rc.Close()

		var stdout, stderr io.Writer
		if tty {
			// Tty containers don't multiplex; one stream is the source.
			pump(ctx, out, rc, LogStreamStdout)
			return
		}

		// stdcopy writes demuxed bytes into the supplied writers; we
		// wrap each in a line-based pump that pushes into the LogLine
		// channel.
		stdoutW, stdoutDone := newLinePump(ctx, out, LogStreamStdout)
		stderrW, stderrDone := newLinePump(ctx, out, LogStreamStderr)
		stdout, stderr = stdoutW, stderrW

		_, copyErr := stdcopy.StdCopy(stdout, stderr, rc)
		_ = stdoutW.Close()
		_ = stderrW.Close()
		<-stdoutDone
		<-stderrDone

		if copyErr != nil && !errors.Is(copyErr, context.Canceled) && !errors.Is(copyErr, io.EOF) {
			// Surface unexpected mid-stream errors via a synthetic
			// stderr LogLine so the user sees something rather than
			// silent disconnect.
			select {
			case out <- LogLine{Time: time.Now(), Stream: LogStreamStderr, Text: "[stream error: " + copyErr.Error() + "]"}:
			case <-ctx.Done():
			}
		}
	}()
	return out, nil
}

// pump reads r line-by-line and pushes each line into out. Honours ctx
// cancellation between reads. Strips a leading RFC3339Nano timestamp
// emitted by the Docker SDK when Timestamps=true.
func pump(ctx context.Context, out chan<- LogLine, r io.Reader, stream LogStream) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		t, text := splitTimestamp(scanner.Text())
		select {
		case out <- LogLine{Time: t, Stream: stream, Text: text}:
		case <-ctx.Done():
			return
		}
	}
}

// linePump writes bytes from the StdCopy demuxer; it splits on newlines
// and pushes each completed line into the target channel. Exposed via an
// io.WriteCloser so the demuxer can drive it directly.
type linePump struct {
	ctx    context.Context
	out    chan<- LogLine
	stream LogStream
	done   chan struct{}
	pipe   *io.PipeWriter
}

func newLinePump(ctx context.Context, out chan<- LogLine, stream LogStream) (*linePump, <-chan struct{}) {
	pr, pw := io.Pipe()
	done := make(chan struct{})
	lp := &linePump{ctx: ctx, out: out, stream: stream, done: done, pipe: pw}
	go func() {
		defer close(done)
		defer func() { _ = pr.Close() }()
		pump(ctx, out, pr, stream)
	}()
	return lp, done
}

func (l *linePump) Write(p []byte) (int, error) {
	return l.pipe.Write(p)
}

func (l *linePump) Close() error {
	return l.pipe.Close()
}

// splitTimestamp peels the leading RFC3339Nano timestamp the Docker SDK
// prepends when Timestamps=true. Format: "2006-01-02T15:04:05.000000000Z message".
// Falls back to time.Now() when parsing fails so the rest of the line is
// still surfaced.
func splitTimestamp(line string) (time.Time, string) {
	const minLen = 30 // "2006-01-02T15:04:05.000000000Z" plus space
	if len(line) < minLen {
		return time.Now(), line
	}
	idx := indexByte(line, ' ')
	if idx <= 0 {
		return time.Now(), line
	}
	t, err := time.Parse(time.RFC3339Nano, line[:idx])
	if err != nil {
		return time.Now(), line
	}
	return t, line[idx+1:]
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
