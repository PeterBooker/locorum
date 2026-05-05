package docker

import "testing"

func TestClassifyProviderName(t *testing.T) {
	cases := []struct {
		name, daemon, os string
		want             string
	}{
		{"docker desktop linux", "docker-desktop", "Docker Desktop", "Docker Desktop"},
		{"docker desktop mac", "docker-desktop", "Docker Desktop (Mac)", "Docker Desktop"},
		{"orbstack", "orbstack", "OrbStack 1.7", "OrbStack"},
		{"rancher desktop", "rancher-desktop", "Rancher Desktop WSL", "Rancher Desktop"},
		{"colima", "colima", "Ubuntu 22.04 (Colima)", "Colima"},
		{"podman", "podman", "Podman 4.7.2 / Linux", "Podman"},
		{"plain docker engine", "moby", "Ubuntu 22.04.3 LTS", "moby"},
		{"unknown name fallback to literal", "weird-daemon", "SomeUnknownOS", "weird-daemon"},
		{"empty everything", "", "", "docker"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyProviderName(c.daemon, c.os)
			if got != c.want {
				t.Errorf("classifyProviderName(%q, %q) = %q; want %q", c.daemon, c.os, got, c.want)
			}
		})
	}
}

func TestIsRootless(t *testing.T) {
	cases := []struct {
		name string
		opts []string
		want bool
	}{
		{"rootless name", []string{"name=rootless"}, true},
		{"rootless mixed case", []string{"Name=ROOTLESS"}, true},
		{"contains rootless substring", []string{"foo=rootless,bar=baz"}, true},
		{"non-rootless", []string{"name=seccomp,profile=builtin", "selinux"}, false},
		{"empty", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isRootless(c.opts)
			if got != c.want {
				t.Errorf("isRootless(%v) = %v; want %v", c.opts, got, c.want)
			}
		})
	}
}

func TestIsDockerDesktop(t *testing.T) {
	cases := []struct {
		name, os, daemon string
		want             bool
	}{
		{"explicit docker desktop os", "Docker Desktop", "docker", true},
		{"explicit docker desktop daemon", "macOS", "docker-desktop", true},
		{"orbstack", "OrbStack", "orbstack", false},
		{"empty", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isDockerDesktop(c.os, c.daemon)
			if got != c.want {
				t.Errorf("isDockerDesktop(%q, %q) = %v; want %v", c.os, c.daemon, got, c.want)
			}
		})
	}
}
