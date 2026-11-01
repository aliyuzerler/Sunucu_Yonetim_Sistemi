# Faz 11 — E-posta servisi yönetimi

## Amaç

Postfix, Dovecot ve Rspamd tabanlı isteğe bağlı mail modülünü tasarla ve test ortamında uygula; mail deliverability dış DNS bağımlılıklarını açıkla.

## Ön koşullar

- Önceki fazın test raporu PASS olmalı (Faz 00 için dokümantasyon tabanı hazır olmalı).
- İlgili ADR, mimari, teknoloji ve güvenlik belgeleri okunmalı.
- Gerçek üretim sunucusu yerine mock veya disposable test ortamı kullanılmalı.

## Kapsam / kabul kriterleri

- [ ] Mail modülü varsayılan kurulumda zorunlu olmamalı; preflight ve port kontrolü yapmalı.
- [ ] Domain/mailbox/alias/quota işlemleri typed ve auditli olmalı.
- [ ] SPF, DKIM, DMARC, PTR/reverse DNS gereksinimleri UI'da açıklanmalı.
- [ ] Mail servis config değişiklikleri syntax check ve rollback olmadan uygulanmamalı.
- [ ] Spam/abuse limitleri ve mail queue görüntüleme bulunmalı; hassas içerik loglanmamalı.

## Kapsam dışı

- Sonraki fazların özelliklerini erken ekleme.
- Kullanıcı istemedikçe ücretli servis veya lisanslı ürün entegrasyonu ekleme.
- Üretim sunucusunda riskli değişiklik yapma.

## Teslimatlar

Çıktı: test mail stack, mailbox lifecycle ve kontrollü teslim testleri.

## Faz çıkış raporu

ZCode `docs/project-status.md` ve `CHANGELOG.md` dosyasını güncellemeli; uygulanan değişiklikleri, test komutlarını, PASS/FAIL/NOT RUN/BLOCKED durumlarını, açık riskleri ve sonraki faza geçiş uygunluğunu raporlamalı.
