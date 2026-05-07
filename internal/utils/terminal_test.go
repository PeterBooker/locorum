package utils

import (
	"errors"
	"testing"
)

func TestErrNoTerminalIsSentinel(t *testing.T) {
	t.Parallel()
	wrapped := wrapNoTerminal()
	if !errors.Is(wrapped, ErrNoTerminal) {
		t.Fatalf("errors.Is on wrapped ErrNoTerminal returned false")
	}
}

func wrapNoTerminal() error { return ErrNoTerminal }

func TestShellQuote(t *testing.T) {
	t.Parallel()
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"docker", "exec", "-it", "name", "/bin/bash"}, "docker exec -it name /bin/bash"},
		{[]string{"echo", "hello world"}, `echo "hello world"`},
		{[]string{"echo", `she said "hi"`}, `echo "she said \"hi\""`},
		{[]string{"path", `c:\windows\system32`}, `path "c:\\windows\\system32"`},
	}
	for _, c := range cases {
		if got := shellQuote(c.argv); got != c.want {
			t.Errorf("shellQuote(%v) = %q, want %q", c.argv, got, c.want)
		}
	}
}

func TestNeedsQuoting(t *testing.T) {
	t.Parallel()
	yes := []string{"", "has space", `has"quote`, `has\backslash`, `has$var`, "has`tick"}
	no := []string{"plain", "with-dashes", "name123", "/usr/bin/foo"}
	for _, s := range yes {
		if !needsQuoting(s) {
			t.Errorf("needsQuoting(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if needsQuoting(s) {
			t.Errorf("needsQuoting(%q) = true, want false", s)
		}
	}
}

func TestEscapeAppleScript(t *testing.T) {
	t.Parallel()
	if got := escapeAppleScript(`a "b" c`); got != `a \"b\" c` {
		t.Errorf("escapeAppleScript = %q", got)
	}
	if got := escapeAppleScript(`back\slash`); got != `back\\slash` {
		t.Errorf("escapeAppleScript = %q", got)
	}
}

func TestOpenInTerminalEmptyArgvFails(t *testing.T) {
	t.Parallel()
	if err := OpenInTerminal(nil); err == nil {
		t.Fatalf("nil argv should return error")
	}
}
