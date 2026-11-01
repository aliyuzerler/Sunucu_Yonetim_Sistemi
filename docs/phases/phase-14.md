# Faz 14 — Yedekleme, geri yükleme, bulut ve çoklu sunucu ölçekleme

## Amaç

Restic tabanlı backup/restore workflows, provider adapter interface ve multi-node throughput/queue behavior geliştirmelerini tamamla.

## Ön koşullar

- Önceki fazın test raporu PASS olmalı (Faz 00 için dokümantasyon tabanı hazır olmalı).
- İlgili ADR, mimari, teknoloji ve güvenlik belgeleri okunmalı.
- Gerçek üretim sunucusu yerine mock veya disposable test ortamı kullanılmalı.

## Kapsam / kabul kriterleri

- [ ] Control Plane backup ve node data backup ayrılmalı.
- [ ] Backup integrity/verification state raporlanmalı; restore isolated target üzerinde denenmeli.
- [ ] Encryption master key backup repository ile aynı yerde tutulmamalı.
- [ ] En az iki-üç node ile task isolation, offline node, reconnect, retry ve certificate revoke test edilmeli.
- [ ] Cloud provider adaptörleri her sağlayıcı için ayrı scope/credential/permission matrix kullanmalı; kaynak silme işlemi onay gerektirmeli.

## Kapsam dışı

- Sonraki fazların özelliklerini erken ekleme.
- Kullanıcı istemedikçe ücretli servis veya lisanslı ürün entegrasyonu ekleme.
- Üretim sunucusunda riskli değişiklik yapma.

## Teslimatlar

Çıktı: restore tatbikatı, multi-node load/chaos testleri ve ilk cloud provider adapter.

## Faz çıkış raporu

ZCode `docs/project-status.md` ve `CHANGELOG.md` dosyasını güncellemeli; uygulanan değişiklikleri, test komutlarını, PASS/FAIL/NOT RUN/BLOCKED durumlarını, açık riskleri ve sonraki faza geçiş uygunluğunu raporlamalı.
