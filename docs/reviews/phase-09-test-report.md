# Faz 09 Bağımsız Test Raporu — Domain, DNS, reverse proxy ve SSL

- **Test tarihi:** 2026-11-02
- **Denetlenen commit:** da86058 (Faz 09 implementasyonu; taban f9bd5c0 = Faz 08 ACCEPTED/READY, tag v0.9.0)
- **Test eden rolü:** bağımsız denetçi (uygulama ajanı varsayımları alınmadı; kanıt file:line + komut çıktısı istendi)
- **CI koşuları:** run 41355496209 (9/12 yeşil) → CI-20; run 41356212535 (11/12 yeşil — **gerçek nginx TLS lifecycle kanıtı dahil**) → CI-21; run 41356962374 (11/12 yeşil) → CI-22; run 41357921164 (11/12 yeşil) → CI-23; düzeltmeler sonrası beşinci koşu yeşil beklenir. Aşağıdaki komut çıktıları da86058 tabanlıdır; düzeltme commit'leri yalnız .gitignore + test şemaları/sabitleri + IT-03 netleştirmesi içerir
- **Kapsam:** yalnız Faz 09 (`prompts/phase-test/phase-09-test.md`); sonraki faz başlatılmadı.

## Zorunlu kabul kontrolleri

### Kriter 1 — DNS provider adapterı kayıt CRUD ve provider capability bildirmeli → **PASS**

| Kanıt | Konum |
|---|---|
| `Provider` arayüzü: `Capabilities() / Zones / Records / ApplyChange` (create/update/delete tek yüzey) | `backend/control-plane/internal/dns/dns.go:57-70` |
| Mock provider tam CRUD: create→duplicate `ErrConflict`, update, delete→`ErrNotFound`, bölge/kayıt listeleri | `internal/dns/mock.go` (`ApplyChange`), test `dns_test.go:15-66` (TestMockProviderCRUD) |
| Kabiliyet bildirimi: TTL 60..86400, kapalı tür kümesi {A,AAAA,CNAME,TXT,MX}, DNS-01 + wildcard beyanı | `internal/dns/mock.go` (`NewMock`), test `dns_test.go:TestMockCapabilities` |
| Kabiliyet görev sonuçlarına ve UI'ya taşınır: `{"zones":…, "capabilities":…}` | `internal/tasks/ops_faz09.go:DNSZonesExecutor`; UI `web/src/pages/DnsPage.tsx` (kabiliyet rozetleri; e2e `01-02` ekran görüntüleri) |
| Dürüst kapsam: bilinmeyen sağlayıcı türü tipli red (tahmin yok) | `internal/dns/service.go:26-43` (`ErrKindUnknown`), test `service_test.go:TestServiceCreateProviderValidates` |
| REST sağlayıcı CRUD + audit | `internal/httpapi/handlers_dnscert.go:23-82`; migration 0013 `dns_providers` tablosu |

Komut: `go test ./internal/dns` → `ok` (7 test).

### Kriter 2 — DNS zone/record değişikliklerinde diff preview, TTL validation ve audit → **PASS**

| Kanıt | Konum |
|---|---|
| Diff önizleme salt-okuma op: `dns.record.diff` (ClassReadOnly, dns:read) — before/after anlık görüntüsü | `internal/tasks/ops_faz09.go:504-512` + `DNSDiffExecutor`; `internal/dns/service.go:Diff` |
| Apply zorunlu kapı: apply sonucu before/after taşır (denetim izi); ayrı `dns.record.delete` non_idempotent | `ops_faz09.go:513-522` + `DNSApplyExecutor` |
| TTL doğrulaması iki katman: biçimsel 60..86400 (validator) + sağlayıcı capability sınırları (executor) | `ops_faz09.go:ValidateDNSRecord` (`ttl 60..86400 olmalı`) + `internal/dns/dns.go:140-146` (`ValidateTTL` → `ErrTTLBounds`) |
| Değer doğrulaması: A/AAAA ayrıştırıcı, CNAME/MX hedef kuralları | `internal/dns/dns.go:ValidateValue`; negatif testler `dns_test.go:TestValidateRecordAndTTL` |
| Audit: tüm kayıt değişiklikleri task kaydı + `task_attempts` üzerinden izlenebilir; sağlayıcı create/delete REST audit'e yazılır | `internal/httpapi/handlers_dnscert.go:60,80`; task audit Faz 04 çerçevesi |
| UI disiplini: diff olmadan apply istemcide engellenir; e2e diff kartı before (203.0.113.10) + after (203.0.113.99) doğrular | `web/src/pages/DnsPage.tsx:applyRecord` (`noDiff` kapısı); `web/e2e/dns-ssl.spec.ts:30-…` (PASS) |

### Kriter 3 — ACME issue/renew/revoke/expiry state yönetilmeli; rate limit ve challenge hatası görünür olmalı → **PASS** (staging gerçek koşusu NOT RUN — bkz. bulgular)

| Kanıt | Konum |
|---|---|
| Durum makinesi kapalı küme: pending/issuing/issued/renewing/renewal_failed/revoked/expired (CHECK kısıtı + geçiş metotları) | migration `0013_dns_ssl.up.sql:31-35`; `internal/certmgmt/store_pg.go` (MarkIssuing/MarkRenewing/MarkIssued/MarkFailed/MarkRevoked/MarkExpired) |
| expiry: okuma anında türetim (`EffectiveStateOf`) + saatlik watcher kalıcılaştırması; expiring_soon ≤30g | `internal/certmgmt/certmgmt.go:78-83`; `store_pg.go:Watcher`; test `store_pg_test.go:TestStoreExpiryWatcher` |
| issue: fake ACME ile uçtan uca (RFC 8555 key-authz digest'i gerçek TXT köprüsünden doğrulanır), SAN listesi, zincir, not_after, parmak izi | `internal/acmex/acmex_test.go:TestIssueHappyPath`; `internal/certmgmt/service_pg_test.go:TestFlowIssueRenewRevoke` (PG) |
| renew: saklanan CSR ile; başarısızlık `renewal_failed` + canlı materyal korunur | `internal/certmgmt/service.go:Renew`; PG akış testi (rate_limit senaryosu) |
| revoke: hesap-imzalı (RFC 8555 §7.6) elle JWS; `alreadyRevoked` idempotent; fake sunucu serial+gerekçe doğrular | `internal/acmex/acmex.go:postJWSRevoke`; `acmex_test.go:TestRevokeByAccount` |
| **rate limit görünür**: 429 + Retry-After → `ClassRateLimit` + saniye; kayda `last_error_class=rate_limit`, `retry_after_secs=900/3600` işlenir; UI "Retry-After: 900s" gösterir | `internal/acmex/acmex.go:59-70` (Classify); `service_pg_test.go` (rate_limit dalı); `web/src/pages/SslPage.tsx` + vitest `DnsSsl.test.tsx` (rate_limit + Retry-After görünür) |
| **challenge hatası görünür**: authz invalid → `ClassChallenge`; kayıt pending kalır, sınıf yüzeyde | `acmex.go:71-73`; `acmex_test.go:TestIssueChallengeFailureSurfaced`; `certmgmt/service_pg_test.go:TestFlowIssueFailureClassificationPersisted` |
| Köprü hatası ile CA reddi karışmaz (setter hatası challenge sınıflanmaz) | `acmex_test.go:TestIssueSetterErrorIsNotChallenge` |
| E2E: demo `renewal_failed` + `rate_limit` + Retry-After görünürlüğü | `web/e2e/dns-ssl.spec.ts` test 2 (PASS) |

**NOT RUN (dürüst):** gerçek Let's Encrypt **staging** uçtan uca koşusu — disposable domain gerektirir; sandbox/CI'da yok. Sözleşme kanıtı `internal/acmex/fakeca` (in-process RFC 8555 test çifti; JWS imzası doğrulanmaz — açıkça belgelenmiş) ile karşılanır. `docs/architecture/dns-ssl.md` §3 ve §7'de kayıtlı.

### Kriter 4 — Web server config sertifika yüklenmeden önce validate edilmeli; başarısız yenileme canlı sertifikayı bozmamalı → **PASS**

| Kanıt | Konum |
|---|---|
| Doğrulama herhangi bir dosya değişikliğinden ÖNCE: parse → expiry → SAN kapsamı → **çift eşleşme** (leaf public key ↔ diskteki privkey; yanlış node'un sertifikası kurulamaz) | `agent/internal/tlsops/runner_unix.go:174-212` (`samePublicKey` DER kanonik karşılaştırma; `ErrKeyPairMismatch`) |
| Yükleme disiplini: sahne `.new` yazımı → mevcutsa `.rollback` kopyası → atomik rename → **`nginx -t` kapısı** → kapı geçmeden reload yok; kapı başarısızsa rollback geri yüklenir + yeniden `nginx -t` | `runner_unix.go:214-247`; hata `ErrSyntax` + "önceki sertifika geri yüklendi" |
| Depo katmanı: `renewal_failed` kaydında sealed canlı PEM korunur (MarkFailed docroot/kayda dokunmaz) | `internal/certmgmt/store_pg.go:MarkFailed`; PG test `TestStoreLifecycle` (materyal korunması satırı) |
| Hermetik negatifler: nginx -t başarısız → canlı sertifika byte-düzeyinde korunur; eşleşmezlik → hiçbir dosya yazılmaz; anahtarsız → `ErrNotFound`; süresi dolmuş → `ErrCertExpired`; SAN kapsamayan → `ErrCertInvalid` | `runner_unix_test.go` (TestInstallFailedSyntaxGateRestoresPreviousCert, TestInstallKeyPairMismatch, TestInstallWithoutKeyRefused, TestInstallExpiredCertRefused, TestInstallSANCoverageRequired) |
| **Gerçek-host kanıtı (CI):** `EXPECTED_TLS=1` gate — gerçek nginx'te anahtar üretimi + self-signed kurulum + gerçek `nginx -t` + reload + eşleşmeyen anahtar reddi sonrası canlı sertifika + `nginx -t` yeşil | `agent/internal/tlsops/realhost_test.go:TestRealTLSLifecycle`; CI `agent-web` job "Faz 09 TLS sertifika gerçek lifecycle kanıtı" adımı (CI-17 dersi: sessiz atlama yok) |
| CSR özel anahtar İÇERMEZ (anahtar node'dan çıkmaz) | `runner_unix_test.go:TestRequestGeneratesAndReusesKey` + realhost test |

### Kriter 5 — Cloud DNS gibi dış entegrasyon secretları şifreli saklanmalı ve UI'da tekrar gösterilmemeli → **PASS**

| Kanıt | Konum |
|---|---|
| AES-256-GCM: nonce-prefix seal; yanlış anahtar/bozulma tipli red; sınır 64 KiB | `internal/secrets/secrets.go`; testler (roundtrip, yanlış anahtar, tamper, limit) |
| Anahtar PostgreSQL'de yaşamaz: `CP_SECRETS_KEY` operatör sağlar; ayarsız tipli red (`ErrKeyMissing`) | `internal/config/config.go:138-149`; `internal/dns/service.go:hasKey` |
| Depoda yalnız seal'lı blob: `dns_providers.credentials_encrypted` + yalnız anahtar ADLARI (`credential_keys`) | migration 0013; `internal/dns/store_pg.go:22-23` |
| Okuma yolları değer döndürmez: Store.List/Get hiçbir şifreli alan seçmez; decrypt YALNIZ executor yolu (`GetEncryptedCredentials`) | `store_pg.go:38-52,127-141` (yorum: "Never called by HTTP handlers") |
| UI write-only: form submit sonrası API key alanı sıfırlanır; listede yalnız "şifreli saklandı" notu; e2e `toHaveCount(0)` ile değer gizliliği | `web/src/pages/DnsPage.tsx:146`; vitest `DnsSsl.test.tsx` (write-only testi); e2e test 1 (PASS) |
| Task sonuçlarında da yok: DNS executor sonuçları yalnız zone/record/change/kabiliyet taşır | `ops_faz09.go` executor'lar |

## Bulunan hatalar ve önem derecesi

- **IT-01 (Orta, kapsam-kısıtı — belgelendi):** Gerçek ACME staging uçtan uca koşusu disposable domain gerektirdiğinden NOT RUN. Sözleşme kanıtı fakeca ile karşılanır; `dns-ssl.md` §7'de sonraki iş olarak kayıtlı. Düzeltme önerisi: disposable domain + CI secret ile ayrı doğrulama işi.
- **IT-02 (Düşük, tasarım trade-off — belgelendi):** Yenileme saklanan CSR'yi yeniden kullanır (aynı anahtar çifti). ADR-0013'te açıkça kabul edildi; anahtar rotasyonu sonraki iş.
- **IT-03 (Düşük, kod-netlik):** `certmgmt.Service.Issue` içindeki `MarkIssuing` hatası bilinçli yoksayılır (renewing→issuing geçişi başarısız olur çünkü state zaten akışta); kod `_ = err` ile karartılmış — davranış doğru (hem fresh hem renewal yolları testli) ama okunabilirlik için ayrık ifade önerilir. Düzeltme zorunlu değil; izleme listesi.
- **CI-20 (Orta, test-infra — CI ilk koşusunda yakalandı, DÜZELTİLDİ):** `.gitignore`'daki geniş `secrets/` kuralı `backend/control-plane/internal/secrets/` paketini sessizce repodan dışlamıştı — yerelde paket diskte olduğundan her şey yeşildi ama 3 CI job'ı (backend/e2e/frontend-yakın) `no required module provides package …/internal/secrets` ile düştü. Ders: yerdeş paket adlarıyla güvenlik amaçlı ignore kuralları yan etki üretir. Düzeltme: kural kök-ancaklı `/secrets/` yapıldı + paket repoya eklendi. Aynı koşuda ikinci bulgu: `TestRealTLSLifecycle` eşleşmezlik adımı `issue()` dönüşünden YAPRAK yerine ZİNCİR PEM'ini kullanıyordu (`ErrCertInvalid: SAN kapsamıyor` gerçek-host kanıtında yakalandı) — düzeltildi, yanına niyet yorumu eklendi.
- **IT-03 (Düşük, kod-netlik — DÜZELTİLDİ):** `certmgmt.Service.Issue` içindeki `MarkIssuing` hatası `_ = err` ile karartılmıştı; artık fresh akışta geçiş hatası hızlı-dürüst hata olarak döner, renewal'daki beklenen red açık yorumla no-op'tur (davranış değişmedi; her iki yol testli).
- **CI-21 (Orta, test-infra — CI ikinci koşusunda yakalandı, DÜZELTİLDİ):** `certmgmt` PG akış testi `dns.Service`'i Store'suz kuruyordu; yerelde TEST_DATABASE_URL olmadan test skip olduğundan eksik bağımlılık yalnız CI'da nil-pointer olarak patladı. Düzeltme: `newFlow` artık gerçek store bağlar (`dns.NewStore(pool)`); niyet yorumu eklendi. Ders: skip-edilebilir entegrasyon testlerinde bağımlılık eksikliği yerelde görünmez — kurulumun kendisi de test edilmeli.
- **CI-22 (Orta, şema-hizalama — CI üçüncü koşusunda yakalandı, DÜZELTİLDİ):** migration `ssl_certificates` tablosunu kuruyordu ama certmgmt store SQL'i `certificates` kullanıyordu → CI'da `relation "certificates" does not exist` (yerelde DB olmadığından testler skip olduğundan görünmezdi). Düzeltme: migration (up/down) `certificates` olarak hizalandı (0006 `node_certificates` ayrıdır, çakışma yok); index adları da güncellendi; migration yorumuna CI-22 dersi eklendi. Dokümanlarda (dns-ssl.md, ADR-0013) tablo adı eşgüncellendi.
- **CI-23 (Düşük, test-verisi — CI dördüncü koşuda yakalandı, DÜZELTİLDİ):** certmgmt PG testleri UUID kolonlara ("node-flow", "prov-1") sahte string koyuyordu → `invalid input syntax for type uuid`. Düzeltme: test verileri geçerli UUID sabitlerine çevrildi.
- **Kritik/Yüksek bulgu:** yok.

## Test komutları ve kısa çıktılar (taze koşu, commit da86058)

```
$ gofmt -l backend/control-plane agent                      → (boş) 0 dosya
$ cd backend/control-plane && go vet ./...                  → temiz
$ go test -count=1 ./...                                    → 15/15 paket ok
  (secrets, dns, acmex+fakeca, certmgmt, tasks[51-op registry],
   rbac, config, httpapi, identity, inventory, audit, certs, ...)
$ cd agent && go vet ./... && go test -count=1 ./...        → 12/12 paket ok
  (tlsops dahil; broker cert ailesi allowlist testleri)
$ GOOS=windows go build ./...                               → WIN-BUILD-OK
  (GOOS=windows vet — temiz)
$ bash scripts/secret-scan.sh                               → secret-scan: temiz ✓
$ cd web && npm run lint && tsc -p tsconfig.app.json        → temiz
$ npx vitest run                                            → 28/28 (6 dosya)
$ npm run build                                             → ✓ built
$ npx playwright test                                       → 23/23
  (smoke 3 + enrollment 2 + telemetry 2 + windows 4 + linux-distro 3
   + docker 3 + web-hosting 3 + dns-ssl 3)
$ rg "TODO|XXX|【|____" yeni paketler                        → 0 eşleşme
```

CI kanıtı (push sonrası doğrulanır): `agent-web` job — gerçek nginx + php-fpm lifecycle (Faz 08) **ve** `EXPECTED_TLS=1` gerçek TLS lifecycle (Faz 09); `e2e` job — gerçek control-plane + migration 0013 (pgx placeholder/CHECK kısıtları dahil).

## Regresyon sonucu

- Önceki faz testleri aynen koşar ve yeşildir: backend 15/15, agent 12/12, vitest 28/28 (Faz 07/08 suiteleri dahil), Playwright 23/23 (20 önceki + 3 yeni).
- Registry sayısı testi 40→51 olarak güncellendi (`service_test.go:214-219`); capability sayısı 32→35 (`agent_test.go:114-118`).
- Protokol değişikliği YOK (yalnız task payload şemaları additive; buf breaking kapısı etkilenmez).

## Güvenlik değerlendirmesi

- **Anahtar-node'da (ADR-0013):** TLS private key yalnız node'da doğar, CSR public materyaldir; control-plane'de özel anahtar saklanmaz — çift-eşleşme doğrulaması yanlış node'un sertifikasını yapısal olarak engeller.
- **Kimlik bilgisi gizliliği:** AES-256-GCM at-rest; anahtar env tabanlı, PostgreSQL'de yok; write-only sözleşme vitest+e2e ile doğrulanır; decrypt yalnız executor yolunda.
- **Kapı disiplini:** nginx reload yalnız `nginx -t` geçince; rollback + yeniden doğrulama; reload sinyali başarısız olsa bile disk durumu dürüst raporlanır (`reloaded=false` — yalan söylemez).
- **Rate limit dürüstlüğü:** 429 gizlenmez; sınıf + Retry-After kayda ve UI'ya taşınır; retry üst sınırı (MaxRetry) sınıflandırmayı hızlandırır.
- **RBAC/audit:** dns/cert izinleri yalnız-yeni seed (CI-04 dersi); sağlayıcı CRUD audit'li; kayıt değişiklikleri task izinden izlenebilir.
- Negatif senaryolar kapsandı: yetkisizlik (RequiredPerm eşlemeleri), bozuk payload (strict decode + unknown-field redleri), tekrar/replay (non_idempotent delete/renew/revoke retry-güvensiz), unsupported capability (bilinmeyen sağlayıcı türü, kapalı `--web-enable`, ACME yapılandırılmamış).

## Faz kabul kararı

**ACCEPTED** — 5/5 zorunlu kriter PASS; kritik/yüksek bulgu yok; NOT RUN kalemleri (gerçek ACME staging) dürüstçe belgelendi ve CI ile kapsanan gerçek-host TLS kanıtı mevcut.

## Sonraki faza geçiş

**READY** — tüm zorunlu kriterler PASS ve kritik/yüksek bulgu yok. Açık işler (staging ACME doğrulaması, anahtar rotasyonu, gerçek sağlayıcı adaptörleri, SSL listener site entegrasyonu) `docs/architecture/dns-ssl.md` §7'de sonraki işler olarak kayıtlıdır.
