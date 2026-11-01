# Faz 07 Bağımsız Test Raporu — Docker Engine ve Compose yönetimi

- **Test tarihi:** 2026-11-02
- **Denetlenen commit:** `fdeb311` (uygulama `50bbf7e` + CI-18 düzeltmesi; origin/main)
- **Test promptu:** `prompts/phase-test/phase-07-test.md`
- **Roller:** Uygulamayı yazan ajan ayrı varsayıldı; kanıt dosya:satır düzeyinde istendi. Yalnızca Faz 07 denetlendi, sonraki faz başlatılmadı.
- **Ortam:** Yerel sandbox (Go 1.27.3, Node 24; Docker/buf YOK) + GitHub Actions CI (run **41341230025**, 11/11 job success).

## 1. Zorunlu kabul kontrolleri

| # | Kriter | Sonuç | Kanıt |
| --- | --- | --- | --- |
| 1 | Docker daemon erişimi sadece Agent/broker tarafında; TCP socket public açılmamalı | **PASS** | Engine dial yalnız unix: `agent/internal/dockerx/client.go` (`socketPath()` → `/var/run/docker.sock`; `Transport.DialContext` "unix" — TCP imkânsız). Dial eden tek süreç broker'dır: `server-privd` `--docker-socket` (`daemon_unix.go:77` `Docker: dockerRunnerOrZero(...)`); agent yalnız VARLIK probe'u yapar, asla bağlanmaz (`executors.go` DockerExecutor.ProbeAvailable → `os.Stat`); control plane ve UI docker'a bağlanmaz (görev → agent → broker zinciri). Windows pipe broker'ında docker ailesi üye değil (`auth_windows.go` default-deny). Belgeler: ADR-0011, `docs/architecture/docker-support.md` §3. Deploy README'ye bayrak semantiği eklendi. |
| 2 | API version negotiation ve unsupported API yanıtları yönetilmeli | **PASS** | `client.go negotiate()` — `/_ping` → `Api-Version` başlığı; `clampAPI()` pencere [1.24, 1.47]: daemon <1.24 → tipli `ErrUnsupportedAPI` (tahmin yok), >1.47 → clamp; istekler `/v{maj}.{min}/` önekli (`do()` :136). Testler: `TestNegotiationClampsAboveMax` (mock 1.99 → path `/v1.47/` kanıtı), `TestNegotiationRejectsBelowMin` (mock 1.22 → `errors.Is(ErrUnsupportedAPI)`). **Gerçek kanıt:** CI agent-docker job (run 41341230025): `gerçek docker: server=28.0.4 api=1.47 os=linux` — gerçek daemon ile pazarlık + clamp. Ping üst bilgisi eksikse de `ErrUnsupportedAPI` (sürüm iddiası üretilemez). |
| 3 | Container start/stop/restart/inspect/logs, images, networks, volumes işlemleri yetki ve scope ile uygulanmalı | **PASS** | 9 yürütülebilir op tipli spec'lerle: `ops_faz07.go` (list/inspect/logs → `ClassReadOnly` + `docker:read`; start/stop → `ClassIdempotent` + `docker:control`; restart → `ClassNonIdem`, `RetrySafe:false` + `docker:control` — systemd/SCM kalıbı). Node scope: `TargetNode`; her görev tasks audit + broker JSONL audit (ret dahil). Üç katmanlı doğrulama: control-plane validator (`ValidateDockerRef/Logs/Action/List`) → agent executor (`DockerExecutor.Execute` sınırları) → broker `validateRequest` (`protocol.go` docker case'leri; ref allowlist `^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$` + `..` reddi; tail 1..500; timeout 1..120). Negatif setler: `ops_faz07_test.go`, `server_test.go:TestValidateRequestDockerFamily`. **Gerçek kanıt:** CI agent-docker — disposable busybox container ile inspect(running)→stop(exited)→start→restart(running)→logs PASS. |
| 4 | Compose deploy öncesi schema/policy validation, port/mount/privileged/capability riskleri gösterilmeli | **PASS** | `agent/internal/composeops` (SAF; daemon erişimi yok): şema hataları (yaml, services yok/boş, image+build yok, geçersiz servis adı, >64 servis, depends_on bilinmeyen/döngü) + 14+ policy kuralı: high (privileged, cap_add ALL/SYS_ADMIN/NET_ADMIN/SYS_PTRACE, network_mode host, pid host, **docker.sock bind**, hassas dizin bind), medium (diğer cap_add, container netns, ipc host, root user, **public port**, gizli-anahtar görünümlü env ANAHTARI — değer asla kopyalanmaz, devices), low (ayrıcalıklı port, bind-mount, env_file, no-restart, writable-rootfs). Testler: `TestValidateDangerousPolicies` (14 kuralın hepsi + değer sızdırmama kanıtı), `TestValidateMapForms`, `TestValidateBounds`, `TestDeployPlanInvariants`. **UI'da gösterim (kriter "gösterilmeli"):** compose görev sonucu poll edilir ve severity renkli bulgular render edilir — Playwright `docker.spec.ts:120` "compose analizi: bulgular ve deploy planı sonuç olarak gösterilir" PASS (privilege/public-port satırları ekranda kanıtlı; ekran görüntüsü `docs/reviews/screenshots/phase-07/04-compose-findings.png`). Deploy yürütme yüzeyi YOKTUR (plan-only, `Executed=false`) — kriterin öngördüğü "önce validation + risk gösterimi" **daha güçlü politikayla** karşılanır (ADR-0011). |
| 5 | Destructive image/volume/network/container işlemleri preview/confirmation ve audit içermeli | **PASS** | remove ailesi (4 op) PREVIEW-ONLY: `dockerx.RenderRemovePreview` Engine `DELETE` çağrısını + CLI eşdeğerini **veri olarak** döndürür; `RemovePreview.Executed` sabit `false` (`TestRemovePreviewInvariants` — 4 kind × tüm bayrak kombinasyonları). **Derleyici-düzeyi invariant:** `*dockerx.Client`'ta remove metodu yok (`TestClientHasNoRemoveExecutionPath` — reflect ile pinli) ve broker `DockerRunner` arayüzünde remove üyesi yok (`config_unix.go`). Audit: her broker isteği (ret dahil) JSONL (0600); her görev tasks audit. Confirmation: UI "Silme önizlemesi (çalıştırmaz)" etiketi + "hiçbir şey silinmez" notu (`docker-support.md` §3); onaylı yürütme ayrı faz + yeni ADR olarak tasarlanacak (ADR-0011 Sonuçlar). E2E kanıtı: geçersiz ref reddi + önizleme görevi kuyruğa alma (`docker.spec.ts:86`, görüntü `02/03`). |

**Zorunlu kriter sonucu: 5/5 PASS.**

## 2. Yerel doğrulama koşuları (bağımsız tekrar)

| Komut | Sonuç |
| --- | --- |
| `agent: go vet ./...` | temiz |
| `agent: go test -race -count=1 ./...` | **10/10 paket ok** (dockerx, composeops, broker dahil) |
| `agent: GOOS=windows go vet ./... && go build ./...` | temiz (platform ayrımı bozulmadı) |
| `agent: gofmt -l .` | boş (temiz) |
| `backend/control-plane: go vet ./...` | temiz |
| `backend: go test -race -count=1 ./...` | **11/11 test paketi ok** (33-op registry + faz07 validator negatif setleri dahil) |
| `web: npm run lint`, `npm run typecheck` | temiz |
| `web: npx vitest run` | **19/19** (4 dosya; Docker suite dahil) |
| `web: npm run build` | ok (324.99 kB js) |
| `web: npx playwright test` × 3 ardışık | **17/17 × 3** (docker spec dahil; IT-01 dersi uygulanmış önkosullar) |
| `bash scripts/secret-scan.sh` | temiz |
| `buf lint/breaking` | **NOT RUN (yerel)** — buf sandbox'ta yok; CI "Protokol (buf lint + üretim)" job'ı success (run 41341230025) ile karşılanıyor |
| Gerçek Docker (yerel) | **BLOCKED (yerel)** — sandbox'ta docker yok; CI `agent-docker` job'ı (11/11 success) gerçek daemon kanıtıyla karşılanıyor (§1 kriter 2-3-5 kanıt satırları) |

## 3. Gerçek Docker kanıtı (CI, run 41341230025 — 11/11 success)

- **agent-docker job** (disposable, runner'ın kendi daemon'ı; `EXPECTED_DOCKER=1` gate — CI-17 dersi gereği gate konmuşsa daemon erişilemezse test BAŞARISIZ olur, sessiz skip olamaz):
  - `TestRealDockerSmoke`: `gerçek docker: server=28.0.4 api=1.47 os=linux`; gerçek container/image/network/volume listeleri okundu — **PASS**
  - `TestRealDockerLifecycle`: `gerçek inspect: 4b4b4e8b6c5d state=running …` → `gerçek stop: srvctl-ci-docker → exited` → start → restart → `gerçek logs: … truncated=false` — **PASS** (10.38s)
  - Temizlik adımı: `docker rm -f srvctl-ci-docker` (disposable garanti)
- Diğer 10 job (backend, frontend, agent×2 distro, windows-2025, rocky/almalinux gerçek konteynerler, e2e, buf, secret-scan) **success** — önceki faz regresyonu temiz.

## 4. Bulgular

| ID | Önem | Bulgu | Durum |
| --- | --- | --- | --- |
| **CI-18** | **Orta** (test altyapısı) | İlk CI koşusunda (run 41340951932) ubuntu-24.04 + debian-13 agent job'ları kırmızı: `TestCapabilityProbing` docker ailesi için TEK gerekçe dalı varsayıyordu ("docker socket bulunamadı"). Docker'lı runner'larda `/var/run/docker.sock` VAR → probe broker ping'ine düşer → "broker_unreachable" (dürüst). Test ortam-bağımlıydı. | **DÜZELTİLDİ** (`fdeb311`) — test iki dürüst dalı da kabul eder (socket gerekçesi VEYA broker_unreachable); unavailable SONUCU her iki ortamda da değişmez. Doğrulama: run **41341230025 11/11 success**. Yerel -race tekrar yeşil. |
| IT-02 | Düşük / bilgi | `TestRealDockerLifecycle` log kanıtında busybox `echo` çıktısı stderr çerçevesinde raporlandı (`stdout=0 stderr=57`) — busybox `sh -c` davranışı; adapter demux'u çerçeve tipine göre dürüst ayırıyor, davranış hatası değil. | AÇIK (bilgi) — aksiyon gerektirmez. |
| IT-03 | Düşük / bilgi | compose `build:` bölümü yalnız varlık olarak bilinir (derin derleme analizi kapsam dışı); `configs`/`secrets` bölümleri ayrıştırılmaz — `docker-support.md` §4'te dürüstçe belgeli. | AÇIK (belgeli) — sonraki fazlarda genişletme. |
| — | — | Yerelde Docker/buf olmadığı için gerçek-docker ve proto kapıları koşulamadı: tahminle PASS YAZILMADI; CI job kanıtıyla karşılandı (§2 ve §3). | NOT RUN/BLOCKED doğru işaretlendi |

## 5. Regresyon sonucu

Önceki fazların tüm otomatik katmanları aynen yeşil: agent 10/10 paket `-race` (Faz 02-06 paketleri dahil), `GOOS=windows` vet+build (Faz 05), backend 11/11 (Faz 04-06 validator/registry testleri dahil — registry 18→33 additive), vitest 19/19 (Faz 04-06 suite'leri dahil), Playwright 17/17 × 3 (smoke + enrollment + telemetry + windows + linux-distro + docker), secret-scan temiz, CI 11/11 (iki-distro matrisi + windows-2025 + rocky/almalinux gerçek kanıt job'ları dahil). Ubuntu/Debian/Windows davranışı değişmedi; protokol sözleşmesi korunuyor (buf breaking gate success).

## 6. Güvenlik değerlendirmesi

- **Docker daemon = root eşdeğeri yüzey:** erişim broker-tekil (unix socket; TCP yok); agent/control-plane/UI hiçbir yerde dial yok; `--docker-socket` boş geçilirse aile tamamen kapalı (default-deny).
- **Silme/deploy yürütme yüzeyi kodda YOK** (ADR-0011): derleyici + arayüz + test üçlüsüyle pinli; preview metni `Executed=false` değişmeziyle döner; onaylı yürütme ayrı faz.
- Üç katmanlı strict doğrulama + ref allowlist (`..`, `-`, `/`, boşluk reddi); tail/timeout/list sınırları; çıktı sınırları (1 MiB/64 KiB + truncation dürüst raporlanır).
- **Secret redaksiyonu:** inspect'te `Config.Env` kasıtlı dışlanır (test: JSON çıktıda "Env" yok); compose bulguları yalnız anahtar adları (test: değer sızdırmama kanıtı); broker JSONL audit her istekte.
- Yeni bağımlılık yalnız `gopkg.in/yaml.v3 v3.0.1` (MIT; pinli; registry kaydı + gerekçe — alias koruması + girdi sınırları).

## 7. Karar

- **Kritik/Yüksek bulgu:** yok.
- **Orta:** CI-18 (test altyapısı, ortam-bağımlı varsayım) — düzeltildi, CI 11/11 ile doğrulandı.
- **Faz kabul kararı: ACCEPTED**
- **Sonraki faza geçiş: READY** (bütün zorunlu kriterler PASS; kritik/yüksek bulgu yok).
