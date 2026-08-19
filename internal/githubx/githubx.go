// Package githubx talks to the GitHub REST API for optional automation:
// listing repositories, adding deploy keys, and creating webhooks. Everything
// here degrades gracefully — without a token, peyk prints copy-paste
// instructions instead.
package githubx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const apiBase = "https://api.github.com"

// Client is a minimal authenticated GitHub API client.
type Client struct {
	Token string
	HTTP  *http.Client
}

// New returns a client, or nil when no token is configured.
func New(token string) *Client {
	if token == "" {
		return nil
	}
	return &Client{Token: token, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github %s %s: %s: %s", method, path, resp.Status, string(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Repo is a minimal repository listing entry.
type Repo struct {
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	SSHURL        string `json:"ssh_url"`
	DefaultBranch string `json:"default_branch"`
	PushedAt      string `json:"pushed_at"`
}

// ListRepos returns the user's repositories, most recently pushed first.
func (c *Client) ListRepos(ctx context.Context, limit int) ([]Repo, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var repos []Repo
	err := c.do(ctx, "GET", fmt.Sprintf("/user/repos?sort=pushed&per_page=%d", limit), nil, &repos)
	return repos, err
}

// AddDeployKey adds a read-only deploy key to owner/repo.
func (c *Client) AddDeployKey(ctx context.Context, ownerRepo, title, publicKey string) error {
	in := map[string]any{"title": title, "key": publicKey, "read_only": true}
	return c.do(ctx, "POST", "/repos/"+ownerRepo+"/keys", in, nil)
}

// AddWebhook creates a push webhook with an HMAC secret on owner/repo.
func (c *Client) AddWebhook(ctx context.Context, ownerRepo, url, secret string) error {
	in := map[string]any{
		"name":   "web",
		"active": true,
		"events": []string{"push"},
		"config": map[string]any{
			"url":          url,
			"content_type": "json",
			"secret":       secret,
			"insecure_ssl": "0",
		},
	}
	return c.do(ctx, "POST", "/repos/"+ownerRepo+"/hooks", in, nil)
}
