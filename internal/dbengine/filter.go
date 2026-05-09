package dbengine

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// MaxLineBytes is the largest line FilterImportStream will buffer during
// preprocessing. 16 MiB comfortably handles serialised option_value rows
// (which can include base64-encoded blobs); bigger than that is more
// likely a pathological binary blob accidentally dumped as text — fail
// loudly rather than silently truncating.
const MaxLineBytes = 16 * 1024 * 1024

// FilterImportStream applies filters to in (line-oriented) and writes the
// result to out. Returns bytes written and any I/O / oversized-line error.
//
// The filter chain is line-oriented because mysqldump output is
// line-oriented (one statement per line in modern versions; older versions
// wrap long INSERTs but the line is still a complete logical unit). Line
// orientation lets us scan in O(n) memory regardless of file size, with a
// hard cap at MaxLineBytes for genuinely pathological input.
func FilterImportStream(filters []ImportFilter, in io.Reader, out io.Writer) (int64, error) {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), MaxLineBytes)

	bw := bufio.NewWriterSize(out, 256*1024)
	var written int64

	for scanner.Scan() {
		line := scanner.Bytes()
		dropped := false
		for _, f := range filters {
			if !f.Pat.Match(line) {
				continue
			}
			rewritten := f.Fn(line)
			if rewritten == nil {
				dropped = true
				break
			}
			line = rewritten
		}
		if dropped {
			continue
		}
		if _, err := bw.Write(line); err != nil {
			return written, fmt.Errorf("write: %w", err)
		}
		if err := bw.WriteByte('\n'); err != nil {
			return written, fmt.Errorf("write: %w", err)
		}
		written += int64(len(line)) + 1
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return written, fmt.Errorf("import dump contains a line longer than %d bytes — refusing to process; the dump is likely binary or corrupted", MaxLineBytes)
		}
		return written, fmt.Errorf("scan: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return written, fmt.Errorf("flush: %w", err)
	}
	return written, nil
}

// FilterNames returns the chain in display order, for surfacing in the
// import wizard's "what's about to happen" view.
func FilterNames(filters []ImportFilter) []string {
	out := make([]string, len(filters))
	for i, f := range filters {
		out[i] = f.Name
	}
	return out
}
