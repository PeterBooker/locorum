package docker

import (
	"strings"
	"testing"
	"time"
)

func TestSpec_ConfigHash_DeterministicForSameSpec(t *testing.T) {
	s := ContainerSpec{
		Name:    "x",
		Image:   "alpine:3",
		Cmd:     []string{"sh", "-c", "true"},
		Env:     []string{"A=1", "B=2"},
		Labels:  map[string]string{"k1": "v1", "k2": "v2"},
		Init:    true,
		Restart: RestartNo,
	}
	if a, b := s.ConfigHash(), s.ConfigHash(); a != b {
		t.Errorf("hash mismatch on identical specs: %s vs %s", a, b)
	}
}

func TestSpec_ConfigHash_OrderInvariantOnEnvAndLabels(t *testing.T) {
	a := ContainerSpec{
		Name:   "x",
		Image:  "alpine:3",
		Env:    []string{"A=1", "B=2"},
		Labels: map[string]string{"k1": "v1", "k2": "v2"},
	}
	b := a
	b.Env = []string{"B=2", "A=1"}
	if a.ConfigHash() != b.ConfigHash() {
		t.Errorf("hash should be invariant to Env order")
	}
}

func TestSpec_ConfigHash_ImageChangeDifferent(t *testing.T) {
	a := ContainerSpec{Name: "x", Image: "alpine:3"}
	b := a
	b.Image = "alpine:3.18"
	if a.ConfigHash() == b.ConfigHash() {
		t.Errorf("hash should change with image tag")
	}
}

func TestSpec_ConfigHash_VersionLabelExcluded(t *testing.T) {
	// LabelVersion is set to different values; hash must stay equal so a
	// Locorum upgrade alone does not force a recreate.
	a := ContainerSpec{
		Name:   "x",
		Image:  "alpine:3",
		Labels: map[string]string{LabelPlatform: PlatformValue, LabelVersion: "v1"},
	}
	b := a
	b.Labels = map[string]string{LabelPlatform: PlatformValue, LabelVersion: "v2"}
	if a.ConfigHash() != b.ConfigHash() {
		t.Errorf("hash must be invariant to LabelVersion")
	}
}

func TestSpec_ConfigHash_ConfigHashLabelExcluded(t *testing.T) {
	a := ContainerSpec{
		Name:   "x",
		Image:  "alpine:3",
		Labels: map[string]string{LabelPlatform: PlatformValue, LabelConfigHash: "old"},
	}
	b := a
	b.Labels = map[string]string{LabelPlatform: PlatformValue, LabelConfigHash: "different"}
	if a.ConfigHash() != b.ConfigHash() {
		t.Errorf("hash must be invariant to LabelConfigHash itself")
	}
}

func TestSpec_ConfigHash_SecretValuesIgnored(t *testing.T) {
	// Rotating a password should not force a recreate. Keys are part of
	// the intent; values are runtime configuration.
	a := ContainerSpec{
		Name:       "x",
		Image:      "alpine:3",
		EnvSecrets: []EnvSecret{{Key: "PASSWORD", Value: "old"}},
	}
	b := a
	b.EnvSecrets = []EnvSecret{{Key: "PASSWORD", Value: "new"}}
	if a.ConfigHash() != b.ConfigHash() {
		t.Errorf("hash must be invariant to EnvSecret values")
	}

	c := a
	c.EnvSecrets = []EnvSecret{{Key: "OTHER", Value: "old"}}
	if a.ConfigHash() == c.ConfigHash() {
		t.Errorf("hash must change when EnvSecret keys change")
	}
}

func TestSpec_ConfigHash_HealthcheckChangeDifferent(t *testing.T) {
	a := ContainerSpec{
		Name:        "x",
		Image:       "alpine:3",
		Healthcheck: &Healthcheck{Test: []string{"CMD", "true"}, Interval: time.Second, Retries: 5},
	}
	b := a
	hc := *a.Healthcheck
	hc.Retries = 10
	b.Healthcheck = &hc
	if a.ConfigHash() == b.ConfigHash() {
		t.Errorf("hash should change with Healthcheck.Retries")
	}
}

func TestSpec_ConfigHash_CapAddOrderInvariant(t *testing.T) {
	a := ContainerSpec{
		Name:     "x",
		Image:    "alpine:3",
		Security: SecurityOptions{CapAdd: []string{"NET_BIND_SERVICE", "CHOWN"}, CapDrop: []string{"ALL"}},
	}
	b := a
	b.Security = SecurityOptions{CapAdd: []string{"CHOWN", "NET_BIND_SERVICE"}, CapDrop: []string{"ALL"}}
	if a.ConfigHash() != b.ConfigHash() {
		t.Errorf("hash should be invariant to CapAdd order")
	}
}

func TestSpec_ConfigHash_MountChangeDifferent(t *testing.T) {
	a := ContainerSpec{
		Name:   "x",
		Image:  "alpine:3",
		Mounts: []Mount{{Bind: &BindMount{Source: "/a", Target: "/a"}}},
	}
	b := a
	b.Mounts = []Mount{{Bind: &BindMount{Source: "/b", Target: "/b"}}}
	if a.ConfigHash() == b.ConfigHash() {
		t.Errorf("hash should change when mounts change")
	}
}

func TestRedactErrSpec_RemovesSecretValues(t *testing.T) {
	spec := ContainerSpec{
		Name:       "x",
		Image:      "alpine:3",
		EnvSecrets: []EnvSecret{{Key: "DBPW", Value: "supersecret"}},
	}
	err := errSimple("create container x: env=DBPW=supersecret blew up")
	out := redactErrSpec(err, spec)
	if strings.Contains(out.Error(), "supersecret") {
		t.Errorf("redactErrSpec leaked password: %q", out.Error())
	}
	if !strings.Contains(out.Error(), "***") {
		t.Errorf("redactErrSpec did not substitute marker: %q", out.Error())
	}
}

type errSimple string

func (e errSimple) Error() string { return string(e) }

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want retryClass
	}{
		{"nil", nil, retryNone},
		{"buildkit", errSimple("error: parent snapshot xyz does not exist"), retryBuildKitRace},
		{"connRefused", errSimple("dial unix /var/run/docker.sock: connect: connection refused"), retryDaemonRestart},
		{"random", errSimple("something else"), retryNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyError(c.err); got != c.want {
				t.Errorf("classifyError = %d, want %d", got, c.want)
			}
		})
	}
}
