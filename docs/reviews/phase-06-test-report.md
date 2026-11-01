# Faz 06 Bağımsız Test Raporu — Ek Linux dağıtımı adaptörleri (Rocky Linux + AlmaLinux)

- **Test tarihi:** 2026-11-02
- **Denetlenen commit:** `b9173e7` (origin/main; uygulama `7a4166e`, CI düzeltmeleri CI-14..CI-17)
- **Test promptu:** `prompts/phase-test/phase-06-test.md`
- **Roller:** Uygulamayı yazan ajan ayrı varsayıldı; kanıt dosya:satır düzeyinde istendi. Yalnızca Faz 06 denetlendi, sonraki faz başlatılmadı.
- **Ortam:** Yerel sandbox (Go 1.27.3, Node 24; Docker/buf YOK) + GitHub Actions CI (run **41332649006**, 10/10 job success).

## 1. Zorunlu kabul kontrolleri

| # | Kriter | Sonuç | Kanıt |
| --- | --- | --- | --- |
| 1 | Distro/version/package manager detection yanlış pozitif üretmemeli | **PASS** | `agent/internal/osdetect/osdetect.go:45-59` — destek matrisi yalnız `ID=` bazlı (`SupportedDistros`: ubuntu 22/24, debian 12/13, rocky 9/10, almalinux 9/10). `osdetect.go:72-94` — aile yalnız ID'den; `ID_LIKE` yalnız `debian-like`/`rhel-like` kaba etiketi üretir, **asla** `supported` döndürmez (`likeFamily` :119-129). `majorOf` :98-115 katı ayrıştırma ("9.5"→"9", boş/sayısal olmayan → ""). Negatif set: `distro_test.go:49-59` (ID_LIKE=rhel olan bilinmeyen distro `rhel-like`+`unknown`), `:31-47` (Rocky 8→untested, boş/`nine` VERSION_ID→untested). Gerçek kanıt: CI rockylinux:9 container `id=rocky version=9.3 … family=rhel support=supported`, almalinux:9 `id=almalinux version=9.8 … support=supported` (run 41332649006 job logları). |
| 2 | Ubuntu/Debian davranışları geriye dönük bozulmamalı | **PASS** | `osdetect.go:46-47` — ubuntu/debian satırları Faz 04 değerleriyle aynı. Protokol additive: `SystemFactsReport` alan 13–16 yeni; mevcut alanlar değişmedi (buf breaking gate job "Protokol (buf lint + üretim)" success). Agent suite tamamen yeşil (Faz 04/05 paketleri dahil 8/8 paket `-race`), backend 11 paket yeşil, registry testi **18 operasyon** bekliyor (`service_test.go:217`) — Faz 04'ün 5 + Faz 05'in 6 + Faz 06'ın 4 + çekirdek operasyonlar. Mock/e2e: Faz 04 `telemetry.spec` ve Faz 05 `windows-node.spec` aynen yeşil. |
| 3 | Rocky/Alma desteklenen majör sürümler açıkça listelenmeli | **PASS** | `osdetect.go:48-49` — `rocky: {9,10}`, `almalinux: {9,10}` açık liste. Doküman matrisi birebir: `docs/architecture/linux-support.md` §1 tablosu (Rocky **9,10** / Alma **9,10**, kanıt sütunu dahil). Fixture gerçek os-release: `osdetect/testdata/rocky-9`, `testdata/almalinux-9` (`distro_test.go:76-106` — rocky 9.5→dnf/rhel/supported; almalinux 9.6→supported). |
| 4 | Firewall backend farklılıkları doğru tanınmalı; otomatik kural değişikliği güvenli preview gerektirmeli | **PASS** | Backend tespiti kapalı küme + dürüst gerekçe: `fwops/runner_linux.go:37-66` (firewalld running→ufw active→nft ruleset okunur→iptables-save okunur→none; kurulu-ama-çalışmıyor durumları ayrıca raporlanır). Kural listesi backend'e göre farklılaşır ve sınırlıdır: `runner_linux.go:69-97` (firewalld aktif zon başına `--list-all` ≤16 zon `fwops.go:176-192`; 64 KiB/400 satır sınırı `runner_linux.go:15,151-169`). **Otomatik değişiklik yok — daha güçlü politika (ADR-0010):** `fwops.go:115-160` `RenderPreview` komutları **veri olarak** üretir; `Preview.Executed` değişmez `false` (`fwops.go:106`); yürütücü yüzey yok (`exec_unix.go:111` yalnız saf render çağırır); nftables/iptables dürüst ret (`ErrPreviewUnsupported`, `fwops_test.go:74-82`). Üç katmanlı doğrulama: control-plane `ops_faz06.go:71-89` → broker `protocol.go:161-169` → agent executor. Gerçek firewalld kanıtı: CI rocky container `backend=firewalld active=true`, 15 satır kural, önizleme `firewall-cmd --permanent --zone=public --add-port=8443/tcp` (yürütülmedi). |
| 5 | Uyumsuz/denenmemiş sürüm desteklenmiyor olarak gösterilmeli | **PASS** | Matris dışı majör → `SupportUntested` ("denenmemiş majör sürüm N — desteklenmiyor olarak raporlanır", `osdetect.go:84`); matris dışı distro → `SupportUnknown` (`osdetect.go:88,93`). UI rozeti: `web/src/i18n/tr.ts:215-216` — "Desteklenmiyor (denenmemiş)" / "Desteklenmiyor (bilinmeyen)" + gerekçe; test `NodeDetail.test.tsx` (Faz 06 vitest paketi). Rocky 8 fixture testi `distro_test.go:31-38`. |

**Zorunlu kriter sonucu: 5/5 PASS.**

## 2. Yerel doğrulama koşuları (bağımsız tekrar)

| Komut | Sonuç |
| --- | --- |
| `agent: go vet ./...` | temiz |
| `agent: go test -race -count=1 ./...` | **8/8 paket ok** (osdetect, pkgops, fwops, broker, agent, svcops, winops, metrics) |
| `agent: GOOS=windows go vet ./... && go build ./...` | temiz (platform ayrımı bozulmadı) |
| `backend/control-plane: go vet ./...` | temiz |
| `backend: go test -race -count=1 ./...` | **11/11 test paketi ok** (tasks + ops_faz06 validator negatif setleri dahil) |
| `web: npm run lint`, `npm run typecheck` | temiz |
| `web: npx vitest run` | **17/17** (3 dosya; Faz 06 dahil) |
| `web: npm run build` | ok (309.63 kB js) |
| `web: npx playwright test` | **14/14** × 6 ardışık koşu (düzeltme sonrası; bkz. IT-01) |
| `bash scripts/secret-scan.sh` | temiz |
| `buf lint/breaking` + `proto generate` deterministik | **NOT RUN (yerel)** — buf sandbox'ta yok; CI job "Protokol" success ile karşılanıyor |
| Docker'lı gerçek distro koşuları (yerel) | **BLOCKED (yerel)** — sandbox'ta Docker yok; CI `agent-rhel` real job'ları ile karşılanıyor (aşağıda) |

## 3. Gerçek dağıtım kanıtı (CI, run 41332649006 — 10/10 success)

Disposable **privileged systemd** container'lar; `EXPECTED_OS_ID` gate'i exec'e taşındığı için eşleşmeyen host'ta test SESSİZ ATLANMAZ, BAŞARISIZ olur (CI-17 düzeltmesi `1e079fd`):

- **rockylinux:9** (job 124925803535): `TestRealHostSmoke` → `gerçek host: id=rocky version=9.3 pretty=Rocky Linux 9.3 (Blue Onyx) pkg=dnf systemd=true family=rhel support=supported`; `TestRealPkgList` → `173 paket (ör. libgcc 11.4.1-2.1.el9)`; `TestRealFirewall` → `backend=firewalld active=true`, `15 satır; önizleme: "firewall-cmd --permanent --zone=public --add-port=8443/tcp"` — hepsi **PASS**.
- **almalinux:9** (job 124925803510): `id=almalinux version=9.8 … family=rhel support=supported`; `178 paket`; firewalld aktif — hepsi **PASS**.

## 4. Bulgular

| ID | Önem | Bulgu | Durum |
| --- | --- | --- | --- |
| **IT-01** | **Orta** (test altyapısı) | e2e yardımcıları `openRocky01`/`openWin01` node listesi YÜKLENMEDEN `row.count()` yapıyordu → ara sıra çift node oluşturuluyor → `getByText('rocky-01') resolved to 2 elements` strict mode ihlaliyle flaky kırmızı (yerel koşularda ~1/3 sıklıkta; seride 3. koşuda yeniden üretildi). CI her koşuda yeşildi ama kanıt güvenilirliği zedeleniyordu. | **DÜZELTİLDİ** — `linux-distro.spec.ts:36-39` ve `windows-node.spec.ts:34-36`: satır sayımından önce `await expect(page.getByText("Yükleniyor…")).toBeHidden()` ile liste yerleşimi bekleniyor. Doğrulama: **6 ardışık tam koşu 14/14 = 84/84 yeşil** (düzeltme öncesi 5 koşuluk ilk denemede ayrıca eksik `.or(empty-state)` beklemesi list dolu-rokysuz durumda kilitleniyordu; tek doğru bekleme loading göstergesinin kaybolmasıdır). |
| IT-02 | Düşük / bilgi | Rocky 10 / Alma 10 matriste desteklenen majör olarak beyan ediliyor; CI kanıtı şu an yalnız 9 container'larından geliyor. Zaten dürüstçe belgelenmiş: `docs/architecture/linux-support.md` §5.2 ("10 container job'a eklendiğinde kanıt eşitlenir"). Kabul edilebilir açık boşluk; iddia kanıttan geniş değil. | AÇIK (belgeli) — takip: sonraki fazlarda rockylinux:10/almalinux:10 job eklenmesi. |
| IT-03 | Bilgi | `ClassifyDistro` doc-comment'i "(family, like, state, reason)" diyor; imza 3 değer döndürüyor (like ayrı alana yazılıyor `detect_linux.go`). Yalnız yorum tutarsızlığı; davranış doğru ve testli. | AÇIK (kozmetik) — zorunlu düzeltme değil. |
| — | — | Yerelde Docker/buf olmadığı için gerçek-distro ve proto kapıları koşulamadı: tahminle PASS YAZILMADI; CI job kanıtıyla karşılandı (bkz. §2 ve §3). | NOT RUN/BLOCKED doğru işaretlendi |

## 5. Regresyon sonucu

Önceki fazların tüm otomatik katmanları bu koşuda aynen yeşil: agent 8/8 paket `-race` (Faz 02-05 paketleri dahil), `GOOS=windows` vet+build (Faz 05), backend 11/11, vitest 17/17, Playwright 14/14 (Faz 02 smoke + Faz 03 enrollment + Faz 04 telemetry + Faz 05 windows + Faz 06 linux-distro), secret-scan temiz, CI 10/10 (Faz 04 iki-distro matrisi ve Faz 05 windows-2025 job'ları dahil). Ubuntu/Debian davranışı değişmedi (kriter 2).

## 6. Güvenlik değerlendirmesi

- **Kural mutasyonu yüzeyi yok** (ADR-0010): preview `Executed=false` değişmezi; nftables/iptables için tahminle komut üretmek yerine dürüst ret.
- Yeni operasyonların dördü de `ClassReadOnly`, `RequiredPerm` katmanlı: `pkg:read` / `firewall:read` (yalnız YENİ izinler seed edildi — migration `0010_linux_distros.up.sql:13-23`, CI-04 dersi korunmuş; down migration mevcut).
- Üç katmanlı strict doğrulama (bilinmeyen alan reddi, kapalı enum'lar, sınırlı int'ler, zon allowlist `^[A-Za-z0-9_.-]{0,32}$`) control-plane + broker + agent executor'da tekrarlanıyor; negatif test setleri `ops_faz06_test.go:9-68`, `fwops_test.go:9-36`, `pkgops_test.go:36-66`.
- Sabit argv (dpkg-query/rpm/firewall-cmd/ufw/nft/iptables-save), shell yok; çıktı sınırları (1 MiB envanter, 64 KiB/400 satır kural listesi, 500 limiti); kontrol-karakter yankı koruması (`pkgops.go:97-106`).
- Broker JSONL audit her istek için (ret dahil) korunuyor; yetkisiz UID varsayılan deny.

## 7. Karar

- **Kritik/Yüksek bulgu:** yok.
- **Orta:** IT-01 (test altyapısı) — düzeltildi ve 6 ardışık koşuyla doğrulandı.
- **Faz kabul kararı: ACCEPTED**
- **Sonraki faza geçiş: READY** (bütün zorunlu kriterler PASS; kritik/yüksek bulgu yok).
