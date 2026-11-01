# Faz 02 — Kimlik, RBAC, sunucu envanteri ve görev altyapısı

## Amaç

Control Plane çekirdeğini geliştir: kimlik doğrulama, roller, node envanteri, audit ve PostgreSQL tabanlı kalıcı task/outbox altyapısı.

## Ön koşullar

- Önceki fazın test raporu PASS olmalı (Faz 00 için dokümantasyon tabanı hazır olmalı).
- İlgili ADR, mimari, teknoloji ve güvenlik belgeleri okunmalı.
- Gerçek üretim sunucusu yerine mock veya disposable test ortamı kullanılmalı.

## Kapsam / kabul kriterleri

- [ ] İlk admin bootstrap güvenli ve tek kullanımlık olmalı; varsayılan parola olmamalı.
- [ ] RBAC default-deny ve node/group scope ile uygulanmalı.
- [ ] Node kayıt modeli, durum, etiket, OS facts metadata ve capability tabloları bulunmalı.
- [ ] Task/outbox tek DB transaction içinde oluşturulmalı; worker lease ve SKIP LOCKED kullanmalı.
- [ ] Audit event her hassas değişiklikte actor/target/action/result/correlation ID içermeli.

## Kapsam dışı

- Sonraki fazların özelliklerini erken ekleme.
- Kullanıcı istemedikçe ücretli servis veya lisanslı ürün entegrasyonu ekleme.
- Üretim sunucusunda riskli değişiklik yapma.

## Teslimatlar

Çıktı: kimlik/RBAC API, node inventory, task state machine ve kapsam testleri.

## Faz çıkış raporu

ZCode `docs/project-status.md` ve `CHANGELOG.md` dosyasını güncellemeli; uygulanan değişiklikleri, test komutlarını, PASS/FAIL/NOT RUN/BLOCKED durumlarını, açık riskleri ve sonraki faza geçiş uygunluğunu raporlamalı.
