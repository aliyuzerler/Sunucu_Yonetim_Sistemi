# ADR-0016: KVM/QEMU/libvirt sanallaştırma yönetimi (opt-in vm ailesi)

Durum: Kabul edildi — Faz 12
Tarih: 2026-11-02

## Bağlam

Faz 12, KVM/libvirt tabanlı isteğe bağlı bir sanallaştırma modülü ister:
VM create/clone/start/stop/reboot/delete, disk, snapshot, network ve konsol
metadata yönetimi. Kabul kriterleri: (1) host capability/CPU flags/bellek/
storage pool/köprü preflight'ı; (2) kaynak limitli tipli VM işlemleri;
(3) disk image path traversal ve host-mount riskine karşı test; (4) snapshot/
restore'un veri bütünlüğü ve GERÇEK disk formatına göre doğrulanması;
(5) VM oluşturmak guest OS yönetimi sağlamaz — guest agent yoksa in-guest
modüller unsupported.

Önceki fazların kalıpları geçerlidir: gerçek mutasyonlar broker'da opt-in
kapının arkasında yapılır (`--web-enable` ADR-0012, `--db-enable/--fs-enable`
ADR-0014, `--mail-enable` ADR-0015); işlemler tipli ve auditlidir; şemalar
control-plane validator ↔ broker ↔ runner üç katmanında aynalanır.

Alternatifler: (a) libvirt Go kütüphanesi (libvirt.org/go) — CGO + libvirt-dev
derleme bağımlılığı getirir ve dependency-policy'nin stdlib disiplinini
bozar; (b) virt-install/virt-clone Python araç zinciri — ikinci bir yorumlayıcı
bağımlılığı ve opak davranış; (c) `virsh` + `qemu-img` CLI'larını sabit argv
ile çalıştırmak — libvirt'in kendi XML/istek sözleşmesini kullanır, yeni harici
Go bağımlılığı gerektirmez ve her argüman allowlist ile denetlenebilir.

## Karar

1. **Aile `vm.*`, `--vm-enable` ile OPT-IN (varsayılan KAPALI).** Kapalıyken
   tüm işlemler tipli `unsupported_operation` reddi döner (ADR-0012/0014/0015
   genelleştirmesi). Modül HİÇBİR paket kurmaz, libvirt'i başlatmaz:
   `vm.preflight` yalnız rapor verir — KVM varlığı (vmx/svm + /dev/kvm),
   CPU flags, bellek (MemTotal/MemAvailable), storage pool'lar (tip/boyut/
   boş alan/hedef yol), host köprüleri (/sys/class/net/*/bridge) ve libvirt
   ağları (kriter 1).

2. **Yürütme `virsh`/`qemu-img` sabit argv, stdlib yalnız.** Tüm dış
   çağrılar sabit ikili + argüman vektörüdür (kabuk yok, kullanıcı girdisi
   kabuğa hiç karışmaz). Kullanıcı adları (domain/volume/pool/bridge/
   snapshot) katı allowlist Regex'lerinden geçer: domain adı
   `^[a-z][a-z0-9-]{0,31}$`, pool `^[a-z0-9][a-z0-9_.-]{0,47}$`, köprü
   Linux ifname kuralları (≤15). Domain XML stdlib `encoding/xml` ile hem
   üretilir (değerler `xml.EscapeText` ile kaçırılır — savunma derinliği)
   hem ayrıştırılır.

3. **Disk görüntü YOLU payload'da ASLA yoktur (kriter 3).** Çağıran yalnız
   pool + birim adı (+ boyut) veya domain adı verir; görüntü yolları
   node'da libvirt'in KENDİ çıktısından (dumpxml / vol-path) türetilir ve
   yalnız `virsh`/`qemu-img` argümanı olarak kullanılır. Host tarafında
   görüntü bağlama (mount) yolu YAPISAL olarak yoktur: losetup/qemu-nbd/
   guestfish hiçbir kod yolunda çağrılmaz. Yönetilen birim ad alanı:
   bu adaptörün yarattığı her birim `sc-<vm>-*` adıyla bir libvirt pool
   içindedir; `vm.delete` yalnız bu birimleri kaldırır, yabancı diskte
   TİPLİ `ErrForeignVolume` ile TÜM silmeyi reddeder ve VM'i tanımlı
   bırakır (durum değişmez, operatör bilinçli karar verir).

4. **Snapshot'lar iç (internal) snapshot'tır ve qcow2 kapısı node'dadır
   (kriter 4).** snapshot create/revert ÖNCE her diskin GERÇEK formatını
   `qemu-img info --output=json` ile doğrular (XML'deki driver type'a
   güvenmez): `qcow2` değilse tipli `ErrDiskFormat`, XML ile gerçek biçim
   çelişirse `ErrFormatMismatch` ile reddeder. `vm.disk.list` de aynı
   doğrulamayı raporlar (`format_verified` + `qemu-img check` sonucu).
   Restore semantiği snapshot-revert'tir; revert retry-güvensiz
   `non_idempotent` sınıfındadır (ara durum tekrar geri dönüşte ikinci kez
   diskard edilir).

5. **Kaynak limitleri iki katmanda kapalı aralıktır (kriter 2).** Payload
   düzeyi: vCPU 1..64, RAM 128..262144 MiB, disk 1..4096 GiB. Node düzeyi:
   create sırasında /proc/meminfo toplam belleği, pool boş alanı, köprü/
   libvirt ağı varlığı ayrıca denetlenir; yetersizlikte tipli
   `ErrInsufficient`. graceful stop sınırlı pencerede bekler ve zaman
   aşımında TİPLİ çelişki döner — node kendi kendine asla destroy'a
   yükseltmez; `force=true` açık çağıran kararıdır (audit'e "forced" olarak
   yazılır).

6. **Guest OS dürüstlüğü (kriter 5).** Domain XML'e her zaman bir virtio
   guest-agent kanalı (`org.qemu.guest_agent.0`) konur (ileride agent
   kurulabilir), ancak VM oluşturmak guest OS yönetimi SAĞLAMAZ: `vm.info`
   kanal varlığını raporlar, UI in-guest modüllerin agent kurulana dek
   unsupported olduğunu açıkça yazar. Konsol yalnız METADATA'dır (tip/
   port/dinleme adresi): graphics cihazı yalnız 127.0.0.1'e bağlanır,
   panelde konsol akışı/proxy yoktur; `local_only=false` bir yapılandırma
   uyarısı olarak gösterilir.

7. **Kayıt yüzeyi.** Control plane registry 83→99 operasyona genişler
   (ops_faz12.go mirror validatorlar); RBAC'a yalnız YENİ `vm:read` /
   `vm:control` izinleri seed edilir (migration 0016, CI-04 dersi; admin
   hepsi, operator salt-okuma). Protokol additive'dir (proto değişikliği
   yok). Görev sınıfları: create/start/stop/disk.add/snapshot.create
   idempotent-desired-state; clone/reboot/snapshot.revert non-idempotent;
   delete/snapshot.delete destructive (retry-güvensiz).

## Sonuçlar

- Pozitif: Hipervizör yönetimi ürünün kalaninden aynı güvenlik sınırlarıyla
  (broker opt-in, tipli işlemler, yol yokluğu, managed namespace) gelir;
  CI'da gerçek libvirt lifecycle kanıtı üretilir (agent-vm job: KVM varsa
  KVM, yoksa TCG — SeaBIOS boot'suz VM "running" kalır, lifecycle kanıtı
  için yeterli ve deterministiktir).
- Negatif/sınır: İç snapshot'lar qcow2 ile sınırlıdır (raw disklerde
  external snapshot/backup bu fazda kapsam dışıdır). Konsol erişimi bu
  fazda metadata raporlamasıdır — akış/proxy (websockify benzeri) ayrı bir
  izin + kayıt gereksinimiyle sonraki fazlarda değerlendirilir. VM ağ
  tanımı (yeni köprü/NAT kurulumu) kapsamda değildir; mevcut köprü/ağ adları
  kullanılır. Migration/clone ADR'si (VM image import/export) sonraki fazlar.
- Takip: Otomatik SBOM (Faz 15); CI'da /dev/kvm yokluğunda TCG performans
  zamanlaması izlenir (timeout 20 dk).
