# Peyk

> **peyk** *(isim, Farsça)*: haberci, uydu — sunucularınla aranda dolaşan hızlı ulak.

Ubuntu sunucularında **Laravel** ve **Next.js** projelerini güvenli, izole ve kesintisiz şekilde
deploy etmek için tek binary'lik bir araç. Go ile yazılmıştır; her proje kendi Docker Compose
stack'inde çalışır, edge'de Caddy otomatik SSL sağlar.

```bash
curl -fsSL https://raw.githubusercontent.com/mdenizay/peyk/main/install.sh | sudo bash
```

## Özellikler

- **Tek binary** — CLI + interaktif TUI + webhook sunucusu tek `peyk` binary'sinde
- **Proje izolasyonu** — her proje kendi compose stack'i, kendi Docker network'ü, kendi
  (opsiyonel) PostgreSQL + Redis container'ları. Birine bulaşan diğerine bulaşamaz.
- **Kesintisiz deployment** — yeni sürüm ayağa kalkar → health check → Caddy trafiği çevirir →
  eski sürüm kapanır. Başarısız deploy otomatik geri alınır.
- **Otomatik SSL** — Caddy, Let's Encrypt ile domain başına sertifikayı kendisi yönetir.
  Çoklu domain desteklenir.
- **GitHub entegrasyonu** — proje başına ayrı read-only deploy key, push'ta HMAC imzalı
  webhook ile otomatik deploy.
- **Laravel birinci sınıf** — queue worker, scheduler (cron), Reverb (websocket), migrate,
  storage link, config/route cache — hepsi compose stack'inin parçası.
- **Kurulum sihirbazı** — sıfır Ubuntu 22.04/24.04 üzerinde TUI ile adım adım kurulum;
  her sertleştirme/optimizasyon adımı açıklamasıyla birlikte seçilebilir. Kurulum yarıda
  kesilirse kaldığı yerden devam eder.
- **Türkçe / İngilizce** — kurulum ve arayüz iki dilde.
- **Otomatik güncelleme** — `peyk self-update`, GitHub Releases'tan imza/checksum doğrulayarak
  kendini günceller; istenirse otomatik.

## Hızlı bakış

```bash
peyk                      # interaktif TUI
peyk new                  # yeni proje sihirbazı (GitHub repo listesinden seç)
peyk deploy blog          # manuel deploy
peyk list                 # projeler ve durumları
peyk logs blog --follow   # uygulama logları
peyk db backup blog       # veritabanı yedeği
peyk self-update          # peyk'i güncelle
```

Ayrıntılı mimari için [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Lisans

Tüm hakları saklıdır.
