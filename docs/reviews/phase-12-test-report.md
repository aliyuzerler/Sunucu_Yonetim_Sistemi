# Faz 12 Bağımsız Test Raporu — KVM/QEMU/libvirt Sanallaştırma ve VPS

- Test eden: ZCode (bağımsız test rolü — `prompts/phase-test/phase-12-test.md`)
- Tarih: 2026-11-02
- İncelenen commit: `23a01d9` (Faz 12 uygulaması) + bu koşuda yapılan düzeltmeler
- Ortam: sandbox (Node 24, Go 1.27.3 — resmi checksum'la kuruldu; Docker/
  PostgreSQL/**libvirt ve /dev/kvm YOK**). Gerçek libvirt kanıtı CI `agent-vm`
  job'ına aittir (EXPECTED_VM=1).

## Zorunlu kabul kontrolleri

### 1. Host capability/CPU flags/bellek/pool/köprü preflight — PASS

- Kanıt: `agent/internal/vmops/runner_linux.go:745` (`Preflight` — rapor-only;
  virsh yoksa bile rapor döner, `Notes` dürüst sınır yazar),
  `runner_linux.go:161` (`kvmAvailable` = vmx/svm **ve** /dev/kvm),
  `:202` (`readMemInfo` MemTotal/MemAvailable), `:328` (pool free/capacity
  parse), `hostBridges` (/sys/class/net/*/bridge) + `listNetworks`.
- Modül kurulum/etkinleştirme yüzeyi YOK (UI testi: `NodeDetailVm.test.tsx`
  "preflight report-only'dur: kurulum butonu ASLA yoktur" — kriter 1).
- Hermetik test: `TestPreflightReportOnly` (KVM/pool/bridge/network/version
  parse), `TestPreflightWithoutKVMReportsHonestNote` (TCG dürüst notu).
- Gerçek kanıt: CI agent-vm (EXPECTED_VM=1) — sandbox'ta **NOT RUN** (aşağıda).

### 2. VM create/clone/start/stop/reboot/delete tipli op + kaynak limitleri — PASS

- Kanıt (üç katman): CP `ops_faz12.go:236` `buildFaz12Registry` (16 spec;
  create/start/stop/disk.add/snapshot.create idempotent; clone/reboot/
  snapshot.revert non-idem; delete/snapshot.delete destructive) —
  `ops_faz12_test.go` sınıf/izin şekil testleri; broker
  `protocol.go:906` limit redleri + `exec_unix.go:116` kapı; runner
  `runner_linux.go:947` (`Create` — MemTotal + pool free + köprü/ağ
  varlığı denetimi), `:1052` (`Clone` — shut-off zorunlu), Stop/Reboot
  durum korumaları.
- Limit negatifleri: `TestCreateRefusals` (vcpu 0/65, mem 127/262145,
  disk 4097, host belleği yetersiz → `ErrInsufficient`), broker
  `TestVMFamilyValidation` (aynı sınırlar broker katmanında), CP
  `TestVMValidatorsRejectBadPayloads`.
- Yaşam döngüsü: `TestCreateLifecycleAndXMLShape` (XML: sc- birimi, vnc
  127.0.0.1, guest kanalı, kvm hızlandırıcı), `TestStopGracefulTimeoutAndForce`
  (graceful zaman aşımı tipli çelişki — destroy YOK; force ayrı karar),
  `TestStartRebootStateGuards`, `TestCreateRollsBackVolumeOnDefineFailure`.
- Gerçek kanıt: CI `TestRealVMLifecycle` (pool → create → disk.add → gerçek
  format doğrulaması → snapshot create/revert → start running → force stop →
  clone → delete; yönetilen birim silimi diskten doğrulanır).

### 3. Disk image path traversal ve host mount riski — PASS

- Kanıt: payload şemasında YOL alanı yok (`protocol.go:296` `vmCall` —
  yalnız name/source/pool/bridge/snapshot/vcpu/mem/disk/force/limit);
  yollar libvirt çıktısından türetilir (`diskSource` — dumpxml/vol-path);
  host mount kod yolu yok (losetup/qemu-nbd/guestfish hiçbir yerde
  çağrılmaz — repo-genel arama boş).
- Ad allowlist'leri: `vmops.go:124` (`ValidVMName`), `ManagedVolume:147`
  (sc- kapısı); negatifler: `TestNameAndLimitAllowlists` ("../../etc",
  "a/b", "vm;rm", "vm<br>", "sc-/../x"...), broker şema negatifleri
  (20 vektör), CP mirror negatifleri.
- Yabancı birim kapısı: `runner_linux.go:1328` `Delete` — yalnız sc-
  birimleri; `TestDeleteManagedGateAndLifecycle` (yabancı dosya diski +
  yabancı birim adı → `ErrForeignVolume`, VM tanımlı kalır), kısmi silme
  kurtarma testi. Gerçek kanıt: CI `TestRealVMForeignVolumeRefusal`.

### 4. Snapshot/restore veri bütünlüğü ve disk formatına göre doğrulama — PASS

- Kanıt: `runner_linux.go:724` `verifyQcow2Gate` — GERÇEK format
  `qemu-img info --output=json` ile saptanır (XML driver type'a güvenilmez):
  raw → `ErrDiskFormat` (:731), XML≠gerçek → `ErrFormatMismatch` (:734);
  `vm.disk.list` `format_verified` + `qemu-img check` raporlar.
- Testler: `TestSnapshotQcow2Gate` (raw gerçek format → red; XML raw +
  gerçek qcow2 → uyuşmazlık red), `TestSnapshotLifecycle` (create doğrulanmış
  formatları döner, mükerrer red, revert/delete + not-found redler),
  revert `non_idempotent + retry-güvensiz` (registry şekil testi).
- Gerçek kanıt: CI lifecycle'da snapshot create/revert + iki diskin de
  `format_verified=true` doğrulaması.

### 5. Guest OS yönetimi dürüstlüğü — PASS

- Kanıt: `runner_linux.go` `Info` — guest_agent kanal varlığı raporlanır
  (kanal XML'de TANIMLIDIR, yönetim SAĞLAMAZ); UI
  `VmPanel.tsx` `vm-guest-note` (her zaman görünür) +
  `nodedetail.vm.info.guestagent.no` = "in-guest modüller unsupported";
  vitest kriter-5 testi + e2e "dürüst guest sınırı yerinde" testi.
- Consol yalnız metadata: VNC `listen='127.0.0.1'` (renderDomain), panel
  `local_only` rozeti, akış/proxy kodu yok.

## Test komutları ve kısa sonuçlar

| Komut | Sonuç |
|---|---|
| `cd agent && gofmt -l .` | temiz (0 dosya) |
| `cd agent && go vet ./... && go build ./...` | temiz |
| `cd agent && go test -race -count=1 ./...` | 16/16 paket ok (vmops 15 test) |
| `cd agent && go test -count=2 ./internal/vmops/` | ok (×2 stabilite) |
| `GOOS=windows go vet ./... && GOOS=windows go build ./...` | temiz |
| `cd backend/control-plane && gofmt -l .` | temiz (0 dosya) |
| `cd backend/control-plane && go vet ./...` | temiz |
| `cd backend/control-plane && go test -race -count=1 ./...` | 16/16 paket ok |
| `cd web && npx tsc --noEmit` | temiz |
| `cd web && npx vitest run` | 40/40 (5 yeni VmPanel testi) |
| `cd web && npm run build` | temiz |
| `cd web && npx playwright test` | 34/34 (4 yeni vm.spec) |
| `bash scripts/secret-scan.sh` | temiz ✓ |
| CI `agent-vm`: `EXPECTED_VM=1 TestRealVMLifecycle + TestRealVMForeignVolumeRefusal` | **NOT RUN (sandbox)** — CI'da zorunlu; gate sessiz atlanamaz (CI-17 dersi: ortam eksikse job BAŞARISIZ) |
| Gerçek KVM/VM diski üzerinde test | **BLOCKED/YAPILMADI** — tasarım gereği; disposable runner yalnız CI'da |

## Bulunan hatalar ve düzeltmeler (bu bağımsız koşuda)

- **IT-01 (ORTA, düzeltildi):** `Clone`'da kopya birim adı, ham disk
  döngü indeksiyle üretiliyordu; cdrom/floppy atlanan disklerde birim adı
  soneki (`sc-<vm>-vdX`) ile render edilen hedef aygıtı (`vdY`)
  kayabiliyordu. Düzeltme: harf kopya sırasından türetilir; hermetik test
  mevcut akışta doğrulanır (tek diskli fixture aynı sonucu korur).
- **IT-02 (ORTA, düzeltildi):** `Clone` anchor havuzu `dir` tipinde
  değilde `qemu-img convert` hedefi havuz hedef yoluna dosya yazmaya
  çalışıyordu (logical/zfs pool'da yanlış konum). Düzeltme: anchor pool
  tipi `dir` değilse tipli `ErrConflict` — nereden kopyalanacağı
  TAHMİN EDİLMEZ. Yeni test: `TestCloneRefusesNonDirAnchorPool`.
- **IT-03 (DÜŞÜK, düzeltildi):** ADR-0015, `docs/decisions/README.md`
  indeksine Faz 11'de eklenmemişti (belge boşluğu). Düzeltme: ADR-0015 +
  ADR-0016 satırları birlikte eklendi.
- **IT-04 (DÜŞÜK, kabul edilebilir sınır — belgelendi):** Broker
  `ExecTimeout` varsayılanı 60s, `vm.clone` registry zaman aşımı 5m;
  büyük diskte yavaş depoda clone önce broker zaman aşımına takılabilir.
  Davranış tipli ve dürüsttür (executor_error); operatör `--timeout` ile
  broker penceresini büyütebilir. `virtualization.md` operasyon notu.
- **IT-05 (BİLGİ):** `vm.create` mükerrer ad → tipli `ErrExists`
  (FAILED) — RetrySafe=true'dur; istenen son durum "VM mevcut" olduğundan
  çelişki operatör çözümler. Faz 11 `mail.domain.create` deseniyle birebir
  tutarlı (bilinçli tekrar).
- **IT-06 (BİLGİ):** `internal/identity` `TestVerifyRejectsTamperedPHC`
  ilk tam süitte 1 kez düştü; ×3 tekrar ve tam süit tekrarında yeşil —
  argon2 zamanlama varyansı (yük altında), Faz 12 ile ilgisiz. İzleme notu.

## Regresyon sonucu

- Agent: 16/16 paket -race yeşil; capability sayısı 67→83 assertion'ları
  güncellendi (`agent_test.go`, `service_exec_test.go`).
- Backend: 16/16 paket -race yeşil; registry 83→99 assertion'ı güncellendi
  (`service_test.go`); migration 0016 embed + rbac testleri yeşil.
- Web: vitest 40/40; Playwright 34/34 (önceki faz spec'leri dahil) —
  yalnız Faz 12 ekran görüntüleri yenilendi, diğer fazların görüntüleri
  commit edilmedi.
- Secret-scan temiz; GOOS=windows vet+build temiz (vm ailesi unix-only,
  windows broker'a sızmıyor).

## Güvenlik değerlendirmesi

- Serbest metin kabuk yolu yok: tüm çağrılar sabit ikili + argv; adlar
  üç katmanda allowlist'li; XML değerleri `xml.EscapeText` ile kaçırılıyor.
- Aile opt-in (varsayılan KAPALI): kapalıyken 16 op'un tümü tipli
  `unsupported` (broker testi); yetki katmanı `vm:read`/`vm:control`,
  migration yalnız YENİ izinleri seed ediyor.
- Disk yolu payload'da hiç yok → traversal/enjeksiyon yüzeyi ad
  allowlist'lerine sıkışıyor; host mount yapısal olarak yok.
- Yıkıcı yüzey: delete yalnız `sc-` namespace'e dokunur (yabancı diskte
  tipli red + durum değişmez), graceful stop kendi kendine destroy'a
  yükselmez, revert/destructive'lar retry-güvensiz işaretli, audit her
  isteği (target dahil) kaydeder.
- Secret: vm payload'ında parola/gizli değer yok (reddilecek bir şey de
  yok — scrubber'a taşınacak materyal üretilmiyor); staged XML dosyaları
  0600 + staging dizini 0750.

## Karar

- **Kabul: ACCEPTED** — beş zorunlu kriterin tümü PASS (kanıt: üç katman
  kod incelemesi + hermetik negatif suites + regresyon; gerçek lifecycle
  kanıtı CI agent-vm'e bağlı ve gate zorunlu).
- **Sonraki faza geçiş: READY** — kritik/yüksek bulgu yok; IT-01/IT-02
  düzeltildi ve regresyonla güvenceye alındı; IT-04 dürüst sınır olarak
  belgelendi. Sahibin CI koşusunu (15 job) ve raporu onaylaması beklenir.
