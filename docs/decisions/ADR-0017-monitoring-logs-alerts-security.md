# ADR-0017: İzleme, Log Arama, Alarm Yaşam Döngüsü ve Güvenlik Merkezi

Durum: Kabul edildi — Faz 13
Tarih: 2026-11-02

## Bağlam

Faz 13, Prometheus uyumlu metrik yüzeyi, log arama, alarm yaşam döngüsü ve
güvenlik durum ekranlarını ister. Kabul kriterleri: (1) CP/Agent/node
metrikleri timestamp ve freshness bilgisi taşır; (2) Prometheus label
kardinalitesi sınırlıdır, kullanıcı girdisi label değeri olamaz; (3) loglar
correlation ID, node ID, severity ve timestamp ile aranabilir, secret
redaksiyonu test edilir; (4) alarm dedupe/acknowledge/silence/resolution
durumları doğru çalışır; (5) güvenlik modülü yalnızca kanıtlanabilir
kontrolleri raporlar, belirsiz veri asla güvenli/temiz gösterilmez.

Önceki faz kalıpları geçerlidir: salt-okuma işlemler agent'ta in-process,
sabit argv ile çalışır; şemalar control-plane validator ↔ agent iki katmanında
aynalanır; her şey PostgreSQL'de kalıcıdır; her mutasyon auditlidir.

Alternatifler: (a) Prometheus client kütüphanesi (prometheus/client_golang)
— yeni harici Go bağımlılığı; dependency-policy'nin stdlib disiplinini bozar
ve kardinalite kontrolünü kütüphaneye devreder; (b) dış alertmanager entegrasyonu
— ağ yüzeyi ve dağıtım bağımlılığı ekler, Faz 13 kapsamı dışıdır; (c) stdlib
ile sabit-şemalı kendi registry + metin eksposasyonu — kardinalite tavanı
kodu içinde zorlanabilir, bağımlılık eklenmez, format Prometheus metin
sözleşmesiyle uyumlu tutulur.

## Karar

1. **Metrik yüzeyi stdlib ile yazılır** (`internal/monitoring`): Prometheus
   metin eksposasyon formatı (`text/plain; version=0.0.4`) elle üretilir.
   Aile tanımları (counter/gauge/histogram) açıkça kaydedilir; aile başına
   **512 seri tavanı** (typed `ErrCardinalityBound`) ve label adı/metric adı
   gramer denetimi vardır. Label değerleri yalnız **kapalı enum'lar** olabilir
   (route desenleri, HTTP metotları, HTTP durum kodları, task durumları) veya
   sunucu üretimi UUID'ler (node_id — envanter boyutuyla sınırlı, kullanıcı
   serbest metni ASLA). Kullanıcı girdisi label değeri olamaz — bu yapısal
   bir kısıttır, kural değildir.

2. **Timestamp/freshness birinci sınıftır**: node telemetri serileri
   `SetGaugeAt` ile toplanma zamanlarını (epoch ms) taşır; freshness gauge'ı
   (saniye cinsinden snapshot yaşı) ayrı seri olarak eksporte edilir. UI'daki
   freshness rozetleri aynı sözleşmeyi kullanır: veri olmayan düğüm asla
   güncel gösterilmez, "Bilinmiyor" dürüst durum olarak render edilir.

3. **`GET /metrics` scrape yüzeyi**: oturum kimlik doğrulaması KULLANMAZ
   (scraper'lar oturum açamaz). `CP_METRICS_TOKEN` yapılandırıldıysa Bearer
   token ZORUNLUDUR (fail-closed); tanımlı değilken yalnız sınırlı, secretsiz
   toplam seriler servis edilir ve ters proxy kısıtı dokümantasyonla yükümlüdür.
   Seriler gizli içermez: kullanıcı adı/e-posta/sertifika CN gibi değerler
   label'a hiç girmez.

4. **Log arama salt-okuma `logs.search` işlemidir** (agent `internal/logops`):
   journald `journalctl` sabit argv şablonuyla çalışır (unit allowlist regex,
   priority 0..7, RFC3339 zamanlar, contains ≤256 karakter kontrol-karaktersiz).
   Çıktı ≤500 kayıt / ≤2 MiB ile sınırlıdır; her mesaj **node'dan çıkmadan
   önce** redakte edilir (logops redact — password/api_key/token desenleri),
   `contains` filtresi redaksiyon SONRASI uygulanır (filtre gizli sonda
   kullanılamaz). Windows'ta aynı yüzey mevcut `windows.eventlog.read`
   (Faz 05) ile sağlanır; yeni bir Windows işlemi eklenmez. CP'de `logs:read`
   izni node scope ile bağlanır; audit kayıtları correlation_id + node_id +
   result (success|failure|denied) + zaman aralığı filtreleriyle aranabilir
   hale gelir (audit.Filter uzatması — Faz 13 kriter 3'ün audit ayağı).

5. **Alarm yaşam döngüsü PostgreSQL'de kalıcıdır** (`internal/alerts`,
   migration 0017): `alert_rules` (kapalı enum metric ∈ {cpu, memory, disk,
   metrics_freshness}; op ∈ {gt, lt}; severity ∈ {info, warning, critical};
   node_id NULL = tüm düğümler) ve `alerts` (fingerprint = rule|node|dimension
   üzerinde **kısmi tekil indeks**: aktif alarm başına TEK satır — dedupe).
   Değerlendirici (evaluator) bir ticker döngüsüdür (varsayılan 1 dk), node
   işlemi ÇALIŞTIRMAZ — yalnız telemetri okur. Durum makinesi:
   open→acknowledged/silenced/resolved; acknowledged→silenced/resolved;
   silenced→yalnız evaluator (süre bitince ihlal sürüyorsa open, iyileşmişse
   resolved); resolved terminaldir. Geçersiz geçiş tipli
   `ErrInvalidState` → HTTP 409. Kuralın ihlal bitince aktif alarmı otomatik
   çözmesi (auto-resolve) evaluator'un işidir; ihmalkârlıkla açık kalan alarm
   birikmez. REST: alert-rules CRUD + alerts liste + acknowledge/silence/resolve,
   tümü auditlidir; `alerts:read`/`alerts:control` izinleri yeni migration'da
   YALNIZ yeni izinleri seed eder (CI-04 dersi).

6. **Güvenlik merkezi yalnız kanıt raporlar** (`internal/securitycenter`):
   sekiz kontrol (admin bootstrapped, secrets key, agent CA, cert expiry,
   telemetri freshness, başarısız giriş, audit akışı, opt-in broker aileleri)
   her biri `state ∈ {ok, warn, alert, unknown}` + evidence + checked_at taşır.
   **"unknown" birinci sınıf durumdur**: DB hatası, boş kayıt, veri yokluğu
   asla ok'a çekilmez (örn. 24 saatte audit kaydı yoksa kontrol "unknown" —
   boş sistem mi akış mı kırık mı bilinemez). UI `stateVariant` sözleşmesiyle
   unknown'u warn-görselliğiyle ayrı rozet olarak gösterir (test edilmiş).

7. **Yeni harici bağımlılık YOK**: metrik registry, eksposasyon, evaluator,
   redaksiyon tamamı stdlib + mevcut pgx/chi üstündedir. Prometheus uyumluluğu
   metin formatı sözleşmesiyle sağlanır; scrape Prometheus'un işidir (bu
   depo Prometheus kurmaz — operatör belgesine örnek scrape config yazılır).

## Sonuçlar

- Kardinalite tavanı aile başına 512 seri ile koddan zorlanır; tavan aşımı
  tipli hatadır, sessiz kayıp değildir.
- `/metrics` token'sız açılırsa yalnız toplam seriler açığa çıkar; yine de
  deploy README ters-proxy kısıtını önerir.
- logs.search node üzerinde `systemd-journal` üyeliği gerektirir; yetersiz
  izin tipli `ErrPermission` ile dürüstçe raporlanır.
- Alarm evaluator'u CP process'i içinde tek örnektir (modüler monolith);
  çoklu CP örneğine geçişte ticker'ın leader seçimiyle korunması gelecek
  çalışmadır (doc'a açık risk olarak işlendi).
