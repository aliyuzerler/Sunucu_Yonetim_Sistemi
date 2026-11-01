# Changelog

## Unreleased

## [0.1.0] — 2026-11-01 — Faz 00: Mimari ve proje sözleşmesi

### Added
- `docs/reviews/phase-00-architecture-review.md` — tam dokümantasyon denetimi, bulgular F-01…F-06 ve çözümleri.
- `docs/reviews/phase-00-test-report.md` — bağımsız test promptu kriterlerine göre öz-denetim raporu.
- `docs/decisions/ADR-0006-multi-node-strategy.md` — çoklu-node stratejisi: ilk dağıtım tek Control Plane, çoklu-node hazır sözleşmeler, ölçüme bağlı ölçekleme yolu.
- `docs/decisions/README.md` — ADR süreci, şablonu ve ADR-0001…0006 dizini.
- `docs/phases/README.md` içine 16 fazın bağımlılık sırası tablosu (teknik bağımlılık + giriş/çıkış kriteri tanımları).
- `.gitignore` — gizli dosya ve derleme çıktılarının repoya girmemesi için temel kural seti.

### Changed
- `docs/architecture/system-architecture.md` — Failure model görev durum listesine `accepted` eklendi; task-lifecycle dokümanıyla hizalandı (F-01).
- `docs/vision/scope.md` — MVP ↔ faz eşleme notu eklendi (MVP = Faz 00–04; Docker sınırlı yaşam döngüsü Faz 07) (F-02).
- `docs/ui-ux/design-system.md` — Operations menü grubuna "Tasks & Activity" eklendi; navigation.md ile hizalandı (F-03).
- `docs/project-status.md` — Faz 00 durumu, kanıtlar, açık kararlar ve ortam riskleriyle güncellendi.
- `README.md` — proje deposu olarak yeniden düzenlendi; faz işleyişi ve bağımsız test promptu gereksinimi korundu.

### Notes
- Bu fazda kod, bağımlılık veya migration yoktur; kit taban dokümantasyonu (`PACKAGE-MANIFEST.json`, `SHA256SUMS`) değişmeden korunmuştur.
- Henüz uygulama kaynak kodu veya dağıtılabilir binary yok.
