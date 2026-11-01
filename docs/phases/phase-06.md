# Faz 06 — Ek Linux dağıtımı adaptörleri

## Amaç

Rocky Linux ve AlmaLinux için paket, servis, firewall ve distro detection adaptörlerini ekle. Her destek iddiasını gerçek VM testiyle kanıtla.

## Ön koşullar

- Önceki fazın test raporu PASS olmalı (Faz 00 için dokümantasyon tabanı hazır olmalı).
- İlgili ADR, mimari, teknoloji ve güvenlik belgeleri okunmalı.
- Gerçek üretim sunucusu yerine mock veya disposable test ortamı kullanılmalı.

## Kapsam / kabul kriterleri

- [ ] Distro/version/package manager detection yanlış pozitif üretmemeli.
- [ ] Ubuntu/Debian davranışları geriye dönük bozulmamalı.
- [ ] Rocky/Alma desteklenen majör sürümleri açıkça listelenmeli.
- [ ] Firewall backend farklılıkları doğru tanınmalı; otomatik kural değişikliği güvenli preview gerektirmeli.
- [ ] Uyumsuz/denenmemiş sürüm desteklenmiyor olarak gösterilmeli.

## Kapsam dışı

- Sonraki fazların özelliklerini erken ekleme.
- Kullanıcı istemedikçe ücretli servis veya lisanslı ürün entegrasyonu ekleme.
- Üretim sunucusunda riskli değişiklik yapma.

## Teslimatlar

Çıktı: distro adapterları, uyumluluk matrisi ve VM test kanıtları.

## Faz çıkış raporu

ZCode `docs/project-status.md` ve `CHANGELOG.md` dosyasını güncellemeli; uygulanan değişiklikleri, test komutlarını, PASS/FAIL/NOT RUN/BLOCKED durumlarını, açık riskleri ve sonraki faza geçiş uygunluğunu raporlamalı.
