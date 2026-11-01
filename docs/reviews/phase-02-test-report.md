# Faz 02 Öz-Denetim Test Raporu — Kimlik, RBAC, Node Envanteri, Görev Altyapısı

- Tarih: 2026-11-01
- Kapsam: `prompts/phase-start/phase-02.md` ve `prompts/phase-test/phase-02-test.md`
- Not: Bu rapor uygulayan tarafın (ZCode) öz-denetimidir; bağımsız test promptu ayrıca çalıştırılmalıdır.
- Ortam kısıtı: Sandbox'ta Docker/PostgreSQL yoktur; PostgreSQL entegrasyon testleri CI'da (PostgreSQL 18 servis konteyneri) çalışır, lokalde açık SKIP verir. Playwright E2E lokalde mock API ile, CI'da gerçek backend ile koşar.

## Kabul kriterleri

| # | Kriter | Sonuç | Kanıt |
|---|---|---|---|
| 1 | İlk admin bootstrap güvenli ve tek kullanımlık; varsayılan parola yok | **PASS** (lokal birim) / **PASS beklenen** (CI PG) | `internal/identity`: `TestBootstrapLifecycle`, `TestBootstrapRejectsBadTokenAndUsername`, `TestBootstrapRacePG` (8 yarışıcı → 1 admin); token yalnızca SHA-256 özeti saklanır; token üretimi yalnızca CLI'da (`control-plane bootstrap`), HTTP'de üretim ucu yok; parola argon2id, min 12 karakter |
| 2 | RBAC default-deny ve node/group scope | **PASS** | `internal/rbac`: `TestAuthorizeDefaultDeny`, node/group/global scope tablolu testler; `ValidatePermission` katalog dışı izinleri reddeder; her uçta `requirePermission` middleware'i; reddedilen istekler audit'e yazılır |
| 3 | Node kayıt modeli, durum, etiket, OS facts metadata ve capability tabloları | **PASS** (lokal birim) / **PASS beklenen** (CI PG) | Migration `0003_inventory`; `internal/inventory`: birim + `TestNodesPG`, `TestGroupsPG` (facts sürüm artışı, capability upsert, grup üyeliği replace) |
| 4 | Task/outbox tek DB transaction; worker lease + SKIP LOCKED | **PASS** (lokal birim) / **PASS beklenen** (CI PG) | Migration `0004_tasks_outbox`; `internal/tasks`: `CreateTaskWithOutbox` tek tx; `TestCreateSingleTransactionPG` (task+outbox aynı tx); `TestWorkerConcurrentClaimPG` (2 işçi yarışı → çift attempt yok); `TestLeaseRecoveryPG`, `TestExpiryPG` |
| 5 | Audit event actor/target/action/result/correlation ID içerir | **PASS** (lokal birim) / **PASS beklenen** (CI PG) | Migration `0005_audit` (append-only trigger); `internal/audit`: `TestAppendOnlyPG` (UPDATE/DELETE engellenir), `TestQueryPaginationPG` (keyset imleç), redaction testleri |

## Çalıştırılan komutlar ve sonuçlar (sandbox, 2026-11-01)

| Komut | Sonuç |
|---|---|
| `gofmt -l .` (backend) | temiz |
| `go vet ./...` | PASS |
| `go build ./...` | PASS |
| `go test ./...` | PASS (8 paket; PG testleri SKIP — `TEST_DATABASE_URL` yok) |
| `go test -race ./...` | PASS (aynı skip durumu) |
| `cd web && npm run lint` | PASS |
| `cd web && npm run typecheck` | PASS (strict TS) |
| `cd web && npm test` | PASS (8 test, 2 dosya) |
| `cd web && npm run build` | PASS |
| `cd web && npm run e2e` | **PASS (5/5 Playwright testi)** — mock API + vite preview; ekran görüntüleri `docs/reviews/screenshots/phase-02/` altında üretildi |
| `bash scripts/secret-scan.sh` | temiz |

## Negatif güvenlik senaryoları (otomatik testle kanıtlananlar)

- Yanlış parola / bilinmeyen kullanıcı: tek uniform hata (`invalid_credentials`), kullanıcı adı sızıntısı yok.
- Rate limit: 8 başarısız deneme sonrası `rate_limited` (429); başarılı giriş sayacı sıfırlar.
- CSRF: mutating isteklerde `X-CSRF-Token` zorunlu; yanlış token 403. (Mock API aynı kuralı uygular; E2E bu yolu gerçekten kullanır.)
- Tek kullanımlık token: tüketim sonrası replay reddi; kurulum sonrası yeni token üretimi `409 bootstrap_done`.
- Yetkisiz erişim: oturum yok → 401; izin yok → 403 `forbidden` + audit `auth.denied`.
- Idempotency: aynı key farklı operasyon → `409 idempotency_conflict`; aynı key aynı operasyon → mevcut task döner (`200`).
- Desteklenmeyen operasyon → `400 unsupported_operation` (registry allowlist).
- Bozuk payload / bilinmeyen alan → `400 invalid_request` (`DisallowUnknownFields` + registry validator).
- Audit tablosunda UPDATE/DELETE → tetikleyici hatası (append-only).
- Parola hash'i bozulan PHC dizisi → doğrulama reddi (panic yok).

## Regresyon

- Faz 01 testleri (`internal/config`, `internal/db/migrate`, `internal/httpapi` health/readyz/version) değişmeden PASS.
- Faz 01 web testleri güncellendi (auth gate + mock'lu API istemcisi) ve PASS.

## Bulgular

| ID | Önem | Bulgu | Durum |
|---|---|---|---|
| CI-1 | Yüksek | İlk CI koşusunda paralel test paketleri migration advisory lock'unda yarıştı: `migrate` fail-fast yaptığı için bazı paketler "başka bir migration çalışıyor" aldı ve migration uygulanmadan kaldı (0 applied). | **Düzeltildi:** `Runner.lock` artık 30s'ye kadar bekler, sonra yüksek sesle hata verir; lock testi kısa context ile iptal yolunu da doğrular. |
| CI-2 | Orta | `PGRecorder.Record` merkezî redaction yapmıyordu; test, ham metadata'nın DB'ye yazıldığını yakaladı (kırmızı şerit: çağıranlar redact ediyor, recorder etmiyordu). | **Düzeltildi:** Record artık girişte `audit.Redact` uygular (defense in depth). |
| CI-3 | Yüksek | CI e2e job'ı `playwright install --with-deps chromium` adımında 25+ dk takıldı (apt tabanlı kurulum). | **Düzeltildi:** CI, runner imajındaki system Chrome'u kullanır (`E2E_BROWSER_CHANNEL=chrome`, indirme/apt yok); job'lara `timeout-minutes` eklendi (gelecekteki takılmalar hızlı başarısız olur). |
| CI-4 | Yüksek | Test paketleri tek test DB'sini paylaştığı için migrate testinin sahte çiftleri gerçek migration'larla çakıştı ("0 applied, want 2") ve DownOne "bilinmeyen sürüm" hatası verdi. | **Düzeltildi:** `internal/testutil.UniqueDB` ile her test süreci kendi izole disposable veritabanını kullanır (test sonunda DROP WITH FORCE); paketler arası şema paylaşımı ortadan kalktı. |
| N-1 | Not | Sandbox'ta PostgreSQL/Docker yok: PG entegrasyon testleri lokalde NOT RUN (SKIP), CI'da koşar. Kullanıcıya `TEST_DATABASE_URL` ile lokal doğrulama komutları verildi. | Açık (ortam kısıtı) |
| N-2 | Not | OpenAPI artefaktı henüz üretilmedi; `/api/v1` yüzeyi Faz 02'de büyüdü. Sonraki faz başında OpenAPI + breaking-change CI eklenmeli. | Açık (planlı) |
| N-3 | Not | Login rate limit tek örnek içi bellektir; HA geçişinde paylaşımlı depoya taşınmalı (ADR-0006/0007 sonucu). | Kabul edilmiş tasarım |
| N-4 | Düşük | Task işleyici Faz 02'de yalnızca control-plane içi operasyonları yürütür (noop.echo, inventory.node.tag.set); agent dağıtımı Faz 03 kapsamı. | Plan gereği |

## Ekran görüntüleri (Playwright E2E)

`docs/reviews/screenshots/phase-02/`: 01-login, 02-overview, 03-nodes-empty, 04-node-created, 05-tasks-empty, 06-task-succeeded, 07-audit, 08-locale-en.

## Faz kabul kararı (öz-denetim)

- Kriterler 1-5: lokal birim seviyede PASS; PostgreSQL entegrasyonu CI koşusuna bağlı.
- Kritik/yüksek bulgu yok.
- Karar: **ACCEPTED (koşullu — CI yeşil onayı beklenir)**, sonraki faza geçiş: **READY (CI doğrulamasından sonra)**
