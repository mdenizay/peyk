// Package config manages peyk's global configuration and persistent state.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Well-known paths. Overridable via PEYK_PREFIX for development/testing:
// with PEYK_PREFIX=/tmp/x, config lives in /tmp/x/etc/peyk, state in
// /tmp/x/var/lib/peyk, apps in /tmp/x/opt/peyk.
func prefix() string { return os.Getenv("PEYK_PREFIX") }

func ConfigDir() string { return filepath.Join(prefix(), "/etc/peyk") }
func StateDir() string  { return filepath.Join(prefix(), "/var/lib/peyk") }
func OptDir() string    { return filepath.Join(prefix(), "/opt/peyk") }
func AppsDir() string   { return filepath.Join(OptDir(), "apps") }
func CaddyDir() string  { return filepath.Join(OptDir(), "caddy") }
func KeysDir() string   { return filepath.Join(StateDir(), "keys") }
func BinPath() string   { return filepath.Join(OptDir(), "bin", "peyk") }

func configPath() string { return filepath.Join(ConfigDir(), "config.json") }
func statePath() string  { return filepath.Join(StateDir(), "state.json") }

// Config is peyk's global configuration.
type Config struct {
	Language     string `json:"language"`      // "tr" | "en"
	ACMEEmail    string `json:"acme_email"`    // Let's Encrypt account e-mail
	WebhookHost  string `json:"webhook_host"`  // public host that proxies webhooks (optional)
	ListenAddr   string `json:"listen_addr"`   // daemon bind address, default 127.0.0.1:2519
	GitHubToken  string `json:"github_token"`  // optional PAT for repo listing / key & hook automation
	AutoUpdate   bool   `json:"auto_update"`   // self-update via systemd timer
	KeepReleases int    `json:"keep_releases"` // releases retained per project
}

// Defaults returns a Config with sane defaults filled in.
func Defaults() Config {
	return Config{
		Language:     "en",
		ListenAddr:   "127.0.0.1:2519",
		KeepReleases: 5,
	}
}

// Load reads the global config; returns Defaults() if it does not exist yet.
func Load() (Config, error) {
	c := Defaults()
	b, err := os.ReadFile(configPath())
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("parsing %s: %w", configPath(), err)
	}
	if c.ListenAddr == "" {
		c.ListenAddr = "127.0.0.1:2519"
	}
	if c.KeepReleases <= 0 {
		c.KeepReleases = 5
	}
	return c, nil
}

// Save writes the global config with restrictive permissions (may hold a token).
func Save(c Config) error {
	if err := os.MkdirAll(ConfigDir(), 0o755); err != nil {
		return err
	}
	return writeJSON(configPath(), c, 0o600)
}

// StepStatus records the outcome of one setup step, enabling resume.
type StepStatus struct {
	Status     string `json:"status"` // "done" | "failed"
	FinishedAt string `json:"finished_at,omitempty"`
	Error      string `json:"error,omitempty"`
}

// State is peyk's persistent machine state.
type State struct {
	SetupSelected []string              `json:"setup_selected,omitempty"` // step ids chosen in the wizard
	SetupSteps    map[string]StepStatus `json:"setup_steps,omitempty"`
	SetupComplete bool                  `json:"setup_complete,omitempty"`
}

// LoadState reads persistent state; returns an empty State if missing.
func LoadState() (State, error) {
	var s State
	b, err := os.ReadFile(statePath())
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("parsing %s: %w", statePath(), err)
	}
	return s, nil
}

// SaveState persists state atomically.
func SaveState(s State) error {
	if err := os.MkdirAll(StateDir(), 0o755); err != nil {
		return err
	}
	return writeJSON(statePath(), s, 0o600)
}

// writeJSON writes v as pretty JSON to path atomically (tmp file + rename).
func writeJSON(path string, v any, mode os.FileMode) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
