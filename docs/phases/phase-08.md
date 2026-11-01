# Faz 08 — Web hosting ve uygulama dağıtımı

## Amaç

Nginx + PHP-FPM tabanlı hosting modülünü oluştur; Apache'ı opsiyonel driver olarak tasarla. Node.js/Python uygulama dağıtımını güvenli ve modüler hale getir.

## Ön koşullar

- Önceki fazın test raporu PASS olmalı (Faz 00 için dokümantasyon tabanı hazır olmalı).
- İlgili ADR, mimari, teknoloji ve güvenlik belgeleri okunmalı.
- Gerçek üretim sunucusu yerine mock veya disposable test ortamı kullanılmalı.

## Kapsam / kabul kriterleri

- [ ] Site create/update/disable/delete typed operation olarak uygulanmalı.
- [ ] Her site ayrı sistem kullanıcısı/izin ve güvenli document root ile izole edilmeli.
- [ ] Nginx config atomik yazılmalı, syntax check geçmeden reload yapılmamalı; rollback kopyası bulunmalı.
- [ ] PHP-FPM pool ve PHP sürüm matrisi açıkça doğrulanmalı.
- [ ] Apache ve Nginx port çakışması/edge proxy seçimi yönetilmeli; mevcut config üzerine kör yazılmamalı.

## Kapsam dışı

- Sonraki fazların özelliklerini erken ekleme.
- Kullanıcı istemedikçe ücretli servis veya lisanslı ürün entegrasyonu ekleme.
- Üretim sunucusunda riskli değişiklik yapma.

## Teslimatlar

Çıktı: site lifecycle, Nginx/PHP adapterı, test sitesi ve rollback doğrulaması.

## Faz çıkış raporu

ZCode `docs/project-status.md` ve `CHANGELOG.md` dosyasını güncellemeli; uygulanan değişiklikleri, test komutlarını, PASS/FAIL/NOT RUN/BLOCKED durumlarını, açık riskleri ve sonraki faza geçiş uygunluğunu raporlamalı.
