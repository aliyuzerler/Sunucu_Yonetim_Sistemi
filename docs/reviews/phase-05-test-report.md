# Faz 05 Bağımsız Test Raporu — Windows Server Agent ve native adaptörler

**Tarih:** 2026-11-01 · **Dal:** main · **Denetim noktası:** 44323e9 (CI-13 düzeltmesi dahil)
**Denetçi rolü:** `prompts/phase-test/phase-05-test.md` — uygulayıcı ajan varsayımı YOK; kanıt dosya:satır düzeyinde istendi.
**Kapsam:** `docs/phases/phase-05.md` kabul kriterleri + 5 zorunlu kabul kontrolü + negatif/regresyon seti.

## Zorunlu kabul kontrolleri (bağımsız kanıt)

| # | Kriter | Sonuç | Bağımsız kanıt (kod okundu, uygulayıcı iddiasına güvenilmedi) |
| --- | --- | --- | --- |
| 1 | Windows Agent outbound mTLS + aynı node/capability modeli | **PASS** | Windows servis sarmalayıcı yalnız giriş noktası değiştirir: `svc-run` yakalanır, aksi halde CLI aynı (`cmd/server-agent/service_windows.go:21-49`); servis yolu Faz 04/03 çekirdeğiyle BİREBİR aynı `agent.Run(ctx, store, opts.server, executors, …)` akışını kullanır (`service_windows.go:70`) — outbound mTLS stream (hello/heartbeat/capability/facts/metrics), enroll akışı ve gRPC sözleşmesi değişmedi. Kayıt modeli aynı soyutlama: `DefaultRegistry` aynı `ExecutorRegistry` + `agentv1.Capability` + `ProbeAvailable` sözleşmesiyle kurulur (`internal/agent/registry_windows.go:19-30`). CI her push'ta `GOOS=windows` derleme + windows-2025 job koşar (`.github/workflows/ci.yml`, agent-windows). |
| 2 | SCM servis listeleme/durum + izinli kontrol | **PASS** | En az ayrıcalıklı SCM: `OpenSCManager(ENUMERATE_SERVICE\|CONNECT)` — ALL_ACCESS değil (`internal/winscm/winscm.go:47-53`); `x/sys/windows/svc` yapılandırılmış API, sc.exe çıktı ayrıştırma yok. Liste 500 ile sınırlı + allowlist şekli dışındaki adlar hiç yankılanmıyor (`winscm.go:109-120`). Kontrol-plane katmanı: 6 spec — list/status `service:read` + ClassReadOnly, start/stop `service:control` + ClassIdempotent, restart ClassNonIdem + RetrySafe=false, timeout'lar 60s/2m/3m (`internal/tasks/ops_faz05.go:94-139`); migration 0009 yalnız YENİ izni seed'liyor (CI-04 dersi). |
| 3 | Event Log + temel host metrics | **PASS** | wevtutil SABİT argv şablonu: `query-events <kanal> /count:N /rd:true /format:xml` — kanal kapalı enum (Application/System/Setup), seviye/sayı sınırlı int, XPath girdiden asla üretilmez; seviye filtresi bilinçli olarak istemci tarafında (`internal/winevent/winevent.go:50-92`). Çıktı sınırlı yapıya ayrıştırılır (≤200 olay, mesaj tek satır ≤4000 B). Metrics tamamen Win32 API: GetSystemTimes, GlobalMemoryStatusEx, GetDiskFreeSpaceEx, GetIfTable, GetTickCount64 — YENİ bağımlılık yok (`internal/metrics/collect_windows.go:46-51`); facts registry CurrentVersion'dan. CI'da gerçek windows-2025 runner'da smoke testleri koşar. |
| 4 | Privileged broker named pipe ACL + caller identity doğrulaması TEST edilmeli | **PASS** | Pipe SDDL `D:P(A;;GA;;;SY)(A;;GA;;;BA)` (varsayılan) + operatör agent SID'ini ekler; `PIPE_REJECT_REMOTE_CLIENTS` + `FILE_FLAG_FIRST_PIPE_INSTANCE` (`internal/broker/pipe_windows.go:17-28,69-89`). Çağırıcı kimliği çekirdek garantili: `ImpersonateNamedPipeClient` + thread-token SID, PID yeniden kullanım yarışı yok; token yalnız TOKEN_QUERY, tampon 4 KiB sınırlı, `RevertToSelf` ertelenmiş çağrı (`internal/broker/auth_windows.go:54-98`). Agent pipe'ı `SECURITY_SQOS_PRESENT\|SECURITY_IDENTIFICATION` ile açar — broker kimliği görür, agent kimliğine hareket EDEMEZ (`internal/broker/pipe_conn_windows.go:107-135`). Pipe dışı taşıyıcı reddedilir (`auth_windows.go:32-36` — Faz 04 IN-01 bu fazda giderildi). TEST: `TestPipeBrokerAllowAndDeny` GERÇEK named pipe üzerinden allow (kendi SID → SCM çağrıldı) + deny (yabancı SID → `peer_denied`) yolunu sınar; CI windows-2025'te unprivileged koşar (`internal/broker/auth_windows_test.go:66-105`). Her istek (ret dahil) yanıt-ÖNCE auditlenir (`internal/broker/server.go:131-146`). |
| 5 | PowerShell arbitrary execution yok; sürüm/OS matrisi açık | **PASS** | grep kanıtı: agent/backend Go kodunda "PowerShell" yalnız ret yorumlarında ve enum-ret test verisinde geçiyor; hiçbir exec yüzeyi PowerShell çağırmıyor (SCM=yapılandırılmış API, eventlog=wevtutil argv, metrics=Win32 LazyProc). Destek matrisi açık: `docs/architecture/windows-support.md` — WS 2019/2022/2025 amd64; Security kanalı ✗ (tasarımdan hariç); PowerShell ✗ **kalıcı ret** (ADR-0003/0009); bilinen sınırlar dürüstçe listelenmiş (§3). ADR-0009 kayıtlı. |

## Bağımsız yerel test koşusu (sandbox, 2026-11-01 — bu raporla eş zamanlı)

| Komut | Sonuç |
| --- | --- |
| `agent`: `go vet ./... && go build ./... && go test -race ./...` (go1.27.3, linux) | **PASS** — 6/6 test paketi (agent, broker, metrics, osdetect, svcops, winops) |
| `agent`: `GOOS=windows GOARCH=amd64 go build ./... && go vet ./...` | **PASS** — windows kod yolu (winscm/winevent/pipe broker) derlenir + vet temiz |
| `backend/control-plane`: `go vet ./... && go test -race ./...` | **PASS** — 11 test paketi OK (tasks/rbac/telemetry dahil); PG 18 entegrasyonları CI kanıtıyla kaplı |
| `web`: `npm run lint && npm run typecheck && npm test && npm run build` | **PASS** — lint 0 uyarı; strict TS; vitest 15/15 (7 NodeDetail — windows ailesi dahil); prod build OK |
| `web`: `npx playwright test` (mock mod) | **PASS** — 11/11 (Faz 02/03/04 klasikleri + Faz 05'in 3 windows testi); ekran görüntüleri tazelendi ve pushlandı: `docs/reviews/screenshots/phase-05/01..04.png` |
| `bash scripts/secret-scan.sh` | **PASS** — temiz ✓ |
| `buf lint/breaking` yerel | **NOT RUN** — sandbox'ta buf yok; CI `Protokol (buf lint + üretim)` job'ı kanıtıyla kaplı (aşağıda) |

## Bağımsız denetim bulguları

| ID | Önem | Bulgu | Durum/Öneri |
| --- | --- | --- | --- |
| CI-13 | **Yüksek** (test altyapısı) | Gerçek `wevtutil` çıktısı `<Events>` köküyle **sarmalamaz** — art arda `<Event>` kökleri basar. Ayrıştırıcı sarmal varsaydığından windows-2025 CI smoke sondası `parse=0` üretti (run 41319001191 `TestReadSmoke` FAIL). Fixture testleri yalnız sarmalanmış şekli içeriyordu. | **Düzeltildi (44323e9):** `ParseWevtutilXML` `xml.Decoder` akış çözümüne geçirildi — hem sarmalanmış hem ham şekil kabul edilir; bozuk öğede temkinli durup o ana dek çözüleni döndürür. Gerçek şekil kalıcı fixture (`TestParseWevtutilXMLUnwrapped`). Yerel yeniden üretim → düzeltme sonrası 8/8 CI job success (run 41325316893). |
| IN-04 | Düşük | Pipe deadline'ları senkron pipe'ta kernel deadline değil, kapanış zamanlayıcısıdır (`pipe_conn_windows.go:42-62`) — uzun tek okuma, timer tetiklenene dek bloklayabilir. | Belgelenmiş sınır (`windows-support.md` §3.3); çağrı timeout'larıyla sınırlı. Faz 06+ IO-overlapped değerlendirilebilir. |
| IN-05 | Bilgi | Gerçek-host doğrulaması (SDDL + agent SID ile broker kurulumu, gerçek servis start/stop, audit dosyası) bu sandbox'ta koşulamadı (Windows VM yok). CI: salt-okuma SCM, gerçek pipe izin/ret, wevtutil sondağı, metrics smoke, registry facts koşuyor; yıkıcı kontrol mock'la sınanıyor. | Sahip, disposable Windows Server VM'de `deploy/agent-windows/README.md` prosedürünü koşmalı (belgelenmiş). |
| IN-06 | Bilgi | Broker audit dosyası rotasyonu Windows'ta operatöre ait (Event Forwarding/zamanlanmış görev). | Belgelenmiş (`windows-support.md` §3.4). |

Kritik bulgu: **YOK**. Yüksek bulgu: yalnız CI-13 — test altyapısında, ürün kodunda değil; düzeltildi ve CI ile doğrulandı.

## Negatif senaryo kapsamı (test kodlarından bağımsız doğrulama)

- Yetkisiz SID → `peer_denied` + audit (gerçek pipe üzerinde `TestPipeBrokerAllowAndDeny` deny yolu); kimlik çıkarılamazsa ret (`server.go:120-128`).
- Allowlist dışı / boş operasyon → `unsupported_operation` / `invalid_request` (`protocol.go:104-161`); Linux ailesi Windows broker'da YOK (`auth_windows.go:143-148`).
- Servis adı enjeksiyonu: öncül `-` (flag injection), `..\evil`, `C:\x`, `a|b`, `a&b`, `a"b`, `a%;`, sondaki boşluk/nokta, 80+ uzunluk — kontrol-plane VE agent VE broker'da reddedilir (`ops_faz05_test.go`, `winops_test.go`, `auth_windows_test.go`).
- Event Log: `Security` kanalı reddi (tasarım), enum dışı kanal, case duyarlı enum, level 6 / count 201 reddi, XPath alanı `DisallowUnknownFields` ile reddedilir (`ops_faz05_test.go`).
- Bozuk payload: strict JSON + trailing data reddi; limit 0..500 dışı reddi; istek boyutu sınırlı; eşzamanlılık sınırı → `rate_limited`; timeout → `executor_timeout` (`auth_windows.go:100-170`).
- Protokol sürümü ≠1 reddi; replay/idempotency: task katmanı Faz 03 sözleşmesiyle korunur (restart non-idempotent + retry-güvensiz işaretli).

## Regresyon sonucu

- Backend tüm paketler (Faz 02 identity/rbac + Faz 03 agentsrv/certs + Faz 04 telemetry + Faz 05 windows op ailesi) `-race` OK.
- Web vitest 15/15 + Playwright 11/11 (Faz 02 smoke, Faz 03 enrollment, Faz 04 telemetry, Faz 05 windows) OK.
- proto: sözleşme DEĞİŞMEDİ; CI `Protokol` job'ı (buf lint + üretim + breaking) yeşil.
- CI: **run 41319001191 (7f40491) agent-windows FAIL → CI-13 yakalandı**; **run 41325316893 (44323e9) success 8/8** — backend, frontend, agent×2-distro, **agent-windows (gerçek wevtutil smoke dahil)**, e2e, proto, secret-scan. Bağımsız API doğrulaması bu denetim sırasında yapıldı (GitHub Actions API, 2026-11-01).

## Güvenlik değerlendirmesi

- Komut yürütme yüzeyi yok: SCM yapılandırılmış API, wevtutil sabit argv, metrics Win32 proc çağrıları; girdiden sorgu/argv metni üretilmiyor (kaynak taramasıyla doğrulandı).
- Kimlik: pipe çağırıcısı çekirdek impersonation ile doğrulanır; agent IDENTIFICATION düzeyinde açar — broker asla agent kimliğine hareket edemez; pipe dışı taşıyıcı reddedilir (Faz 04 IN-01 kapatıldı).
- En az ayrıcalık: SCM handle salt-okuma haklarıyla; Security kanalı ve PowerShell tasarımdan hariç; üç katmanlı allowlist (control-plane validator → agent executor → broker validateRequest) Windows ailesine taşındı; default-deny SID listesi boşsa tam kapalı.
- Denetim: her broker isteği (ret dahil) yanıt-öncesi JSONL audit; kontrol düzlemi audit append-only; eventlog:read ayrı izin.
- Bilinen boşluklar: IN-04/05/06 (yukarıda) — hiçbiri kabul engelleyici değil.

## Faz kabul kararı

**ACCEPTED** — 5/5 zorunlu kriter PASS; kritik bulgu yok; CI-13 (yüksek) düzeltildi ve CI ile doğrulandı.

**Sonraki faza geçiş: READY** — bütün zorunlu kriterler PASS, kritik/yüksek açık bulgu yok. Sahibin disposable Windows VM prosedürü (IN-05) kabul sonrası kalıcı takip maddesidir.
