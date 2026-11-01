# ZCode Bağımsız Test Promptu — Faz 04: Linux Agent ve temel OS yönetimi

Test eden rolünde davran. Önce `agents.md`, ilgili mimari/güvenlik belgeleri, `docs/phases/phase-04.md` ve uygulama değişikliklerini incele. Uygulama kodunu yazan ajan gibi varsayım yapma; kanıt iste. Yalnızca bu fazı denetle ve sonraki fazı başlatma.

## Test hedefi

Linux Agent ve dar kapsamlı privileged broker geliştir. Ubuntu 24.04 ve Debian 13 üzerinde system facts, metrik ve izinli servis işlemlerini destekle.

## Zorunlu kabul kontrolleri

- [ ] Agent ve root broker ayrı servis/kimlik olmalı; broker yalnızca Unix socket üzerinden erişilebilir olmalı.
- [ ] OS/distro/version/systemd/package manager tespiti yapılmalı ve capability beyanı test edilmelidir.
- [ ] CPU/RAM/disk/network/uptime metrikleri kaynağı ve timestamp ile raporlanmalı.
- [ ] Servis listele/status/start/stop/restart işlemleri allowlist ve RBAC ile çalışmalı.
- [ ] İşlem girdileri shell string olarak birleştirilmemeli; güvenli broker ve audit zorunlu olmalı.

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
