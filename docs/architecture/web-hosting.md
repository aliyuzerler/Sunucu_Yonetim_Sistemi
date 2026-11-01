# Web Hosting Destek Matrisi (Faz 08 — nginx + PHP-FPM)

Bu doküman Faz 08 web hosting modülünün dürüst destek sınırlarını, güvenlik
modelini ve işlem kataloğunu belgeler. Karar gerekçeleri: ADR-0012.

## Destek matrisi

| Bileşen | Durum | Not |
|---|---|---|
| nginx (systemd kurulu, `nginx` binary PATH'te) | **Destekleniyor** | config `/etc/nginx/conf.d/srvctl-*.conf` ad-uzayında yönetilir; `conf.d/*.conf` include'ı Ubuntu 24.04 + Debian 13 varsayılanlarında vardır. Include etkisizse `nginx -T` kanıt adımı tipli `ErrNotIncluded` verir. |
| PHP-FPM 8.2 / 8.3 / 8.4 / 8.5 | **Destekleniyor** (kapalı matris + kurulu tespiti) | Matris dışı sürüm → `ErrPHPUnsupported`; matriste ama node'da kurulu değil → `ErrPHPNotInstalled` (tahmin yok). |
| Apache | **Driver arayüzü tasarlandı; adapter uygulanmadı** | Port çakışması tespiti apache2/httpd sahipliğini tanır ve tipli `ErrPortConflict` + edge-proxy önerisi verir. Apache config'ine dokunulmaz. |
| Node.js/Python uygulama dağıtımı | **backend=proxy ile destekleniyor** | nginx, uygulamayı yalnız loopback hedefe reverse-proxy eder (SSRF guard). Uygulama runtime'ı yönetimi (npm/pip/unit üretimi) ayrı faz. |
| Windows node'lar | **Desteklenmiyor** | web ailesi yalnız unix broker'dadır; capability beyan edilmez. |

## Güvenlik modeli (ADR-0012)

1. **Broker-tekil + opt-in:** tüm işlemler `server-privd` içinde yürütülür
   ve aile `--web-enable` ile AÇILIR (varsayılan kapalı). Kapalıyken tipli
   `unsupported_operation` reddi döner.
2. **İsim türetme:** `name` allowlist'i `[a-z0-9][a-z0-9-]{0,22}[a-z0-9]`
   (≤24). Kullanıcı `sc-web-<name>` (≤31, useradd NAME_REGEX uyumlu),
   config `conf.d/srvctl-<name>.conf`, docroot `/var/www/server-control/<name>/html`
   YALNIZ addan türetilir. Docroot override yoktur (path traversal
   yapısal olarak imkânsız).
3. **Site izolasyonu:** site başına sistem kullanıcısı (`--system
   --user-group --shell /usr/sbin/nologin`), docroot 0750
   (sahip: site kullanıcısı, grup: www-data) → site kullanıcıları
   birbirinin dosyalarını okuyamaz; nginx yalnız kendi grup üyeliğiyle
   okur. PHP pool süreçleri site kullanıcısı altında çalışır; socket ACL'si
   (`listen.owner/group = www-data`, mode 0660) yalnız web sunucusuna
   açıktır.
4. **Config disiplini:**
   - İlk satır `MarkerHeader`; markersız dosya → `ErrForeignConfig`
     (kör yazım yasağı, kriter 5).
   - Yazım temp+rename (atomik); önceki içerik daima `<path>.rollback`.
   - Reload kapıları: `nginx -t` (syntax) → `nginx -T` (include kanıtı)
     → `systemctl reload-or-try-restart nginx`. Kapı başarısızsa rollback
     geri yüklenir, reload sinyallenmez (kriter 3).
   - disable: config `*.conf.disabled` olarak yeniden adlandırılır (include
     glob'u durur); tersine çevrilebilir; her adımda syntax kapısı işler.
   - delete: SADECE marker'lı config/pool dosyaları (her biri için önce
     `.rollback`) + türetilen kullanıcı kaldırılır; **docroot ASLA
     silinmez** (`docroot_preserved=true`).
5. **Port çakışması:** `/proc/net/tcp{,6}` + `/proc/<pid>/fd` inode
   eşlemesiyle dinleyici sahipliği belirlenir:
   - serbest port → OK
   - nginx sahipli → OK (isim-tabanlı vhost; çoklu server block tek socket)
   - apache2/httpd sahipli → tipli red + edge-proxy önerisi
   - bilinmeyen/sahipsiz → güvenli tarafta red
   - update'te sitenin KENDİ config'inin beyan ettiği port serbesttir
     (yerinde güncelleme).
6. **SSRF guard (proxy backend):** hedef yalnız `127.0.0.1:port`,
   `[::1]:port`, `unix:/run/<safe>` — dış hedef/red yok; `..` reddi.
7. **Argv disiplini:** useradd/userdel/nginx/systemctl sabit binary +
   argüman vektörü; girdi asla shell string'ine birleşmez. Tüm broker
   istekleri (redler dahil) JSONL audit'e yazılır.
8. **RBAC:** `web:read` (list/status/php.list), `web:control`
   (create/update/disable/delete). create/update/disable
   idempotent-desired-state + retry-safe; delete non_idempotent +
   retry-unsafe (ikinci çağrı `ErrNotFound`).

## PHP-FPM pool şablonu

- `[srvctl-<site>]` havuzu; `pm = dynamic`, `pm.max_children` 1..200
  (varsayılan 10), start/spare değerleri türetilir (1 çocuk → 1/1/1).
- `php_admin_value[disable_functions] = exec,passthru,shell_exec,system,proc_open,popen`
- `chdir = <docroot>`; pool süreci site kullanıcısı + grubu altında.

## İşlem kataloğu (7 op — registry 33 → 40)

| İşlem | Sınıf | İzin | Yürütme |
|---|---|---|---|
| `web.site.list` | read_only | web:read | gerçek (conf.d taraması, marker doğrulamalı) |
| `web.site.status` | read_only | web:read | gerçek |
| `web.php.list` | read_only | web:read | gerçek (matris vs kurulu tespit) |
| `web.site.create` | idempotent_desired_state | web:control | gerçek (opt-in) |
| `web.site.update` | idempotent_desired_state | web:control | gerçek (opt-in) |
| `web.site.disable` | idempotent_desired_state | web:control | gerçek (opt-in) |
| `web.site.delete` | non_idempotent | web:control | gerçek, dosya-koruyucu (opt-in) |

## Dürüst sınırlar

- Uygulama runtime yönetimi (git/npm/pip/systemd unit üretimi, süreç
  denetimi) bu fazın kapsamı DEĞİLDİR; `backend=proxy` ile nginx tarafı
  hazır, runtime tarafı ayrı faz + ADR.
- Apache adapter uygulanmadı (arayüz + çakışma tespiti hazır).
- RHEL-family web sunucu grubu (`nginx`) bu fazda varsayılan değildir;
  Runner.NginxGroup alanı ile yapılandırılabilir (doc: `www-data` Debian/
  Ubuntu varsayılanı). Destek beyanı Ubuntu 24.04 + Debian 13 ile sınırlıdır.
- nginx kurulu değilse tüm aile tipli `ErrUnavailable` verir (tahmin yok).
- Sertifikasyon/TLS terminali bu fazda değildir (443 yönlendirme dahil) —
  Faz 09 SSL kapsamı.
