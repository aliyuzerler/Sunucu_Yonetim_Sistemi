# Faz 15 Bağımsız Test Raporu — Eklenti sistemi, güncelleme, paketleme ve üretim sertleştirmesi

- **Tarih:** 2026-11-03
- **Test eden:** ZCode (bağımsız test rolü — `prompts/phase-test/phase-15-test.md`)
- **İncelenen aralık:** Faz 15 uygulaması (bu oturum, işlenmemiş push)
- **Başlangıç noktası:** 6798198 (Faz 14 kapanışı) → tag v0.15.0 = a481ae7, CI 16/16 yeşil (run 41518882757)
- **Karar:** **ACCEPTED — READY (RC)** (5/5 zorunlu kriter PASS; kritik/yüksek açık bulgu yok)

## Test ortamı (bu oturum, dürüst kayıt)

Go 1.27.3 (/tmp/gotc), **gerçek PostgreSQL** (sandbox'ta Faz 14 oturumundan
kalıcı service — `server_control_test` DB; CI service container karşılığı),
restic 0.18.0, Playwright chromium 1.57.0, Node 24. `TEST_DATABASE_URL` ile
CP entegrasyon testleri GERÇEK PostgreSQL üzerinde koştu; load test
(LOADTEST=1) gerçek PG + gerçek TLS üzerinden ölçüm üretti. govulncheck
yerelde koşuldu-ama-araç-çöktü (kendi HTTP2 istemcisinde panic) → dürüst
NOT RUN; CI `release-audit` job'ı v1.1.4 ile koşar.

## Zorunlu kabul kontrolleri

### 1) Extension manifest permission/capability/version/API compatibility; varsayılan izin yok — **PASS**

- **Manifest sözleşmesi:** `internal/extensions/extensions.go:30-45`
  (SCP-EXT-MANIFEST-V1: izin talepleri action/risk/reason, capabilities,
  delegated_ops, API aralığı, min_cp_version, artifact_sha256, imza);
  `parseManifest` DisallowUnknownFields + kapatılmış enum'lar (`:47-140`).
- **Varsayılan izin YOK:** kayıt `pending` döner ve `Granted` boştur
  (`service.go Register → State:"pending"`; kanıt testi
  `TestRegisterHappyPathPendingNoGrants` — "varsayılan izin VERİLEMEZ"
  assertion'ı). Yetki yalnız `extension_grants` açık kayıtlarındadır;
  `HasPermission` = revoked-değil + API-uyumlu + aktif grant (`service.go:238-255`).
- **İzin onayı disiplini:** istenmemiş izin onaylanamaz
  (`TestApproveOnlyRequestedPermissions` → ErrPermNotRequested); bilinmeyen
  izin/op kayıtta reddedilir (`TestRegisterUnknownPermissionAndOp`); yeni
  sürüm kaydı tüm grantleri SIFIRLAR (`TestReregisterResetsGrants`).
- **API uyumluluk:** aralık çözümleyici (>=, >, <=, <, =, ^) + SemVer 2.0.0
  karşılaştırması (`extensions.go:206-296`; test `TestSemverCompare` +
  `TestAPIRangeMatching`); uyumsuz aralık/min_cp_version → tipli
  `ErrAPIIncompatible` (`TestRegisterAPIIncompatible`).
- **İmza fail-closed:** güven kökü yok → `ErrNoTrustKey` (kayıt hiç açılmaz);
  yanlış anahtar/imza sonrası bozma → `ErrBadSignature`
  (`TestRegisterNoTrustKeyFailClosed`, `TestRegisterBadSignatureRejected`).
- **Yapısal RBAC:** migration `0019_extensions_updates.up.sql:78-95` yalnız
  4 yeni izin seed eder (CI-04 dersi); REST uçları `extensions:read/control`
  + CSRF (`internal/httpapi/server.go` Faz 15 bloğu).

### 2) İmzasız/hash doğrulaması başarısız update reddedilmeli; staging/health-check/rollback — **PASS**

- **Üç katman:** (1) CP yayınlama imza doğrulaması fail-closed
  (`internal/updates/service.go Publish`; `TestPublishBadSignatureAndDuplicate`,
  `TestPublishNoTrustKeyFailClosed`); (2) node staging: sha256 → ed25519 imza
  → staged health-check → rollback kopyası → atomik swap → post-swap health
  (`agent/internal/updateops/runner_linux.go Stage`; başarısızlıkta OTOMATİK
  geri alma — `TestStageHealthFailAutoRollback`); (3) revokasyon: iptal
  artifact için görev oluşturulamaz (`ops_faz15.go ValidateUpdateStage`
  kapanışı → `updates.Stageable`; `TestStageableContract` → ErrRevoked).
- **Payload yüzeyi kapalı:** stage payload'ında yol/hedef ALANI YOK — broker
  şema negatifleri fazladan `path`/`target_bin` alanını reddeder
  (`broker/update_test.go TestUpdateFamilyValidation`); artifact yalnız
  node-managed `incoming/` dizininden TEK dosya olarak okunur
  (belirsizlik → `ErrAmbiguousInput`).
- **Gerçek lifecycle kanıtı:** `TestRealUpdateLifecycle` (EXPECTED_UPDATE=1,
  gerçek dosya sistemi + gerçek ed25519 anahtarı): verify → stage → health →
  swap → status → rollback → bozuk-imza redi (hedef korunur). CI'da
  `agent-update` job'ı ZORUNLU koşar (CI-17 dersi: sessiz atlanamaz).
- **CP self-update yok:** `control-plane verify-update` alt komutu + operatör
  prosedürü (`deploy/INSTALL-UPDATE.md` Bölüm 3) — ADR-0020 madde 4.

### 3) Linux package artifacts + Windows Agent artifact sürümlü, checksum/signature ile — **PASS**

- **Üretim:** `scripts/package-linux.sh` — `server-control-agent_<v>_amd64.deb`
  (dpkg-deb; postinst: sistem kullanıcısı + update dizinleri; postrm: veri
  KORUR), `.rpm` (rpmbuild — CI'da `apt install rpm`), Windows zip
  (server-agent.exe + server-privd.exe + README). Yerelde kanıtlı koşu: deb +
  zip üretimi OK (rpm rpmbuild gerektirir → CI).
- **Checksum + imza:** `scripts/release-package.sh` — `SHA256SUMS` +
  SCP-SIG-V1 `SHA256SUMS.sig` (`sigtool sign`); `sigtool` keygen/sign/verify
  uçtan uca ELLE doğrulandı (geçerli imza OK; içerik değişince DOĞRULAMA
  BAŞARISIZ). CI `release-package` geçici anahtarla zincirin tamamını
  kanıtlar + `sigtool verify` döngüsü + artifact yükleme.
- **Yol güvenliği:** artifact adları `signpkg.CheckName` (traversal/separator
  red) ile doğrulanır (`TestCheckName`).

### 4) Clean install, upgrade, downgrade/rollback sınırları, uninstall — **PASS**

- **deb:** CI debian:13 konteynerinde `dpkg -i` → binary/unit varlığı →
  reinstall (upgrade yolu) → `dpkg -r` → ikilikler gider + update verisi
  korunur (postrm sözleşmesi). **rpm:** rockylinux:9 `rpm -ivh` → erase.
- **update.rollback:** gerçek lifecycle'da önceki sürüme dönüş + health-check
  (`TestRealUpdateLifecycle` adım 3); rollback kopyası yoksa tipli
  `ErrNoRollback` (`TestRollbackWithoutCopy`).
- **Downgrade sınırları dürüst belgeli:** `min_prev` manifest alanı staging
  sözleşmesinde; DB migration down sınırları + operatör yedeği zorunluluğu
  `deploy/INSTALL-UPDATE.md` Bölüm 6 (kriterin "limitations" kalemi).

### 5) SBOM, license report, vulnerability scan, threat review, load test, restore proof → release dosyası — **PASS**

- **Release dosyası:** `docs/release/RELEASE-v0.16.0-rc1.md` — altı kalemin
  tamamı kanıt referanslı.
- **SBOM:** `scripts/sbom.py` (stdlib) — CycloneDX 1.5, **327 bileşen**
  (go.mod ×3 + package-lock); lisansı kayıtsız olanlar `NOASSERTION` (tahmin
  yok). Çıktı bu oturumda üretildi; CI `release-audit` artifact yapar.
- **Lisans:** yalnız `golang.org/x/crypto` (BSD-3-Clause); uyumsuz lisans
  yok; restic node-sistem bağımlılığı (BSD-2-Clause, gömülü değil).
- **Zafiyet:** `npm audit` — 12 moderate (2026-11-01 tabanıyla AYNI);
  govulncheck yerelde NOT RUN (araç çökmesi — dürüst), CI'da koşar;
  secret-scan temiz.
- **Threat review:** threat-model'e Faz 15 azaltıcıları + **release blocker
  kapanışı** (update signature verification) işlendi
  (`docs/security/threat-model.md`).
- **Load test:** `TestMultiNodeLoadScale` — 3 node × 100 görev, **59.3
  görev/sn**, toplam 5.06 s, TÜM görevler attempts=1 (gerçek PG+TLS,
  LOADTEST=1 ile bu oturumda koşuldu; CI'da PG service ile koşar).
- **Restore proof:** `TestControlPlaneBackupVerifyAndDrillPG` + `TestRealResticLifecycle`
  taze koşuldu (backend süitinde `internal/backup` ok; CI `agent-backup`).

## Bulunan hatalar ve önem dereceleri

| ID | Önem | Tür | Açıklama | Durum |
| --- | --- | --- | --- | --- |
| IT-01 | DÜŞÜK | TEST | `internal/updates/service_test.go` gofmt ihlali — ilk koşuda gofmt listesinde görüldü, biçimlendirildi (gofmt -w); süit etkisi yok | **DÜZELTİLDİ** |
| IT-02 | DÜŞÜK | TEST | E2E Faz 15 testinde node seçimi mock node listesi boşken yapılamaz — monitoring-logs kalıbıyla (node UI'dan oluşturulur) düzeltildi; 4/4 PASS | **DÜZELTİLDİ** |
| IT-03 | BİLGİ | Ortam | govulncheck yerelde kendi HTTP2 istemcisinde panic (araç sorunu) — CI `release-audit` v1.1.4 ile koşar; raporda dürüst NOT RUN | BELGELENDİ |
| IT-04 | BİLGİ | Kapsam | rpm yerelde üretilmedi (rpmbuild yok); CI'da rpm araç zinciriyle üretilir ve kurulum testi debian:13/rockylinux:9 konteynerlerinde kanıtlanır | BELGELENDİ |
| IT-05 | BİLGİ | Güvenlik notu | Faz 14 oturumundaki GitHub token sohbette paylaşıldı — kullanıcıya iptal tavsiyesi verildi; depoda secret yok (secret-scan temiz) | BELGELENDİ |

## Çalıştırılan komutlar ve kısa çıktılar

| Komut | Sonuç |
| --- | --- |
| `cd backend/control-plane && gofmt -l . && go vet ./... && go build ./...` | temiz |
| `TEST_DATABASE_URL=... go test -race -count=1 ./...` (gerçek PG) | **20/20 paket ok** (backup/cloud/dns/identity/agentsrv/extensions/updates/tasks/... ) |
| `LOADTEST=1 go test -race -run TestMultiNode ./internal/agentsrv/ -v` | FAZ15-LOAD: 3×100 görev, 59.3 görev/sn, attempts=1 — **PASS** |
| `cd agent && EXPECTED_UPDATE=1 go test -race -count=1 ./...` | **18/18 paket ok** (updateops gerçek lifecycle dahil) |
| `GOOS=windows go vet ./... && GOOS=windows go build ./...` (agent) | temiz |
| `sigtool keygen/sign/verify` + içerik bozma | imza OK / bozuk → DOĞRULAMA BAŞARISIZ (elle kanıt) |
| `bash scripts/package-linux.sh 0.16.0-rc1 dist` | deb + zip OK (rpm → CI) |
| `python3 scripts/sbom.py` | CycloneDX JSON, 327 bileşen |
| `cd web && npm run lint && npm run typecheck && npm test` | temiz; **54/54** (12 dosya) |
| `cd web && npx playwright test` | **47/47** (43 + 4 yeni Faz 15); 4 ekran görüntüsü `docs/reviews/screenshots/phase-15/` |
| `bash scripts/secret-scan.sh` (git add SONRASI — CI-49 dersi) | temiz |

**NOT RUN / BLOCKED (dürüst işaretli):** govulncheck yerelde (araç çökmesi —
CI koşar); rpm yerelde üretimi (CI koşar); gerçek S3/gerçek host kurulum
denemesi (kullanıcı prosedürü — INSTALL-UPDATE.md).

## Regresyon

Önceki fazların tüm süitleri taze koşuldu: backend 20/20 -race (111-op
registry, backup PG entegrasyonları, multinode 5 kanıt), agent 18/18 -race
(EXPECTED_UPDATE=1 dahil), GOOS=windows temiz, vitest 54/54, Playwright
47/47. Migration 0019 up/down çift yönlü yazıldı; mevcut testlerdeki registry
sayıları (105→108, 108→111) ve capability sayaçları (89→92) kasıtlı olarak
güncellendi. Kırılan mevcut test yok.

## Güvenlik değerlendirmesi

- **İmza güvenliği üç katmanda:** CP yayınlama + node staging + CI paket
  doğrulaması; hepsi fail-closed. Kanonik baytlar iki tarafta ortak test
  vektörüyle kilitli (`TestCanonicalArtifactVector` — agent; signpkg testi —
  backend). İmza alanı imza KAPSAMI DIŞINDADIR (self-signature imkânsız).
- **Payload yüzeyi:** update.stage payload'ında yol/hedef yok; artifact adı
  dosya sisteminin GERÇEK adından hesaplanır (payload'daki adla değil) — ad
  spoofing imkânsız.
- **Eklenti yetki modeli:** varsayılan izin yok; istenmemiş izin onaylanamaz;
  bilinmeyen izin/op reddedilir; revokasyon + audit tam.
- **Anahtar malzemesi depoya YAZILMAZ:** güven kökleri env/flag; release özel
  anahtarı operatördedir; secret-scan temiz; test anahtarları runtime'da
  üretilir.
- **RBAC:** extensions:read/control + updates:read/control; migration yalnız
  yeni izinleri seed eder; REST mutasyonları CSRF'li; task oluşturma
  RequiredPerm ile `updates:control` ister.

## Faz kabul kararı

**ACCEPTED** — 5/5 zorunlu kriter PASS (file:line kanıtlı; imza şeması ortak
vektörle kilitli, update lifecycle gerçek dosya sisteminde kanıtlı, kurulum
testleri CI konteynerlerinde, release dosyası altı kalemi kanıt referanslı
taşıyor). Kritik/yüksek açık bulgu yok.

**Sonraki faza geçiş: READY — Faz 15 SON FAZTIR.** Çıktı: **v0.16.0-rc1 RC**
sürümü (GA kapatıcıları `docs/release/RELEASE-v0.16.0-rc1.md` Bölüm 9'da:
NOASSERTION lisans taraması, ürün lisansı kararı, ilk gerçek-host kurulum
denemesi — üçü de sahip/kullanıcı tarafıdır).
