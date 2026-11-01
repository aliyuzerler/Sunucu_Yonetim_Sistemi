# ZCode Bağımsız Test Promptu — Faz 10: Veritabanları ve güvenli dosya yöneticisi

Test eden rolünde davran. Önce `agents.md`, ilgili mimari/güvenlik belgeleri, `docs/phases/phase-10.md` ve uygulama değişikliklerini incele. Uygulama kodunu yazan ajan gibi varsayım yapma; kanıt iste. Yalnızca bu fazı denetle ve sonraki fazı başlatma.

## Test hedefi

MariaDB/MySQL ve PostgreSQL yönetim adaptörlerini; dosya listeleme/yükleme/indirme ve izin yönetimini geliştir.

## Zorunlu kabul kontrolleri

- [ ] Panelin kendi PostgreSQL veritabanı ile yönetilen müşteri DB instance ayrılmalı.
- [ ] Database/user/grant işlemleri typed, scoped ve auditli olmalı; password hiçbir zaman tekrar düz metin gösterilmemeli.
- [ ] Backup/restore DB-native tutarlılık kurallarıyla yapılmalı.
- [ ] File manager canonical path, symlink, traversal, race, size ve MIME/uzantı kontrollerine sahip olmalı.
- [ ] Dosya işlemleri yalnızca atanmış site/root alanında çalışmalı; host genel dosya sistemi browse edilememeli.

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
