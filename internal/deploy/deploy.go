// Package deploy implements the zero-downtime blue/green deploy pipeline.
//
// Pipeline: fetch → checkout release → build image → start inactive slot →
// (Laravel: migrate) → health check → point Caddy at the new slot → reload →
// roll workers to the new image → stop the old slot. Any failure before the
// Caddy switch leaves the previous release serving untouched.
package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mdenizay/peyk/internal/caddy"
	"github.com/mdenizay/peyk/internal/config"
	"github.com/mdenizay/peyk/internal/execx"
	"github.com/mdenizay/peyk/internal/gitx"
	"github.com/mdenizay/peyk/internal/i18n"
	"github.com/mdenizay/peyk/internal/project"
)

// Run deploys the project's configured branch. Concurrent deploys of the same
// project are serialized via an exclusive file lock (webhook + CLI safe).
func Run(ctx context.Context, cfg config.Config, p *project.Project) error {
	unlock, err := lock(p)
	if err != nil {
		return err
	}
	defer unlock()

	fmt.Println(i18n.T("deploy.start", p.Name, p.Branch))

	fmt.Println(i18n.T("deploy.fetch", p.Repo))
	releaseDir, sha, err := gitx.FetchAndCheckout(ctx, p)
	if err != nil {
		return err
	}

	image := p.ImageTag(sha)
	fmt.Println(i18n.T("deploy.build"))
	if err := p.EnsureDockerfile(releaseDir); err != nil {
		return err
	}
	if err := execx.Run(ctx, "docker", "build", "-t", image, releaseDir); err != nil {
		return err
	}

	if p.Framework == project.Laravel {
		if err := ensureLaravelShared(p); err != nil {
			return err
		}
	}
	if err := p.WriteCompose(); err != nil {
		return err
	}

	firstDeploy := p.ActiveSlot == ""
	target := p.InactiveSlot()
	oldImage := ""
	if !firstDeploy {
		oldImage = p.ImageTag(p.CurrentSHA)
	}

	// New slot gets the new image; workers keep the old image until the switch.
	blue, green := image, image
	if !firstDeploy {
		if target == "blue" {
			green = oldImage
		} else {
			blue = oldImage
		}
	}
	activeForWorkers := oldImage
	if firstDeploy {
		activeForWorkers = image
	}
	if err := p.WriteComposeEnv(blue, green, activeForWorkers); err != nil {
		return err
	}

	appSvc := "app_" + target
	fmt.Println(i18n.T("deploy.health"))
	if err := compose(ctx, p, "--profile", target, "up", "-d", "--wait", appSvc); err != nil {
		_ = compose(ctx, p, "--profile", target, "rm", "-sf", appSvc)
		return fmt.Errorf("%s: %w", i18n.T("deploy.rollback", err), err)
	}

	if p.Framework == project.Laravel {
		fmt.Println(i18n.T("deploy.migrate"))
		for _, args := range [][]string{
			{"php", "artisan", "storage:link"},
			{"php", "artisan", "migrate", "--force", "--isolated"},
			{"php", "artisan", "optimize"},
		} {
			execArgs := append([]string{"--profile", target, "exec", "-T", appSvc}, args...)
			if err := compose(ctx, p, execArgs...); err != nil {
				// storage:link fails harmlessly when the link exists; only
				// migrate/optimize failures abort the deploy.
				if args[2] != "storage:link" {
					_ = compose(ctx, p, "--profile", target, "rm", "-sf", appSvc)
					return fmt.Errorf("%s: %w", i18n.T("deploy.rollback", err), err)
				}
			}
		}
	}

	fmt.Println(i18n.T("deploy.switch"))
	if err := caddy.WriteSite(p, target); err != nil {
		return err
	}
	if err := caddy.Reload(ctx); err != nil {
		// Config didn't apply; the old site file semantics may be gone from
		// disk but Caddy still runs the previous in-memory config.
		_ = compose(ctx, p, "--profile", target, "rm", "-sf", appSvc)
		if !firstDeploy {
			_ = caddy.WriteSite(p, p.ActiveSlot)
		}
		return fmt.Errorf("%s: %w", i18n.T("deploy.rollback", err), err)
	}

	// Point of no return: traffic is on the new slot. Roll workers and stop the old app.
	if err := p.WriteComposeEnv(blue, green, image); err != nil {
		return err
	}
	if p.Framework == project.Laravel && (p.Services.Queue || p.Services.Scheduler || p.Services.Reverb) {
		var workers []string
		if p.Services.Queue {
			workers = append(workers, "queue")
		}
		if p.Services.Scheduler {
			workers = append(workers, "scheduler")
		}
		if p.Services.Reverb {
			workers = append(workers, "reverb")
		}
		if err := compose(ctx, p, append([]string{"up", "-d"}, workers...)...); err != nil {
			fmt.Fprintf(os.Stderr, "warning: worker roll failed: %v\n", err)
		}
	}
	if !firstDeploy {
		fmt.Println(i18n.T("deploy.cleanup"))
		oldSvc := "app_" + p.ActiveSlot
		if err := compose(ctx, p, "--profile", p.ActiveSlot, "rm", "-sf", oldSvc); err != nil {
			fmt.Fprintf(os.Stderr, "warning: old slot cleanup failed: %v\n", err)
		}
	}

	prevSHA := p.CurrentSHA
	p.ActiveSlot = target
	p.CurrentSHA = sha
	if err := p.Save(); err != nil {
		return err
	}
	_ = gitx.PruneReleases(p, cfg.KeepReleases)
	if prevSHA != "" && prevSHA != sha {
		pruneOldImages(ctx, p, cfg.KeepReleases)
	}

	fmt.Println(i18n.T("deploy.ok", p.Name, short(sha)))
	return nil
}

// compose runs `docker compose` against the project's stack.
func compose(ctx context.Context, p *project.Project, args ...string) error {
	base := []string{"compose", "--project-directory", p.Dir()}
	return execx.Run(ctx, "docker", append(base, args...)...)
}

// ensureLaravelShared creates the persistent storage skeleton and env file.
func ensureLaravelShared(p *project.Project) error {
	for _, d := range []string{
		"storage/app/public",
		"storage/framework/cache/data",
		"storage/framework/sessions",
		"storage/framework/views",
		"storage/logs",
	} {
		if err := os.MkdirAll(filepath.Join(p.SharedDir(), d), 0o775); err != nil {
			return err
		}
	}
	// The container runs as www-data (uid 33); storage must be writable by it.
	_ = filepath.WalkDir(filepath.Join(p.SharedDir(), "storage"), func(path string, _ os.DirEntry, err error) error {
		if err == nil {
			_ = os.Chown(path, 33, 33)
		}
		return nil
	})
	envPath := filepath.Join(p.SharedDir(), ".env")
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		return os.WriteFile(envPath, []byte(p.DefaultEnv()), 0o640)
	}
	return nil
}

// pruneOldImages removes peyk-<name>:* images not referenced by kept releases.
func pruneOldImages(ctx context.Context, p *project.Project, keep int) {
	out, err := execx.Output(ctx, "docker", "images", "peyk-"+p.Name, "--format", "{{.Repository}}:{{.Tag}}")
	if err != nil {
		return
	}
	kept := map[string]bool{p.ImageTag(p.CurrentSHA): true}
	if entries, err := os.ReadDir(p.ReleasesDir()); err == nil {
		for _, e := range entries {
			kept[p.ImageTag(e.Name())] = true
		}
	}
	for _, img := range strings.Split(out, "\n") {
		img = strings.TrimSpace(img)
		if img != "" && !kept[img] {
			_ = execx.Run(ctx, "docker", "rmi", img)
		}
	}
}

// lock takes an exclusive non-blocking flock on the project's deploy lock.
func lock(p *project.Project) (func(), error) {
	if err := os.MkdirAll(p.Dir(), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(p.Dir(), ".deploy.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another deploy of %s is already running", p.Name)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
