# backend/control-plane

Control Plane — Go modüler monolit REST API (Faz 01 iskeleti).

## Komutlar

```bash
go build ./...          # derle
go test ./...           # birim testler
go test -race ./...     # yarış dedektörlü testler
go vet ./...            # statik analiz
gofmt -l .              # biçim kontrolü (çıktı boş olmalı)
```

## Entegrasyon testleri (PostgreSQL gerekir)

`TEST_DATABASE_URL` tanımlıysa migration runner testleri gerçek PostgreSQL 18
üzerinde çalışır; tanımsızsa açıkça `SKIP` olur (sessiz geçilmez):

```bash
TEST_DATABASE_URL="postgres://serverctl:...@localhost:5432/server_control?sslmode=disable" go test -race ./internal/db/migrate/
```

## API (Faz 01)

| Yol | Amaç |
|---|---|
| `GET /healthz` | Süreç canlılığı (bağımlılık sorgulamaz) |
| `GET /readyz` | Hazırlık; DB erişilebilirliğini raporlar (503 = degraded) |
| `GET /api/v1/version` | Sürüm/derleme bilgisi |

Hata zarfı: `{code, message, details, correlation_id, retryable}` —
`docs/architecture/api-and-protocol.md` gereği. Query string'ler loglanmaz;
`CP_DATABASE_URL` loglarda maskelenir.

## Yapı

- `cmd/control-plane` — sunucu girişi (graceful shutdown)
- `cmd/migrate` — explicit migration runner CLI (`up` / `down` / `status`)
- `internal/config` — env yapılandırma + doğrulama + secret maskeleme
- `internal/httpapi` — chi router, middleware zinciri, health uçları
- `internal/db` — pgx havuzu
- `internal/db/migrate` — advisory-locked, transactional migration motoru
- `db/migrations` — gömülü SQL migration dosyaları (`NNNN_name.up/down.sql`)
