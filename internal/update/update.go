// Package update implements checksum-verified self-update from GitHub Releases.
package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mdenizay/peyk/internal/config"
	"github.com/mdenizay/peyk/internal/execx"
	"github.com/mdenizay/peyk/internal/i18n"
)

const repo = "mdenizay/peyk"

type release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		ID   int64  `json:"id"`
		URL  string `json:"url"` // API asset URL; works for private repos with a token
	} `json:"assets"`
}

// Run checks the latest release and replaces the running binary if newer.
// ifNeeded suppresses the "already latest" as an error path for timers.
func Run(ctx context.Context, cfg config.Config, current string, ifNeeded bool) error {
	fmt.Println(i18n.T("update.checking"))
	rel, err := latest(ctx, cfg.GitHubToken)
	if err != nil {
		return err
	}
	if rel.TagName == "" || strings.TrimPrefix(rel.TagName, "v") == strings.TrimPrefix(current, "v") {
		fmt.Println(i18n.T("update.latest", current))
		return nil
	}
	fmt.Println(i18n.T("update.found", rel.TagName, current))

	assetName := fmt.Sprintf("peyk_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	var assetURL, checksumsURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case assetName:
			assetURL = a.URL
		case "checksums.txt":
			checksumsURL = a.URL
		}
	}
	if assetURL == "" || checksumsURL == "" {
		return fmt.Errorf("release %s is missing %s or checksums.txt", rel.TagName, assetName)
	}

	tmpDir, err := os.MkdirTemp("", "peyk-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	archive := filepath.Join(tmpDir, assetName)
	if err := download(ctx, cfg.GitHubToken, assetURL, archive); err != nil {
		return err
	}
	sums := filepath.Join(tmpDir, "checksums.txt")
	if err := download(ctx, cfg.GitHubToken, checksumsURL, sums); err != nil {
		return err
	}
	if err := verifyChecksum(archive, assetName, sums); err != nil {
		return err
	}

	newBin := filepath.Join(tmpDir, "peyk")
	if err := extractBinary(archive, "peyk", newBin); err != nil {
		return err
	}
	if err := os.Chmod(newBin, 0o755); err != nil {
		return err
	}

	target := config.BinPath()
	if self, err := os.Executable(); err == nil && !strings.HasPrefix(self, os.TempDir()) {
		target = self
	}
	// Atomic replace: copy next to target, then rename over it.
	staged := target + ".new"
	if err := copyFile(newBin, staged, 0o755); err != nil {
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		os.Remove(staged)
		return err
	}
	fmt.Println(i18n.T("update.ok", rel.TagName))

	// Restart the daemon so it runs the new binary (no-op if not installed).
	_ = execx.Run(ctx, "systemctl", "try-restart", "peyk")
	return nil
}

func latest(ctx context.Context, token string) (release, error) {
	var rel release
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://api.github.com/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return rel, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient().Do(req)
	if err != nil {
		return rel, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return rel, fmt.Errorf("release lookup failed: %s: %s", resp.Status, string(b))
	}
	return rel, json.NewDecoder(resp.Body).Decode(&rel)
}

func download(ctx context.Context, token, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/octet-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func verifyChecksum(file, name, sumsPath string) error {
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	var want string
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			want = fields[0]
			break
		}
	}
	if want == "" {
		return fmt.Errorf("checksums.txt has no entry for %s", name)
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s want %s — refusing to install", name, got, want)
	}
	return nil
}

func extractBinary(archive, name, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("binary %q not found in archive", name)
		}
		if err != nil {
			return err
		}
		if filepath.Base(hdr.Name) == name && hdr.Typeflag == tar.TypeReg {
			out, err := os.Create(dest)
			if err != nil {
				return err
			}
			// The release archive is our own; size is bounded by the header.
			if _, err := io.CopyN(out, tr, hdr.Size); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		}
	}
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func httpClient() *http.Client { return &http.Client{Timeout: 5 * time.Minute} }
