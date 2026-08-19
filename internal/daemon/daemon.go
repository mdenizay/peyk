// Package daemon runs peyk's long-lived webhook listener.
//
// It binds to loopback only; Caddy proxies https://<domain>/_peyk/hooks/<name>
// to it. Every request must carry a valid X-Hub-Signature-256 HMAC computed
// with that project's individual secret.
package daemon

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mdenizay/peyk/internal/config"
	"github.com/mdenizay/peyk/internal/deploy"
	"github.com/mdenizay/peyk/internal/project"
)

const maxBody = 1 << 20 // 1 MiB: GitHub push payloads are far smaller

// Serve blocks, listening for webhooks on cfg.ListenAddr.
func Serve(cfg config.Config) error {
	d := &daemon{cfg: cfg, pending: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /_peyk/hooks/{name}", d.handleHook)
	mux.HandleFunc("GET /_peyk/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	log.Printf("peyk daemon listening on %s", cfg.ListenAddr)
	return srv.ListenAndServe()
}

type daemon struct {
	cfg config.Config

	mu      sync.Mutex
	pending map[string]bool // project name → deploy queued/running
}

type pushPayload struct {
	Ref   string `json:"ref"`
	After string `json:"after"`
}

func (d *daemon) handleHook(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !project.ValidName(name) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	p, err := project.Load(name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !validSignature(p.WebhookSecret, r.Header.Get("X-Hub-Signature-256"), body) {
		log.Printf("hook %s: invalid signature from %s", name, r.RemoteAddr)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	if event == "ping" {
		fmt.Fprintln(w, "pong")
		return
	}
	if event != "push" {
		fmt.Fprintln(w, "ignored event")
		return
	}
	var payload pushPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if payload.Ref != "refs/heads/"+p.Branch {
		fmt.Fprintf(w, "ignored ref %s\n", payload.Ref)
		return
	}
	if strings.HasPrefix(payload.After, "000000000000") {
		fmt.Fprintln(w, "ignored branch deletion")
		return
	}

	d.mu.Lock()
	already := d.pending[name]
	d.pending[name] = true
	d.mu.Unlock()
	if already {
		fmt.Fprintln(w, "deploy already queued")
		return
	}

	go func() {
		defer func() {
			d.mu.Lock()
			delete(d.pending, name)
			d.mu.Unlock()
		}()
		log.Printf("hook %s: deploying %s", name, payload.After)
		fresh, err := project.Load(name)
		if err != nil {
			log.Printf("hook %s: %v", name, err)
			return
		}
		// context.Background(): the deploy must outlive the HTTP request.
		if err := deploy.Run(context.Background(), d.cfg, fresh); err != nil {
			log.Printf("hook %s: deploy failed: %v", name, err)
			return
		}
		log.Printf("hook %s: deploy done", name)
	}()
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintln(w, "deploy queued")
}

// validSignature checks GitHub's X-Hub-Signature-256 in constant time.
func validSignature(secret, header string, body []byte) bool {
	if secret == "" || !strings.HasPrefix(header, "sha256=") {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(strings.TrimPrefix(header, "sha256=")))
}
