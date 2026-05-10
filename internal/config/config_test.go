package config

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeStore is a thread-safe in-memory Store used only in tests.
type fakeStore struct {
	mu       sync.Mutex
	values   map[string]string
	getErr   error
	setCalls atomic.Int64
}

func newFake() *fakeStore { return &fakeStore{values: map[string]string{}} }

func (f *fakeStore) GetSetting(key string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.values[key], nil
}
func (f *fakeStore) SetSetting(key, value string) error {
	f.setCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[key] = value
	return nil
}

func TestNew_RejectsNilStore(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("expected error for nil store")
	}
}

func TestNew_PropagatesReloadErr(t *testing.T) {
	f := newFake()
	f.getErr = errors.New("boom")
	if _, err := New(f); err == nil {
		t.Fatal("expected reload error to propagate")
	}
}

func TestDefaultsWhenStorageEmpty(t *testing.T) {
	c, err := New(newFake())
	if err != nil {
		t.Fatal(err)
	}

	if c.ThemeMode() != "system" {
		t.Errorf("ThemeMode default: got %q", c.ThemeMode())
	}
	if c.PHPVersionDefault() != DefaultPHPVersion {
		t.Errorf("PHPVersionDefault default: got %q", c.PHPVersionDefault())
	}
	if c.DBEngineDefault() != DefaultDBEngine {
		t.Errorf("DBEngineDefault default: got %q", c.DBEngineDefault())
	}
	if c.DBVersionDefault() != "" {
		t.Errorf("DBVersionDefault should be empty (engine-aware fallback at call site)")
	}
	if c.RedisVersionDefault() != DefaultRedisVersion {
		t.Errorf("RedisVersionDefault default: got %q", c.RedisVersionDefault())
	}
	if c.WebServerDefault() != DefaultWebServer {
		t.Errorf("WebServerDefault default: got %q", c.WebServerDefault())
	}
	if c.PublishDBPortDefault() != false {
		t.Errorf("PublishDBPortDefault default: got true")
	}
	if c.RouterHTTPPort() != DefaultRouterHTTP {
		t.Errorf("RouterHTTPPort default: got %d", c.RouterHTTPPort())
	}
	if c.RouterHTTPSPort() != DefaultRouterHTTPS {
		t.Errorf("RouterHTTPSPort default: got %d", c.RouterHTTPSPort())
	}
	if c.UpdateCheckEnabled() != true {
		t.Errorf("UpdateCheckEnabled default: got false (should be opt-out)")
	}
	if c.UpdateCheckChannel() != DefaultUpdateChannel {
		t.Errorf("UpdateCheckChannel default: got %q", c.UpdateCheckChannel())
	}
	if c.PerformanceMode() != DefaultPerformance {
		t.Errorf("PerformanceMode default: got %q", c.PerformanceMode())
	}
}

func TestSettersAndGetters(t *testing.T) {
	f := newFake()
	c, err := New(f)
	if err != nil {
		t.Fatal(err)
	}

	must := func(name string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}

	must("php", c.SetPHPVersionDefault("8.4"))
	if got := c.PHPVersionDefault(); got != "8.4" {
		t.Errorf("php: got %q", got)
	}
	must("engine", c.SetDBEngineDefault("mariadb"))
	if got := c.DBEngineDefault(); got != "mariadb" {
		t.Errorf("engine: got %q", got)
	}
	must("dbver", c.SetDBVersionDefault("11.4"))
	if got := c.DBVersionDefault(); got != "11.4" {
		t.Errorf("dbver: got %q", got)
	}
	must("redis", c.SetRedisVersionDefault("7.2"))
	if got := c.RedisVersionDefault(); got != "7.2" {
		t.Errorf("redis: got %q", got)
	}
	must("web", c.SetWebServerDefault("apache"))
	if got := c.WebServerDefault(); got != "apache" {
		t.Errorf("web: got %q", got)
	}
	must("publishport", c.SetPublishDBPortDefault(true))
	if !c.PublishDBPortDefault() {
		t.Errorf("publishport: false")
	}
	must("http", c.SetRouterHTTPPort(8080))
	if got := c.RouterHTTPPort(); got != 8080 {
		t.Errorf("http: got %d", got)
	}
	must("https", c.SetRouterHTTPSPort(8443))
	if got := c.RouterHTTPSPort(); got != 8443 {
		t.Errorf("https: got %d", got)
	}
	must("mkcert", c.SetMkcertPath("/usr/local/bin/mkcert"))
	if got := c.MkcertPath(); got != "/usr/local/bin/mkcert" {
		t.Errorf("mkcert: got %q", got)
	}
	must("perf", c.SetPerformanceMode("mutagen"))
	if got := c.PerformanceMode(); got != "mutagen" {
		t.Errorf("perf: got %q", got)
	}
	must("ucenable", c.SetUpdateCheckEnabled(false))
	if c.UpdateCheckEnabled() {
		t.Errorf("ucenable: true")
	}
	must("ucchannel", c.SetUpdateCheckChannel("beta"))
	if got := c.UpdateCheckChannel(); got != "beta" {
		t.Errorf("ucchannel: got %q", got)
	}
	must("theme", c.SetThemeMode("dark"))
	if got := c.ThemeMode(); got != "dark" {
		t.Errorf("theme: got %q", got)
	}
}

func TestEnumValidationRejectsBadValues(t *testing.T) {
	c, _ := New(newFake())

	cases := []struct {
		name string
		fn   func() error
	}{
		{"theme", func() error { return c.SetThemeMode("rainbow") }},
		{"engine", func() error { return c.SetDBEngineDefault("postgres") }},
		{"web", func() error { return c.SetWebServerDefault("iis") }},
		{"perf", func() error { return c.SetPerformanceMode("fast") }},
		{"channel", func() error { return c.SetUpdateCheckChannel("nightly") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.fn(); err == nil {
				t.Fatalf("expected error from invalid %s value", tc.name)
			}
		})
	}
}

func TestEmptyStringSettersRejected(t *testing.T) {
	c, _ := New(newFake())
	for _, fn := range []func() error{
		func() error { return c.SetPHPVersionDefault("") },
		func() error { return c.SetDBVersionDefault("") },
		func() error { return c.SetRedisVersionDefault("") },
	} {
		if err := fn(); err == nil {
			t.Fatalf("expected error for empty version setter")
		}
	}
}

func TestPortValidation(t *testing.T) {
	c, _ := New(newFake())
	for _, p := range []int{0, -1, 70000} {
		if err := c.SetRouterHTTPPort(p); err == nil {
			t.Fatalf("expected error for port %d", p)
		}
	}
	if err := c.SetRouterHTTPPort(80); err != nil {
		t.Fatalf("port 80 rejected: %v", err)
	}
	if err := c.SetRouterHTTPSPort(65535); err != nil {
		t.Fatalf("port 65535 rejected: %v", err)
	}
}

func TestSetUsesStoreOnce(t *testing.T) {
	f := newFake()
	c, _ := New(f)
	before := f.setCalls.Load()
	if err := c.SetPHPVersionDefault("8.4"); err != nil {
		t.Fatal(err)
	}
	if got := f.setCalls.Load() - before; got != 1 {
		t.Fatalf("Set hit storage %d times, want 1", got)
	}
	// Second identical Set still hits storage (we don't dedupe; the
	// upsert in storage.SetSetting is itself idempotent).
	if err := c.SetPHPVersionDefault("8.4"); err != nil {
		t.Fatal(err)
	}
	if got := f.setCalls.Load() - before; got != 2 {
		t.Fatalf("Set hit storage %d times, want 2", got)
	}
}

func TestReloadPicksUpExternalWrites(t *testing.T) {
	f := newFake()
	c, _ := New(f)
	if c.PHPVersionDefault() != DefaultPHPVersion {
		t.Fatal("seed: expected default")
	}

	// Bypass Config.Set: write straight to the underlying store
	// (simulates a second process editing the DB).
	if err := f.SetSetting(KeyDefaultPHPVersion, "8.5"); err != nil {
		t.Fatal(err)
	}
	if c.PHPVersionDefault() != DefaultPHPVersion {
		t.Fatal("config saw external write before Reload — cache should be authoritative")
	}
	if err := c.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := c.PHPVersionDefault(); got != "8.5" {
		t.Fatalf("after Reload: got %q", got)
	}
}

func TestParseBoolCases(t *testing.T) {
	cases := []struct {
		in   string
		def  bool
		want bool
	}{
		{"true", false, true},
		{"TRUE", false, true},
		{"yes", false, true},
		{"on", false, true},
		{"1", false, true},
		{"false", true, false},
		{"no", true, false},
		{"off", true, false},
		{"0", true, false},
		{"", false, false},
		{"", true, true},
		{"garbage", true, true},
		{"garbage", false, false},
	}
	for i, tc := range cases {
		if got := parseBool(tc.in, tc.def); got != tc.want {
			t.Errorf("case %d parseBool(%q, %v) = %v; want %v", i, tc.in, tc.def, got, tc.want)
		}
	}
}

func TestParseIntCases(t *testing.T) {
	cases := []struct {
		in   string
		def  int
		want int
	}{
		{"", 80, 80},
		{"443", 80, 443},
		{"-1", 80, 80}, // negatives fall back
		{"abc", 80, 80},
		{"0", 80, 0}, // 0 is honoured (caller validates port range)
	}
	for _, tc := range cases {
		if got := parseInt(tc.in, tc.def); got != tc.want {
			t.Errorf("parseInt(%q, %d) = %d; want %d", tc.in, tc.def, got, tc.want)
		}
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	c, _ := New(newFake())

	var wg sync.WaitGroup
	wg.Add(2)
	stop := make(chan struct{})
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
				_ = c.SetRouterHTTPPort(8000 + (i % 100))
				i++
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = c.RouterHTTPPort()
			}
		}
	}()

	// Run for a short time; if we race-detect, the test runner with
	// -race surfaces the issue.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			_ = strconv.Itoa(i)
		}
		close(done)
	}()
	<-done
	close(stop)
	wg.Wait()
}
