# RELEASE — v0.16.0-rc1 (Release Candidate 1)

- **Tarih:** 2026-11-03
- **Faz:** 15 — Eklenti sistemi, güncelleme, paketleme ve üretim sertleştirmesi
- **Karar:** RC. Aşağıdaki kanıtlar ve bilinen sınırlamalarla üretim dağıtımı
  için **aday** sürüm; final GA öncesi tek kapatıcı madde Bölüm 9'daki açık
  kalemlerin sahip onayıdır.
- **Referanslar:** ADR-0019 (eklenti sözleşmesi), ADR-0020 (imzalı güncelleme
  hattı), `deploy/INSTALL-UPDATE.md` (operatör prosedürü).

## 1. Sürüm içeriği

| Alan | İçerik |
| --- | --- |
| Eklenti sözleşmesi | SCP-EXT-MANIFEST-V1: imzalı manifest (ed25519, kanonik JSON), izin/capability/API-uyumluluk deklarasyonu, **varsayılan izin YOK** (pending + tek-tek onaylı grant modeli), tip iş delegasyonu yalnız kayıtlı op adlarıyla (ADR-0019) |
| Güncelleme mekanizması | `update.stage/status/rollback` (broker `--update-enable` opt-in; payload'da yol alanı YOK); üç katmanda fail-closed doğrulama: CP yayınlama + node sha256/imza + health-check; otomatik rollback (ADR-0020) |
| Paketleme | deb + rpm + Windows zip (sürümlü), `SHA256SUMS` + SCP-SIG-V1 imzalar (`sigtool`); systemd unit'leri + postinst/postrm veri sözleşmesi |
| Kurulum testleri | CI `release-package`: debian:13 clean install → upgrade → uninstall; rockylinux:9 install → erase; zip/checksum/imza doğrulaması |
| Sürüm dosyası | Bu belge: SBOM + lisans + zafiyet + threat review + load test + restore proof + PRR |

Registry: 108 → **111** (executor'lu kurulum; +3 `update.*`); unix capability
89 → **92**; RBAC +4 izin (`extensions:read/control`, `updates:read/control`,
migration 0019 yalnız yeni izinleri seed eder). **Yeni harici bağımlılık YOK**
(imza + SBOM + paketleme yalnız stdlib/sistem araçları; restic BSD-2-Clause
yalnız node çalışma-zamanı — Faz 14 kaydı).

## 2. Artifact'lar ve doğrulama zinciri

| Artifact | Hedef | Doğrulama |
| --- | --- | --- |
| `server-control-agent_<v>_amd64.deb` | Debian/Ubuntu agent | sha256 (SHA256SUMS) + SCP-SIG-V1 imza (SHA256SUMS.sig) |
| `server-control-agent_<v>_amd64.rpm` | RHEL ailesi agent | aynı |
| `server-control-agent_<v>_windows_amd64.zip` | Windows agent | aynı (zip yapısı CI'da kanıtlanır) |

Zincir: `sigtool sign` (sürüm yöneticisinin özel anahtarı — depoya ASLA
yazılmaz) → `SHA256SUMS` + `SHA256SUMS.sig` → kurulum öncesi `sha256sum -c` +
`sigtool verify` → node update akışında İKİNCİ doğrulama (broker güven kökü).
CI `release-package` job'ı geçici anahtarla bu zincirin tamamını kanıtlar ve
`release-packages` artifact'ı olarak yayımlar.

## 3. SBOM

- **Üretici:** `scripts/sbom.py` (yalnız stdlib) — CycloneDX 1.5 uyumlu JSON.
- **Kapsam:** backend/control-plane + agent + gen/proto go.mod/go.sum ve
  web/package-lock.json.
- **Bileşen sayısı:** **327** (Go: 1 harici modül ailesi `golang.org/x/*`;
  npm: runtime + dev bağımlılıkları; lisansı kataloğda kayıtsız olanlar
  `NOASSERTION` olarak DÜRÜST işaretlenir — tahmin yazılmaz).
- **CI:** `release-audit` job'ı `sbom.json`'u artifact olarak yayımlar.
- **Politika bağlantısı:** `docs/technology/dependency-policy.md` madde 3.

## 4. Lisans raporu

- **Go:** yalnız `golang.org/x/crypto` (BSD-3-Clause) — uyumlu, dağıtım
  yükümlülüğü NOTICE/Attribution korunarak karşılanır.
- **npm:** package-lock lisans alanlarından; dev-only araç zinciri
  (MIT/Apache-2.0/ISC/BSD ağırlıklı) çalışma-zamanı dağıtımına giren JS
  kümesinden ayrı raporlanır (SBOM `scope` alanı).
- **Uyumsuz lisans (SSPL/Common Clause/AGPL) taraması:** kayıtlı katalogda
  YOK; `NOASSERTION` kalan npm bileşenleri GA öncesi tarama listesindedir
  (Bölüm 9). Yeni harici Go bağımlılığı bu fazda eklenmedi.
- **restic:** node'da SİSTEM bağımlılığı olarak kurulur (paket yöneticisi;
  BSD-2-Clause) — ürün dağıtımına gömülmez (Faz 14 kaydı, değişmedi).

## 5. Zafiyet taraması

| Tarama | Sonuç | Kanıt |
| --- | --- | --- |
| `npm audit` (web) | **12 moderate — bilinen taban değişmedi** (2026-11-01 kaydıyla aynı; eslint dev-only zinciri) | CI `release-audit` (`npm-audit.json` artifact) |
| `govulncheck` (backend + agent) | Yerelde koşulamadı (araç kendi HTTP2 istemcisinde çöktü — dürüst NOT RUN); CI `release-audit` job'ı govulncheck v1.1.4 ile koşar ve çıktıyı artifact yapar | `.github/workflows/ci.yml` release-audit |
| `secret-scan.sh` | Temiz (bu fazın tüm koşularında) | CI `secret-scan` |
| Threat-model release blocker | **KAPANDI:** "update signature verification" azaltıcısı bu sürümle üç katmanda kanıtlandı (Bölüm 6) | `docs/security/threat-model.md` |

## 6. Threat review (güncelleme/eklenti adversitiesi)

Tehdit modelindeki "malicious/misconfigured extension or dependency" ve
"Network attacker ... downgrade" adversitieslerine karşı bu fazın eklediği
azaltıcılar:

1. **İmzasız update imkânsız:** CP yayınlama imza zorunlu; node staging
   sha256+imza fail-closed; health-check + otomatik rollback. Kanıt:
   `TestStageRejectsUnsignedAndTampered`, `TestRealUpdateLifecycle`
   (EXPECTED_UPDATE=1 — CI `agent-update`), broker şema negatifleri
   (`TestUpdateFamilyValidation` — payload'da yol/hedef alanı ASLA yok).
2. **Eklenti = imzalı manifest + izinsiz başlangıç:** varsayılan izin yok;
   onaylanmamış izin için talep tipli `ErrPermNotRequested`; bilinmeyen
   izin/op kayıtta reddedilir; yeni sürüm tüm onayları sıfırlar. Kanıt:
   `TestRegister*`, `TestApproveOnlyRequestedPermissions`,
   `TestReregisterResetsGrants` + PG entegrasyon yolu.
3. **Revokasyon:** iptal edilen update artifact için görev oluşturulamaz
   (validator kapanışı `Stageable`); eklenti devre dışı bırakma tüm grantleri
   geri alır; her onay/geri alma auditli.
4. **Anahtar ayrımı korunur:** imza güven kökleri ortam/bayrakla gelir,
   depoya yazılmaz; CP_SECRETS_KEY disiplini (Faz 14) değişmedi.

## 7. Load test (gerçek PG + gerçek TLS)

`internal/agentsrv/loadtest_test.go` — `TestMultiNodeLoadScale` (LOADTEST=1):

| Ölçüm | Değer (bu oturum, yerelde gerçek PostgreSQL 16 + TLS) |
| --- | --- |
| Ölçek | 3 sanal node × 100 görev = **300 tip iş (agent.echo)** |
| Görev oluşturma | 136 ms |
| Tamamlama (toplam) | 5.06 s |
| **Throughput** | **59.3 görev/sn** |
| Disiplin | TÜM görevler `succeeded`, **attempts=1** (tek deneme, doğru node sahipliği) |

Aynı test CI'da PostgreSQL service container ile koşar (agent iş gücü
ölçümü; sınırlar "kırılma eşiği" değil, kanıt ölçümüdür).

## 8. Restore proof (kriter 5 — release dosyası girdisi)

- **CP yedeği:** `TestControlPlaneBackupVerifyAndDrillPG` — Dump → verify →
  İZOLE restore tatbikatı (`scp_drill_<8hex>` DB, migration şema + COPY +
  setval + satır karşılaştırma + DROP) gerçek PostgreSQL üzerinde; bu oturumda
  taze koşuldu (backend süiti `internal/backup` ok).
- **Node yedeği:** `TestRealResticLifecycle` — restic 0.18.0 ile init →
  backup → snapshot list → verify (`--read-data-subset=10%`) → İZOLE restore
  + içerik kanıtı; CI `agent-backup` job'ı GERÇEK restic ile kanıtlar
  (Faz 14 run 41518882757 ve sonraki koşular).
- **Doğrulama disiplini:** doğrulamasız yedek asla `success` olamaz
  (`verify_state/verify_at`); anahtar sızma taraması fail-closed
  (`ErrKeyMaterialInBackup`).
- **Operatör prosedürü:** her CP yükseltmesi ÖNCE `backup.cp.run` + verify +
  drill ile başlar (`deploy/INSTALL-UPDATE.md` Bölüm 3).

## 9. Production readiness review (PRR)

| Alan | Durum | Not |
| --- | --- | --- |
| Kimlik/oturum/RBAC (Faz 02-15) | ✅ | typed izin kataloğu; migration seed disiplini (CI-04 dersi) |
| Agent mTLS + iptal (Faz 03/12/14) | ✅ | multinode 5 kanıt + cert revoke testi |
| İmzalı update (bu faz) | ✅ | üç katman fail-closed; Windows aktivasyonu operatör prosedürü (dürüst sınır) |
| Eklenti sözleşmesi (bu faz) | ✅ | varsayılan izinsiz; genişleme (sandbox'lı çalışma-zamanı) ayrı ADR gerektirir |
| Yedek/restore kanıtı (Faz 14) | ✅ | Bölüm 8 |
| İzleme/alarm/güvenlik merkezi (Faz 13) | ✅ | 8 kanıt-temelli kontrol, dürüst unknown |
| Load kanıtı | ✅ | Bölüm 7 (59.3 görev/sn, attempts=1) |
| Paketleme + kurulum kanıtı | ✅ | CI release-package (debian:13 + rockylinux:9) |
| SBOM/lisans/zafiyet | ✅ (NOASSERTION taraması GA kalemi) | Bölüm 3-5 |
| Ürün lisansı kararı | ⏳ Sahip kararı | Phase 00'dan taşınan açık karar (dağıtım öncesi) |
| Windows update aktivasyonu | ⏳ Operatör prosedürü | ADR-0020 madde 3 — SCM dur/değiştir/başlat |
| Güven kökü rotasyonu | ⏳ İleri çalışma | tek anahtar RC; rotasyon ADR revizyonu gerektirir |

**GA kapatıcıları (RC → final):** (1) `NOASSERTION` npm bileşenleri için
lisans taraması, (2) sahip ürün lisansı kararı, (3) ilk gerçek host kurulum
denemesi (disposable VM) — üçü operatör/kullanıcı tarafıdır, kod engeli yok.

## 10. Bilinen sınırlar (dürüst kayıt)

1. Control Plane kendi kendini güncellemez — imzalı artifact + verify-update
   + önce-yedek prosedürü operatördedir (ADR-0020 madde 4).
2. Update aktivasyonu node'da servis restart'ı gerektirir (mevcut tip iş
   `linux.systemd.service.restart`); zero-downtime self-swap bu fazın
   kapsamı değildir.
3. Tek güven kökü; çoklu anahtar/rotasyon ileri çalışma.
4. `govulncheck` yerelde koşulamadı (araç çökmesi); CI artifact'i beklenir.
5. Downgrade, `migrate down` belgelenmiş sınırlarına tabidir — geriye
   uyumsuz şema adımlarında geri alma YOK (ileri yama yolu).
