# Sanallaştırma Mimarisi — KVM/QEMU/libvirt Adaptörü (Faz 12)

## Genel Bakış

`agent/internal/vmops` paketi, node'da KVM/QEMU/libvirt ile sanal makine
yaşam döngüsünü (create/clone/start/stop/reboot/delete), disk ve snapshot
yönetimini ve konsol metadata raporlamasını sağlar. Tüm yürütme ayrıcalıklı
broker'da (`server-privd --vm-enable`) **sabit argv** ile yapılır — kabuk yok,
serbest metin yok (ADR-0003, ADR-0016).

```
Control Plane ──gRPC(mTLS)──▶ Agent (server-agent, yetkisiz)
                                 │  vm.* executor (relay, byte-capped)
                                 ▼
                          Unix socket (SO_PEERCRED)
                                 ▼
                    Broker (server-privd, ROOT, --vm-enable)
                        │ fixed argv
                        ▼
              virsh / qemu-img ──▶ libvirtd ──▶ QEMU/KVM
```

## Güvenlik modeli

| Katman | Kural |
|---|---|
| Control plane | `ops_faz12.go` mirror validatorlar; `vm:read`/`vm:control` RBAC; audit |
| Broker | `--vm-enable` kapısı (VARSAYILAN KAPALI); strict şema; allowlist adlar; hata kategori eşlemesi |
| Runner | Sabit argv (`virsh -q --connect <uri> <cmd>`); allowlist Regex'ler; XML escape; managed `sc-` namespace; yol payload'da yok |

### Disk yolu disiplini (kriter 3)

Payload'da **hiçbir yerde** disk görüntü yolu alanı yoktur. Kullanıcı:

- `vm.create` → `{name, vcpu, mem_mib, disk_gib, pool, bridge}`
- `vm.disk.add` → `{name, pool, size_gib}`
- `vm.delete` → `{name}`

Görüntü yolları yalnız libvirt'in kendi çıktısından türetilir
(`virsh dumpxml` → `<source>`; `virsh vol-path`). Runner host tarafında
görüntü **bağlamaz**: losetup/qemu-nbd/guestfish hiçbir kod yolunda yoktur.
`vm.delete` yalnız `sc-<vm>-*` önekli birimleri kaldırır; yabancı diskte
tüm silme tipli `ErrForeignVolume` ile reddedilir ve VM tanımlı kalır.

Negatif testler (hermetik): `../../etc`, `VM1`, `vm;rm`, `vm<br>`, `a/b`
adları; `/etc/evil` pool'u; boşluklu köprü; XML enjeksiyon vektörleri —
broker validateRequest katmanında reddedilir, runner'a ulaşmaz.

### Snapshot ve format doğrulaması (kriter 4)

İç snapshot'lar (`virsh snapshot-create-as`) **yalnız qcow2** diskinde
tanımlıdır. Runner, snapshot create/revert ÖNCESİ her diskin gerçek
formatını `qemu-img info --output=json` ile saptar:

- gerçek format ≠ `qcow2` → tipli `ErrDiskFormat`
- XML driver type ≠ gerçek format → tipli `ErrFormatMismatch`
- qcow2 ise `qemu-img check` sonucu `vm.disk.list` raporunda taşınır

Restore semantiği snapshot-revert'tir ve `non_idempotent` sınıfındadır
(ikinci revert ara durumu yeniden diskard eder — retry-güvensiz işaretli).

### Kaynak limitleri (kriter 2)

| Sınır | Aralık | Katman |
|---|---|---|
| vCPU | 1..64 | CP + broker + runner (host çekirdek sayısı denetlenmez — honest not) |
| RAM | 128..262144 MiB | CP + broker + runner (/proc/meminfo MemTotal ile karşılaştırma) |
| Disk | 1..4096 GiB | CP + broker + runner (pool free_bytes ile karşılaştırma) |
| Domain adı | `^[a-z][a-z0-9-]{0,31}$` | CP + broker + runner |
| Pool adı | `^[a-z0-9][a-z0-9_.-]{0,47}$` | CP + broker + runner |
| Köprü/ağ | Linux ifname (≤15) | CP + broker + runner (host köprüleri ∪ libvirt ağları) |

### Guest OS dürüstlüğü (kriter 5)

VM oluşturmak **guest OS yönetimi sağlamaz**. Domain XML'i virtio guest-agent
kanalı içerir (ileride agent kurulabilir); `vm.info` kanal varlığını raporlar
(`guest_agent`), UI in-guest modüllerin (paket/servis/web/db/mail) agent
kurulana dek unsupported olduğunu açıkça gösterir. Konsol yalnız metadata:
VNC cihazı **yalnız 127.0.0.1**'e bağlanır; akış/proxy bu fazda yoktur.

## Yaşam döngüsü ve rollback

| İşlem | Adımlar | Başarısızlıkta |
|---|---|---|
| `vm.create` | domuuid (var mı?) → pool aktif+boş alan → köprü/ağ → bellek → `vol-create-as` (sc-<vm>-root, qcow2) → XML staging (0600) → `virsh define` | define başarısız → birim geri silinir (best-effort) → tipli `ErrConflict` |
| `vm.clone` | kaynak `shut off` zorunlu → dumpxml → her disk `qemu-img convert -O qcow2` (yeni sc- birim) → yeni UUID/MAC → define | define başarısız → kopya birimler silinir |
| `vm.start` | domstate; `running` ise dürüst no-op (`already`) | `virsh start` hatası → typed error |
| `vm.stop` | graceful `virsh shutdown` + sınırlı bekleme (60s) | zaman aşımı → tipli `ErrConflict` (destroy YAPILMAZ); `force=true` → `virsh destroy` (audit: forced) |
| `vm.delete` | `shut off` zorunlu → dumpxml disk envanteri → sc- kapısı → `virsh undefine [--nvram]` → `vol-delete` | kısmi silme sonrası ikinci koşu kurtarır (undefine not-found = tamam sayılır) |
| `vm.disk.add` | sonraki boş `vd*` → `vol-create-as` → `attach-device --persistent` | attach başarısız → birim geri silinir |
| `vm.snapshot.*` | qcow2 kapısı → `snapshot-create-as/revert/delete` → listede doğrulama | yoksa/kural dışıysa tipli red |

## Host gereksinimleri (operatör için)

- `qemu-kvm`, `qemu-utils` (qemu-img), `libvirt-daemon-system`,
  `libvirt-clients`; libvirtd çalışır durumda (`systemctl start libvirtd`).
- KVM için CPU sanallaştırma bayrağı (vmx/svm) + erişilebilir `/dev/kvm`
  (yoksa preflight dürüstçe TCG-only notu verir; TCG yavaştır).
- En az bir **aktif** storage pool (ör. `virsh pool-define-as default dir
  --target /var/lib/libvirt/images && virsh pool-start default`).
- VM ağı: host köprüsü (`br0`…) veya libvirt ağı (`default` NAT).
- Broker: `server-privd --vm-enable` (root); agent kullanıcısı socket grubu.

## Test kanıtı

- Hermetik (sandbox): sahte `virsh`/`qemu-img` ikilileri ile stateful
  simülasyon — 14 test (preflight parse, create XML şekli + rollback,
  traversal/limit negatifleri, graceful stop zaman aşımı + force, yabancı
  birim kapısı, kısmi silme kurtarma, qcow2 kapısı + format uyuşmazlığı,
  snapshot/disk/clone yaşam döngüleri, ErrUnavailable).
- Gerçek host (CI `agent-vm` job, `EXPECTED_VM=1`): gerçek libvirt ile
  pool → create → disk.add → **gerçek** qemu-img format doğrulaması →
  snapshot create/revert → start (`running`) → force stop → clone → delete
  (yönetilen birim silimi diskten doğrulanır) + elle kurulmuş yabancı
  diskin tipli reddi. Gate sessiz atlanamaz (CI-17 dersi).
