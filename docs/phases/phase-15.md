# Faz 15 — Eklenti sistemi, güncelleme, paketleme ve üretim sertleştirmesi

## Amaç

İmzalı Agent/Control Plane güncelleme mekanizması, sınırlı eklenti sözleşmesi, paketleme, üretim konfigürasyonu ve son güvenlik/perf sertleştirmesini yap.

## Ön koşullar

- Önceki fazın test raporu PASS olmalı (Faz 00 için dokümantasyon tabanı hazır olmalı).
- İlgili ADR, mimari, teknoloji ve güvenlik belgeleri okunmalı.
- Gerçek üretim sunucusu yerine mock veya disposable test ortamı kullanılmalı.

## Kapsam / kabul kriterleri

- [ ] Extension manifest permission/capability/version/API compatibility tanımlamalı; varsayılan izin yok olmalı.
- [ ] İmzasız veya hash doğrulaması başarısız update reddedilmeli; staging/health-check/rollback olmalı.
- [ ] Linux package artifacts ve Windows Agent artifactları sürümlü, checksum/signature ile yayımlanmalı.
- [ ] Clean host install, upgrade, downgrade/rollback limitations ve uninstall testleri yapılmalı.
- [ ] SBOM, license report, vulnerability scan, threat review, load test ve restore proof release dosyasına eklenmeli.

## Kapsam dışı

- Sonraki fazların özelliklerini erken ekleme.
- Kullanıcı istemedikçe ücretli servis veya lisanslı ürün entegrasyonu ekleme.
- Üretim sunucusunda riskli değişiklik yapma.

## Teslimatlar

Çıktı: RC sürümü, kurulum/güncelleme rehberi, SBOM/lisans raporu ve production readiness review.

## Faz çıkış raporu

ZCode `docs/project-status.md` ve `CHANGELOG.md` dosyasını güncellemeli; uygulanan değişiklikleri, test komutlarını, PASS/FAIL/NOT RUN/BLOCKED durumlarını, açık riskleri ve sonraki faza geçiş uygunluğunu raporlamalı.
