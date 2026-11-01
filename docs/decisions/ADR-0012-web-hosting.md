# ADR-0012: Web hosting — broker-tekil yürütme, dosya-koruyucu delete, marker'lı config disiplini

**Tarih:** 2026-11-02 · **Durum:** Kabul edildi · **Faz:** 08

## Bağlam

Faz 08, nginx + PHP-FPM tabanlı bir hosting modülü gerektirir
(`docs/phases/phase-08.md`): site create/update/disable/delete typed
işlemleri, site başına sistem kullanıcısı izolasyonu, atomik nginx config
yazımı + syntax-check kapısı + rollback kopyası, PHP-FPM pool ve sürüm
matrisinin açık doğrulaması, Apache/nginx port çakışması + edge-proxy
yönetimi ("mevcut config üzerine kör yazım yasağı") ve Node.js/Python
uygulama dağıtımının güvenli/modüler hale getirilmesi.

Bu, projedeki ilk ailedir ki **gerçek mutasyon** (sistem kullanıcısı
oluşturma, servis reload'ı) Faz kabul kriterinin doğrudan parçasıdır.
ADR-0010/0011'deki preview-only politikası silme yüzeyleri için konmuştu;
hosting modülünde ise "site oluşturma" preview-only olsaydu Faz 08'in
teslimatı (site lifecycle, test sitesi ve rollback doğrulaması) anlamsız
olurdu. Ayrıca agents.md §4 "gerçek sistemde destructive işlemi varsayılan
olarak çalıştırma" der.

## Karar

1. **Yürütme broker-tekilidir ve opt-in'dir.** Tüm web işlemleri yalnız
   `server-privd` içinde çalışır (`agent/internal/webops`, stdlib — yeni
   harici bağımlılık YOK). Aile `--web-enable` bayrağı ile AÇILIR;
   varsayılan KAPALIDIR. Kapalıyken işlemler tipli
   `unsupported_operation` ile reddedilir (sessiz kalabalık yok). Gerekçe:
   useradd/rollback/reload gerçek sistem mutasyonudur; operatör bilinçli
   seçim yapmalıdır.
2. **Site adı tek tohumdur.** `name` allowlist'i
   `[a-z0-9][a-z0-9-]{0,22}[a-z0-9]` (≤24) dışındaki her girdiyi reddeder;
   sistem kullanıcısı (`sc-web-<name>` ≤31 karakter), config yolu
   (`/etc/nginx/conf.d/srvctl-<name>.conf`), docroot
   (`/var/www/server-control/<name>/html`) YALNIZ bu addan türetilir.
   Docroot override YOKTUR — path traversal yapısala reddedilmiş olur.
   Docroot 0750'dir: sahip site kullanıcısı, grup web-sunucu grubu
   (www-data) → site kullanıcıları birbirinin dosyasını okuyamaz.
3. **Config disiplini — marker + atomik yazım + rollback + iki kapı.**
   Her yönetilen dosyanın ilk satırı `MarkerHeader`'dır. Var olan dosya
   markersız ise `ErrForeignConfig` ile REDDEDİLİR (kör yazım imkânsız —
   kriter 5). Yazım temp-dosya + rename ile atomiktir; önceki içerik
   daima `.rollback` kopyası olarak kalır. Reload YALNIZ (a) `nginx -t`
   geçince ve (b) `nginx -T` dump'ında bizim site metadata'sı görününce
   (include kanıtı) gönderilir; kapı başarısızsa rollback geri yüklenir,
   reload hiç sinyallenmez.
4. **Port çakışması sahipli analiziyle yönetilir.** `/proc/net/tcp{,6}`
   dinleyici tablosu + `/proc/<pid>/fd` socket-inode eşlemesi ile portun
   SAHİBİ belirlenir: nginx'e ait portta ek vhost serbest (isim-tabanlı
   vhost normaldir), **Apache** (apache2/httpd) sahipliği tipli
   `ErrPortConflict` + edge-proxy önerisi ile reddedilir; sahipsiz
   (bilinmeyen) dinleyici güvenli tarafta reddedilir. Apache **opsiyonel
   driver** olarak tasarlanır: driver arayüzü nginx renderer'ı ile
   soyutlanmıştır (web-hosting.md §Apache driver); Apache'i apache'nin
   kendi config'ini düzenleyerek arka uca taşımak KESİNLİKLE yapılmaz —
   edge-proxy topolojisi operatör kararıdır ve backend=proxy ile desteklenir.
5. **PHP-FPM pool + sürüm matrisi açık doğrulanır.** Kapalı matris
   {8.2, 8.3, 8.4, 8.5} dışındaki sürümler `ErrPHPUnsupported` ile reddedilir
   (tahmin yok); matriste olmak yetmez — node'da `/etc/php/<v>/fpm/pool.d`
   tespit edilmeden pool yazılmaz (`ErrPHPNotInstalled`). Pool dosyası
   site kullanıcısı altında çalışır, socket ACL'si yalnız web-sunucu
   grubuna açıktır (`listen.owner/group = www-data`, 0660) ve
   `disable_functions` havuzu tehlikeli fonksiyonlardan arındırır.
   Reload `php<v>-fpm.service` birimine tipli olarak sinyallenir.
6. **Delete dosya-koruyucudur (preview-only DEĞİL, ama veri-silici de
   değil).** Faz kabul kriteri delete'i typed işlem olarak ister; ADR-0011
   alışkanlığıyla preview'a çekmek teslimatı boşa düşürürdü. Karar:
   delete GERÇEK yürütülür AMA yalnız (a) kendi marker'lı config/pool
   dosyalarını (her biri için önce `.rollback` kopyası) ve (b) türetilen
   sistem kullanıcısını kaldırır; **document root ve tüm kullanıcı verisi
   asla silinmez** (`docroot_preserved=true` sonuçta raporlanır). Yıkıcılık
   seviyesi, ADR-0011'deki docker-DELETE'ten yapısal olarak düşüktür ve
   "verified rollback path" (security-boundaries.md) yerine geçer.
   delete `non_idempotent` + retry-unsafe işaretlenir (ikinci çağrı
   `ErrNotFound`).
7. **Node.js/Python dağıtımı: backend=proxy.** Uygulama çalıştırma
   (npm/pip/systemd unit üretimi) bu fazın kapsamı DEĞİLDİR; onun yerine
   site backend'i `proxy` ile nginx, uygulamayı **yalnız loopback**
   hedefe (`127.0.0.1:port`, `[::1]:port`, `unix:/run/...`) reverse-proxy
   eder. Dış hedef REDDEDİLİR (SSRF guard). Uygulama runtime yönetimi
   ayrı bir faz + ayrı ADR ile tasarlanacaktır (dürüst sınır,
   web-hosting.md §Uygulama dağıtımı).
8. **RBAC:** `web:read` (list/status/php.list), `web:control`
   (create/update/disable/delete). Migration 0012 yalnız yeni izinleri
   seed eder (CI-04 dersi); operator salt-okuma. delete `non_idempotent`
   + retry-unsafe; create/update/disable idempotent-desired-state +
   retry-safe (yeniden çalıştırma yakınsar; içerik aynıysa dürüst
   `Executed=false`).

## Alternatifler

- **Tam preview-only hosting (create dahil):** ADR-0011 ile tutarlı ama Faz
  08 teslimatını (site lifecycle + test sitesi + rollback doğrulaması)
  karşılamaz. Reddedildi; yıkıcı yüzey (docroot silimi) ayrı bir kararla
  yapısal olarak ortadan kaldırıldı (madde 6).
- **Tam silme (rm -rf docroot):** kullanıcı verisini yok eder; geri
  dönüşsüz. Reddedildi (security-boundaries.md destructive eylem politikası).
- **cPanel tarzı ortak kullanıcı (nobody/apache):** site izolasyonu
  kriterini ihlal eder. Reddedildi — site başına ayrı sistem kullanıcısı.
- **conf.d yerine sites-enabled yönetimi:** Debian'a özgü; conf.d include'ı
  hem Ubuntu hem Debian nginx.conf'ta varsayılan vardır. `nginx -T` include
  kanıtı, include' etkisizse tipli `ErrNotIncluded` verir (sessiz yanlış
  yapılandırma yok).
- **Docker/nginx container'a web stack kurulumu:** Faz 12+ sanallaştırma
  kapsamı; kapsam kayması. Reddedildi.

## Sonuçlar

- Faz 08 kriterleri karşılanır: 7 typed işlem (40-op registry), site
  izolasyonu (kullanıcı + 0750 docroot), atomik yazım + syntax-check
  kapısı + rollback kopyası (kriter 3), PHP matrisi + pool doğrulaması
  (kriter 4), port çakışması/edge-proxy yönetimi + kör yazım yasağı
  (kriter 5).
- CI `agent-web` job'ı gerçek nginx + php8.3-fpm ile tam lifecycle kanıtı
  üretir (create→update→disable→delete; rollback dosyası varlığı + docroot
  korunması doğrulanır); `EXPECTED_WEB=1` gate'i sessiz atlanamaz
  (CI-17 dersi).
- Mutasyon ailesi opt-in'dir; kapatma varsayılandır ve deploy README'sinde
  açıkça belgelenmiştir.
- Apache driver arayüzü tasarlanmıştır; gerçek Apache adapter'ı ayrı
  faz/ADR'de gelir (dürüst kapsam sınırı).
