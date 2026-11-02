# Faz 13 Bağımsız Test Raporu — İzleme, log, alarm ve güvenlik merkezi

- **Tarih:** 2026-11-03
- **Test eden:** ZCode (bağımsız test rolü — `prompts/phase-test/phase-13-test.md`)
- **İncelenen aralık:** çalışma dizinindeki Faz 13 implementasyonu (önceki oturumda başlanmış, işlenmemiş) + bu oturumda tamamlanan kapanış işleri
- **Başlangıç HEAD:** c7aad2a (= tag v0.13.0, Faz 12 COMPLETE — bağımsız test ACCEPTED/READY, CI 15/15 run 41465331984)
- **Karar:** **ACCEPTED — READY** (5/5 zorunlu kriter PASS)

## Geliş notu (dürüst kayıt)

Faz 13 implementasyonu önceki oturumda başlatılmıştı; çalışma dizininde işlenmemiş
~4.500 satır kod bulundu (agent logops, CP monitoring/alerts/securitycenter,
handlers, migration 0017, 3 UI sayfası). Bu oturumda: kod incelendi, eksikler
(e2e spec + ekran görüntüleri, mock API Faz 13 uçları, ADR-0017,
docs/architecture/monitoring.md, ADR index, deploy README, CHANGELOG,
project-status) tamamlandı, iki ürün hatası düzeltildi (IT-01, IT-02) ve tüm
süitler taze koşuldu.

## Zorunlu kabul kontrolleri

### 1) Metrics timestamp ve freshness — **PASS**

- `internal/monitoring/monitoring.go:87-97` — `gaugeValue{value, ts}`; `:196` `SetGaugeAt`
  (epoch ms; yalnız toplanma zamanı bilinen değerler için — kullanıcı verisi değil).
- `internal/monitoring/monitoring.go:355-365` — render, seri başına toplanma
  timestamp'ini yazar (Prometheus metin eksposyonu `<name> <value> <ts>` biçimi).
- `internal/monitoring/export.go:212-214` — node CPU/bellek gauge'ları snapshot
  `collected_at` ile; `sc_node_metrics_freshness_seconds` snapshot yaşı ayrı gauge.
- `web/src/pages/MonitoringPage.tsx:26-35` — `freshnessState(null)=warn`,
  stale (>10 dk)=warn; `:185-194` veri olmayan düğüm "Bilinmiyor" rozeti —
  **asla güncel gösterilmez**.
- Test: `monitoring_test.go` (timestamp'li render + freshness); vitest
  `freshnessState` (3 iddia); e2e `01-monitoring-freshness.png`.

### 2) Prometheus labels bounded cardinality; user input label olmamalı — **PASS**

- `internal/monitoring/monitoring.go:22` — `maxSeriesPerFamily = 512`; `:26`
  tipli `ErrCardinalityBound`; tavan kontrolü `:179/:209/:234` (counter/gauge/hist).
- `monitoring.go:29-64` — metric/label ad grameri; `:132-144` `checkLabels`
  (tekrarlı label, `le` rezervi reddi); aile çelişkisi reddi `:258/:270/:292`.
- Label değerleri **yalnız** kapalı enum'lar (route desenleri — `httpmetrics.go`,
  HTTP metotları, durum kodları, task durumları) veya sunucu üretimi node UUID
  (`export.go:3` yorumu: "server-issued UUIDs (never user input)"). Kullanıcı
  serbest metni label'a girecek kod yolu YOK.
- Test: `monitoring_test.go` kardinalite tavanı + gramer + escape + deterministik
  render.

### 3) Loglar correlation ID / node ID / severity / timestamp ile aranabilir; redaksiyon testli — **PASS**

- `agent/internal/logops/logops.go:148-164` — journalctl **sabit argv** (unit
  allowlist `:57`, priority 0..7, RFC3339 `:60`, contains ≤256 kontrol-karaktersiz
  `:84-90`, limit ≤500); çıktı ≤2 MiB `:177-182`, mesaj ≤4 KiB tek satır `:232`.
- **Redaksiyon ÇIKIŞTAN ÖNCE**: `logops.go:231-234` — `RedactLine` uygulanır,
  `contains` filtresi redaksiyon SONRASI çalışır (filtre gizli sonda kullanılamaz);
  desenler `redact.go`; birim testleri `logops_test.go` (224 satır — şema/argv/
  redaksiyon/sınır/izin eşleme).
- CP aynası: `internal/tasks/ops_faz13.go:42-88` `ValidateLogSearch` (aynı sınırlar,
  `until ≥ since`); registry 99→100 (`service_test.go:217`).
- Aranabilirlik: timestamp (journald `__REALTIME_TIMESTAMP` → RFC3339Nano `:217-220`),
  severity (PRIORITY 0..7 + ad `:222-227`), node ID (görev node hedefli) ve CP
  audit ayağı: `internal/audit/audit.go` — `Filter.CorrelationID` (tam eşleşme),
  `Filter.NodeID` (UUID doğrulamalı metadata), `Filter.Result ∈ {success, failure,
  denied}`, Since/Until (bu oturum diff'inde).
- Windows: Faz 05 `windows.eventlog.read` aynı UI yüzeyinde (yeni Windows işlemi
  eklenmedi — additive disiplin).

### 4) Alert dedupe/acknowledge/silence/resolution — **PASS**

- **Dedupe**: `db/migrations/0017_monitoring.up.sql:40` — `alerts_active_fingerprint_idx`
  (fingerprint üzerinde kısmi tekil indeks `WHERE status <> 'resolved'`) — aktif
  alarm başına TEK satır; tekrar ihlal `occurrences` artırır.
- **Durum makinesi**: `internal/alerts/alerts.go:259-300` — Acknowledge
  (open|acknowledged→acknowledged, idempotent; silenced/resolved → tipli
  `ErrInvalidState`), Silence (resolved reddi; pencere uzatma idempotent), Resolve
  (non-resolved→resolved). `handlers_alerts.go:143-193` REST geçişleri (auditli,
  duration_minutes 1..43200).
- **Evaluator**: `internal/alerts/evaluator.go:89-105` (tek geçiş; bozuk kural
  döngüyü çökertmez), `:192-208` (ihlal → `UpsertViolation` dedupe; iyileşme →
  auto-resolve), `:242-277` (silence-expiry: ihlal sürüyorsa open, iyileşmişse
  resolved; kural silinmişse orphan resolve).
- Test: `alerts_test.go` (216 satır durum makinesi) + evaluator testleri + vitest
  aksiyon POST'ları + e2e lifecycle testi (open→acknowledged→silenced→resolved
  gözlemlenir; `02-alert-open.png`).

### 5) Güvenlik modülü yalnız kanıtlanabilir kontrolleri raporlar — **PASS**

- `internal/securitycenter/security.go:15-24` — `State ∈ {ok, warn, alert,
  unknown}`; **"unknown" birinci sınıf durum**.
- Kanıtsızlık asla ok'a çekilmez: DB hatası → unknown `:114-115/:148-149/:173-174/
  :262-263/:268-273`; node yok / taze telemetri yok → unknown `:197-201`; 24 saatte
  audit kaydı yok → unknown `:248-251` ("boşta veya akış kesildi — kanıt yetersiz").
- UI sözleşmesi: `SecurityPage.tsx:22-35` `stateVariant` (unknown → warn görselli
  AYRI rozet; `:121` ayrı "Bilinmiyor" metni) — vitest "unknown asla Tamam görünmez"
  + `stateVariant` testi; e2e sayım testi (4×Tamam / 1×Uyarı / 1×Alarm / 2×Bilinmiyor
  + 2×"kanıt yok" kanıtı; `05-security-honest.png`).

## Bulunan hatalar ve önem dereceleri

| ID | Önem | Tür | Açıklama | Durum |
| --- | --- | --- | --- | --- |
| IT-01 | ORTA | ÜRÜN | `MonitoringPage.tsx` kural formu `submit()`: `await apiFetch` sonrası `e.currentTarget.reset()` — React'te `currentTarget` async devamda `null` → TypeError → catch → `onCreated()` hiç çağrılmıyor (kural oluşturma sonrası liste tazelenmiyordu; Playwright e2e ile yakalandı) | **DÜZELTİLDİ** — form referansı await ÖNCESİ alınır (`MonitoringPage.tsx:287-302`); vitest (POST gövdesi + reload GET ayrımı) ve e2e (liste görünürlüğü) ile kilitlendi |
| IT-02 | DÜŞÜK | ÜRÜN/a11y | `LogsPage` unit + contains girdilerinde `aria-label` eksik (diğer tüm filtre alanlarında var) — ekran okuyucu erişimi ve test erişilebilirliği | **DÜZELTİLDİ** (`LogsPage.tsx:202, :213`) |
| IT-03 | ORTA | KAPSAM (dürüst NOT RUN) | Gerçek journald üzerinde logs.search kanıtı: sandbox'ta systemd journal yok → **NOT RUN**. Hermetik süit (sahte çıktı dosyalarıyla tam akış + tipli `ErrPermission`/`ErrTooLarge`/`ErrJournalctlUnavailable` eşleme) kapsamı sağlar; operatör doğrulaması disposable VM'de `deploy/README.md` notuyla (systemd-journal üyeliği) | BELGELENDİ |
| IT-04 | DÜŞÜK | Doküman | Alarm evaluator'u CP process içinde tekil çalışır; çoklu CP/HA senaryosunda leader seçimi gelecek çalışma | ADR-0017 + monitoring.md "Açık riskler" bölümüne işlendi |
| IT-05 | DÜŞÜK | Doküman | `/metrics` token'sız modda yalnız secretsiz toplam seriler servis eder; yine de ters-proxy kısıtı önerilir | `handlers_metrics.go` yorumu + deploy README + monitoring.md |
| IT-06 | BİLGİ | Test altyapısı | 5 test-tarafı düzeltmesi: (a) LogsPage vitest — düğüm listesi yerleşmeden `selectOptions` (Faz 06 IT-01 ile aynı async yarış sınıfı); (b) MonitoringPage vitest — "Tekrar: 3" exact-match inline tarihle bölünüyor → regex; (c) acknowledge sonrası reload GET'leri yanıtlanmalı (notice `!error` koşulu); (d) pollTask 1 sn aralığı → `timeout: 4000` (NodeDetailWeb kuralı); (e) e2e tam-süit paylaşımlı mock durumunda strict-mode sayımları (4×Tamam, 6 node satırı → mon-01 satırına daraltma) | DÜZELTİLDİ (ürün etkilenmedi) |

## Çalıştırılan komutlar ve kısa çıktılar

| Komut | Sonuç |
| --- | --- |
| `cd agent && go build ./... && go vet ./...` | temiz |
| `cd agent && go test ./... -race -count=1` | **18/18 paket ok** (logops dahil) |
| `GOOS=windows go build ./... && GOOS=windows go vet ./...` | temiz |
| `cd backend/control-plane && go build ./... && go vet ./...` | temiz |
| `cd backend/control-plane && go test ./... -race -count=1` | **19/19 paket ok** (PG entegrasyonları CI'da koşar — sandbox'ta PostgreSQL yok; testdb kurgusu bunları skip'ler) |
| `gofmt -l .` (agent + backend) | temiz (2 dosya bu oturumda düzeltildi: `logops/redact.go`, `internal/config/config.go`) |
| `cd web && npx vitest run` | **46/46** (10 dosya) |
| `cd web && npm run build` (tsc -b + vite) | temiz (exactOptionalPropertyTypes dahil) |
| `cd web && npx playwright test` | **39/39** (34 önceki + 5 yeni Faz 13) |
| `bash scripts/secret-scan.sh` | temiz ✓ |
| Migration 0017 incelemesi | up/down çift yönlü; dedupe kısmi tekil indeks; **yalnız yeni izinler seed** (CI-04 dersi); testdb.Reset `alerts, alert_rules` içerir |
| Mock API Faz 13 uçları | curl ile elle doğrulandı (login → POST/GET alert-rules 201/200) |

**NOT RUN / BLOCKED (dürüst işaretli):** gerçek journald üzerinde logs.search
(sandbox'ta systemd journal yok — IT-03); PostgreSQL entegrasyon testleri
(alerts PG store, securitycenter DB kontrolleri) sandbox'ta koşmaz — CI `backend`
job'ının PostgreSQL 18 service container'ında koşar; gerçek Prometheus scrape
(bu depo Prometheus kurmaz — operatör tarafı; metin formatı birim testli).

## Regresyon

Önceki fazların süitleri taze koşuldu: agent 18/18 -race (vmops/mailops/dbops/fsops
dahil), GOOS=windows vet+build, backend 19/19 -race (registry 100-op testi
`service_test.go:217`, route regression `routes_regression_test.go`), vitest
46/46 (önceki 40 + Faz 13), Playwright 39/39 (önceki 34 + Faz 13). Kırılan
mevcut test olmadı; ürün değişiklikleri yalnız IT-01/IT-02 (düzeltme) ve Faz 13
eklemeleri.

## Güvenlik değerlendirmesi

- Kullanıcı girdisinin label/argv/yol yüzeyleri: metrik label'ları kapalı enum
  (yapısal); journalctl argv'si sabit şablon + allowlist (kabuk/flag enjeksiyonu
  yolu yok); contains serbest metni yalnız post-redaksiyon substring filtresi.
- Redaksiyon sırası (redact → filter) gizli sonda kullanımını kapatır.
- `/metrics` fail-closed Bearer + secretsiz seriler; constant-time token
  karşılaştırma (`crypto/subtle`).
- Alarm REST tümü `alerts:control` + CSRF + audit; migration yalnız yeni izin
  seed eder.
- Güvenlik merkezinin "unknown" disiplini kanıtsız güvenlik iddiasını imkânsız kılar.

## Faz kabul kararı

**ACCEPTED** — 5/5 zorunlu kriter PASS (file:line kanıtlı); kritik/yüksek açık
bulgu yok; iki orta bulgu ya düzeltildi (IT-01) ya da dürüstçe NOT RUN
işaretlendi ve hermetik kapsamla belgelendi (IT-03).

**Sonraki faza geçiş: READY.**
