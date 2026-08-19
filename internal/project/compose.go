package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteCompose (re)generates the project's docker-compose.yml and the
// compose-interpolation .env file next to it.
//
// Layout notes:
//   - apps/<name>/.env        → compose interpolation only (image tags); peyk-owned
//   - apps/<name>/shared/.env → the application's env file, mounted read-only
//
// Zero-downtime model: app_blue / app_green are identical services behind
// compose profiles. Exactly one is active; deploys start the inactive slot,
// health-check it, point Caddy at it, then stop the old slot.
func (p *Project) WriteCompose() error {
	if err := os.WriteFile(filepath.Join(p.Dir(), "docker-compose.yml"), []byte(p.composeYAML()), 0o644); err != nil {
		return err
	}
	return nil
}

// WriteComposeEnv writes the compose interpolation env file.
func (p *Project) WriteComposeEnv(imageBlue, imageGreen, imageActive string) error {
	content := fmt.Sprintf("# Managed by peyk — image tags for compose interpolation\nIMAGE_BLUE=%s\nIMAGE_GREEN=%s\nIMAGE_ACTIVE=%s\n",
		orPlaceholder(imageBlue), orPlaceholder(imageGreen), orPlaceholder(imageActive))
	return os.WriteFile(filepath.Join(p.Dir(), ".env"), []byte(content), 0o600)
}

func orPlaceholder(s string) string {
	if s == "" {
		return "scratch" // never started; placeholder keeps compose config valid
	}
	return s
}

func (p *Project) composeYAML() string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	w("# Managed by peyk — do not edit by hand; changes are overwritten on redeploy.")
	w("# To adjust services, edit peyk.json via `peyk edit %s` and redeploy.", p.Name)
	w("name: %s", p.ComposeProject())
	w("services:")

	for _, slot := range []string{"blue", "green"} {
		w("  app_%s:", slot)
		w("    image: ${IMAGE_%s}", strings.ToUpper(slot))
		w("    container_name: %s", p.SlotContainerHost(slot))
		w("    profiles: [%q]", slot)
		p.commonAppLines(&b, true)
		p.healthcheckLines(&b)
	}

	if p.Framework == Laravel {
		if p.Services.Queue {
			w("  queue:")
			w("    image: ${IMAGE_ACTIVE}")
			w("    command: [\"php\", \"artisan\", \"queue:work\", \"--tries=3\", \"--max-time=3600\"]")
			w("    stop_grace_period: 60s")
			p.commonAppLines(&b, false)
		}
		if p.Services.Scheduler {
			w("  scheduler:")
			w("    image: ${IMAGE_ACTIVE}")
			w("    command: [\"php\", \"artisan\", \"schedule:work\"]")
			p.commonAppLines(&b, false)
		}
		if p.Services.Reverb {
			w("  reverb:")
			w("    image: ${IMAGE_ACTIVE}")
			w("    container_name: %s-reverb", p.Name)
			w("    command: [\"php\", \"artisan\", \"reverb:start\", \"--host=0.0.0.0\", \"--port=6001\"]")
			p.commonAppLines(&b, true)
		}
	}

	if p.Services.Postgres {
		w("  postgres:")
		w("    image: postgres:16-alpine")
		w("    restart: unless-stopped")
		w("    security_opt: [\"no-new-privileges:true\"]")
		w("    environment:")
		w("      POSTGRES_DB: %s", p.Name)
		w("      POSTGRES_USER: %s", p.Name)
		w("      POSTGRES_PASSWORD: %s", p.DBPassword)
		w("    volumes:")
		w("      - pgdata:/var/lib/postgresql/data")
		w("    healthcheck:")
		w("      test: [\"CMD-SHELL\", \"pg_isready -U %s -d %s\"]", p.Name, p.Name)
		w("      interval: 5s")
		w("      timeout: 3s")
		w("      retries: 10")
	}

	if p.Services.Redis {
		w("  redis:")
		w("    image: redis:7-alpine")
		w("    restart: unless-stopped")
		w("    security_opt: [\"no-new-privileges:true\"]")
		w("    command: [\"redis-server\", \"--requirepass\", \"%s\", \"--maxmemory-policy\", \"noeviction\"]", p.RedisPassword)
		w("    volumes:")
		w("      - redisdata:/data")
		w("    healthcheck:")
		w("      test: [\"CMD\", \"redis-cli\", \"-a\", \"%s\", \"ping\"]", p.RedisPassword)
		w("      interval: 5s")
		w("      timeout: 3s")
		w("      retries: 10")
	}

	w("networks:")
	w("  default:")
	w("    name: %s-net", p.ComposeProject())
	w("  peyk-edge:")
	w("    external: true")

	if p.Services.Postgres || p.Services.Redis {
		w("volumes:")
		if p.Services.Postgres {
			w("  pgdata:")
		}
		if p.Services.Redis {
			w("  redisdata:")
		}
	}
	return b.String()
}

// commonAppLines emits the shared block for app-image services.
// edge=true additionally attaches the service to the peyk-edge network.
func (p *Project) commonAppLines(b *strings.Builder, edge bool) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format+"\n", args...) }
	w("    restart: unless-stopped")
	w("    security_opt: [\"no-new-privileges:true\"]")
	w("    env_file:")
	w("      - ./shared/.env")
	if p.Framework == Laravel {
		w("    volumes:")
		w("      - ./shared/storage:/var/www/html/storage")
	}
	if p.Services.Postgres || p.Services.Redis {
		w("    depends_on:")
		if p.Services.Postgres {
			w("      postgres:")
			w("        condition: service_healthy")
		}
		if p.Services.Redis {
			w("      redis:")
			w("        condition: service_healthy")
		}
	}
	if edge {
		w("    networks:")
		w("      - default")
		w("      - peyk-edge")
	}
}

func (p *Project) healthcheckLines(b *strings.Builder) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format+"\n", args...) }
	w("    healthcheck:")
	switch p.Framework {
	case Laravel:
		w("      test: [\"CMD\", \"curl\", \"-fsS\", \"http://localhost:%d%s\"]", p.Port, p.HealthPath)
	default:
		w("      test: [\"CMD\", \"wget\", \"-qO-\", \"http://127.0.0.1:%d%s\"]", p.Port, p.HealthPath)
	}
	w("      interval: 5s")
	w("      timeout: 5s")
	w("      retries: 12")
	w("      start_period: 15s")
}
