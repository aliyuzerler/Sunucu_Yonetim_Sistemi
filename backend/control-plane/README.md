# backend/control-plane

Control Plane — Go modüler monolit: REST API + gRPC agent sunucusu (outbound mTLS) + CLI yardımcıları. 16 fazlık yol haritasının sunucu tarafı bu modüldedir (kimlik/RBAC'tan eklenti ve imzalı güncelleme hattına).

## Komutlar

```bash
go build ./...          # derle
go test ./...           # birim testler
go test -race ./...     # yarış dedektörlü testler (CI kapısı)
go vet ./...            # statik analiz
gofmt -l .              # biçim kontrolü (çıktı boş olmalı)
```

## Entegrasyon testleri (PostgreSQL gerekir)

`TEST_DATABASE_URL` tanımlıysa mağaza/migration testleri gerçek PostgreSQL 18
üzerinde çalışır; tanımsızsa açıkça `SKIP` olur (sessiz geçilmez):

```bash
TEST_DATABASE_URL="postgres://serverctl:...@localhost:5432/server_control?sslmode=disable" go test -race ./...
```

CI'da backend job'ı 20/20 paketi `-race` ile gerçek PostgreSQL'e karşı koşturur.

## Çalıştırma ve CLI alt komutları

```bash
go run ./cmd/control-plane                 # API: http://127.0.0.1:8080
go run ./cmd/control-plane bootstrap       # tek kullanımlık ilk yönetici tokenı
go run ./cmd/control-plane ca              # agent CA sertifikası üretimi
go run ./cmd/control-plane enroll-token --scope group:<uuid> --ttl 15m
go run ./cmd/control-plane verify-update <artifact>   # imzalı update artifact doğrulama
go run ./cmd/migrate status|up|down        # explicit migration (otomatik çalışmaz)
go run ./cmd/sigtool keygen|sign|verify    # SCP-SIG-V1 ed25519 sürüm imzaları
```

## API yüzeyi

| Yol | Amaç |
|---|---|
| `GET /healthz` | Süreç canlılığı (bağımlılık sorgulamaz) |
| `GET /readyz` | Hazırlık; DB erişilebilirliğini raporlar (503 = degraded) |
| `GET /api/v1/version` | Sürüm/derleme bilgisi (APIVersion: 1.0.0 — eklenti uyumluluk sözleşmesi) |
| `GET /metrics` | Prometheus scrape yüzeyi (`CP_METRICS_TOKEN` Bearer zorunlu; fail-closed) |
| `/api/v1/...` | Alan bazlı REST: kimlik/oturum, node/görev, DNS/SSL, web, DB/fs, mail, VM, backup/bulut, izleme/log/alarm/güvenlik merkezi, eklenti/update |

Agent tarafı ayrıca gRPC `:9090` üzerinde `servercontrol.agent.v1`
(Enroll + Connect bidi stream, outbound mTLS) servis eder.

Tam uç nokta listesi için `internal/httpapi/server.go` +
`routes_regression_test.go` (route regresyon kapısı); sözleşme:
`docs/architecture/api-and-protocol.md`.

Hata zarfı: `{code, message, details, correlation_id, retryable}` —
`docs/architecture/api-and-protocol.md` gereği. Query string'ler loglanmaz;
`CP_DATABASE_URL` loglarda maskelenir.

## Yapı

- `cmd/control-plane` — sunucu girişi (graceful shutdown) + CLI alt komutları
- `cmd/migrate` — explicit migration runner CLI (`up` / `down` / `status`)
- `cmd/sigtool` — SCP-SIG-V1 ed25519 imza aracı (keygen/sign/verify)
- `internal/httpapi` — chi router, middleware zinciri, REST handler'ları (alan bazlı dosyalar)
- `internal/agentsrv` — gRPC agent sunucusu (mTLS, hello/heartbeat/facts/metrics, çoklu-node)
- `internal/identity` · `internal/rbac` · `internal/enroll` — kimlik/oturum/bootstrap, izin modeli, node kayıt tokenları
- `internal/inventory` · `internal/telemetry` — node envanteri ve telemetri kabulü
- `internal/tasks` — görev motoru (outbox, idempotency, retry/cancellation) + aile operasyonları (`ops_faz05`–`ops_faz15`)
- `internal/audit` — audit olayları + log redaksiyonu
- `internal/dns` · `internal/certmgmt` · `internal/acmex` · `internal/certs` — DNS sağlayıcı adaptörleri, sertifika yönetimi, ACME DNS-01 orkestrasyonu
- `internal/secrets` — AES-256-GCM sealed secret deposu (`CP_SECRETS_KEY` zorunlu)
- `internal/dbcred` · `internal/backup` · `internal/cloud` — DB kimlik bilgileri (tek seferlik reveal), yedekleme/geri yükleme, bulut sağlayıcı adaptörleri
- `internal/extensions` · `internal/updates` · `internal/signpkg` — eklenti sözleşmesi (SCP-EXT-MANIFEST-V1), imzalı güncelleme hattı (ADR-0020), imza şeması
- `internal/monitoring` · `internal/alerts` · `internal/securitycenter` — metrik yüzeyi, alarm evaluator (60 sn), güvenlik merkezi kontrolleri
- `internal/db` + `internal/db/migrate` + `db/migrations` — pgx havuzu, advisory-locked transactional migration motoru, gömülü SQL (`0001`–`0019`)
- `internal/config` · `internal/platform` — env yapılandırma + doğrulama + secret maskeleme; sürüm/APIVersion sabitleri
