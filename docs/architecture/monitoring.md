# İzleme, log arama, alarm ve güvenlik merkezi (Faz 13 — ADR-0017)

Control Plane'in Prometheus uyumlu metrik yüzeyi, düğüm loglarının
salt-okuma araması, alarm yaşam döngüsü ve güvenlik durum ekranı. Yeni
harici Go bağımlılığı YOK (stdlib); modül paket KURMAZ, node
yapılandırması DEĞİŞTİRMEZ.

## Bileşenler ve veri akışı

```
Control Plane
  ├─ internal/monitoring   Prometheus metin eksposyonu (GET /metrics)
  │     └─ HTTP observer (route/method/status histogram) + node/task/
  │        alarm gauge'ları — aile başına 512 seri tavanı
  ├─ internal/alerts       kural + alarm deposu (migration 0017),
  │     └─ Evaluator (ticker, node işlemi YOK — yalnız telemetri okur)
  ├─ internal/securitycenter  kanıt temelli 8 kontrol (ok/warn/alert/unknown)
  └─ internal/httpapi      /metrics + /alert-rules + /alerts + /security/overview

Agent
  └─ internal/logops       logs.search: journalctl sabit argv, çıktı
        ≤500 kayıt / ≤2 MiB, her mesaj node'dan çıkmadan ÖNCE redakte
```

## Metrik yüzeyi (kriter 1 + 2)

- Format: `text/plain; version=0.0.4` (Prometheus metin eksposyonu).
- Node serileri toplanma zamanlarını taşır (`SetGaugeAt`, epoch ms);
  `sc_node_metrics_freshness_seconds` snapshot yaşını gauge olarak verir.
- Label değerleri yalnız **kapalı enum** olabilir (route deseni, HTTP
  metodu, durum kodu, task durumu) veya sunucu üretimi node UUID'si.
  Kullanıcı girdisi label değeri ASLA olamaz; tavan aşımı tipli hatadır
  (`ErrCardinalityBound`), sessiz kayıp değildir.
- `GET /metrics` oturum doğrulaması kullanmaz (scraper oturum açamaz);
  `CP_METRICS_TOKEN` tanımlıysa Bearer ZORUNLU (fail-closed). Seriler
  secretsiz toplam serieslerdir; kullanıcı adı/anahtar/CN label'a girmez.
  Ters proxy ile kısıtlama önerilir (deploy README).

## Log arama (kriter 3)

- `logs.search` (ClassReadOnly, `logs:read` izni): journald sorgusu sabit
  argv şablonuyla — unit allowlist (`*.service`), priority 0..7, RFC3339
  since/until, contains ≤256 karakter, limit ≤500.
- Çıktı sınırları: 2 MiB stdout tavanı, mesaj başına 4 KiB, tek satır.
- **Redaksiyon**: password/api_key/token desenleri node'dan çıkmadan
  maskelenir; `contains` filtresi redaksiyon SONRASI uygulanır (filtre
  gizli sonda kullanılamaz). Redaksiyon birim testleri logops'ta.
- Windows: aynı UI yüzeyi mevcut `windows.eventlog.read` (Faz 05) ile
  çalışır; yeni Windows işlemi eklenmedi.
- Audit araması correlation_id + node_id + result (success|failure|denied)
  + zaman aralığı filtreleriyle genişletildi (aynı kriterin CP ayağı).

## Alarm yaşam döngüsü (kriter 4)

| Geçiş | Kim | Koşul |
| --- | --- | --- |
| open → acknowledged | operatör (`alerts:control`) | her zaman |
| open/acknowledged → silenced | operatör | duration_minutes 1..43200 |
| open/acknowledged/silenced → resolved | operatör veya evaluator | ihlal bitince (evaluator auto-resolve) |
| silenced → open | yalnız evaluator | sessizlik süresi bitti, ihlal sürüyor |
| silenced → resolved | yalnız evaluator | sessizlik süresi bitti, değer iyileşti |

- **Dedupe**: `fingerprint = rule|node|dimension` üzerinde kısmi tekil
  indeks — aktif alarm başına TEK satır; tekrar ihlal `occurrences` sayacını
  artırır, yeni satır açmaz.
- Geçersiz geçiş tipli `ErrInvalidState` → HTTP 409; tüm geçişler audit'e
  yazılır (`alert.acknowledge` / `alert.silence` / `alert.resolve`).
- Kurallar kapalı enum'dur: metric ∈ {cpu, memory, disk, metrics_freshness},
  op ∈ {gt, lt}, severity ∈ {info, warning, critical}; node_id NULL =
  tüm düğümler. Telemetri hiç olmayan düğümde alarm üretilmez (honest skip)
  ve kural doğrulaması süre-penceresi verisini zorunlu kılar.

## Güvenlik merkezi (kriter 5)

Sekiz kontrol; her biri `state ∈ {ok, warn, alert, unknown}` + evidence +
checked_at döner. **"unknown" birinci sınıftır**: DB hatası, boş kayıt,
veri yokluğu asla ok'a çekilmez. Örnekler:

- 24 saatte audit kaydı yok → `unknown` ("boşta veya akış kesildi").
- Hiçbir düğümde taze telemetri yok → `unknown` (kanıt yok).
- `CP_SECRETS_KEY` tanımsız → `warn` (redaksiyon kazanır — fail-closed).

UI (`SecurityPage`) unknown'u warn-görselliğiyle AYRI rozet olarak
basar; `stateVariant` sözleşmesi unit testli, e2e görüntülüdür.

## Testler

- `internal/monitoring` (registry kardinalite/format/timestamp),
  `internal/alerts` (durum makinesi + dedupe + evaluator), `logops`
  (şema/redaksiyon/sınırlar) hermetik birim testleri.
- `vitest MonitoringLogsSecurity` (46/46'nın parçası): freshness dürüstlüğü,
  alarm aksiyon POST'ları, kural formu şeması, redaksiyon görünümü,
  unknown-asla-ok sözleşmesi.
- Playwright `monitoring-logs.spec.ts` (5 test): mock modda uçtan uca;
  ekran görüntüleri `docs/reviews/screenshots/phase-13/`.

## Açık riskler / dürüst sınırlar

- Alarm evaluator'u tek CP örneğinde tekil çalışır; çoklu örnek/HA
  senaryosunda leader seçimi sonraki çalışmadır.
- `/metrics` token'sız modda yalnız toplam seriler açığa çıkar; operatör
  yine de ters proxy ile kısıtlamalıdır (deploy README notu).
- logs.search `systemd-journal` üyeliği gerektirir; izin yetersizse tipli
  `ErrPermission` döner (sessiz boş sonuç DEĞİL).
- Kalıcı metrik saklama/prune politikası Faz 04 telemetri saklama
  politikasıyla aynıdır; Prometheus tarafında saklama operatöre aittir.
