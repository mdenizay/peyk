// Package project defines the project manifest and on-disk layout.
package project

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mdenizay/peyk/internal/config"
)

// Framework identifiers.
const (
	Laravel = "laravel"
	NextJS  = "nextjs"
	Static  = "static"
)

// Services toggles the optional pieces of a project's stack.
type Services struct {
	Postgres  bool `json:"postgres"`
	Redis     bool `json:"redis"`
	Queue     bool `json:"queue"`
	Scheduler bool `json:"scheduler"`
	Reverb    bool `json:"reverb"`
}

// Project is the persisted manifest (apps/<name>/peyk.json).
type Project struct {
	Name          string   `json:"name"`
	Repo          string   `json:"repo"` // SSH clone URL
	Branch        string   `json:"branch"`
	Framework     string   `json:"framework"`
	Domains       []string `json:"domains"`
	Services      Services `json:"services"`
	Port          int      `json:"port"`        // app container port
	HealthPath    string   `json:"health_path"` // HTTP path probed before switching traffic
	PHPVersion    string   `json:"php_version,omitempty"`
	PHPExtensions []string `json:"php_extensions,omitempty"` // installed on top of the base image
	NodeVersion   string   `json:"node_version,omitempty"`
	TLSMode       string   `json:"tls_mode,omitempty"` // "" = HTTP/TLS-ALPN challenge, "cloudflare-dns" = DNS-01 via Cloudflare
	WebhookSecret string   `json:"webhook_secret"`
	ActiveSlot    string   `json:"active_slot"` // "blue" | "green" | "" (never deployed)
	CurrentSHA    string   `json:"current_sha,omitempty"`
	DBPassword    string   `json:"db_password,omitempty"`
	RedisPassword string   `json:"redis_password,omitempty"`
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)

// ValidName reports whether name is a safe project identifier.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// Dir returns the project's root directory.
func (p *Project) Dir() string { return filepath.Join(config.AppsDir(), p.Name) }

// ReleasesDir, SharedDir, KeyPath: on-disk layout helpers.
func (p *Project) ReleasesDir() string { return filepath.Join(p.Dir(), "releases") }
func (p *Project) SharedDir() string   { return filepath.Join(p.Dir(), "shared") }
func (p *Project) KeyPath() string     { return filepath.Join(config.KeysDir(), p.Name+"_deploy_key") }

// ComposeProject is the docker compose project name (also container prefix).
func (p *Project) ComposeProject() string { return "peyk-" + p.Name }

// ImageTag returns the image reference for a given git SHA.
func (p *Project) ImageTag(sha string) string {
	return fmt.Sprintf("peyk-%s:%s", p.Name, short(sha))
}

// SlotContainerHost returns the DNS name Caddy uses to reach a slot.
func (p *Project) SlotContainerHost(slot string) string {
	return fmt.Sprintf("%s-app-%s", p.Name, slot)
}

// InactiveSlot returns the slot the next deploy should target.
func (p *Project) InactiveSlot() string {
	if p.ActiveSlot == "blue" {
		return "green"
	}
	return "blue"
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func (p *Project) manifestPath() string { return filepath.Join(p.Dir(), "peyk.json") }

// Save persists the manifest (0600: contains secrets).
func (p *Project) Save() error {
	if err := os.MkdirAll(p.Dir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := p.manifestPath() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.manifestPath())
}

// Load reads one project by name.
func Load(name string) (*Project, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("invalid project name %q", name)
	}
	p := &Project{Name: name}
	b, err := os.ReadFile(p.manifestPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("project %q not found", name)
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, p); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", p.manifestPath(), err)
	}
	return p, nil
}

// List returns all projects sorted by name.
func List() ([]*Project, error) {
	entries, err := os.ReadDir(config.AppsDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Project
	for _, e := range entries {
		if !e.IsDir() || !ValidName(e.Name()) {
			continue
		}
		p, err := Load(e.Name())
		if err != nil {
			continue // skip half-created directories
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// NewSecret returns a hex-encoded 32-byte random secret.
func NewSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return hex.EncodeToString(b)
}

// NormalizeRepo turns "owner/repo" into an SSH clone URL and passes SSH URLs through.
func NormalizeRepo(s string) (string, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "git@") || strings.HasPrefix(s, "ssh://") {
		return s, nil
	}
	if strings.HasPrefix(s, "https://github.com/") {
		s = strings.TrimSuffix(strings.TrimPrefix(s, "https://github.com/"), ".git")
	}
	if m := regexp.MustCompile(`^[\w.-]+/[\w.-]+$`).FindString(s); m != "" {
		return "git@github.com:" + m + ".git", nil
	}
	return "", fmt.Errorf("unrecognized repository %q (use owner/repo or an SSH URL)", s)
}

// OwnerRepo extracts "owner/repo" from an SSH GitHub URL, or "" if not GitHub.
func OwnerRepo(repo string) string {
	m := regexp.MustCompile(`github\.com[:/]([\w.-]+/[\w.-]+?)(\.git)?$`).FindStringSubmatch(repo)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}
