package applog

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"strings"
)

// tailLines reads the trailing n lines from path. The implementation is
// chunked from the back of the file so a 10 MiB log doesn't materialise
// in memory just to grab the last 200 lines.
//
// Returns at most n lines, oldest first. If the file is shorter than
// n lines, all available lines are returned.
func tailLines(path string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size == 0 {
		return nil, nil
	}

	const chunk = 64 * 1024
	var (
		buf   []byte
		lines []string
		off   = size
	)
	for off > 0 && len(lines) <= n {
		read := int64(chunk)
		if off < read {
			read = off
		}
		off -= read
		tmp := make([]byte, read)
		if _, err := f.ReadAt(tmp, off); err != nil && err != io.EOF {
			return nil, err
		}
		buf = append(tmp, buf...)
		lines = splitLines(buf)
		if off == 0 {
			break
		}
	}

	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// splitLines breaks buf on '\n'. A trailing newline does not produce an
// empty final entry; bufio.Scanner handles that distinction.
func splitLines(buf []byte) []string {
	scanner := bufio.NewScanner(bytes.NewReader(buf))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var out []string
	for scanner.Scan() {
		out = append(out, scanner.Text())
	}
	return out
}

// FormatTail returns a clipboard-ready string of the given lines: newline
// joined, with a single trailing newline so paste targets that strip the
// last line behave consistently.
func FormatTail(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
