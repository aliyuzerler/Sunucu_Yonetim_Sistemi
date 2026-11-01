# ZCode Bağımsız Test Promptu — Faz 12: KVM/QEMU/libvirt sanallaştırma ve VPS

Test eden rolünde davran. Önce `agents.md`, ilgili mimari/güvenlik belgeleri, `docs/phases/phase-12.md` ve uygulama değişikliklerini incele. Uygulama kodunu yazan ajan gibi varsayım yapma; kanıt iste. Yalnızca bu fazı denetle ve sonraki fazı başlatma.

## Test hedefi

KVM/libvirt adapterı ile sanal makine oluşturma, başlatma/durdurma, snapshot, disk, network ve konsol metadata yönetimini geliştir.

## Zorunlu kabul kontrolleri

- [ ] Host virtualization capability, CPU flags, memory, storage pool ve bridge preflight edilmelidir.
- [ ] VM create/clone/start/stop/reboot/delete typed operation ve kaynak limitleriyle çalışmalı.
- [ ] Disk image path traversal ve host mount riski test edilmeli.
- [ ] Snapshot ve restore veri bütünlüğü ve disk formatına göre doğrulanmalı.
- [ ] VM oluşturmak guest OS yönetimi sağlamaz; guest Agent yoksa in-guest modüller unsupported gösterilmelidir.

## Test yaklaşımı

1. Kod incelemesi: mimari sınır, yetki kontrolü, hata yönetimi, secret redaction ve audit.
2. Otomatik testler: unit, integration, contract, lint, typecheck, build ve uygun security-negative testler.
3. Pozitif senaryolar: desteklenen işlem ve doğru sonuç.
4. Negatif senaryolar: yetkisiz kullanıcı, yanlış node, süresi geçmiş görev, bozuk payload, tekrar/replay, timeout, offline node ve unsupported capability.
5. Regresyon: önceki fazların testlerini çalıştır veya uygun olduğunda en azından ilgili contract testlerini doğrula.
6. Test ortamı yoksa sonucu NOT RUN/BLOCKED olarak işaretle; tahminle PASS verme.
7. Üretim sunucusuna, gerçek alan adına, gerçek mail akışına veya gerçek VM diskine dokunma. Disposable VM/container/mock kullan.

## Rapor biçimi

Her kriter için `PASS`, `FAIL`, `NOT RUN` veya `BLOCKED`; kanıt/komut; bulunan hata; önem derecesi; düzeltme önerisi ver. Sonunda:

- Kritik/Yüksek/Orta/Düşük bulgular
- Test komutları ve kısa çıktılar
- Regresyon sonucu
- Güvenlik değerlendirmesi
- Faz kabul kararı: `ACCEPTED` veya `REJECTED`
- Sonraki faza geçiş: yalnızca bütün zorunlu kriterler PASS ve kritik/yüksek bulgu yoksa `READY`; aksi halde `NOT READY`

Testleri geçmeyen kodu onaylama, zorunlu testi silme/skip etme veya kabul kriterini sessizce değiştirme.
