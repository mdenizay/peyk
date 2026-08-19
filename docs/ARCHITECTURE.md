# Peyk Mimari Dokümanı

## 1. Genel bakış

Peyk, Ubuntu sunucusunda çalışan tek bir Go binary'sidir. Üç modda çalışır:

1. **CLI** — `peyk deploy blog` gibi scriptlenebilir komutlar (cobra)
2. **TUI** — `peyk` yazınca açılan interaktif arayüz (bubbletea)
3. **Daemon** — `peyk serve`: systemd altında çalışan webhook dinleyici + zamanlanmış işler

```
                        ┌──────────────────────── Ubuntu sunucu ────────────────────────┐
                        │                                                               │
 GitHub push ──webhook──┼──► peyk daemon (systemd, :8443 loopback→Caddy)                │
                        │        │ deploy pipeline                                      │
                        │        ▼                                                      │
 İnternet ──:80/:443──► │   Caddy (container, admin API)                                │
                        │        │ reverse proxy (peyk-edge network)                    │
                        │   ┌────┴─────────┬───────────────┐                            │
                        │   ▼              ▼               ▼                            │
                        │  blog stack     shop stack      panel stack                   │
                        │  (kendi net)    (kendi net)     (kendi net)                   │
                        │  app,queue,     app,pg,redis    app (next.js)                 │
                        │  cron,reverb,                                                 │
                        │  pg,redis                                                     │
                        └───────────────────────────────────────────────────────────────┘
```

## 2. Dizin yerleşimi (sunucu)

```
/opt/peyk/
├── bin/peyk                    # binary
├── caddy/                      # edge stack (compose + Caddyfile taban)
└── apps/<proje>/
    ├── peyk.json               # proje manifesti (peyk üretir/yönetir)
    ├── docker-compose.yml      # peyk üretir — elle düzenlenebilir alanlar işaretli
    ├── .env                    # uygulama env (600, root:root → container'a mount)
    ├── releases/<sha>/         # git checkout'ları (son N tutulur)
    ├── current -> releases/x   # aktif sürüm symlink'i
    └── shared/                 # storage/, .env gibi kalıcı veriler
/var/lib/peyk/
├── state.json                  # global durum (kurulum resume dahil)
├── keys/<proje>_deploy_key     # SSH deploy key'ler (600)
└── backups/
/etc/peyk/config.json           # global konfig
```

## 3. Kurulum akışı (`install.sh` + `peyk setup`)

`install.sh` bilinçli olarak **küçük** tutulur; işi yalnızca:

1. Ubuntu 22.04/24.04 doğrulaması, root kontrolü
2. Mimarinin algılanması (amd64/arm64), en son release binary'sinin indirilmesi
3. **SHA256 checksum doğrulaması** (checksums dosyası release'e eklenir)
4. `peyk setup` komutunu başlatmak

Asıl kurulum sihirbazı Go tarafındadır (`peyk setup`): dil seçimi (TR/EN), sonra
**seçilebilir, açıklamalı adımlar** listesi. Her adım idempotent'tir ve
`/var/lib/peyk/state.json`'a işlenir → kurulum yarıda kesilirse `peyk setup` kaldığı
adımdan devam eder.

### Kurulum adımları (her biri açıklamalı + seçilebilir)

| Adım | Varsayılan | Açıklama |
|---|---|---|
| `system-update` | ✅ | apt update && upgrade |
| `docker` | ✅ (zorunlu) | Docker Engine + Compose plugin resmi repo'dan |
| `unattended-upgrades` | ✅ | Otomatik güvenlik yamaları |
| `firewall` | ✅ | ufw: yalnız 22, 80, 443 açık; Docker-ufw bypass düzeltmesi (DOCKER-USER chain) |
| `fail2ban` | ✅ | SSH brute-force koruması |
| `ssh-hardening` | ✅ | PasswordAuthentication no, PermitRootLogin prohibit-password vb. (mevcut key kontrol edilerek, kullanıcıyı kilitlemeden) |
| `sysctl-tuning` | ✅ | somaxconn, tcp_fin_timeout, swappiness, fs.inotify vb. |
| `swap` | ✅ | RAM'e göre swap dosyası (yoksa) |
| `sysstat/limits` | ⬜ | nofile limitleri, journald boyut sınırı |
| `caddy-edge` | ✅ (zorunlu) | Caddy container + peyk-edge network |
| `peyk-daemon` | ✅ (zorunlu) | systemd unit + webhook secret üretimi |
| `auto-update` | ⬜ | peyk'in kendini otomatik güncellemesi (systemd timer) |

## 4. Proje yaşam döngüsü

### `peyk new`
1. GitHub repo listesi (`gh` token varsa API'den; yoksa URL elle girilir)
2. Framework algılama (composer.json → Laravel, package.json+next → Next.js)
3. Servis seçimi: PostgreSQL? Redis? Queue? Scheduler? Reverb?
4. Domain(ler) girilir → Caddy'ye route eklenir
5. Deploy key üretilir → GitHub'a eklenir (token varsa otomatik, yoksa kopyala-yapıştır)
6. Webhook oluşturulur (proje başına ayrı HMAC secret)
7. compose.yml + .env üretilir, ilk deploy başlar

### Deploy pipeline (kesintisiz)
```
webhook/manuel → git fetch (deploy key) → releases/<sha> checkout
→ image build (Laravel: serversideup/php taban; Next: standalone çıktı)
→ app_new container'ı ayağa kalkar (aynı network)
→ Laravel ise: migrate --force (advisory lock ile), cache'ler
→ health check (HTTP /up veya konfigüre edilen endpoint, timeout'lu)
→ Caddy upstream'i yeni container'a çevrilir (admin API, anlık)
→ eski container graceful stop → queue/cron/reverb yeni image ile restart
→ başarısızsa: yeni container silinir, Caddy dokunulmaz → rollback tamam
```

### İzolasyon modeli
- Proje başına ayrı bridge network; yalnız `app` servisi `peyk-edge`'e de bağlanır.
- DB/Redis yalnız proje network'ünde, host'a port açılmaz.
- Container'lar non-root kullanıcıyla çalışır; `no-new-privileges`, bellek/CPU limitleri
  compose'da tanımlı.
- .env dosyaları 600; loglarda secret maskeleme.

## 5. Güvenlik ilkeleri

- Webhook: HMAC-SHA256 imza doğrulaması + timestamp toleransı; proje başına ayrı secret.
- Deploy key'ler read-only, proje başına ayrı; sızıntı tek repoya sınırlı kalır.
- peyk daemon dışarıya doğrudan port açmaz; webhook trafiği Caddy üzerinden
  (`hooks.domain.com` veya `/_peyk/hooks/*` path'i) loopback'e proxy'lenir.
- self-update: yalnız `mdenizay/peyk` release'lerinden, SHA256 + (ileride minisign imzası)
  doğrulanarak; başarısız doğrulamada eski binary korunur.
- Tüm shell çağrıları argüman dizisiyle (`exec.Command`), string interpolasyonlu shell yok.

## 6. Go paket yapısı

```
main.go
internal/
├── cli/        # cobra komutları
├── tui/        # bubbletea ekranları (dashboard, setup sihirbazı, new sihirbazı)
├── i18n/       # tr/en katalogları
├── config/     # /etc/peyk/config.json + state.json (resume)
├── sysinfo/    # OS/RAM/disk algılama
├── setup/      # kurulum adımları (Step interface: Id, Detect, Apply, Explain)
├── project/    # manifest, framework algılama, compose üretimi (template'ler embed)
├── dockerx/    # docker/compose exec sarmalayıcı
├── caddy/      # admin API istemcisi
├── githubx/    # deploy key, webhook, repo listeleme
├── deploy/     # pipeline + rollback
├── daemon/     # webhook HTTP sunucusu + timer'lar
└── update/     # self-update
```

## 7. Sürümleme ve dağıtım

- GitHub Actions: tag push'ta (`v*`) goreleaser ile linux/amd64 + linux/arm64 build,
  checksums, release.
- `install.sh` her zaman "latest release"i kurar; `peyk self-update` aynı mekanizmayı kullanır.

## 8. Yol haritası (fazlar)

- **Faz 1 (MVP):** setup sihirbazı (resume'lu), Caddy edge, Laravel projesi new/deploy
  (kesintisiz), manuel + webhook deploy, TR/EN
- **Faz 2:** Next.js desteği, Reverb, `peyk db backup/restore`, self-update, repo listeleme
- **Faz 3:** metrics/health dashboard (TUI), log takibi, çoklu sunucu (uzak peyk'lere komut)
