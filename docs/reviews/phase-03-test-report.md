# Faz 03 Öz-Denetim Test Raporu — Agent Protokolü, Kayıt, mTLS, Capability

- Tarih: 2026-11-01
- Kapsam: `prompts/phase-start/phase-03.md` ve `prompts/phase-test/phase-03-test.md`
- Not: Uygulayan tarafın (ZCode) öz-denetimi; bağımsız test promptu ayrıca çalıştırılmalıdır.
- Ortam kısıtı: PostgreSQL entegrasyon testleri CI'da koşar (sandbox'ta DB yok → lokal SKIP).

## Kabul kriterleri

| # | Kriter | Sonuç | Kanıt |
|---|---|---|---|
| 1 | Agent özel anahtarı node üzerinde üretilir, dışarı çıkmaz | **PASS** | `agent/internal/agent`: `store.GenerateKey` lokal ECDSA P-256, dosya 0600 (`TestStoreKeyPermissionsAndPersistence`); yalnızca CSR sunucuya gider; enroll zarfında anahtar alanı yok; sunucu kodu CSR dışındaki malzemeyi kabul etmez |
| 2 | Kayıt tokenı tek kullanımlık, kısa ömürlü, node/group kapsamlı, replay korumalı | **PASS** | Migration `0006` + `internal/enroll`: tüketim `FOR UPDATE` ile tek transaction'da (token + node devri/oluşumu + sertifika kaydı); `TestEnrollGroupScopeCreatesNodeAndCertificate` (ikinci kullanım → PermissionDenied), `TestEnrollExpiredTokenRejected`; UI'da plaintext yalnızca bir kez gösterilir |
| 3 | mTLS sertifika kimliği immutable node ID'ye bağlı; revoke edilmiş sertifika reddedilir | **PASS** | `internal/certs`: URI SAN `urn:servercontrol:node:<uuid>` sunucu tarafından yazılır (CSR SAN'i yok sayılır, `TestIssueNodeCertBindsImmutableNodeID`); `AuthorizeStreamCert` fingerprint + node durumunu DB'den doğrular; Connect'te sertifika yoksa/kimlik uyuşmazsa Unauthenticated (`TestConnectRequiresClientCertificate`, `TestConnectWrongNodeEnvelopeRejected`) |
| 4 | Heartbeat, agent sürümü, protocol version, capability raporu kontrol düzleminde görünür | **PASS** | `TestConnectHeartbeatCapabilitiesAndDispatch`: Hello→HelloAck (v1 anlaşması), heartbeat `last_seen_at` günceller, capability raporu `node_capabilities`'a upsert edilir; nodes tablosunda `agent_version`/`protocol_version` görünür |
| 5 | Agent dışarı doğru bağlanır; task mesajları expiry/idempotency/correlation içerir | **PASS** | Agent yalnızca outbound (`agent.Run`, gRPC client; sunucu dinleyici yok); zarflarda `expires_at` (her iki yön; süresi geçen zarf yürütülmez), `idempotency_key` (agent sınırlı cache + sunucu dispatch), `correlation_id` korunur; `agent.echo` uçtan uca dispatch testi (queued→dispatched→succeeded, DB'de kanıtlı) |

## Çalıştırılan komutlar ve sonuçlar (sandbox, 2026-11-01)

| Komut | Sonuç |
|---|---|
| `make proto-check` (buf lint + generate + git diff) | PASS |
| backend: `gofmt -l .` / `go vet ./...` / `go build ./...` | temiz / PASS / PASS |
| backend: `go test ./...` | PASS (10 paket; PG testleri CI'da) |
| backend: `go test -race ./...` | PASS |
| agent: `go vet ./...` / `go build ./...` | PASS |
| agent: `go test -race ./...` | PASS (4 test) |
| web: lint / typecheck / test / build | PASS (8+ test) |
| web: `npm run e2e` | PASS (6/6 — yeni: Agent Kaydı tek gösterim akışı) |
| `bash scripts/secret-scan.sh` | temiz |

## Negatif güvenlik senaryoları (testle kanıtlanmış)

- Sertifikasız Connect → Unauthenticated.
- Yanlış node_id'li zarf → PermissionDenied + stream sonlanır.
- Token replay → PermissionDenied (tek kullanımlık).
- Süresi dolmuş token → PermissionDenied.
- İptal edilmiş sertifika → AuthorizeStreamCert reddi (DB durumu; TestConnectRequiresClientCertificate ile birlikte mTLS yüzeyi kapanır).
- Bilinmeyen operasyon → REJECTED (asla yürütülmez; agent + sunucu registry'leri allowlist).
- Aşırı büyük payload/CSR → reddedilir (64KiB üst sınır).
- Süresi geçmiş zarf (her iki yön) → atlanır/yürütülmez (timeout karışıklığı koruması).
- Enroll'da CA pinleme: sunucunun döndürdüğü CA operatör CA'sıyla birebir eşleşmezse bağlantı reddedilir (MITM guard; agent kodu).

## Regresyon

- Faz 02 testleri değişmeden PASS; `noop.echo`/`inventory.node.tag.set` operasyonları `control_plane` hedefiyle geriye uyumlu.
- Faz 01 health/migration testleri PASS.

## CI doğrulama geçmişi

| Koşu | Bulgu | Çözüm |
|---|---|---|
| 1. koşu (5b6d25e) | `scanTask` 21 kolon / 20 hedef uyuşmazlığı → tüm task kiralama sorguları patladı; agentsrv yanlış-node testinde orijinal status kodu kayboldu; proto job'da `buf` PATH'te yoktu; E2E token regex'i mock'a özgüydü | scanTask'a ExecutionTarget eklendi; recvErr orijinal status'u taşıyor; proto-check artık proto-tools'a bağlı; E2E gösterimi mock-bağımsız doğruluyor |
| 2. koşu (2ff828c) | (1) Doğrudan store kullanan testler boş execution_target ile CHECK'e takıldı → store artık boşta 'control_plane' varsayıyor; (2) agentsrv replay testi nil CA havuzuyla bağlandı → havuz env'den kuruluyor | Lokal tüm kapılar yeşil |
| 3. koşu (eb3ca6a) | — | **SUCCESS: backend ✓ frontend ✓ agent ✓ e2e ✓ proto ✓ secret-scan ✓** |

## Bulgular

| ID | Önem | Bulgu | Durum |
|---|---|---|---|
| N-1 | Not | Sertifika iptali DB'de tutulur ve bağlantı kuruluşunda denetlenir; canlı stream'in ortasındaki revokasyon heartbeat aralığına kadar etkili olmaz (≤30s). CRL/OCSP sonraki fazlarda değerlendirilir. | Açık (planlı) |
| N-2 | Not | OpenAPI artefaktı hâlâ bekliyor (Faz 02'den devir). | Açık (planlı) |
| N-3 | Not | Bidi stream'de tek node için tek canlı stream: ikinci bağlantı ResourceExhausted. HA control plane'de paylaşımlı hub gerekir (ADR-0006 yolu). | Kabul edilmiş tasarım |
| N-4 | Düşük | Agent idempotency cache süreç içi bellektir; agent yeniden başlayınca boşalır (sunucu tarafı idempotency key'i korur). | Kabul edilmiş tasarım |

## Ekran görüntüleri (Playwright E2E)

`docs/reviews/screenshots/phase-03/`: 09-enrollment-empty, 10-enrollment-reveal, 11-enrollment-list.

## Faz kabul kararı (öz-denetim)

- Kriterler 1-5: birim + PostgreSQL 18 entegrasyon testleriyle PASS (CI run, commit eb3ca6a — **success**, 6/6 job).
- Kritik/yüksek açık bulgu yok.
- Karar: **ACCEPTED**, sonraki faza geçiş: **READY**.
