# Faz 00 — Mimari ve proje sözleşmesini kesinleştirme

## Amaç

Faz 00 — yalnızca mimariyi kesinleştir; ürün dokümanlarını, ADR ve teknik sözleşmeleri tutarlı hale getir. Kod yazma veya sunucuya bağlanma.

## Ön koşullar

- Önceki fazın test raporu PASS olmalı (Faz 00 için dokümantasyon tabanı hazır olmalı).
- İlgili ADR, mimari, teknoloji ve güvenlik belgeleri okunmalı.
- Gerçek üretim sunucusu yerine mock veya disposable test ortamı kullanılmalı.

## Kapsam / kabul kriterleri

- [ ] Ürün kapsamı, non-goals, kullanıcı rolleri ve başarı ölçütleri çelişkisiz olmalı.
- [ ] Control Plane, Agent, privileged broker, adapter, capability ve task lifecycle netleşmeli.
- [ ] Linux-first + Windows Agent sonraki faz kararı ve çoklu-node stratejisi ADR ile kaydedilmeli.
- [ ] API/protocol versioning, güvenlik sınırları, threat model ve veri varlıkları dokümante edilmeli.
- [ ] 16 fazın bağımlılık sırası ve her fazın giriş/çıkış kriterleri tanımlanmalı.

## Kapsam dışı

- Sonraki fazların özelliklerini erken ekleme.
- Kullanıcı istemedikçe ücretli servis veya lisanslı ürün entegrasyonu ekleme.
- Üretim sunucusunda riskli değişiklik yapma.

## Teslimatlar

Çıktı: mimari gözden geçirme raporu, güncellenmiş dokümanlar ve ADR listesi.

## Faz çıkış raporu

ZCode `docs/project-status.md` ve `CHANGELOG.md` dosyasını güncellemeli; uygulanan değişiklikleri, test komutlarını, PASS/FAIL/NOT RUN/BLOCKED durumlarını, açık riskleri ve sonraki faza geçiş uygunluğunu raporlamalı.
