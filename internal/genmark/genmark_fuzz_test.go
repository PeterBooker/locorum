package genmark

import (
	"bytes"
	"testing"
)

func FuzzHasMarker(f *testing.F) {
	seeds := []string{
		"",
		"# locorum-generated\nfoo: bar\n",
		"// locorum-generated\nfoo: bar\n",
		"locorum-generated",
		"LOCORUM-GENERATED",
		"locorum-genrated",
		"random preamble\n# locorum-generated\n",
		// Marker past the peek window — must not match.
		string(make([]byte, peekBytes+100)) + "locorum-generated",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		head := data
		if len(head) > peekBytes {
			head = head[:peekBytes]
		}
		want := bytes.Contains(head, []byte(Marker))
		if got := HasMarker(data); got != want {
			t.Fatalf("HasMarker mismatch: got=%v want=%v len=%d", got, want, len(data))
		}
	})
}
