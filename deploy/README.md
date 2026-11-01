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

# 5) UI (ayrı terminal)
cd ../../web && npm install && npm run dev
```

## Güvenlik notları

- PostgreSQL portu yalnızca `127.0.0.1`'e bağlanır.
- İmaj, tag + sha256 digest ile sabitlenmiştir (sürüm politikası); güncellerken digest'i yenileyin ve değişikliği kaydedin.
- `.env` dosyası repoya girmez; `.env.example` yalnızca placeholder içerir.
- Bu compose dosyası üretim dağıtımı değildir; üretim topolojisi Faz 15'te paketlenir.
