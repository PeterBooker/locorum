package version

import "testing"

func TestParseDockerServer(t *testing.T) {
	cases := []struct {
		in    string
		major int
		minor int
		patch int
		suff  string
	}{
		{"24.0.7", 24, 0, 7, ""},
		{"25.0.3-rc1", 25, 0, 3, "-rc1"},
		{"20.10.21+azure", 20, 10, 21, "+azure"},
		{"27.3", 27, 3, 0, ""},
		{"  18.09.6  ", 18, 9, 6, ""},
		{"", 0, 0, 0, ""},
		{"dev", 0, 0, 0, "dev"},
	}
	for _, c := range cases {
		got := ParseDockerServer(c.in)
		if got.Major != c.major || got.Minor != c.minor || got.Patch != c.patch || got.Suffix != c.suff {
			t.Errorf("ParseDockerServer(%q) = %+v; want major=%d minor=%d patch=%d suffix=%q", c.in, got, c.major, c.minor, c.patch, c.suff)
		}
	}
}

func TestDockerServerVersionLessThan(t *testing.T) {
	cases := []struct {
		v      DockerServerVersion
		mj, mn int
		want   bool
	}{
		{DockerServerVersion{23, 0, 5, ""}, 24, 0, true},
		{DockerServerVersion{24, 0, 0, ""}, 24, 0, false},
		{DockerServerVersion{24, 0, 7, ""}, 24, 0, false},
		{DockerServerVersion{25, 0, 0, ""}, 24, 0, false},
		{DockerServerVersion{24, 0, 0, ""}, 25, 0, true},
		{DockerServerVersion{20, 10, 21, ""}, 24, 0, true},
	}
	for _, c := range cases {
		got := c.v.LessThan(c.mj, c.mn)
		if got != c.want {
			t.Errorf("(%+v).LessThan(%d, %d) = %v; want %v", c.v, c.mj, c.mn, got, c.want)
		}
	}
}

func TestDockerServerVersionIsZero(t *testing.T) {
	if !(DockerServerVersion{}).IsZero() {
		t.Error("zero value should be IsZero")
	}
	if (DockerServerVersion{Major: 1}).IsZero() {
		t.Error("non-zero major should NOT be IsZero")
	}
	if (DockerServerVersion{Suffix: "dev"}).IsZero() {
		t.Error("a parsed but text-only version should NOT be IsZero")
	}
}
