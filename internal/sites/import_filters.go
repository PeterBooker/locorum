package sites

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
)

// importFilter is a single line-rewriting rule applied to a streamed SQL
// dump during ImportDB. Each filter receives the raw line bytes (without
// trailing newline) and returns either:
//   - the bytes unchanged → written to the output verbatim;
//   - a different slice → written instead;
//   - nil → the line is dropped entirely.
//
// Filters MUST be deterministic and side-effect-free. They run in the
// order declared in importFilters and are tested individually against
// known-quirky exports from cPanel, WP Engine, Kinsta, and Pressable.
type importFilter struct {
	name string
	pat  *regexp.Regexp
	fn   func(line []byte) []byte
}

// matchByteSize is the largest line we will buffer during preprocessing.
// 16 MiB comfortably handles even the most pathological serialised
// option_value rows (which can include base64-encoded blobs); bigger than
// that is not a real WordPress option in practice and is more likely a
// pathological binary blob accidentally dumped as text — we fail loudly
// rather than silently truncating.
const importMaxLineBytes = 16 * 1024 * 1024

var importFilters = []importFilter{
	// 1. CREATE DATABASE — local sites already have a database; the import
	//    target is the site's `wordpress` DB. Re-creating it inside an
	//    import would either fail (no perms) or wipe a DB we don't own.
	{
		name: "drop CREATE DATABASE",
		pat:  regexp.MustCompile(`(?i)^\s*CREATE\s+DATABASE\b`),
		fn:   func(_ []byte) []byte { return nil },
	},

	// 2. USE — selects a database to import into. Locorum chooses the DB
	//    by virtue of who's running the wp-cli import (the wordpress
	//    user). USE statements either land us in a non-existent DB or
	//    silently jump us out of the right one.
	{
		name: "drop USE database",
		pat:  regexp.MustCompile(`(?i)^\s*USE\s+`),
		fn:   func(_ []byte) []byte { return nil },
	},

	// 3. MariaDB sandbox-mode header. MariaDB 10.6+ prefixes mysqldump
	//    output with a conditional comment that no MySQL server
	//    interprets, so stripping it is safe across both engines. The
	//    literal "enable the sandbox mode" is the unambiguous marker.
	{
		name: "drop MariaDB sandbox comment",
		pat:  regexp.MustCompile(`/\*!9{6}\\?- enable the sandbox mode \*/`),
		fn:   func(_ []byte) []byte { return nil },
	},

	// 4. MariaDB 11.x utf8mb4_uca1400_* collations are not recognised by
	//    MySQL 8.x or by older MariaDB. Rewriting to utf8mb4_unicode_ci
	//    keeps the table creatable; the cost is a slightly different
	//    sort order on multi-byte text, which is acceptable for a local
	//    dev import.
	{
		name: "rewrite uca1400 collations",
		pat:  regexp.MustCompile(`utf8mb4_uca1400_[A-Za-z0-9_]+`),
		fn: func(line []byte) []byte {
			return regexpReplace(line, importUCA1400Pat, []byte("utf8mb4_unicode_ci"))
		},
	},

	// 5. DEFINER=`user`@`host` — references a database user that doesn't
	//    exist locally, breaking CREATE TRIGGER/VIEW/FUNCTION/PROCEDURE.
	//    Stripping the clause makes the object owned by CURRENT_USER on
	//    re-creation, which is the desired local-dev behaviour.
	{
		name: "strip DEFINER clause",
		pat:  regexp.MustCompile("DEFINER=[^ ]+@[^ ]+ "),
		fn: func(line []byte) []byte {
			return regexpReplace(line, importDefinerPat, []byte(""))
		},
	},
}

// importUCA1400Pat / importDefinerPat: pre-compiled patterns referenced
// by filter functions (separated from the matching pat to keep filter
// declarations declarative and avoid re-allocation per call).
var (
	importUCA1400Pat = regexp.MustCompile(`utf8mb4_uca1400_[A-Za-z0-9_]+`)
	importDefinerPat = regexp.MustCompile("DEFINER=[^ ]+@[^ ]+ ")
)

// regexpReplace is a small helper that bridges regexp.Regexp.ReplaceAll
// (which works on []byte) without allocating a new pattern per call.
func regexpReplace(line []byte, pat *regexp.Regexp, repl []byte) []byte {
	return pat.ReplaceAll(line, repl)
}

// FilterImportStream streams `in` through the registered import filters
// and writes the cleaned SQL to `out`. Returns the number of bytes
// written and any I/O or oversized-line error.
//
// The filter is deliberately line-oriented:
//   - mysqldump output is line-oriented (one statement per line in modern
//     versions; older versions wrap long INSERTs but the line is still a
//     complete logical unit).
//   - Line orientation lets us scan in O(n) memory regardless of file
//     size, with a hard cap (importMaxLineBytes) for genuinely
//     pathological input.
func FilterImportStream(in io.Reader, out io.Writer) (int64, error) {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), importMaxLineBytes)

	bw := bufio.NewWriterSize(out, 256*1024)
	var written int64

	for scanner.Scan() {
		line := scanner.Bytes()
		// Apply filters in declaration order. A filter that wants to
		// drop the line returns nil; filters chain otherwise.
		dropped := false
		for _, f := range importFilters {
			if !f.pat.Match(line) {
				continue
			}
			out := f.fn(line)
			if out == nil {
				dropped = true
				break
			}
			line = out
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
			return written, fmt.Errorf("import dump contains a line longer than %d bytes — refusing to process; the dump is likely binary or corrupted", importMaxLineBytes)
		}
		return written, fmt.Errorf("scan: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return written, fmt.Errorf("flush: %w", err)
	}
	return written, nil
}

// AppliedImportFilters returns the list of filter names — for surfacing
// the filter chain in the import wizard's "what's about to happen" view.
func AppliedImportFilters() []string {
	out := make([]string, len(importFilters))
	for i, f := range importFilters {
		out[i] = f.name
	}
	return out
}

// quickSniff returns the first 256 non-blank bytes of r without consuming
// more than that — used by callers that need a peek to decide between
// import paths (e.g. distinguishing a SQL header from a binary file).
// Returns the bytes read and a Reader that replays them followed by the
// rest of the stream.
func quickSniff(r io.Reader) ([]byte, io.Reader, error) {
	buf := make([]byte, 256)
	n, err := io.ReadFull(r, buf)
	if err == io.EOF {
		return nil, bytes.NewReader(nil), nil
	}
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, nil, err
	}
	buf = buf[:n]
	return buf, io.MultiReader(bytes.NewReader(buf), r), nil
}
