# Faz 07 — Docker Engine ve Compose yönetimi

## Amaç

Docker Engine API üzerinden Docker modülünü geliştir: container/image/network/volume/Compose işlemleri, loglar ve kaynak görünümü.

## Ön koşullar

- Önceki fazın test raporu PASS olmalı (Faz 00 için dokümantasyon tabanı hazır olmalı).
- İlgili ADR, mimari, teknoloji ve güvenlik belgeleri okunmalı.
- Gerçek üretim sunucusu yerine mock veya disposable test ortamı kullanılmalı.

## Kapsam / kabul kriterleri

- [ ] Docker daemon erişimi sadece Agent/broker tarafında olmalı; TCP socket public açılmamalı.
- [ ] API version negotiation ve unsupported API yanıtları yönetilmeli.
- [ ] Container start/stop/restart/inspect/logs, images, networks, volumes işlemleri yetki ve scope ile uygulanmalı.
- [ ] Compose deploy öncesi schema/policy validation, port/mount/privileged/capability riskleri gösterilmeli.
- [ ] Destructive image/volume/network/container işlemleri preview/confirmation ve audit içermeli.

## Kapsam dışı

- Sonraki fazların özelliklerini erken ekleme.
- Kullanıcı istemedikçe ücretli servis veya lisanslı ürün entegrasyonu ekleme.
- Üretim sunucusunda riskli değişiklik yapma.

## Teslimatlar

Çıktı: Docker modülü, tehlikeli Compose policy testleri ve disposable Docker host entegrasyon testi.

## Faz çıkış raporu

ZCode `docs/project-status.md` ve `CHANGELOG.md` dosyasını güncellemeli; uygulanan değişiklikleri, test komutlarını, PASS/FAIL/NOT RUN/BLOCKED durumlarını, açık riskleri ve sonraki faza geçiş uygunluğunu raporlamalı.
