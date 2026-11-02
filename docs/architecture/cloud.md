# Cloud Provider Adapters (Faz 14)

Kapsam: sağlayıcı bağdaştırıcı çerçevesi ve ilk adaptör (S3-uyumlu nesne depolama). ADR-0018 ile bağlıdır.

## Çerçeve

```
internal/cloud
├── cloud.go     Provider arayüzü, PermissionMatrix, tipli hatalar
├── s3.go        İlk adaptör: S3-uyumlu (AWS SigV4, yalnız stdlib, path-style)
├── service.go   Use cases: Create/List/Verify/ListResources/DeleteResource(confirm)
└── store_pg.go  cloud_providers (kimlik bilgileri AES-256-GCM mühürlü)
```

`Provider` sözleşmesi: `Kind()`, `PermissionMatrix()`, `RequiredCredentialKeys()`, `VerifyCredentials()` (yalnız salt-okuma sonda), `ListResources()`, `DeleteResource()`.

## Her sağlayıcı AYRI matris (kriter 5)

| Sağlayıcı | list | read | delete |
|---|---|---|---|
| s3 | `s3:ListAllMyBuckets` (risk: read) | `s3:ListBucket` (read) | `s3:DeleteBucket` (destructive, **confirm zorunlu**) |

Matris UI'da birebir gösterilir; sahtecilik yapısı: sağlayıcının izin modeli adapter'ın statik beyanıdır, `verify` ise kimliğin GERÇEKTE ne yapabildiğini salt-okuma sonda ile raporlar (kanıt → probe OK/fail + neden). Tahmin yoktur: probe edilemeyen eylem "unverified" kalır.

## Kimlik bilgisi disiplini (ADR-0013 ile aynı)

- Kayıt: `POST /cloud-providers {name, kind, endpoint, region, credentials}` — kind kapalı enum (`s3`); ad 3..64; endpoint https zorunlu (yalnız `127.0.0.1/localhost/::1` için http istisnası — test/geliştirme).
- Sealing: `credentials` JSON → AES-256-GCM (CP_SECRETS_KEY) → `credentials_encrypted BYTEA`. CP_SECRETS_KEY ayarsızsa kayıt/verify/list/silme tipli `ErrKeyNotConfigured` ile reddedilir (sessiz degrade yok).
- Yaz-amaçlı: hiçbir okuma yolu değer döndürmez; yalnız `credential_keys` (anahtar ADLARI) listelenir.
- En fazla 20 sağlayıcı kaydı (`ErrProviderLimit`).

## Doğrulama ve kaynak yüzeyleri

| Uç | İzin | Davranış |
|---|---|---|
| `POST /cloud-providers/{id}/verify` | cloud:control | Kimlik sondası (GET / ListBuckets) + matris + probe raporu; audit `cloud.provider.verify` |
| `GET /cloud-providers/{id}/resources` | cloud:read | Bucket listesi (limit ≤100); audit `cloud.resource.list` |
| `DELETE /cloud-providers/{id}/resources/{name}?confirm=true` | cloud:control | **Onay kapısı servis sınırlarında**: confirm yoksa `ErrConfirmationRequired` → 400 (UI atlatamaz; query/body ikisi de kabul edilir). Ad biçimi (3..63, [a-z0-9.-]) geçersizse HTTP çağrısı HİÇ yapılmaz. Sağlayıcı 409 → tipli `ErrResourceNotEmpty` — panel zorla silmez. |

Audit: `cloud.provider.create|delete|verify`, `cloud.resource.list|delete` — hepsi correlation_id + redaksiyonlu metadata ile.

## S3 imza katmanı

- AWS Signature Version 4 (HMAC-SHA256 zinciri; unsigned-empty-body; host/x-amz-date/x-amz-content-sha256 imzalı başlıklar), path-style addressing (`https://endpoint/bucket`).
- Sürümlü istemci zaman aşımı 15 sn; 404/409/401/403/5xx → tipli hata eşlemesi (`ErrResourceNotFound`, `ErrResourceNotEmpty`, `ErrPermission`, `ErrProviderUnreachable`).
- Sonda yalnız salt-okuma: VerifyCredentials asla mutasyon çağırmaz.

## Test kanıtı

- Hermetik: httptest sahte S3 (SigV4 başlık şekli, ListBuckets XML, delete confirm/non-empty/missing yolları, geçersiz ad HTTP'siz red, uç nokta politikası, ulaşılamaz sağlayıcı); servis katmanı (mühelleme sızma taraması, onay kapısı provider çağrısız red, matris raporu).
- CI: backend job (bu paketin birim testleri) + e2e backup-cloud.spec.ts (UI matris + iki adımlı silme).

## Dürüst sınırlar (bu fazda YOK)

- Yalnız S3-uyumlu adaptör; başka sağlayıcı kind'ları (hesap VM/DNS sağlayıcıları) ayrı faz + ayrı test seti ile gelir. Bucket oluşturma/yeniden adlandırma, nesne düzeyi işlemler, restic uzak repo bağlama kapsam dışıdır; silme tek kaynak-başına ve confirm kapılıdır.
