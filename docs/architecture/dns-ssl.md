# DNS & SSL mimarisi (Faz 09)

Bu belge, domain/DNS sağlayıcı adaptörü ve ACME sertifika yaşam döngüsünün mimari sınırlarını, destek matrisini ve güvenlik modelini tanımlar. Karar gerekçeleri ADR-0013'tedir.

## 1. Bileşen yerleşimi

```
┌─ Control Plane ──────────────────────────────────────────────┐
│ dns.Service        → Provider adapter (mock) + şifreli creds │
│ certmgmt.Service   → ACME akışı + certificates durumu    │
│ acmex.Manager      → x/crypto/acme + hesap-imzalı revoke     │
│ typed ops (worker) : dns.zone.list / dns.record.list /        │
│   dns.record.diff / dns.record.apply / dns.record.delete /    │
│   cert.issue / cert.renew / cert.revoke                       │
└──────────────────────────────┬───────────────────────────────┘
                               │ mTLS gRPC (typed ops)
┌─ Node (broker, --web-enable) ─▼──────────────────────────────┐
│ tlsops.Runner: web.cert.request (anahtar+CSR) /              │
│   web.cert.install (nginx -t kapısı + rollback + reload) /   │
│   web.cert.status                                            │
│ /etc/nginx/ssl/srvctl/<domain>/{privkey.pem 0600,            │
│                                fullchain.pem 0644}           │
└──────────────────────────────────────────────────────────────┘
```

- Sağlayıcı kimlik bilgileri yalnız control-plane'de, AES-256-GCM sealed (`dns_providers.credentials_encrypted`); anahtar `CP_SECRETS_KEY` (base64 32 bayt) ile operatörce sağlanır ve PostgreSQL'de asla yaşamaz.
- TLS özel anahtarı yalnız node'da doğar ve asla iletilmez; control-plane yalnız CSR (public) tutar ve yenilemede onu yeniden kullanır.

## 2. Typed operasyonlar (registry 40 → 51)

| Operasyon | Hedef | Sınıf | İzin | Not |
|---|---|---|---|---|
| dns.zone.list | control_plane | read_only | dns:read | Sağlayıcı kabiliyetleriyle döner |
| dns.record.list | control_plane | read_only | dns:read | limit ≤ 500 |
| dns.record.diff | control_plane | read_only | dns:read | **apply öncesi zorunlu önizleme** |
| dns.record.apply | control_plane | idempotent | dns:control | TTL aralığı capability'den; sonuç before/after (audit) |
| dns.record.delete | control_plane | non_idempotent | dns:control | retry-güvensiz |
| cert.issue | control_plane | non_idempotent | cert:control | CSR node'dan; hata sınıfı kayda işlenir |
| cert.renew | control_plane | non_idempotent | cert:control | Saklanan CSR; canlı materyal korunur |
| cert.revoke | control_plane | non_idempotent | cert:control | Hesap-imzalı (RFC 8555 §7.6) |
| web.cert.request | node | idempotent | web:control | CSR döner; anahtar diskte 0600 |
| web.cert.install | node | idempotent | web:control | Yalnız public PEM; nginx -t kapısı |
| web.cert.status | node | read_only | web:read | Diskteki sertifikaların not_after'ı |

## 3. Destek matrisi (dürüst)

| Yetenek | Durum |
|---|---|
| DNS sağlayıcı türleri | `mock` (in-memory) + **`cloudflare`** (gerçek sağlayıcı, API v4 — Faz 16, ADR-0021). Route53/Google DNS/... → sonraki iş (aynı şablon: BuildX + httptest sahte süit + ADR) |
| Kayıt türleri | A, AAAA, CNAME, TXT, MX (kapalı küme) |
| TTL aralığı | 60–86400 (sağlayıcı capability beyanıyla doğrulanır) |
| Wildcard kayıt (*.sub) | destekli (capability: wildcard) |
| ACME challenge | **yalnız DNS-01** (control-plane → sağlayıcı TXT) |
| ACME sunucusu | `CP_ACME_DIRECTORY_URL` ile seçilir; ayarsız → cert operasyonları dürüst red |
| Yenileme anahtar rotasyonu | YOK (aynı CSR/anahtar; belgelenmiş trade-off — ADR-0013) |
| Gerçek ACME staging kanıtı | disposable domain gerektirir — bu sürümde NOT RUN (test raporu) |

## 4. Sertifika durum makinesi

```
pending ──issue──▶ issuing ──başarılı──▶ issued ──renew──▶ renewing ──▶ issued
   ▲                  │fail                  │                  │fail
   └── retry ◀── (last_error_class)           │            renewal_failed
                                              └── not_after geçti ──▶ expired
issued ──revoke──▶ revoked
```

- Başarısızlık `last_error` + `last_error_class` (`rate_limit` | `challenge` | `order_invalid` | `csr_invalid` | `network` | `acme_other`) + `retry_after_secs` ile kayda işlenir; UI rozetleri bunu gösterir.
- `expired`: okuma anında türetilir (`EffectiveStateOf`) VE saatlik watcher ile kalıcılaşır.
- `renewal_failed` kaydında canlı sertifika materyali (sealed PEM) KORUNUR — kriter 4'ün depo katmanındaki karşılığı; node tarafında da install kapısı geçmeden diske yazım yoktur.

## 5. Güvenlik modeli

1. **Anahtar-node'da**: `web.cert.request` CSR döndürür; `web.cert.install` yalnız public PEM kabul eder ve leaf'in diskteki özel anahtarla **çift eşleşmesini** doğrular (yanlış node'un sertifikası kurulamaz).
2. **Kapı disiplini (ADR-0012 genelleştirmesi)**: doğrulama (parse/expiry/SAN/pair) herhangi bir dosya değişikliğinden ÖNCE; sahne → rollback kopyası → atomik rename; `nginx -t` başarısızsa geri alma + yeniden doğrulama; reload yalnız kapı geçince.
3. **Kimlik bilgisi gizliliği**: write-only UI; hiçbir okuma yanıtı (REST, task sonucu, audit) credential değeri taşımaz; `internal/secrets` GCM bütünlük kanıtıyla bozuk/yanlış anahtarı tipli reddeder.
4. **RBAC**: dns:read/dns:control + cert:read/cert:control (migration 0013 yalnız yeni izinleri seed eder — CI-04 dersi); node cert işlemleri web:read/web:control altındadır.
5. **Audit**: sağlayıcı create/delete REST audit'e; tüm kayıt/sertifika değişiklikleri task kayıtları + task_attempts üzerinden izlenebilir (apply sonucu before/after taşır).

## 6. Yapılandırma

| Değişken | Anlamı |
|---|---|
| `CP_SECRETS_KEY` | base64 32 bayt AES-256 anahtarı. Ayarsız: sağlayıcı kaydı + sertifika materyali tipli hatayla reddedilir (sessiz fallback yok). |
| `CP_ACME_ACCOUNT_KEY` | ACME hesap anahtarı dosya yolu (yoksa P-256 üretir, 0600). `CP_ACME_DIRECTORY_URL` ile birlikte zorunlu. |
| `CP_ACME_DIRECTORY_URL` | ACME dizin URL'si (test/disposable: Let's Encrypt **staging**). |
| `CP_ACME_CONTACT` | Hesap iletişimi (örn. `mailto:ops@example.com`). |

## 7. Sonraki işler (dürüst liste)

- ~~Gerçek sağlayıcı adaptörleri (Cloudflare/...)~~ → **Cloudflare TAMAMLANDI (Faz 16, ADR-0021)**. Route53/Google DNS/... aynı şablonla eklenir; her biri kendi kabul testiyle gelir.
- Yenilemede anahtar rotasyonu (yeni CSR zinciri + eski anahtarın imhası).
- Gerçek ACME staging uçtan uca koşusu (disposable domain + CI secret'ı).
- SSL listener'lı site config entegrasyonu (web.site.create'e ssl bloğu — Faz 08 şablonunun uzantısı).
