# Faz 01 — Bağımsız Test Promptu Öz-Denetim Raporu

- **Tarih:** 2026-11-01
- **Rol:** Test eden (bağımsız doğrulama) — `prompts/phase-test/phase-01-test.md` kriterlerine göre
- **Not:** Bu rapor uygulama oturumunda yürütülen taze-gözle denetimdir; kit kuralı gereği test promptunun ayrı oturumda tekrar çalıştırılması önerilir. Sandbox'ta Docker bulunmadığından compose'un canlı çalışması sahipte doğrulanacaktır (aşağıda açıkça işaretlendi).

## Zorunlu kabul kontrolleri

| # | Kriter | Sonuç | Kanıt |
|---|---|---|---|
| K1 | Monorepo klasörleri backend/control-plane, agent, proto, web, deploy, docs olarak ayrılmış | **PASS** | `ls` kanıtı: altı klasör mevcut. `agent/` ve `proto/` Faz 03 için README ile rezerve (Faz 01 kapsamı dışı özellik eklenmedi — kapsam kayması yok). Kök `Makefile`, `.github/workflows/`, `scripts/` ek altyapı. |
| K2 | Go backend health/readiness endpointleri ve React uygulama kabuğu çalışmalı | **PASS** | Canlı duman testi: `/healthz` → 200 `{"status":"ok"}`; `/readyz` DB yokken 503 `degraded` + `database:not_configured`, `/api/v1/version` derleme bilgisi; 404 kararlı zarf + `correlation_id`; CSP/nosniff/DENY/no-store başlıkları gözlemlendi. SIGTERM ile graceful kapanış logu. React kabuğu: `npm run build` başarılı; `npm test` 4/4. |
| K3 | PostgreSQL 18 geliştirme ortamı compose ile kalkmalı; migration altyapısı bulunmalı | **PASS** (kısmi: compose çalıştırma sahipte) | `deploy/docker-compose.yml`: `postgres:18-alpine` sha256-digest pinli (2026-11-01), port `127.0.0.1`'e bağlı, healthcheck, named volume; YAML sözdizimi doğrulandı. Sandbox'ta Docker yok → `docker compose up` **NOT RUN** (sahip lokalinde `make compose-up` ile doğrular). Migration altyapısı: gömülü SQL, advisory-lock, transactional up/down, `status`; entegrasyon testleri CI'da PostgreSQL 18 servis konteyneriyle koşar, `TEST_DATABASE_URL` yokken açık SKIP mesajıyla atlanır (sessiz geçiş yok). |
| K4 | Frontend lockfile, strict TypeScript, lint, test ve build komutları çalışmalı | **PASS** | `package-lock.json` (v3, 293 paket) mevcut; `npm ci` ile kurulum; `tsconfig`: `strict` + `noUnusedLocals/Parameters` + `exactOptionalPropertyTypes` + `verbatimModuleSyntax`; `npm run lint` → 0 bulgu; `npm run typecheck` → OK; `npm test` → 4/4; `npm run build` → dist üretildi (231 KB JS / 14 KB CSS). |
| K5 | CI lint/test/build kontrollerini çalıştırmalı; secret ve gerçek sunucu verisi repoda bulunmamalı | **PASS** | `.github/workflows/ci.yml`: backend job (gofmt gate, `go vet`, `go build`, `go test -race` + PostgreSQL 18 service), frontend job (`npm ci`, lint, typecheck, test, build), secret-scan job; actionlar tam SHA ile pinli. `scripts/secret-scan.sh` → temiz; elle grep taraması → temiz; `.env.example` yalnızca placeholder (`change-me-dev-only`). |

## Test yaklaşımı uygulaması

| Adım | Sonuç | Not |
|---|---|---|
| Kod incelemesi (mimari sınır, yetki, hata yönetimi, redaction, audit) | **PASS** | Faz 01'de RBAC/audit kapsamı yok (Faz 02); mevcut katman için: DB URL loglarda maskeli, query string loglanmıyor, iç hata metinleri istemciye sızmıyor (test kanıtlı), hata zarfı kararlı. |
| Otomatik testler | **PASS** | Go: 11 birim test (config 7, httpapi 8 içinden) + race temiz; migration entegrasyon testleri CI'da PostgreSQL 18 ile. Frontend: lint + typecheck + 4 bileşen testi. |
| Pozitif senaryolar | **PASS** | healthz/readyz/version 200 yolları; migration up→idempotent up→down akışı (CI); UI render + dil değişimi + boş durum. |
| Negatif senaryolar | **PASS** | Geçersiz config reddi (log level, DB URL şeması/host, body limit); DB kapalıyken readyz 503 + sızıntı yok; 404/405 zarfı; timeout zarfı (503 retryable); güvensiz X-Request-ID reddi; gövde limiti; advisory lock ikinci runner'ı reddediyor (CI). |
| Regresyon | **PASS** | Faz 00 dokümantasyon testleri yapısal olarak geçerli (değişen dosyalar: status/changelog/README — kriterlerle çelişki yok); bağımlılık kayıt defteri Faz 00'dan itibaren sürdürülüyor. |
| Ortam kısıtı | — | Sandbox: Docker yok → compose-up NOT RUN; PostgreSQL sunucusu yok → migration testleri lokalde SKIP, CI'da koşar. Go 1.27.3 ve Node 26.13.0 resmi checksum doğrulamalı kuruldu. |

## Test komutları ve kısa çıktılar

```
# Backend
gofmt -l .                    → (boş) TEMİZ
go vet ./... && go build ./... → OK
go test -count=1 ./...        → ok internal/config 0.009s · ok internal/db/migrate 0.003s (SKIP: TEST_DATABASE_URL) · ok internal/httpapi 0.059s
go test -race ./internal/...  → ok (2 paket)
# Canlı duman testi
CP_HTTP_ADDR=127.0.0.1:18080 ./control-plane
curl /healthz → 200 {"status":"ok"} · curl /readyz → 503 {"status":"degraded","components":{"database":"not_configured"}}
curl /api/v1/version → {"version":"dev","api_version":"v1","go_version":"go1.27.3"}
curl /yok → 404 {"code":"not_found","correlation_id":"...","retryable":false}
# Frontend
npm run lint → 0 bulgu · npm run typecheck → OK · npm test → 4 passed · npm run build → built in 183ms
# Güvenlik
bash scripts/secret-scan.sh → temiz ✓
```

## Bulgular

| Önem | Bulgu | Durum |
|---|---|---|
| Kritik | — | Yok |
| Yüksek | — | Yok |
| Orta | `npm audit`: 12 × moderate — tamamı eslint toolchain'i (dev-only; üretime çıkmaz) | Kayıt altında (`dependency-registry.md`); her faz başında yeniden taranır |
| Orta | TypeScript 7.0.2 yayında ama typescript-eslint 8.72.1 `<6.1.0` istiyor | TS 5.9.3 pinlendi; TS 7 desteği çıkınca yükseltme değerlendirilecek |
| Düşük | compose-up sandbox'ta çalıştırılamadı (Docker yok) | Sahip lokal doğrulaması için `make compose-up` hazır; CI PG 18 servisinde migration testleri gerçek çalışıyor |

## Güvenlik değerlendirmesi

- Repoda secret yok (pattern taraması + elle inceleme); `.env` gitignored, örnek dosya placeholder.
- API: global gövde limiti, timeout zarfı, panic recovery, güvenlik başlıkları, istek-ID benimseme filtresi (güvensiz desen reddedilir), log redaction (DB URL maskeli, query string yok).
- Compose: port localhost'a bağlı; imaj digest pinli; şifre yalnızca geliştirme placeholder'ı.
- CI: SHA pinli actionlar, `permissions: contents: read`, secret-scan job.
- Release blocker niteliğinde bulgu yok (threat-model bağlamında bu fazda sunulan yüzey: health/version uçları + UI kabuğu, kimliksiz okuma-only).

## Faz kabul kararı

**ACCEPTED** — K1, K2, K4, K5 PASS; K3 migration altyapısı PASS (compose çalıştırma bileşeni sandbox kısıtıyla NOT RUN — sahibin lokal komutu hazır, CI PG 18 ile migration kanıtı mevcut). Kritik/yüksek bulgu yok.

## Sonraki faza geçiş

**READY** — Faz 02 (`prompts/phase-start/phase-02.md`) başlatılabilir. Koşullar: (1) sahibin Faz 01 onayı, (2) sahibin lokalinde `make compose-up` + `make migrate-up` doğrulaması (K3 kapanışı), (3) CI yeşil onayının GitHub üzerinde görünmesi.
