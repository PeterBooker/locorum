package health

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/docker/fake"
	"github.com/PeterBooker/locorum/internal/platform"
	"github.com/PeterBooker/locorum/internal/version"
)

func TestRosettaCheckFiresOnAmd64UnderRosetta(t *testing.T) {
	info := &platform.Info{OS: "darwin", Arch: "amd64", UnderRosetta: true}
	c := NewRosettaCheck(info)
	out, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(out))
	}
	if out[0].Severity != SeverityBlocker {
		t.Errorf("expected blocker, got %v", out[0].Severity)
	}
}

func TestRosettaCheckSilentOnArm64Native(t *testing.T) {
	info := &platform.Info{OS: "darwin", Arch: "arm64", UnderRosetta: false}
	c := NewRosettaCheck(info)
	out, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected no findings, got %v", out)
	}
}

func TestDockerDownCheckRequiresTwoFailures(t *testing.T) {
	e := fake.New()
	e.SetPingErr(errors.New("connection refused"))
	c := NewDockerDownCheck(e)

	out, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("first run err: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("first failure should be silent, got %d findings", len(out))
	}

	out, err = c.Run(context.Background())
	if err != nil {
		t.Fatalf("second run err: %v", err)
	}
	if len(out) != 1 || out[0].Severity != SeverityBlocker {
		t.Errorf("second failure should produce a blocker; got %+v", out)
	}

	// Recovery resets the counter.
	e.SetPingErr(nil)
	out, _ = c.Run(context.Background())
	if len(out) != 0 {
		t.Errorf("after recovery, should be silent; got %d", len(out))
	}
}

func TestDockerOldCheck(t *testing.T) {
	e := fake.New()
	e.SetProvider(docker.ProviderInfo{
		ServerVersion:  "20.10.21",
		ServerVersionP: version.ParseDockerServer("20.10.21"),
	})
	c := NewDockerOldCheck(e)

	out, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) != 1 || out[0].ID != "docker-old" {
		t.Errorf("expected one docker-old finding for 20.10.21; got %+v", out)
	}

	// Newer version → no finding.
	e.SetProvider(docker.ProviderInfo{
		ServerVersion:  "26.0.0",
		ServerVersionP: version.ParseDockerServer("26.0.0"),
	})
	out, _ = c.Run(context.Background())
	if len(out) != 0 {
		t.Errorf("expected no findings for newer; got %+v", out)
	}
}

func TestProviderVirtioFSCheckOnlyFiresOnDarwinDockerDesktop(t *testing.T) {
	cases := []struct {
		name           string
		os             string
		dockerDesktop  bool
		expectFindings bool
	}{
		{"darwin docker desktop", "darwin", true, true},
		{"darwin orbstack", "darwin", false, false},
		{"linux docker desktop is impossible", "linux", true, false},
		{"linux native", "linux", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := fake.New()
			e.SetProvider(docker.ProviderInfo{IsDockerDesktop: c.dockerDesktop})
			check := NewProviderVirtioFSCheck(e, c.os)
			out, _ := check.Run(context.Background())
			if (len(out) > 0) != c.expectFindings {
				t.Errorf("got %d findings, expectFindings=%v", len(out), c.expectFindings)
			}
		})
	}
}

func TestPortConflictCheckOurRouter(t *testing.T) {
	// Spin up a local listener — that's our "port in use".
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	e := fake.New()
	// Pretend our router is running. This means the bind is "ours".
	_, _ = e.EnsureContainer(context.Background(), docker.ContainerSpec{
		Name:   "locorum-global-router",
		Image:  "fake",
		Labels: map[string]string{"x": "y"},
	})
	_ = e.StartContainer(context.Background(), "locorum-global-router")

	c := NewPortConflictCheck(e, port, "locorum-global-router")
	out, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected no finding when router is ours; got %+v", out)
	}
}

func TestPortConflictCheckForeignBind(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	e := fake.New() // no router container

	c := NewPortConflictCheck(e, port, "locorum-global-router")
	out, _ := c.Run(context.Background())
	if len(out) != 1 {
		t.Fatalf("expected 1 conflict warning; got %d", len(out))
	}
	if out[0].Severity != SeverityWarn {
		t.Errorf("expected warn; got %v", out[0].Severity)
	}
	if out[0].ID != "port-conflict-"+strconv.Itoa(port) {
		t.Errorf("ID mismatch: %q", out[0].ID)
	}
}

func TestPortConflictCheckNoListener(t *testing.T) {
	// Pick a high port nothing is on by listening then closing.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	e := fake.New()
	c := NewPortConflictCheck(e, port, "locorum-global-router")
	out, _ := c.Run(context.Background())
	if len(out) != 0 {
		t.Errorf("expected no finding when nothing listens; got %+v", out)
	}
}

// fakeRoots satisfies SiteRootLister.
type fakeRoots struct{ list []string }

func (f *fakeRoots) Roots(_ context.Context) []string { return f.list }

func TestWSLMntCCheck(t *testing.T) {
	wsl := &platform.Info{OS: "linux", WSL: platform.WSLInfo{Active: true}}
	restore := platform.NewForTest(wsl)
	defer restore()

	c := NewWSLMntCCheck(wsl, &fakeRoots{list: []string{
		"/mnt/c/Users/foo/sites/a",
		"/home/foo/sites/b",
		"/mnt/d/projects/c",
	}})
	out, _ := c.Run(context.Background())
	if len(out) != 2 {
		t.Errorf("expected 2 mnt-c findings, got %d", len(out))
	}
}

func TestWSLMntCCheckSilentOutsideWSL(t *testing.T) {
	notWSL := &platform.Info{OS: "linux", WSL: platform.WSLInfo{Active: false}}
	restore := platform.NewForTest(notWSL)
	defer restore()

	c := NewWSLMntCCheck(notWSL, &fakeRoots{list: []string{"/mnt/c/x"}})
	out, _ := c.Run(context.Background())
	if len(out) != 0 {
		t.Errorf("expected no findings when not in WSL; got %+v", out)
	}
}
