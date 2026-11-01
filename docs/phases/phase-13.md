# Faz 13 — İzleme, log, alarm ve güvenlik merkezi

## Amaç

Prometheus uyumlu metrik, log arama, alert lifecycle ve güvenlik durum ekranlarını entegre et.

## Ön koşullar

- Önceki fazın test raporu PASS olmalı (Faz 00 için dokümantasyon tabanı hazır olmalı).
- İlgili ADR, mimari, teknoloji ve güvenlik belgeleri okunmalı.
- Gerçek üretim sunucusu yerine mock veya disposable test ortamı kullanılmalı.

## Kapsam / kabul kriterleri

- [ ] Control Plane/Agent/node metrics timestamp ve freshness bilgisi taşımalı.
- [ ] Prometheus metric labels bounded cardinality kullanmalı; user input label olmamalı.
- [ ] Loglar correlation ID, node ID, severity ve timestamp ile aranabilmeli; secret redaction test edilmeli.
- [ ] Alert dedupe/acknowledge/silence/resolution durumları doğru çalışmalı.
- [ ] Güvenlik modülü yalnızca kanıtlanan kontrolleri raporlamalı; belirsiz veriyi güvenli/temiz olarak göstermemeli.

## Kapsam dışı

- Sonraki fazların özelliklerini erken ekleme.
- Kullanıcı istemedikçe ücretli servis veya lisanslı ürün entegrasyonu ekleme.
- Üretim sunucusunda riskli değişiklik yapma.

## Teslimatlar

Çıktı: izleme dashboardları, log arama, alarm kuralları ve yük testi.

## Faz çıkış raporu

ZCode `docs/project-status.md` ve `CHANGELOG.md` dosyasını güncellemeli; uygulanan değişiklikleri, test komutlarını, PASS/FAIL/NOT RUN/BLOCKED durumlarını, açık riskleri ve sonraki faza geçiş uygunluğunu raporlamalı.
