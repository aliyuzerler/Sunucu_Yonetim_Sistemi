# ADR-0011: Docker Engine erişimi — broker-tekil + preview-only silme

**Tarih:** 2026-11-02 · **Durum:** Kabul edildi · **Faz:** 07

## Bağlam

Faz 07 Docker Engine ve Compose yönetimi gerektirir (`docs/phases/phase-07.md`):
container/image/network/volume işlemleri, loglar, kaynak görünümü ve compose
deploy öncesi risk analizi. Docker daemon'ı root'a eşdeğer yetki veren yüksek
riskli bir yüzeydir; `agents.md` §4 açıkça "Docker socket'i web tarayıcısına
veya genel ağa açma" der. Ayrıca kriter, yıkıcı işlemlerin preview/confirmation
ve audit içermesini ister.

## Karar

1. **Erişim broker-tekildir.** Engine API'ye yalnız `server-privd` (root, unix
   socket 0660 + SO_PEERCRED UID allowlist) `/var/run/docker.sock` üzerinden
   bağlanır. Agent ve control plane asla doğrudan dial yapmaz; TCP socket
   yüzeyi hiç yoktur. İstemci `agent/internal/dockerx` (yalnız stdlib —
   `docker/docker` SDK'sı getirilmedi: dev bağımlılık ağacı + saldırı yüzeyi).
2. **API sürüm pazarlığı katıdır.** `/_ping` → `Api-Version` okunur; istemci
   penceresi [1.24, 1.47]; daemon daha eskiyse tipli `ErrUnsupportedAPI`
   (tahmin yok), daha yeniyse 1.47'ye clamp. İstekler `/v{maj}.{min}/` önekli.
3. **Silme yüzeyi Faz 07'de YOKTUR (preview-only).** container/image/volume/
   network remove, Engine `DELETE` çağrısını ve CLI eşdeğerini **veri olarak**
   üretir (`Executed=false` değişmezi; `*dockerx.Client`'ın remove metodu bile
   yoktur — derleyici düzeyinde invariant). Bu, ADR-0010 firewall
   politikasının aynısıdır; "preview/confirmation + audit" kriterini daha
   güçlü bir politikayla karşılar. Yürütme, onay akışlı (dual-control) ayrı
   bir fazda yeni bir ADR ile tasarlanacaktır.
4. **Compose deploy Faz 07'de YÜRÜTÜLMEZ.** `docker.compose.validate` şema +
   policy risk taraması yapar (privileged, cap_add, host network/pid, docker-
   sock bind, public port, gizli-anahtar adı — 14+ kural); `docker.compose.
   deploy` yalnız doğrulama + sıralı aksiyon planı döndürür (`Executed=false`).
   Analiz agent-local SAF işlevdir (`agent/internal/composeops`) — daemon'a
   ihtiyaç duymaz, broker'a girmez. Compose YAML ayrıştırması için
   `gopkg.in/yaml.v3 v3.0.1` (MIT) getirildi; girdi ≤64 KiB + NUL reddi +
   servis ≤64 + bulgu ≤100 sınırlıdır (billion-laughs: yaml.v3 alias koruması
   + girdi sınırı).
5. **Secret redaksiyonu:** container inspect çıktısında `Config.Env`
   KASITLI olarak dışlanır; compose bulguları yalnız ortam ANAHTAR adlarını
   taşır, değerleri asla. Log çıktısı 64 KiB ile sınırlıdır.
6. **RBAC:** `docker:read` (liste/inspect/logs/önizlemeler/compose analizi),
   `docker:control` (container start/stop/restart). restart `non_idempotent`
   + retry-unsafe (systemd/SCM kalıbı). Operator rolü salt-okuma alır.

## Alternatifler

- **docker/docker SDK:** tam tip güvenliği var ama çok geniş bağımlılık
  ağacı; Engine API uçları zaten sabit ve az sayıda. Reddedildi
  (dependency-policy §1/§9; stdlib istemci ~600 satır, tamamen test edilebilir).
- **Onaylı silme akışı (dual-control):** güçlü ama onay kaydı/rollback
  semantiği ayrı tasarım gerektirir; ADR-0010 önceliğiyle sonraki faza
  ertelendi.
- **Compose yürütmesi (mini-compose):** build/network/volume/depends_on
  semantiğini doğru tahmin etmek hataya açık; ADR-0010 ilkesi gereği tahminle
  yanlış üretmek yerine dürüst ret seçildi.

## Sonuçlar

- Faz 07 kriterleri karşılanır: daemon erişimi agent/broker'da (kriter 1),
  API pazarlığı + desteklenmeyen sürüm yanıtı (kriter 2), yetki/skop'lu
  işlemler (kriter 3), compose öncesi risk gösterimi (kriter 4), yıkıcı
  işlemlerde preview+audit (kriter 5 — preview-only + her istekte broker
  JSONL audit).
- CI `agent-docker` job'ı gerçek runner daemon'ı üzerinde disposable busybox
  container ile tam lifecycle kanıtı üretir; `EXPECTED_DOCKER=1` gate'i
  sessiz atlanamaz (CI-17 dersi).
- Gelecek mutasyon fazı bu ADR'yi güncelleyen yeni bir ADR ile onay akışını
  tanımlar.
