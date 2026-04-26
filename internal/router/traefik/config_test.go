package traefik

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PeterBooker/locorum/internal/router"
	tlspkg "github.com/PeterBooker/locorum/internal/tls"
)

// newRendererForTest loads the production templates from the repo root.
// Tests run with the package directory as cwd, so the repo root is two
// directories up.
func newRendererForTest(t *testing.T) *Renderer {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	r, err := NewRenderer(os.DirFS(repoRoot))
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	return r
}

func TestBuildSiteRule(t *testing.T) {
	tests := []struct {
		name  string
		route router.SiteRoute
		want  string
	}{
		{
			name:  "primary host only",
			route: router.SiteRoute{PrimaryHost: "myslug.localhost"},
			want:  "Host(`myslug.localhost`)",
		},
		{
			name: "primary plus extra host",
			route: router.SiteRoute{
				PrimaryHost: "myslug.localhost",
				ExtraHosts:  []string{"alias.localhost"},
			},
			want: "Host(`myslug.localhost`) || Host(`alias.localhost`)",
		},
		{
			name: "primary plus multiple extras",
			route: router.SiteRoute{
				PrimaryHost: "myslug.localhost",
				ExtraHosts:  []string{"a.localhost", "b.localhost"},
			},
			want: "Host(`myslug.localhost`) || Host(`a.localhost`) || Host(`b.localhost`)",
		},
		{
			name: "primary with wildcard subdomain",
			route: router.SiteRoute{
				PrimaryHost:  "myslug.localhost",
				WildcardHost: "*.myslug.localhost",
			},
			want: "Host(`myslug.localhost`) || HostRegexp(`^[^.]+\\.myslug\\.localhost$`)",
		},
		{
			name: "primary, extras, and wildcard",
			route: router.SiteRoute{
				PrimaryHost:  "myslug.localhost",
				ExtraHosts:   []string{"alias.localhost"},
				WildcardHost: "*.myslug.localhost",
			},
			want: "Host(`myslug.localhost`) || Host(`alias.localhost`) || HostRegexp(`^[^.]+\\.myslug\\.localhost$`)",
		},
		{
			name: "slug containing hyphens",
			route: router.SiteRoute{
				PrimaryHost:  "my-slug.localhost",
				WildcardHost: "*.my-slug.localhost",
			},
			want: "Host(`my-slug.localhost`) || HostRegexp(`^[^.]+\\.my-slug\\.localhost$`)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildSiteRule(tt.route); got != tt.want {
				t.Errorf("got\n  %s\nwant\n  %s", got, tt.want)
			}
		})
	}
}

func TestBuildServiceRule(t *testing.T) {
	tests := []struct {
		name  string
		route router.ServiceRoute
		want  string
	}{
		{
			name:  "single hostname",
			route: router.ServiceRoute{Hostnames: []string{"mail.localhost"}},
			want:  "Host(`mail.localhost`)",
		},
		{
			name:  "multiple hostnames",
			route: router.ServiceRoute{Hostnames: []string{"mail.localhost", "smtp.localhost"}},
			want:  "Host(`mail.localhost`) || Host(`smtp.localhost`)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildServiceRule(tt.route); got != tt.want {
				t.Errorf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestContainerCertPath(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		hostR  string
		ctnrR  string
		want   string
	}{
		{
			name:  "translate inside hostRoot",
			host:  "/home/u/.locorum/certs/site-myslug/cert.pem",
			hostR: "/home/u/.locorum/certs",
			ctnrR: "/etc/traefik/certs",
			want:  "/etc/traefik/certs/site-myslug/cert.pem",
		},
		{
			name:  "empty hostPath",
			host:  "",
			hostR: "/home/u/.locorum/certs",
			ctnrR: "/etc/traefik/certs",
			want:  "",
		},
		{
			name:  "outside hostRoot passes through",
			host:  "/tmp/cert.pem",
			hostR: "/home/u/.locorum/certs",
			ctnrR: "/etc/traefik/certs",
			want:  "/tmp/cert.pem",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containerCertPath(tt.host, tt.hostR, tt.ctnrR); got != tt.want {
				t.Errorf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestRendererStatic(t *testing.T) {
	r := newRendererForTest(t)

	t.Run("default log level INFO", func(t *testing.T) {
		out, err := r.Static("")
		if err != nil {
			t.Fatal(err)
		}
		s := string(out)
		assertContains(t, s,
			"level: INFO",
			"websecure:",
			"address: \":443\"",
			"providers:",
			"directory: /etc/traefik/dynamic",
		)
	})

	t.Run("custom log level", func(t *testing.T) {
		out, err := r.Static("DEBUG")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), "level: DEBUG") {
			t.Errorf("missing log level: %q", string(out))
		}
	})
}

func TestRendererSite(t *testing.T) {
	r := newRendererForTest(t)
	cert := tlspkg.CertPath{
		CertFile: "/etc/traefik/certs/site-myslug/cert.pem",
		KeyFile:  "/etc/traefik/certs/site-myslug/key.pem",
	}

	cases := []struct {
		name       string
		route      router.SiteRoute
		cert       tlspkg.CertPath
		mustHave   []string
		mustNotHave []string
	}{
		{
			name: "single host with cert",
			route: router.SiteRoute{
				Slug:        "myslug",
				PrimaryHost: "myslug.localhost",
				Backend:     "http://locorum-myslug-web:80",
			},
			cert: cert,
			mustHave: []string{
				"locorum-myslug:",
				"rule: 'Host(`myslug.localhost`)'",
				"service: locorum-myslug",
				"tls: {}",
				"url: 'http://locorum-myslug-web:80'",
				"certFile: '/etc/traefik/certs/site-myslug/cert.pem'",
				"keyFile: '/etc/traefik/certs/site-myslug/key.pem'",
				"passHostHeader: true",
			},
		},
		{
			name: "single host without cert",
			route: router.SiteRoute{
				Slug:        "noslug",
				PrimaryHost: "noslug.localhost",
				Backend:     "http://locorum-noslug-web:80",
			},
			cert: tlspkg.CertPath{},
			mustHave: []string{
				"rule: 'Host(`noslug.localhost`)'",
				"tls: {}",
			},
			mustNotHave: []string{
				"certFile:",
				"keyFile:",
			},
		},
		{
			name: "multisite subdomain wildcard",
			route: router.SiteRoute{
				Slug:         "msite",
				PrimaryHost:  "msite.localhost",
				WildcardHost: "*.msite.localhost",
				Backend:      "http://locorum-msite-web:80",
			},
			cert: cert,
			mustHave: []string{
				"Host(`msite.localhost`) || HostRegexp(`^[^.]+\\.msite\\.localhost$`)",
			},
		},
		{
			name: "extra hostnames",
			route: router.SiteRoute{
				Slug:        "multi",
				PrimaryHost: "multi.localhost",
				ExtraHosts:  []string{"alt1.localhost", "alt2.localhost"},
				Backend:     "http://locorum-multi-web:80",
			},
			cert: cert,
			mustHave: []string{
				"Host(`multi.localhost`) || Host(`alt1.localhost`) || Host(`alt2.localhost`)",
			},
		},
		{
			name: "hyphenated slug",
			route: router.SiteRoute{
				Slug:        "my-store",
				PrimaryHost: "my-store.localhost",
				Backend:     "http://locorum-my-store-web:80",
			},
			cert: cert,
			mustHave: []string{
				"locorum-my-store:",
				"Host(`my-store.localhost`)",
				"url: 'http://locorum-my-store-web:80'",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := r.Site(tc.route, tc.cert)
			if err != nil {
				t.Fatal(err)
			}
			s := string(data)
			assertContains(t, s, tc.mustHave...)
			assertNotContains(t, s, tc.mustNotHave...)
		})
	}
}

func TestRendererAPI(t *testing.T) {
	r := newRendererForTest(t)

	t.Run("renders with credentials", func(t *testing.T) {
		out, err := r.API("locorum", "$2a$04$abcdefghijabcdefghijabcdefghijabcdefghijabcdefghijabcd")
		if err != nil {
			t.Fatal(err)
		}
		s := string(out)
		assertContains(t, s,
			"locorum-api:",
			"PathPrefix(`/api`)",
			"PathPrefix(`/dashboard`)",
			"service: api@internal",
			"- traefik",
			"locorum-api-auth:",
			"basicAuth:",
			`"locorum:$2a$04$abcdefghijabcdefghijabcdefghijabcdefghijabcdefghijabcd"`,
		)
	})

	t.Run("rejects empty credentials", func(t *testing.T) {
		if _, err := r.API("", "hash"); err == nil {
			t.Error("expected error with empty username")
		}
		if _, err := r.API("user", ""); err == nil {
			t.Error("expected error with empty hash")
		}
	})
}

func TestRendererService(t *testing.T) {
	r := newRendererForTest(t)
	cert := tlspkg.CertPath{
		CertFile: "/etc/traefik/certs/svc-mail/cert.pem",
		KeyFile:  "/etc/traefik/certs/svc-mail/key.pem",
	}

	cases := []struct {
		name     string
		route    router.ServiceRoute
		cert     tlspkg.CertPath
		mustHave []string
	}{
		{
			name: "mail service with cert",
			route: router.ServiceRoute{
				Name:      "mail",
				Hostnames: []string{"mail.localhost"},
				Backend:   "http://locorum-global-mail:8025",
			},
			cert: cert,
			mustHave: []string{
				"locorum-svc-mail:",
				"rule: 'Host(`mail.localhost`)'",
				"service: locorum-svc-mail",
				"url: 'http://locorum-global-mail:8025'",
				"certFile: '/etc/traefik/certs/svc-mail/cert.pem'",
			},
		},
		{
			name: "service without cert",
			route: router.ServiceRoute{
				Name:      "adminer",
				Hostnames: []string{"db.localhost"},
				Backend:   "http://locorum-global-adminer:8080",
			},
			cert: tlspkg.CertPath{},
			mustHave: []string{
				"rule: 'Host(`db.localhost`)'",
				"tls: {}",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := r.Service(tc.route, tc.cert)
			if err != nil {
				t.Fatal(err)
			}
			assertContains(t, string(data), tc.mustHave...)
		})
	}

	t.Run("no hostnames is rejected", func(t *testing.T) {
		_, err := r.Service(router.ServiceRoute{Name: "x"}, tlspkg.CertPath{})
		if err == nil {
			t.Error("expected error for service with no hostnames")
		}
	})
}

func assertContains(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			t.Errorf("missing substring %q in:\n%s", n, haystack)
		}
	}
}

func assertNotContains(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			t.Errorf("unexpected substring %q in:\n%s", n, haystack)
		}
	}
}
