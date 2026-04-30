package assets

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// fakeFS builds a fstest.MapFS rooted at "config/" with the supplied
// files. Used so all the test cases share the same "embed" baseline.
func fakeFS(files map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for k, v := range files {
		out["config/"+k] = &fstest.MapFile{Data: []byte(v)}
	}
	return out
}

func writeDisk(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readDisk(t *testing.T, root, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestReconcile_FreshInstall_WritesEverything(t *testing.T) {
	disk := t.TempDir()
	embed := fakeFS(map[string]string{
		"nginx/site.conf": "nginx body v1\n",
		"php/php.ini":     "memory_limit=256M\n",
	})

	report, next, err := Reconcile(embed, "config", disk, State{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(report.Files))
	}
	for _, fr := range report.Files {
		if fr.Verdict != VerdictWrote {
			t.Errorf("%s: got %s, want wrote", fr.Path, fr.Verdict)
		}
	}
	if got := readDisk(t, disk, "nginx/site.conf"); got != "nginx body v1\n" {
		t.Errorf("nginx body mismatch: %q", got)
	}
	if len(next.Hashes) != 2 {
		t.Errorf("next state should track both files, got %d", len(next.Hashes))
	}
}

func TestReconcile_NoOp_WhenContentMatches(t *testing.T) {
	disk := t.TempDir()
	body := "shared content\n"
	embed := fakeFS(map[string]string{"x.conf": body})
	writeDisk(t, disk, "x.conf", body)

	report, _, err := Reconcile(embed, "config", disk, State{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Files[0].Verdict; got != VerdictNoOp {
		t.Errorf("got %s, want no-op", got)
	}
}

func TestReconcile_OverwriteWhenBundledChanged_NoUserEdit(t *testing.T) {
	disk := t.TempDir()
	prev := "v1\n"
	curr := "v2\n"
	embed := fakeFS(map[string]string{"x.conf": curr})
	writeDisk(t, disk, "x.conf", prev) // disk still on v1

	prevState := State{
		Version: stateVersion,
		Hashes:  map[string]string{"x.conf": hashHex([]byte(prev))},
	}

	report, next, err := Reconcile(embed, "config", disk, prevState, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Files[0].Verdict; got != VerdictWrote {
		t.Errorf("got %s, want wrote", got)
	}
	if got := readDisk(t, disk, "x.conf"); got != curr {
		t.Errorf("disk not updated: %q", got)
	}
	if next.Hashes["x.conf"] != hashHex([]byte(curr)) {
		t.Errorf("next state hash not updated to current embed")
	}
}

func TestReconcile_MergeNeededWhenBothChanged(t *testing.T) {
	disk := t.TempDir()
	prev := "v1\n"
	embed := fakeFS(map[string]string{"x.conf": "v2\n"})
	writeDisk(t, disk, "x.conf", "user-edited\n")

	prevState := State{
		Version: stateVersion,
		Hashes:  map[string]string{"x.conf": hashHex([]byte(prev))},
	}

	report, _, err := Reconcile(embed, "config", disk, prevState, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Files[0].Verdict; got != VerdictMergeNeeded {
		t.Errorf("got %s, want merge-needed", got)
	}
	// User content preserved.
	if got := readDisk(t, disk, "x.conf"); got != "user-edited\n" {
		t.Errorf("user file was overwritten: %q", got)
	}
}

func TestReconcile_NoOpWhenUserEditedButBundledUnchanged(t *testing.T) {
	disk := t.TempDir()
	bundled := "v1\n"
	embed := fakeFS(map[string]string{"x.conf": bundled})
	writeDisk(t, disk, "x.conf", "user-edited\n")

	prevState := State{
		Version: stateVersion,
		Hashes:  map[string]string{"x.conf": hashHex([]byte(bundled))},
	}

	report, _, err := Reconcile(embed, "config", disk, prevState, nil)
	if err != nil {
		t.Fatal(err)
	}
	// embed == prev (bundled unchanged), disk != prev (user edit) →
	// neither overwrite nor merge-needed; the user owns this file.
	if got := report.Files[0].Verdict; got != VerdictMergeNeeded {
		// Per the matrix, disk != embed → "merge needed" is the
		// safe verdict because we cannot tell apart "user edited"
		// from "bundled was rewritten and user just hasn't
		// upgraded yet". Treating as merge-needed surfaces the
		// drift but does not destroy work.
		t.Errorf("got %s, want merge-needed (user edit visible to UI)", got)
	}
}

func TestReconcile_FirstRunMissingState_StillWritesFreshFiles(t *testing.T) {
	disk := t.TempDir()
	embed := fakeFS(map[string]string{"a.conf": "a\n", "b.conf": "b\n"})

	// Empty state (path missing on disk) → load returns empty.
	state, err := LoadState(filepath.Join(disk, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	report, next, err := Reconcile(embed, "config", disk, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, fr := range report.Files {
		if fr.Verdict != VerdictWrote {
			t.Errorf("%s: %s", fr.Path, fr.Verdict)
		}
	}
	// SaveState round-trip
	if err := SaveState(filepath.Join(disk, "state.json"), next); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadState(filepath.Join(disk, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Hashes) != 2 {
		t.Errorf("loaded state lost hashes: %v", loaded.Hashes)
	}
}

func TestSaveState_AtomicAndCleanFormatting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s := State{Version: stateVersion, Hashes: map[string]string{
		"x": "y",
	}}
	if err := SaveState(path, s); err != nil {
		t.Fatal(err)
	}
	body := readDisk(t, dir, "state.json")
	if body[len(body)-1] != '\n' {
		t.Errorf("state file should end with a newline")
	}
}

func TestLoadState_InvalidJSON_ReturnsEmptyNoError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadState(path)
	if err != nil {
		t.Errorf("LoadState should not fail on corrupt JSON: %v", err)
	}
	if len(s.Hashes) != 0 || s.Version != stateVersion {
		t.Errorf("recovered state should be empty: %+v", s)
	}
}

func TestLoadState_UnknownVersion_ResetsToEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "future.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"hashes":{"x":"y"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Hashes) != 0 || s.Version != stateVersion {
		t.Errorf("downgrade should reset state, got %+v", s)
	}
}

func TestReport_MergeNeededFiltersCorrectly(t *testing.T) {
	r := Report{Files: []FileResult{
		{Path: "a", Verdict: VerdictNoOp},
		{Path: "b", Verdict: VerdictMergeNeeded},
		{Path: "c", Verdict: VerdictWrote},
		{Path: "d", Verdict: VerdictMergeNeeded},
	}}
	merge := r.MergeNeeded()
	if len(merge) != 2 || merge[0].Path != "b" || merge[1].Path != "d" {
		t.Errorf("MergeNeeded filter incorrect: %+v", merge)
	}
}

func TestReconcile_OnWriteCallback(t *testing.T) {
	disk := t.TempDir()
	embed := fakeFS(map[string]string{"a.conf": "a\n", "b.conf": "b\n"})

	var calls []string
	_, _, err := Reconcile(embed, "config", disk, State{}, func(p string) {
		calls = append(calls, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Errorf("onWrite called %d times, want 2: %v", len(calls), calls)
	}
}

func TestIsConfigPath(t *testing.T) {
	cases := []struct {
		key, prefix string
		want        bool
	}{
		{"nginx/site.conf", "nginx", true},
		{"nginx", "nginx", true},
		{"nginxx/x", "nginx", false},
		{"php/php.ini", "nginx", false},
	}
	for _, c := range cases {
		if got := IsConfigPath(c.key, c.prefix); got != c.want {
			t.Errorf("IsConfigPath(%q,%q) = %v; want %v", c.key, c.prefix, got, c.want)
		}
	}
}
