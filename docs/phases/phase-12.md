# Faz 12 — KVM/QEMU/libvirt sanallaştırma ve VPS

## Amaç

KVM/libvirt adapterı ile sanal makine oluşturma, başlatma/durdurma, snapshot, disk, network ve konsol metadata yönetimini geliştir.

## Ön koşullar

- Önceki fazın test raporu PASS olmalı (Faz 00 için dokümantasyon tabanı hazır olmalı).
- İlgili ADR, mimari, teknoloji ve güvenlik belgeleri okunmalı.
- Gerçek üretim sunucusu yerine mock veya disposable test ortamı kullanılmalı.

## Kapsam / kabul kriterleri

- [ ] Host virtualization capability, CPU flags, memory, storage pool ve bridge preflight edilmelidir.
- [ ] VM create/clone/start/stop/reboot/delete typed operation ve kaynak limitleriyle çalışmalı.
- [ ] Disk image path traversal ve host mount riski test edilmeli.
- [ ] Snapshot ve restore veri bütünlüğü ve disk formatına göre doğrulanmalı.
- [ ] VM oluşturmak guest OS yönetimi sağlamaz; guest Agent yoksa in-guest modüller unsupported gösterilmelidir.

## Kapsam dışı

- Sonraki fazların özelliklerini erken ekleme.
- Kullanıcı istemedikçe ücretli servis veya lisanslı ürün entegrasyonu ekleme.
- Üretim sunucusunda riskli değişiklik yapma.

## Teslimatlar

Çıktı: libvirt adapter, VM lifecycle tests, failure/rollback ve host requirements dokümanı.

## Faz çıkış raporu

ZCode `docs/project-status.md` ve `CHANGELOG.md` dosyasını güncellemeli; uygulanan değişiklikleri, test komutlarını, PASS/FAIL/NOT RUN/BLOCKED durumlarını, açık riskleri ve sonraki faza geçiş uygunluğunu raporlamalı.
