// Package caddy manages per-project Caddy site files and graceful reloads.
//
// Peyk uses file-based Caddy config (admin API disabled for a smaller attack
// surface): each project gets sites/<name>.caddy, imported by the root
// Caddyfile. `caddy reload` inside the container applies changes gracefully —
// existing connections are not dropped, which is what makes traffic switches
// zero-downtime.
package caddy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mdenizay/peyk/internal/config"
	"github.com/mdenizay/peyk/internal/execx"
	"github.com/mdenizay/peyk/internal/project"
)

func sitePath(name string) string {
	return filepath.Join(config.CaddyDir(), "sites", name+".caddy")
}

// WriteSite writes the project's site file pointing at the given app slot.
func WriteSite(p *project.Project, slot string) error {
	upstream := fmt.Sprintf("%s:%d", p.SlotContainerHost(slot), p.Port)
	var b strings.Builder
	fmt.Fprintf(&b, "# Managed by peyk — project %s (slot %s)\n", p.Name, slot)
	fmt.Fprintf(&b, "%s {\n", strings.Join(p.Domains, ", "))
	fmt.Fprintf(&b, "\tencode zstd gzip\n")
	fmt.Fprintf(&b, "\theader -Server\n")
	// GitHub webhooks arrive at https://<domain>/_peyk/hooks/<project> and
	// are proxied to the peyk daemon on the host loopback.
	fmt.Fprintf(&b, "\thandle /_peyk/hooks/* {\n")
	fmt.Fprintf(&b, "\t\treverse_proxy host.docker.internal:2519\n")
	fmt.Fprintf(&b, "\t}\n")
	if p.Framework == project.Laravel && p.Services.Reverb {
		// Laravel Reverb speaks Pusher protocol under /app (ws) and /apps (api).
		fmt.Fprintf(&b, "\t@reverb path /app/* /apps/*\n")
		fmt.Fprintf(&b, "\treverse_proxy @reverb %s-reverb:6001\n", p.Name)
	}
	fmt.Fprintf(&b, "\treverse_proxy %s {\n", upstream)
	fmt.Fprintf(&b, "\t\thealth_uri %s\n", p.HealthPath)
	fmt.Fprintf(&b, "\t\thealth_interval 10s\n")
	fmt.Fprintf(&b, "\t}\n}\n")

	if err := os.MkdirAll(filepath.Dir(sitePath(p.Name)), 0o755); err != nil {
		return err
	}
	return os.WriteFile(sitePath(p.Name), []byte(b.String()), 0o644)
}

// RootCaddyfile renders the root config. The admin API stays on its default
// (localhost:2019 *inside* the container — never published to the host), which
// `caddy reload` requires for graceful, zero-downtime config swaps.
func RootCaddyfile(acmeEmail string) string {
	var b strings.Builder
	b.WriteString("{\n")
	if acmeEmail != "" {
		b.WriteString("\temail " + acmeEmail + "\n")
	}
	b.WriteString("}\n\nimport sites/*.caddy\n")
	return b.String()
}

// EnsureRootConfig writes the root Caddyfile if its content drifted,
// reporting whether it changed (a change requires a container restart, since
// an earlier config may have the admin endpoint disabled).
func EnsureRootConfig(acmeEmail string) (bool, error) {
	path := filepath.Join(config.CaddyDir(), "Caddyfile")
	want := RootCaddyfile(acmeEmail)
	if cur, err := os.ReadFile(path); err == nil && string(cur) == want {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(want), 0o644)
}

// RemoveSite deletes the project's site file.
func RemoveSite(name string) error {
	err := os.Remove(sitePath(name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Reload gracefully applies the current config inside the Caddy container.
// It self-heals a stale root Caddyfile (e.g. one that disabled the admin
// endpoint, which reload depends on) by rewriting it and restarting Caddy.
func Reload(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	changed, err := EnsureRootConfig(cfg.ACMEEmail)
	if err != nil {
		return err
	}
	if changed {
		return execx.Run(ctx, "docker", "restart", "peyk-caddy")
	}
	return execx.Run(ctx, "docker", "exec", "-w", "/etc/caddy", "peyk-caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile")
}

// Validate checks config syntax without applying it.
func Validate(ctx context.Context) error {
	return execx.Run(ctx, "docker", "exec", "-w", "/etc/caddy", "peyk-caddy", "caddy", "validate", "--config", "/etc/caddy/Caddyfile")
}
