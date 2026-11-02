# deploy/ — Dağıtım ve Geliştirme Ortamı

## Geliştirme (Faz 01)

`docker-compose.yml` yalnızca **PostgreSQL 18** kaldırır. Control Plane ve UI lokalde çalışır:

```bash
# 1) Ortam değişkenleri
cp .env.example .env   # değerleri düzenleyin (.env gitignored)

# 2) Veritabanı
docker compose up -d
docker compose ps      # healthy bekleyin

# 3) Migration (explicit, otomatik çalışmaz)
cd ../backend/control-plane
export CP_DATABASE_URL="postgres://serverctl:...@localhost:5432/server_control?sslmode=disable"
go run ./cmd/migrate status
go run ./cmd/migrate up

# 4) Control Plane
go run ./cmd/control-plane
# healthz:  http://127.0.0.1:8080/healthz
# readyz:   http://127.0.0.1:8080/readyz  (DB up → ready)
#
# Faz 09: DNS/SSL için ek ortam değişkenleri (hepsi OPSİYONEL; ayarsız
# ilgili aile dürüstçe tipli hata verir — sessiz fallback yoktur):
#   CP_SECRETS_KEY        base64 32 bayt AES-256 anahtarı (sağlayıcı
#                         kimlik bilgileri + sertifika materyali için
#                         ZORUNLU; üretimi: openssl rand -base64 32)
#   CP_ACME_ACCOUNT_KEY   ACME hesap anahtarı dosya yolu (yoksa P-256
#                         üretir, 0600)
#   CP_ACME_DIRECTORY_URL ACME dizin URL'si — test için LE STAGING:
#                         https://acme-staging-v02.api.letsencrypt.org/directory
#   CP_ACME_CONTACT       mailto:ops@example.com
# Ayrıntılar: docs/architecture/dns-ssl.md + ADR-0013.

# 5) UI (ayrı terminal)
cd ../../web && npm install && npm run dev
```

## Agent: web hosting + TLS sertifikaları (Faz 08/09)

`server-privd` gerçek sistem mutasyonlarını (web hosting: kullanıcı/config/reload;
TLS: anahtar üretimi, sertifika sahneleme, nginx reload) yalnız opt-in ile yapar:

```bash
server-privd --web-enable ...   # VARSAYILAN KAPALI — her iki aile için tek kapı

### Faz 10: veritabanı ve dosya yöneticisi kapıları

```
server-privd --db-enable   # VARSAYILAN KAPALI — node-local PostgreSQL/MariaDB-MySQL yönetimi
server-privd --fs-enable   # VARSAYILAN KAPALI — scoped dosya yöneticisi (yalnız site docroot'ları)
```

- db ailesi yalnız node-local unix soketlerine dokunur; panelin kendi PostgreSQL'i yapısal olarak erişilemez (ADR-0014). Korumalı adlar (sistem + panel DB'leri) tipli red ile korunur.
- DB kullanıcı şifresi node'da üretilir, control-plane'de AES-256-GCM seal edilir ve TEK SEFERLİK reveal edilir — `CP_SECRETS_KEY` ayarlı OLMALI (ayarsızsa şifre kalıcı saklanmaz ve dürüstçe redakte edilir).
- Dosya yöneticisi kökleri yalnız web hosting site docroot'larıdır (`/var/www/server-control/<site>/html`); upload/indirme 512 KiB ile sınırlıdır.
```

Kapalıyken ilgili operasyonlar tipli `unsupported_operation` döndürür (sessiz
kalabalık yok). TLS özel anahtarı yalnız bu broker'da doğar ve node'dan asla
çıkmaz (ADR-0013).

## Güvenlik notları

- PostgreSQL portu yalnızca `127.0.0.1`'e bağlanır.
- İmaj, tag + sha256 digest ile sabitlenmiştir (sürüm politikası); güncellerken digest'i yenileyin ve değişikliği kaydedin.
- `.env` dosyası repoya girmez; `.env.example` yalnızca placeholder içerir.
- Bu compose dosyası üretim dağıtımı değildir; üretim topolojisi Faz 15'te paketlenir.
