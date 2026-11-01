# Faz 04 — Linux Agent ve temel OS yönetimi

## Amaç

Linux Agent ve dar kapsamlı privileged broker geliştir. Ubuntu 24.04 ve Debian 13 üzerinde system facts, metrik ve izinli servis işlemlerini destekle.

## Ön koşullar

- Önceki fazın test raporu PASS olmalı (Faz 00 için dokümantasyon tabanı hazır olmalı).
- İlgili ADR, mimari, teknoloji ve güvenlik belgeleri okunmalı.
- Gerçek üretim sunucusu yerine mock veya disposable test ortamı kullanılmalı.

## Kapsam / kabul kriterleri

- [ ] Agent ve root broker ayrı servis/kimlik olmalı; broker yalnızca Unix socket üzerinden erişilebilir olmalı.
- [ ] OS/distro/version/systemd/package manager tespiti yapılmalı ve capability beyanı test edilmelidir.
- [ ] CPU/RAM/disk/network/uptime metrikleri kaynağı ve timestamp ile raporlanmalı.
- [ ] Servis listele/status/start/stop/restart işlemleri allowlist ve RBAC ile çalışmalı.
- [ ] İşlem girdileri shell string olarak birleştirilmemeli; güvenli broker ve audit zorunlu olmalı.

## Kapsam dışı

- Sonraki fazların özelliklerini erken ekleme.
- Kullanıcı istemedikçe ücretli servis veya lisanslı ürün entegrasyonu ekleme.
- Üretim sunucusunda riskli değişiklik yapma.

## Teslimatlar

Çıktı: Linux Agent paketi, servis adapterı, metrik akışı ve iki distro test matrisi.

## Faz çıkış raporu

ZCode `docs/project-status.md` ve `CHANGELOG.md` dosyasını güncellemeli; uygulanan değişiklikleri, test komutlarını, PASS/FAIL/NOT RUN/BLOCKED durumlarını, açık riskleri ve sonraki faza geçiş uygunluğunu raporlamalı.
