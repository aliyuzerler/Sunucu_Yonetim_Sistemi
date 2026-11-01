# Faz 09 — Domain, DNS, reverse proxy ve SSL

## Amaç

Domain envanteri, DNS provider interface, reverse proxy yönlendirme ve ACME sertifika yaşam döngüsünü geliştir.

## Ön koşullar

- Önceki fazın test raporu PASS olmalı (Faz 00 için dokümantasyon tabanı hazır olmalı).
- İlgili ADR, mimari, teknoloji ve güvenlik belgeleri okunmalı.
- Gerçek üretim sunucusu yerine mock veya disposable test ortamı kullanılmalı.

## Kapsam / kabul kriterleri

- [ ] DNS provider adapterı kayıt CRUD ve provider capability bildirmeli.
- [ ] DNS zone/record değişikliklerinde diff preview, TTL validation ve audit olmalı.
- [ ] ACME issue/renew/revoke/expiry state yönetilmeli; rate limit ve challenge hatası görünür olmalı.
- [ ] Web server config sertifika yüklenmeden önce validate edilmeli; başarısız yenileme canlı sertifikayı bozmamalı.
- [ ] Cloud DNS gibi dış entegrasyon secretları şifreli saklanmalı ve UI'da tekrar gösterilmemeli.

## Kapsam dışı

- Sonraki fazların özelliklerini erken ekleme.
- Kullanıcı istemedikçe ücretli servis veya lisanslı ürün entegrasyonu ekleme.
- Üretim sunucusunda riskli değişiklik yapma.

## Teslimatlar

Çıktı: DNS/SSL modülleri, mock provider ve staging ACME test raporu.

## Faz çıkış raporu

ZCode `docs/project-status.md` ve `CHANGELOG.md` dosyasını güncellemeli; uygulanan değişiklikleri, test komutlarını, PASS/FAIL/NOT RUN/BLOCKED durumlarını, açık riskleri ve sonraki faza geçiş uygunluğunu raporlamalı.
