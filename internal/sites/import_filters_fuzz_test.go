package sites

import (
	"bytes"
	"strings"
	"testing"
)

// Filter must never panic, never grow output beyond a small factor of
// input (a runaway rewrite cycle would burn memory), and only drop on
// line boundaries.
func FuzzFilterImportStream(f *testing.F) {
	seeds := []string{
		"",
		"INSERT INTO foo VALUES (1);\n",
		"CREATE DATABASE x;\nUSE x;\nINSERT INTO foo VALUES (1);\n",
		"-- comment only\n",
		`/*!999999\- enable the sandbox mode */;` + "\n",
		"DEFINER=`root`@`%` ",
		"INSERT INTO foo VALUES ('utf8mb4_uca1400_ai_ci');\n",
		"\x00\x01\x02 binary garbage\n",
		strings.Repeat("INSERT INTO foo VALUES (1);\n", 100),
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		if len(in) > 1<<20 {
			t.Skip("input too large for fuzz")
		}

		var out bytes.Buffer
		_, err := FilterImportStream(bytes.NewReader(in), &out)
		if err != nil {
			// Only "line too long" is conditionally acceptable; flag it
			// when the input had no oversize line.
			if strings.Contains(err.Error(), "longer than") && longestLine(in) <= importMaxLineBytes {
				t.Fatalf("ErrTooLong reported but max line was %d ≤ cap", longestLine(in))
			}
			return
		}

		if int64(out.Len()) > int64(len(in))*2+1024 {
			t.Fatalf("output grew unexpectedly: in=%d out=%d", len(in), out.Len())
		}
	})
}

func longestLine(in []byte) int {
	maxLine, cur := 0, 0
	for _, b := range in {
		if b == '\n' {
			if cur > maxLine {
				maxLine = cur
			}
			cur = 0
			continue
		}
		cur++
	}
	if cur > maxLine {
		maxLine = cur
	}
	return maxLine
}
