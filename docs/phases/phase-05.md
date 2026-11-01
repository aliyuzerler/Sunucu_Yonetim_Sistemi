# Faz 05 — Windows Server Agent ve native adaptörler

## Amaç

Windows Server için Go Agent ve Windows-native adapterları ekle. Control Plane ve gRPC sözleşmesi geriye uyumlu kalmalı.

## Ön koşullar

- Önceki fazın test raporu PASS olmalı (Faz 00 için dokümantasyon tabanı hazır olmalı).
- İlgili ADR, mimari, teknoloji ve güvenlik belgeleri okunmalı.
- Gerçek üretim sunucusu yerine mock veya disposable test ortamı kullanılmalı.

## Kapsam / kabul kriterleri

- [ ] Windows Agent mTLS ile outbound bağlantı kurmalı ve aynı node/capability modelini kullanmalı.
- [ ] SCM üzerinden servis listeleme/durum ve izinli servis kontrolü yapılmalı.
- [ ] Windows Event Log ve temel host metrics toplanmalı.
- [ ] Privileged broker/named pipe ACL ve caller identity doğrulaması test edilmeli.
- [ ] PowerShell arbitrary execution endpointi bulunmamalı; sürüm/OS destek matrisi açık olmalı.

## Kapsam dışı

- Sonraki fazların özelliklerini erken ekleme.
- Kullanıcı istemedikçe ücretli servis veya lisanslı ürün entegrasyonu ekleme.
- Üretim sunucusunda riskli değişiklik yapma.

## Teslimatlar

Çıktı: Windows Agent build, servis/Event Log adapterı ve disposable Windows Server test raporu.

## Faz çıkış raporu

ZCode `docs/project-status.md` ve `CHANGELOG.md` dosyasını güncellemeli; uygulanan değişiklikleri, test komutlarını, PASS/FAIL/NOT RUN/BLOCKED durumlarını, açık riskleri ve sonraki faza geçiş uygunluğunu raporlamalı.
