# Faz 04 Test Raporu — Linux Agent ve temel OS yönetimi

**Tarih:** 2026-11-01 · **Dal:** main · **Sürüm:** v0.5.0 (aday)
**Kapsam:** `docs/phases/phase-04.md` kabul kriterleri + `prompts/phase-test/phase-04-test.md` zorunlu kontroller.

## Kabul kriteri sonuçları

| # | Kriter | Sonuç | Kanıt |
| --- | --- | --- | --- |
| 1 | Agent ve root broker ayrı servis/kimlik; broker yalnız Unix socket | **PASS** | `agent/cmd/server-agent` (yetkisiz) + `agent/cmd/server-privd` (root) ayrı ikililer; broker TCP dinlemez, `/run/server-control/privd.sock` 0660 + dizin 0750; unit test: `TestPeerDeniedWhenUIDNotAllowlisted`, `TestDefaultDenyWhenNoUIDs` (`agent/internal/broker/server_test.go`); sertleştirme unit'leri `deploy/agent/*.service` |
| 2 | OS/distro/sürüm/systemd/paket yöneticisi tespiti + capability beyanı test edildi | **PASS** | `agent/internal/osdetect` fixture matrisi (Ubuntu 24.04 + Debian 13): `TestDetectUbuntu2404`, `TestDetectDebian13`; capability probe: `TestCapabilityProbing` (canlı broker → available, ölü socket → `broker_unavailable`/`broker_unreachable` nedeni); control-plane tarafı `TestConnectFactsAndMetricsTelemetry` (CI'da PG ile) |
| 3 | CPU/RAM/disk/network/uptime metrikleri kaynak + timestamp ile | **PASS** | `agent/internal/metrics` testleri (fixture + canlı smoke); zarflar `CollectedAt` + `Source` taşıyor; REST çıktı doğrulaması `TestTelemetryJSONShape` |
| 4 | Servis listele/status/start/stop/restart allowlist + RBAC | **PASS** | Registry: `linux.systemd.service.*` 5 operasyon; RBAC katmanı `TestServiceOpPermissionLayering` (okuma→`service:read`, start/stop/restart→`service:control`); broker allowlist `TestAllowlistAndServiceOps`; migration 0008 seed (admin=hepsi, operator=sadece okuma) |
| 5 | İşlem girdileri shell string olarak birleştirilmemeli; güvenli broker + audit | **PASS** | systemctl yalnız argv: `TestVerbExecutesWithFixedArgv`, `TestListAndStatusArgv` (meta karakter sızıntı denetimi); üç katmanlı birim doğrulama: `TestValidateUnit`, `TestRejectsBadUnitPayloads`, `TestValidateServiceUnitPayload`; audit zorunluluğu `TestAllowlistAndServiceOps` (5/5 kayıt), `TestPeerDeniedWhenUIDNotAllowlisted`, `TestRejectsNonJSONRawRequest` |

## Çalıştırılan testler (yerel sandbox, 2026-11-01)

| Komut | Sonuç |
| --- | --- |
| `cd agent && go build ./... && go vet ./... && go test -race ./internal/...` | **PASS** — 5 paket (agent, broker, metrics, osdetect, svcops) |
| `cd backend/control-plane && go build ./... && go vet ./... && go test ./...` | **PASS** — 11 paket; PG entegrasyon testleri lokalde SKIP (testdb TEST_DATABASE_URL ister) |
| `cd proto && buf lint && buf generate` (+ determinizmi iki kez üretimle doğrulama) | **PASS** — lint temiz; üretim byte-deterministik |
| `cd web && npm run lint && npm run typecheck && npm test && npm run build` | **PASS** — lint 0 uyarı, strict TS, 12/12 vitest, build OK |
| `cd web && npx playwright test` (mock mod) | **PASS** — 8/8 (6 mevcut + 2 yeni Faz 04) |
| `bash scripts/secret-scan.sh` | **PASS** — temiz |

## CI'da çalışacaklar (lokalde NOT RUN)

- PostgreSQL 18 entegrasyon testleri: telemetry PG testleri (`TestFactsUpsertPG`, `TestMetricsPG`), agentsrv telemetry akışı, migration 0008 up/down — CI `backend` job'ı.
- İki distro matrisi: CI `agent` job'ı ubuntu-24.04 runner + debian:13 container üzerinde aynı test setini koşar.
- Gerçek backend E2E (`E2E_REAL=1`): telemetry spec gerçek modda dürüst boş durumları + görev oluşturmayı doğrular.

## Güvenlik değerlendirmesi

- **Shell string yok:** broker tipli JSON; systemctl `exec.Command` argv; statik analiz testleri argv'ya meta karakter sızmadığını kanıtlıyor.
- **Üç katmanlı birim allowlist'i:** sunucu validator → agent executor → broker `validateRequest`; `-flag` enjeksiyonu, yol geçişi, komut ikamesi, boşluk/`%` reddedilir.
- **Broker kimlik doğrulama:** SO_PEERCRED UID allowlist; boş allowlist = tam kapalı; her reddedilen istek auditlenir.
- **En az ayrıcalık:** agent yetkisiz kullanıcı; `service:control` ayrı izin — görev oluşturma yetkisi servis kontrolü vermez.
- **Kaynak koruması:** metrics yutumu node başına 10s hız sınırı + alan sınırları; facts sınırlı boyut.
- **Bilinen boşluklar:** gerçek host doğrulaması yapılamadı (sandbox'ta systemd/Docker yok) — sahibi için `deploy/agent/README.md` prosedürü; broker audit rotasyonu yok; metrics prune işi periyodik job bekliyor.

## CI koşusu (kanıt)

- **Run 41309395227 (commit e0b0bb4): success — 7/7 job** — Backend (Go, PostgreSQL 18 entegrasyon dahil), Frontend (React/TS), Agent (Go, ubuntu-24.04), Agent (Go, **debian-13** container), E2E (Playwright, gerçek backend), Protokol (buf), Secret taraması.
- **CI bulguları ve düzeltmeleri:**
  - **CI-04 (kritik):** 0008 migration, admin'e CROSS JOIN ile 0002'de verilmiş 7 izni yeniden ekliyordu → `role_permissions_pkey` ihlali; yalnız YENİ izinlerin verilmesiyle düzeltildi. PG entegrasyon testleri bu hatayı lokalde (PG yok) yakalayamadı; CI yakaladı.
  - **CI-05:** debian-13 container'da `go build` VCS damgası dubious-ownership hatası → `-buildvcs=false`.
  - **CI-06:** `.gitignore` `bin/` kuralı `testdata/**/usr/bin` fixture'larını dışlıyordu (osdetect "unknown" paket yöneticisi hatası) → `!**/testdata/**/bin/`.
  - **CI-07:** debian:13'te `-race` cgo ister → gcc+libc6-dev kuruldu, `CGO_ENABLED=1`.
  - **CI-08:** telemetry prune test beklentisi düzeltildi (kod doğruydu; ikinci snapshot ilk cutoff'ta kalıyor).
- Gerçek-host broker/systemd doğrulaması: **NOT RUN** (sandbox'ta systemd/VM yok) — `deploy/agent/README.md` prosedürü sahibine.

## Karar

- **Kabul:** ACCEPTED (CI kanıtı: run 41309395227, 7/7 yeşil)
- **Sonraki faza geçiş:** READY
