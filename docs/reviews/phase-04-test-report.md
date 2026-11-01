# Faz 04 Bağımsız Test Raporu — Linux Agent ve temel OS yönetimi

**Tarih:** 2026-11-01 · **Dal:** main · **Sürüm:** v0.5.0 (commit 2eb2ee8)
**Denetçi rolü:** `prompts/phase-test/phase-04-test.md` — uygulayıcı ajan varsayımı YOK; kanıt dosya:satır düzeyinde istendi.
**Kapsam:** `docs/phases/phase-04.md` kabul kriterleri + 5 zorunlu kabul kontrolü + negatif/regresyon seti.

## Zorunlu kabul kontrolleri (bağımsız kanıt)

| # | Kriter | Sonuç | Bağımsız kanıt (kod okundu, uygulayıcı iddiasına güvenilmedi) |
| --- | --- | --- | --- |
| 1 | Agent ve root broker ayrı servis/kimlik; broker yalnız Unix socket | **PASS** | Ayrı ikililer `agent/cmd/server-agent` + `agent/cmd/server-privd`; broker dinleyicisi yalnız `net.Listen("unix", …)` (`cmd/server-privd/main.go:86`); TCP hiçbir yerde açılmıyor; socket 0660 + grup chown (`main.go:90-95`), dizin 0750 (`main.go:81`, `broker.SocketDirPerms`); systemd birim ayrımı `deploy/agent/server-agent.service` (User=server-agent) vs `server-privd.service` (User=root, RestrictAddressFamilies=AF_UNIX). Default-deny + zorunlu `--allow-uid` (`main.go:68-70`); boş allowlist tam kapalı (`server.go:119-126` unit test: `TestDefaultDenyWhenNoUIDs`) |
| 2 | OS/distro/sürüm/systemd/paket yöneticisi tespiti + capability beyanı | **PASS** | `osdetect.go:45-73` (os-release + procfs kernel + systemd probe + pkg manager, kaynak provenansı `Source` alanı); Ubuntu 24.04 + Debian 13 fixture matrisi `osdetect_test.go` (`TestDetectUbuntu2404`, `TestDetectDebian13`); canlı socket capability probe testi `broker/server_test.go` (available vs `broker_unavailable`) |
| 3 | CPU/RAM/disk/network/uptime — kaynak + timestamp | **PASS** | `metrics.go:57-66` Snapshot `CollectedAt` + `Source` zorunlu alanlar; `metrics.go:99-120` procfs/statfs okuma; disk filtresi gerçek FS (`realFSTypes` metrics.go:70-74) + list bound 500 (`systemd.go:169`); CI'da PG tarafı `TestTelemetryJSONShape` + telemetry entegrasyonu |
| 4 | Servis list/status/start/stop/restart — allowlist + RBAC | **PASS** | Broker allowlist yalnız 5 op (`protocol.go:28-34`); üç katmanlı birim doğrulama: `svcops.ValidateUnit` (`systemd.go:34-42`) → broker `validateRequest` (`protocol.go:92-126`, strict JSON) → backend task validator; RBAC katmanı `httpapi/handlers_tasks.go` (`spec.RequiredPerm` kontrolü, tasks:write ÜSTÜNE ek)`; ops.go` 5 operasyon: okuma→`service:read`, start/stop/restart→`service:control`; migration 0008 seed admin=2 izin, operator=salt okuma |
| 5 | Girdiler shell string'e birleştirilmez; güvenli broker + audit | **PASS** | `svcops/systemd.go:196-204` `exec.CommandContext(bin, args...)` — argv yalnız, shell yok; birim adı regex allowlist `^[A-Za-z0-9][A-Za-z0-9@._-]{0,246}\.service$` öncül `-` reddi (flag injection yok); audit HER istekte (reddedilenler dahil) yazılır ve yanıttan ÖNCE (`server.go:173-184`); audit kimliği sanitize edilir (`protocol.go:160-168`); istek 16 KiB sınırlı (`protocol.go:88`) |

## Bağımsız yerel test koşusu (sandbox, 2026-11-01 — bu raporla eş zamanlı)

| Komut | Sonuç |
| --- | --- |
| `agent`: `go build ./... && go vet ./... && go test -race ./internal/...` | **PASS** — 5/5 paket (agent, broker, metrics, osdetect, svcops) |
| `backend/control-plane`: `go build ./... && go vet ./... && go test ./...` | **PASS** — 12 test paketi OK; PG entegrasyonları TEST_DATABASE_URL olmadan SKIP (aşağıda CI kanıtı) |
| `web`: `npm run lint && npm run typecheck && npm test && npm run build` | **PASS** — lint 0 uyarı; strict TS; vitest 12/12; prod build OK (295.12 kB js) |
| `web`: `npx playwright test` (mock mod) | **PASS** — 8/8 (6 Faz 02/03 + 2 Faz 04); ekran görüntüleri tazelendi: `docs/reviews/screenshots/phase-04/01..03.png` (+ Faz 02/03 klasikleri) |
| `proto`: `buf lint` + 2× `buf generate` (pinli protoc-gen-go@v1.36.12, protoc-gen-go-grpc@v1.6.1) | **PASS** — lint temiz; üretim byte-deterministik (ikinci üretim git diff üretmiyor) |
| `bash scripts/secret-scan.sh` | **PASS** — temiz |

## Bağımsız denetim bulguları

| ID | Önem | Bulgu | Öneri |
| --- | --- | --- | --- |
| IN-01 | **Düşük** | `broker.Server.Handle` Unix socket dışı bağlantıda (test taşımacılığı) kimlik 0/0/0 varsayıyor (`server.go:165-166`). Üretimde sömürülemiyor: main yalnız Unix dinleyici kurar (`main.go:86`) ve UID 0 allowlist'e ancak operatör açıkça yazarsa girer. Ancak savunma derinliği olarak test-affordansı üretim kodunda duruyor. | `Handle`'ın UnixConn olmayan bağlantıyı reddetmesi; test taşımacılığı için ayrı `_test` yapıcısı. Faz 05+ temizliğinde değerlendir. |
| IN-02 | **Düşük** | Broker audit yazım hataları sessizce yutuluyor (`server.go:59-74`); kontrol düzlemi audit'i yetkili kabul edilmiş (belgelenmiş karar). Disk dolu senaryoda lokal kanıt kaybı olası. | journald/logrotate yönlendirmesi + disk-dolu alarmı; operasyon dokümanına not (öneri mevcut: `deploy/agent/README.md`). |
| IN-03 | **Bilgi** | Gerçek-host doğrulaması (canlı systemd start/stop/restart, socket izinleri gerçek kullanıcılarla) bu sandbox'ta koşulamadı (Docker/systemd yok). CI iki distro matrisi + hermetik socket testleri kapsıyor. | Sahip, disposable Ubuntu 24.04 / Debian 13 VM'de `deploy/agent/README.md` prosedürini koşmalı. |

Kritik/Yüksek bulgu: **YOK**. (Uygulayıcının CI turunda yakaladığı CI-04 kritik seed çakışması düzeltildi ve bu bağımsız koşuda regression yok.)

## Negatif senaryo kapsamı (test kodlarından bağımsız doğrulama)

- Yetkisiz UID → `peer_denied` + audit (`TestPeerDeniedWhenUIDNotAllowlisted`); creds alınamazsa da reddi.
- Allowlist dışı operasyon → `unsupported_operation` (`protocol.go:123-124`); boş operasyon reddi.
- Bozuk payload: strict JSON (`DisallowUnknownFields`, trailing data reddi, `protocol.go:129-139`); unit şema ihlali `invalid_params`.
- Birim adı: öncül `-`, `/`, `%`, boşluk, 256+ uzunluk reddi (regex + `TestRejectsBadUnitPayloads`).
- İstek taşması: 16 KiB üstü reddi (`readBoundedLine`); eşzamanlılık sınırı → `rate_limited`; işlem timeout → `executor_timeout`.
- Protokol sürümü ≠1 reddi. Proxy'de tekrar/replay: task katmanı idempotency + lease (Faz 03 testleri, CI).

## Regresyon sonucu

- Backend tüm paketler (Faz 02 identity/rbac/tasks/audit + Faz 03 agentsrv/certs + Faz 04 telemetry) OK.
- Web vitest 12/12 + Playwright 8/8 (Faz 02 smoke + Faz 03 enrollment + Faz 04 telemetry) OK.
- proto: pinned toolchain ile deterministik üretim, lint temiz.
- CI: **run 41309395227 (e0b0bb4) success 7/7** ve **run 41309860892 (2eb2ee8, v0.5.0) success** — bağımsız API doğrulaması bu denetim sırasında yapıldı (GitHub Actions API, 2026-11-01). PG 18 entegrasyon testleri ve debian:13 matrisi CI kanıtıyla kaplı.

## Güvenlik değerlendirmesi

- Shell/spray yüzeyi yok: tek exec yüzeyi systemctl argv; girdi hiçbir yerde string birleştirilmiyor (3 ayrı pakette `exec.CommandContext` kullanımı doğrulandı).
- En az ayrıcalık: agent yetkisiz; broker root ama AF_UNIX kısıtlı systemd sandbox'ı; `service:control` ayrı izin — görev yazma yetkisi servis kontrolü vermiyor.
- Denetim: reddedilen istekler dahil her broker işlemi auditlenir (yanıt-öncesi yazım yarışsız gözlem sağlar); kontrol düzlemi audit append-only.
- Bilinen boşluklar: IN-01/02/03 (yukarıda) — hiçbiri kabul engelleyici değil.

## Karar

- **Kabul:** **ACCEPTED** — 5/5 zorunlu kriter PASS (bağımsız kod incelemesi + yerel koşu + CI kanıtı); kritik/yüksek bulgu yok.
- **Sonraki faza geçiş:** **READY** — Faz 05 başlatılabilir (sahip onayı 2026-11-01'de verildi).
