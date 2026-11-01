# Faz 03 — Agent protokolü, kayıt, mTLS ve capability

## Amaç

Protobuf/gRPC sözleşmesini, enrollment akışını, mTLS kimliğini, heartbeat ve capability raporunu geliştir. Henüz OS değiştirici işlemler ekleme.

## Ön koşullar

- Önceki fazın test raporu PASS olmalı (Faz 00 için dokümantasyon tabanı hazır olmalı).
- İlgili ADR, mimari, teknoloji ve güvenlik belgeleri okunmalı.
- Gerçek üretim sunucusu yerine mock veya disposable test ortamı kullanılmalı.

## Kapsam / kabul kriterleri

- [ ] Agent özel anahtarı node üzerinde üretilmeli ve dışarı çıkmamalı.
- [ ] Kayıt tokenı tek kullanımlık, kısa ömürlü, node/group kapsamlı ve replay korumalı olmalı.
- [ ] mTLS sertifika kimliği immutable node ID ile bağlanmalı; revoke edilmiş sertifika reddedilmeli.
- [ ] Heartbeat, Agent sürümü, protocol version ve capability report kontrol düzleminde görünmeli.
- [ ] Agent dışarı doğru bağlanmalı; task mesajları expiry, idempotency ve correlation ID içermeli.

## Kapsam dışı

- Sonraki fazların özelliklerini erken ekleme.
- Kullanıcı istemedikçe ücretli servis veya lisanslı ürün entegrasyonu ekleme.
- Üretim sunucusunda riskli değişiklik yapma.

## Teslimatlar

Çıktı: proto dosyaları, enrollment flow, gRPC stream, sertifika testleri.

## Faz çıkış raporu

ZCode `docs/project-status.md` ve `CHANGELOG.md` dosyasını güncellemeli; uygulanan değişiklikleri, test komutlarını, PASS/FAIL/NOT RUN/BLOCKED durumlarını, açık riskleri ve sonraki faza geçiş uygunluğunu raporlamalı.
