package sites

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

func BenchmarkImportFilter_SmallDump(b *testing.B) {
	dump := buildSyntheticDump(1 << 20)
	b.SetBytes(int64(len(dump)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out bytes.Buffer
		if _, err := FilterImportStream(bytes.NewReader(dump), &out); err != nil {
			b.Fatal(err)
		}
	}
}

// 16 MiB is bandwidth-bound; the meaningful regression signal is
// allocation count, not raw throughput.
func BenchmarkImportFilter_LargeDump(b *testing.B) {
	dump := buildSyntheticDump(16 << 20)
	b.SetBytes(int64(len(dump)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := FilterImportStream(bytes.NewReader(dump), io.Discard); err != nil {
			b.Fatal(err)
		}
	}
}

func buildSyntheticDump(size int) []byte {
	var buf bytes.Buffer
	buf.WriteString("/*!999999\\- enable the sandbox mode */;\n")
	buf.WriteString("CREATE DATABASE prod;\n")
	buf.WriteString("USE prod;\n")
	buf.WriteString(`CREATE TABLE wp_options (option_id bigint, option_name varchar(191), option_value longtext) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_uca1400_ai_ci;` + "\n")
	for buf.Len() < size {
		fmt.Fprintf(&buf, "INSERT INTO wp_options VALUES (%d, '%s', '%s');\n",
			buf.Len(),
			"option_name_"+strings.Repeat("x", 16),
			strings.Repeat("y", 64))
	}
	return buf.Bytes()[:size]
}
