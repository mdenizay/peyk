// Package gitx handles per-project deploy keys and git operations.
package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mdenizay/peyk/internal/config"
	"github.com/mdenizay/peyk/internal/execx"
	"github.com/mdenizay/peyk/internal/project"
)

// EnsureDeployKey generates an ed25519 deploy key for the project if missing
// and returns the public key line to add to GitHub.
func EnsureDeployKey(ctx context.Context, p *project.Project) (string, error) {
	if err := os.MkdirAll(config.KeysDir(), 0o700); err != nil {
		return "", err
	}
	key := p.KeyPath()
	if _, err := os.Stat(key); os.IsNotExist(err) {
		if err := execx.Run(ctx, "ssh-keygen", "-t", "ed25519", "-N", "", "-C", "peyk-deploy-"+p.Name, "-f", key); err != nil {
			return "", err
		}
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		return "", err
	}
	return string(pub), nil
}

// sshEnv returns GIT_SSH_COMMAND pinned to the project's deploy key.
func sshEnv(p *project.Project) []string {
	return []string{fmt.Sprintf(
		"GIT_SSH_COMMAND=ssh -i %s -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new",
		p.KeyPath(),
	)}
}

// bareDir is the project's persistent mirror clone, fetched on each deploy.
func bareDir(p *project.Project) string { return filepath.Join(p.Dir(), "repo.git") }

// FetchAndCheckout updates the mirror and materializes branch HEAD into a new
// release directory, returning (releaseDir, sha).
func FetchAndCheckout(ctx context.Context, p *project.Project) (string, string, error) {
	bare := bareDir(p)
	env := sshEnv(p)
	if _, err := os.Stat(bare); os.IsNotExist(err) {
		if err := execx.RunEnv(ctx, env, "git", "clone", "--bare", p.Repo, bare); err != nil {
			return "", "", err
		}
	}
	if err := execx.RunEnv(ctx, env, "git", "--git-dir", bare, "fetch", "origin",
		fmt.Sprintf("+refs/heads/%s:refs/heads/%s", p.Branch, p.Branch)); err != nil {
		return "", "", err
	}
	sha, err := execx.Output(ctx, "git", "--git-dir", bare, "rev-parse", p.Branch)
	if err != nil {
		return "", "", err
	}
	releaseDir := filepath.Join(p.ReleasesDir(), sha)
	if _, err := os.Stat(releaseDir); err == nil {
		return releaseDir, sha, nil // already checked out
	}
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		return "", "", err
	}
	// git archive | tar keeps the release dir free of .git metadata.
	if err := execx.Run(ctx, "bash", "-c",
		fmt.Sprintf("git --git-dir=%q archive %q | tar -x -C %q", bare, sha, releaseDir)); err != nil {
		os.RemoveAll(releaseDir)
		return "", "", err
	}
	return releaseDir, sha, nil
}

// PruneReleases removes old release directories, keeping the newest keep.
func PruneReleases(p *project.Project, keep int) error {
	entries, err := os.ReadDir(p.ReleasesDir())
	if err != nil {
		return nil
	}
	type rel struct {
		name string
		mod  int64
	}
	var rels []rel
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		rels = append(rels, rel{e.Name(), info.ModTime().UnixNano()})
	}
	if len(rels) <= keep {
		return nil
	}
	for i := 0; i < len(rels); i++ {
		for j := i + 1; j < len(rels); j++ {
			if rels[j].mod > rels[i].mod {
				rels[i], rels[j] = rels[j], rels[i]
			}
		}
	}
	for _, r := range rels[keep:] {
		if r.name == p.CurrentSHA {
			continue
		}
		os.RemoveAll(filepath.Join(p.ReleasesDir(), r.name))
	}
	return nil
}
