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

// RemoveSite deletes the project's site file.
func RemoveSite(name string) error {
	err := os.Remove(sitePath(name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Reload gracefully applies the current config inside the Caddy container.
func Reload(ctx context.Context) error {
	return execx.Run(ctx, "docker", "exec", "-w", "/etc/caddy", "peyk-caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile")
}

// Validate checks config syntax without applying it.
func Validate(ctx context.Context) error {
	return execx.Run(ctx, "docker", "exec", "-w", "/etc/caddy", "peyk-caddy", "caddy", "validate", "--config", "/etc/caddy/Caddyfile")
}
