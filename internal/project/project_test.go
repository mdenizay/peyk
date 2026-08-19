package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testProject(t *testing.T) *Project {
	t.Setenv("PEYK_PREFIX", t.TempDir())
	p := &Project{
		Name:          "blog",
		Repo:          "git@github.com:owner/blog.git",
		Branch:        "main",
		Framework:     Laravel,
		Domains:       []string{"blog.example.com"},
		Services:      Services{Postgres: true, Redis: true, Queue: true, Scheduler: true, Reverb: true},
		WebhookSecret: NewSecret(),
		DBPassword:    "dbpass",
		RedisPassword: "redispass",
	}
	DefaultsFor(p)
	return p
}

func TestValidName(t *testing.T) {
	for name, want := range map[string]bool{
		"blog":        true,
		"my-app-2":    true,
		"Blog":        false,
		"a":           false,
		"-x":          false,
		"a b":         false,
		"../etc":      false,
		"app_underscore": false,
	} {
		if got := ValidName(name); got != want {
			t.Errorf("ValidName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestComposeYAML(t *testing.T) {
	p := testProject(t)
	yaml := p.composeYAML()
	for _, want := range []string{
		"app_blue:", "app_green:", "queue:", "scheduler:", "reverb:",
		"postgres:", "redis:", "peyk-edge:", "no-new-privileges",
		"container_name: blog-app-blue", "profiles: [\"green\"]",
		"POSTGRES_PASSWORD: dbpass", "condition: service_healthy",
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("compose YAML missing %q\n%s", want, yaml)
		}
	}
}

func TestManifestRoundTrip(t *testing.T) {
	p := testProject(t)
	if err := p.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load("blog")
	if err != nil {
		t.Fatal(err)
	}
	if got.Repo != p.Repo || got.WebhookSecret != p.WebhookSecret || !got.Services.Reverb {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	// Manifest holds secrets: must be 0600.
	fi, err := os.Stat(filepath.Join(got.Dir(), "peyk.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("manifest mode = %o, want 0600", fi.Mode().Perm())
	}
}

func TestNormalizeRepo(t *testing.T) {
	cases := map[string]string{
		"owner/repo":                        "git@github.com:owner/repo.git",
		"https://github.com/owner/repo":     "git@github.com:owner/repo.git",
		"https://github.com/owner/repo.git": "git@github.com:owner/repo.git",
		"git@github.com:owner/repo.git":     "git@github.com:owner/repo.git",
	}
	for in, want := range cases {
		got, err := NormalizeRepo(in)
		if err != nil || got != want {
			t.Errorf("NormalizeRepo(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := NormalizeRepo("not a repo!!"); err == nil {
		t.Error("expected error for invalid repo")
	}
}

func TestOwnerRepo(t *testing.T) {
	if got := OwnerRepo("git@github.com:owner/repo.git"); got != "owner/repo" {
		t.Errorf("OwnerRepo = %q", got)
	}
	if got := OwnerRepo("git@gitlab.com:owner/repo.git"); got != "" {
		t.Errorf("OwnerRepo(gitlab) = %q, want empty", got)
	}
}

func TestDetectPHP(t *testing.T) {
	dir := t.TempDir()
	composer := `{
		"require": {
			"php": "^8.4",
			"laravel/framework": "^12.0",
			"ext-exif": "*",
			"ext-imagick": "*"
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(composer), 0o644); err != nil {
		t.Fatal(err)
	}
	ver, exts := DetectPHP(dir)
	if ver != "8.4" {
		t.Errorf("version = %q, want 8.4", ver)
	}
	want := "exif gd imagick intl"
	if got := strings.Join(exts, " "); got != want {
		t.Errorf("extensions = %q, want %q", got, want)
	}
}

func TestGeneratedDockerfileRegenerated(t *testing.T) {
	p := testProject(t)
	p.PHPVersion = "8.4"
	p.PHPExtensions = []string{"exif", "intl"}
	dir := t.TempDir()
	if err := p.EnsureDockerfile(dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if !strings.Contains(string(b), "serversideup/php:8.4-fpm-nginx") {
		t.Errorf("Dockerfile missing PHP 8.4 base:\n%s", b)
	}
	if !strings.Contains(string(b), "install-php-extensions exif intl") {
		t.Errorf("Dockerfile missing extension install:\n%s", b)
	}
	// A peyk-generated Dockerfile is regenerated when the manifest changes…
	p.PHPVersion = "8.5"
	if err := p.EnsureDockerfile(dir); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if !strings.Contains(string(b), "serversideup/php:8.5-fpm-nginx") {
		t.Errorf("generated Dockerfile was not regenerated:\n%s", b)
	}
	// …but a project-supplied Dockerfile is never touched.
	custom := "FROM php:8.3\n"
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(custom), 0o644)
	if err := p.EnsureDockerfile(dir); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if string(b) != custom {
		t.Errorf("project's own Dockerfile was overwritten:\n%s", b)
	}
}
