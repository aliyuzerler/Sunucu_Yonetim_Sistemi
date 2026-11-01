# Faz 01 — Monorepo, geliştirme ortamı, temel UI/API

## Amaç

Monorepo oluştur; Go API ve React/TypeScript UI başlangıç iskeletini, Postgres geliştirme ortamını ve kalite kapılarını kur.

## Ön koşullar

- Önceki fazın test raporu PASS olmalı (Faz 00 için dokümantasyon tabanı hazır olmalı).
- İlgili ADR, mimari, teknoloji ve güvenlik belgeleri okunmalı.
- Gerçek üretim sunucusu yerine mock veya disposable test ortamı kullanılmalı.

## Kapsam / kabul kriterleri

- [ ] Monorepo klasörleri backend/control-plane, agent, proto, web, deploy, docs olarak ayrılmış olmalı.
- [ ] Go backend health/readiness endpointleri ve React uygulama kabuğu çalışmalı.
- [ ] PostgreSQL 18 geliştirme ortamı compose ile kalkmalı; migration altyapısı bulunmalı.
- [ ] Frontend lockfile, strict TypeScript, lint, test ve build komutları çalışmalı.
- [ ] CI lint/test/build kontrollerini çalıştırmalı; secret ve gerçek sunucu verisi repoda bulunmamalı.

## Kapsam dışı

- Sonraki fazların özelliklerini erken ekleme.
- Kullanıcı istemedikçe ücretli servis veya lisanslı ürün entegrasyonu ekleme.
- Üretim sunucusunda riskli değişiklik yapma.

## Teslimatlar

Çıktı: yerel geliştirme talimatı, çalışan iskelet, CI ve test raporu.

## Faz çıkış raporu

ZCode `docs/project-status.md` ve `CHANGELOG.md` dosyasını güncellemeli; uygulanan değişiklikleri, test komutlarını, PASS/FAIL/NOT RUN/BLOCKED durumlarını, açık riskleri ve sonraki faza geçiş uygunluğunu raporlamalı.
