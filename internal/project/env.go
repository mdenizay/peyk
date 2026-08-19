package project

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

// DefaultEnv renders an initial application .env for the project, wired to
// the services in its compose stack. Written once; the user edits afterwards.
func (p *Project) DefaultEnv() string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	switch p.Framework {
	case Laravel:
		w("APP_NAME=%s", p.Name)
		w("APP_ENV=production")
		w("APP_KEY=base64:%s", randKey())
		w("APP_DEBUG=false")
		if len(p.Domains) > 0 {
			w("APP_URL=https://%s", p.Domains[0])
		}
		w("")
		w("LOG_CHANNEL=stderr")
		w("LOG_LEVEL=warning")
		w("")
		if p.Services.Postgres {
			w("DB_CONNECTION=pgsql")
			w("DB_HOST=postgres")
			w("DB_PORT=5432")
			w("DB_DATABASE=%s", p.Name)
			w("DB_USERNAME=%s", p.Name)
			w("DB_PASSWORD=%s", p.DBPassword)
			w("")
		}
		if p.Services.Redis {
			w("REDIS_HOST=redis")
			w("REDIS_PASSWORD=%s", p.RedisPassword)
			w("REDIS_PORT=6379")
			w("CACHE_STORE=redis")
			w("SESSION_DRIVER=redis")
			if p.Services.Queue {
				w("QUEUE_CONNECTION=redis")
			}
			w("")
		} else if p.Services.Queue {
			w("QUEUE_CONNECTION=database")
			w("")
		}
		if p.Services.Reverb {
			w("BROADCAST_CONNECTION=reverb")
			w("REVERB_APP_ID=%s", p.Name)
			w("REVERB_APP_KEY=%s", NewSecret()[:32])
			w("REVERB_APP_SECRET=%s", NewSecret()[:32])
			w("REVERB_HOST=%s", firstOr(p.Domains, "localhost"))
			w("REVERB_PORT=443")
			w("REVERB_SCHEME=https")
			w("")
		}
	case NextJS:
		w("NODE_ENV=production")
		if len(p.Domains) > 0 {
			w("NEXT_PUBLIC_APP_URL=https://%s", p.Domains[0])
		}
	}
	return b.String()
}

func firstOr(s []string, def string) string {
	if len(s) > 0 {
		return s[0]
	}
	return def
}

func randKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}
