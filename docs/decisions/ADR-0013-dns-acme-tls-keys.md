# ADR-0013 — DNS Provider Adapter, ACME orchestration ve TLS anahtar yerleşimi

Durum: ACCEPTED
Tarih: 2026-11-02
Faz: 09 (Domain, DNS, reverse proxy ve SSL)
İlgili: ADR-0003 (typed operations), ADR-0008 (agent mTLS), ADR-0012 (web hosting broker-tekil)

## Bağlam

Faz 09; domain envanteri, DNS sağlayıcı entegrasyonu ve ACME sertifika yaşam döngüsünü ekler. Kabul kriterleri şunları zorunlu kılar: sağlayıcı adaptörü kayıt CRUD + kabiliyet bildirimi; bölge/kayıt değişikliklerinde diff önizleme + TTL doğrulama + denetim; ACME issue/renew/revoke/expiry durum yönetimi ve rate limit/challenge hatalarının görünürlüğü; web sunucu config'inin sertifika yüklenmeden doğrulanması ve başarısız yenilemenin canlı sertifikayı bozmaması; dış entegrasyon kimlik bilgilerinin şifreli saklanması ve UI'da tekrar gösterilmemesi.

## Karar

### 1. DNS sağlayıcı adaptörü control-plane'dedir (node'larda değil)

`dns.Provider` arayüzü (Capabilities/Zones/Records/ApplyChange) control-plane tarafında yaşar. Gerekçe: sağlayıcı API anahtarları dış internet erişimi ister; bu anahtarları node'a dağıtmak saldırı yüzeyini büyütür. Typed operasyonlar `TargetControlPlane` olarak registry'ye girer (`dns.zone.list`, `dns.record.list`, `dns.record.diff`, `dns.record.apply`, `dns.record.delete`) ve worker'da Exec ile koşar — task/RBAC/audit çerçevesi aynen korunur.

Bu fazda desteklenen sağlayıcı türü kapalı kümedir: **yalnız `mock`** (in-memory, tam CRUD, DNS-01 TXT saklama, TTL 60–86400, wildcard). Cloudflare/Route53 gibi gerçek entegrasyonlar bilinçli olarak BU FAZDA YOKTUR: hesap gerektirirler, test edilemezler ve "kullanıcı istemedikçe ücretli servis entegrasyonu" kapsam dışıdır. Bilinmeyen tür dürüstçe reddedilir (`ErrKindUnknown`); tahminle açılmaz.

### 2. TLS özel anahtarı node'da doğar; control-plane asla görmez

Sertifika akışı üç typed görev zinciridir:

1. `web.cert.request` (node, broker, `--web-enable`): ECDSA P-256 anahtar üretir, **privkey.pem 0600** olarak diskte tutar, yalnız **CSR** döndürür (public materyal).
2. `cert.issue` (control-plane): CSR ile ACME order açar, **DNS-01** challenge'larını sağlayıcı adaptörü üzerinden yayımlar (`_acme-challenge.<domain>` TXT, 60 sn), finalize eder, sertifika+zinciri AES-256-GCM sealed saklar ve **cert_pem/chain_pem public materyalini** görev sonucuna koyar.
3. `web.cert.install` (node, broker, `--web-enable`): public PEM'i doğrular (parse, expiry, SAN kapsamı, **mevcut özel anahtarla çift eşleşmesi**), sahne yazımı → rollback kopyası → atomik rename → **`nginx -t` kapısı** → kapı geçmeden reload yok; kapı başarısızsa önceki sertifika geri yüklenir.

Bu, ADR-0002'deki "agent anahtarını node'da üretir, anahtar node'dan çıkmaz" ilkesinin TLS sertifikalarına genelleştirmesidir. Control-plane'de özel anahtar OLMADIĞI için iptal **hesap anahtarıyla** (RFC 8555 §7.6) elle JWS imzalanarak yapılır (`acmex.Manager.Revoke`).

### 3. ACME istemcisi: golang.org/x/crypto/acme + elle hesap-imzalı revoke

`internal/acmex`, x/crypto/acme'yi sarmalar (zaten doğrudan bağımlılık; sürüm 0.58.0, BSD-3-Clause — yeni modül YOK). Hata sınıflandırması yüzeye çıkarılır: `rate_limit` (429 + Retry-After), `challenge` (yetkilendirme başarısızlığı), `order_invalid`, `csr_invalid`, `network`, `acme_other` — hepsi sertifika kaydına yazılır ve UI'da görünür. ACME hesap anahtarı control-plane host'unda dosyadır (0600; PostgreSQL'de asla); dizin URL'si `CP_ACME_DIRECTORY_URL` ile verilir — ayarlanmadığında cert operasyonları dürüstçe `ErrACMEUnconfigured` döner.

Yenileme, **saklanan CSR'yi yeniden kullanır** (aynı anahtar çifti). Trade-off dürüstçe kabul edildi: anahtar rotasyonu idealdir ama faz kapsamı dışıdır; kayıt `renewal_failed` durumunda CANLI SERTİFİKA MATERYALİ KORUNUR (kriter 4'ün depo-düzeyi karşılığı) ve key-rotation sonraki iş olarak `docs/architecture/dns-ssl.md`'de listelenir.

### 4. Kimlik bilgileri: AES-256-GCM at-rest + write-only UI

`dns_providers.credentials_encrypted` yalnız **seal'lı** blob tutar (`internal/secrets`, AES-256-GCM, nonce-prefix). Anahtar `CP_SECRETS_KEY` (base64, 32 bayt) ile operatörce sağlanır; PostgreSQL'de asla yaşamaz; eksikse sağlayıcı kaydı tipli hatayla reddedilir. Okuma yolları (REST list/get, task sonuçları) kimlik bilgisi DEĞERİ döndürmez — yalnız `has_credentials` ve anahtar **adları**. UI formu yaz-amaçlıdır: submit sonrası alan sıfırlanır.

### 5. Sertifika durumu makinesi + süre izleyicisi

`ssl_certificates` tablosu kapalı durum kümesi taşır: `pending → issuing → issued → (renewing → issued | renewal_failed)`, `revoked`, `expired`. "expired" hem okuma anında türetilir (`EffectiveStateOf`) hem de `certmgmt.Watcher` tarafından kalıcılaştırılır; 30 günden az ömür `expiring_soon` rozetidir. Tüm başarısızlıklar `last_error` + `last_error_class` + `retry_after_secs` ile kayda işlenir.

## Alternatifler ve neden reddedildiği

- **ACME'yi node'da koşturmak** (certbot benzeri): sağlayıcı anahtarını node'a dağıtmak gerekir; control-plane'deki şifreli kimlik bilgi disiplini (kriter 5) bozulur. Reddedildi.
- **Private key'i control-plane'de şifreli saklamak** (revoke için `RevokeCert` kolay yolu): ADR-0002 ilkesini çiğner; anahtar rotasyonu/ithali yüzeyi ekler. Reddedildi — hesap-imzalı revoke yeterli.
- **autocert (HTTP-01) kullanmak**: control-plane'in edge'e 80/443 maruziyeti gerekir; mimaride edge, node'dadır ve broker-tekil disiplini vardır. Reddedildi — DNS-01, control-plane'de kalır.
- **gerçek sağlayıcı SDK'ları eklemek**: test edilemez (hesap yok), lisans/bağımlılık yükü, kapsam kayması. Reddedildi — Provider arayüzü sözleşme olarak yeterli.

## Sonuçlar

- Pozitif: private key node'da; kimlik bilgileri GCM-sealed; diff→apply ayrımı zorunlu; başarısız yenileme kurulum/depo katmanında canlı sertifikayı korur; tüm yol auditable task'lar.
- Negatif: gerçek sağlayıcı entegrasyonu sonraki iş; yenilemede anahtar rotasyonu yok (belgelendi); gerçek ACME staging koşusu disposable domain gerektirir — bu fazın test raporunda NOT RUN olarak dürüstçe işaretlenir.
