# Faz 10 — Veritabanları ve güvenli dosya yöneticisi

## Amaç

MariaDB/MySQL ve PostgreSQL yönetim adaptörlerini; dosya listeleme/yükleme/indirme ve izin yönetimini geliştir.

## Ön koşullar

- Önceki fazın test raporu PASS olmalı (Faz 00 için dokümantasyon tabanı hazır olmalı).
- İlgili ADR, mimari, teknoloji ve güvenlik belgeleri okunmalı.
- Gerçek üretim sunucusu yerine mock veya disposable test ortamı kullanılmalı.

## Kapsam / kabul kriterleri

- [ ] Panelin kendi PostgreSQL veritabanı ile yönetilen müşteri DB instance ayrılmalı.
- [ ] Database/user/grant işlemleri typed, scoped ve auditli olmalı; password hiçbir zaman tekrar düz metin gösterilmemeli.
- [ ] Backup/restore DB-native tutarlılık kurallarıyla yapılmalı.
- [ ] File manager canonical path, symlink, traversal, race, size ve MIME/uzantı kontrollerine sahip olmalı.
- [ ] Dosya işlemleri yalnızca atanmış site/root alanında çalışmalı; host genel dosya sistemi browse edilememeli.

## Kapsam dışı

- Sonraki fazların özelliklerini erken ekleme.
- Kullanıcı istemedikçe ücretli servis veya lisanslı ürün entegrasyonu ekleme.
- Üretim sunucusunda riskli değişiklik yapma.

## Teslimatlar

Çıktı: DB adapters, scoped file manager ve güvenlik negatif testleri.

## Faz çıkış raporu

ZCode `docs/project-status.md` ve `CHANGELOG.md` dosyasını güncellemeli; uygulanan değişiklikleri, test komutlarını, PASS/FAIL/NOT RUN/BLOCKED durumlarını, açık riskleri ve sonraki faza geçiş uygunluğunu raporlamalı.
