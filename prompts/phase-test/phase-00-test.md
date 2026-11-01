# ZCode Bağımsız Test Promptu — Faz 00: Mimari ve proje sözleşmesini kesinleştirme

Test eden rolünde davran. Önce `agents.md`, ilgili mimari/güvenlik belgeleri, `docs/phases/phase-00.md` ve uygulama değişikliklerini incele. Uygulama kodunu yazan ajan gibi varsayım yapma; kanıt iste. Yalnızca bu fazı denetle ve sonraki fazı başlatma.

## Test hedefi

Faz 00 — yalnızca mimariyi kesinleştir; ürün dokümanlarını, ADR ve teknik sözleşmeleri tutarlı hale getir. Kod yazma veya sunucuya bağlanma.

## Zorunlu kabul kontrolleri

- [ ] Ürün kapsamı, non-goals, kullanıcı rolleri ve başarı ölçütleri çelişkisiz olmalı.
- [ ] Control Plane, Agent, privileged broker, adapter, capability ve task lifecycle netleşmeli.
- [ ] Linux-first + Windows Agent sonraki faz kararı ve çoklu-node stratejisi ADR ile kaydedilmeli.
- [ ] API/protocol versioning, güvenlik sınırları, threat model ve veri varlıkları dokümante edilmeli.
- [ ] 16 fazın bağımlılık sırası ve her fazın giriş/çıkış kriterleri tanımlanmalı.

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
