package caddy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdenizay/peyk/internal/config"
	"github.com/mdenizay/peyk/internal/project"
)

func TestWriteSiteCloudflareDNS(t *testing.T) {
	t.Setenv("PEYK_PREFIX", t.TempDir())
	p := &project.Project{
		Name: "blog", Framework: project.Laravel,
		Domains: []string{"blog.example.com", "www.blog.example.com"},
		TLSMode: "cloudflare-dns", Port: 8080, HealthPath: "/up",
		ActiveSlot: "blue",
	}
	if err := WriteSite(p, "blue"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(config.CaddyDir(), "sites", "blog.caddy"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"blog.example.com, www.blog.example.com {",
		"dns cloudflare {env.CF_API_TOKEN}",
		"reverse_proxy blog-app-blue:8080",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("site file missing %q:\n%s", want, s)
		}
	}

	// Default mode must NOT carry the DNS challenge block.
	p.TLSMode = ""
	if err := WriteSite(p, "blue"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(config.CaddyDir(), "sites", "blog.caddy"))
	if strings.Contains(string(b), "dns cloudflare") {
		t.Error("default TLS mode should not use the DNS challenge")
	}
}

func TestCloudflareToken(t *testing.T) {
	t.Setenv("PEYK_PREFIX", t.TempDir())
	if CloudflareEnabled() {
		t.Fatal("enabled before a token was set")
	}
	if err := SetCloudflareToken("tok-123\n"); err != nil {
		t.Fatal(err)
	}
	if !CloudflareEnabled() {
		t.Fatal("not enabled after setting a token")
	}
	fi, err := os.Stat(envPath())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("token file mode = %o, want 0600", fi.Mode().Perm())
	}
}
