# ZCode Bağımsız Test Promptu — Faz 15: Eklenti sistemi, güncelleme, paketleme ve üretim sertleştirmesi

Test eden rolünde davran. Önce `agents.md`, ilgili mimari/güvenlik belgeleri, `docs/phases/phase-15.md` ve uygulama değişikliklerini incele. Uygulama kodunu yazan ajan gibi varsayım yapma; kanıt iste. Yalnızca bu fazı denetle ve sonraki fazı başlatma.

## Test hedefi

İmzalı Agent/Control Plane güncelleme mekanizması, sınırlı eklenti sözleşmesi, paketleme, üretim konfigürasyonu ve son güvenlik/perf sertleştirmesini yap.

## Zorunlu kabul kontrolleri

- [ ] Extension manifest permission/capability/version/API compatibility tanımlamalı; varsayılan izin yok olmalı.
- [ ] İmzasız veya hash doğrulaması başarısız update reddedilmeli; staging/health-check/rollback olmalı.
- [ ] Linux package artifacts ve Windows Agent artifactları sürümlü, checksum/signature ile yayımlanmalı.
- [ ] Clean host install, upgrade, downgrade/rollback limitations ve uninstall testleri yapılmalı.
- [ ] SBOM, license report, vulnerability scan, threat review, load test ve restore proof release dosyasına eklenmeli.

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
