package caddy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mdenizay/peyk/internal/config"
	"github.com/mdenizay/peyk/internal/execx"
)

// envPath holds edge secrets (currently the Cloudflare API token), 0600.
func envPath() string { return filepath.Join(config.CaddyDir(), ".env") }

// CloudflareEnabled reports whether a Cloudflare API token is configured.
func CloudflareEnabled() bool {
	b, err := os.ReadFile(envPath())
	return err == nil && strings.Contains(string(b), "CF_API_TOKEN=")
}

// SetCloudflareToken stores the token used for DNS-01 challenges.
func SetCloudflareToken(token string) error {
	if err := os.MkdirAll(config.CaddyDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(envPath(), []byte("CF_API_TOKEN="+strings.TrimSpace(token)+"\n"), 0o600)
}

// cloudflareDockerfile builds Caddy with the Cloudflare DNS challenge module.
const cloudflareDockerfile = `# Managed by peyk — Caddy with the Cloudflare DNS-01 module
FROM caddy:2-builder AS builder
RUN xcaddy build --with github.com/caddy-dns/cloudflare

FROM caddy:2-alpine
COPY --from=builder /usr/bin/caddy /usr/bin/caddy
`

// EnsureEdgeStack (re)generates the edge compose stack and brings it up.
// With a Cloudflare token configured, Caddy is built locally with the
// cloudflare DNS module and receives the token via env file.
func EnsureEdgeStack(ctx context.Context, acmeEmail string) error {
	dir := config.CaddyDir()
	if err := os.MkdirAll(filepath.Join(dir, "sites"), 0o755); err != nil {
		return err
	}
	if _, err := EnsureRootConfig(acmeEmail); err != nil {
		return err
	}

	cf := CloudflareEnabled()
	var image string
	if cf {
		if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(cloudflareDockerfile), 0o644); err != nil {
			return err
		}
		image = "    build: .\n    image: peyk-caddy:latest\n    env_file:\n      - ./.env\n"
	} else {
		image = "    image: caddy:2-alpine\n"
	}

	compose := fmt.Sprintf(`# Managed by peyk — edge proxy stack
name: peyk-edge
services:
  caddy:
%s    container_name: peyk-caddy
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "80:80"
      - "443:443"
      - "443:443/udp"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - ./sites:/etc/caddy/sites:ro
      - caddy_data:/data
      - caddy_config:/config
    networks:
      - peyk-edge
networks:
  peyk-edge:
    external: true
volumes:
  caddy_data:
  caddy_config:
`, image)
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(compose), 0o644); err != nil {
		return err
	}

	args := []string{"compose", "--project-directory", dir, "up", "-d", "--wait"}
	if cf {
		args = append(args, "--build")
	}
	return execx.Run(ctx, "docker", args...)
}
