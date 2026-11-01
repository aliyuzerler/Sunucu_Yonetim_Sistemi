# proto/

Agent ↔ Control Plane protokol sözleşmeleri (Protocol Buffers) — **Faz 03'te**
oluşturulur.

Bu klasör monorepo yerleşimini rezerve eder. Faz 03'te burada:

- `servercontrol/agent/v1/*.proto` — sürümlü mesajlar, `request_id`, `node_id`,
  `issued_at`, `expires_at`, `idempotency_key`, correlation ID alanları
- buf veya protoc tabanlı üretim ve breaking-change kontrolü (CI adımı)
- Üretim kodu `go generate` ile sabitlenmiş sürümden üretilir; elle düzenlenmez

Kurallar: `docs/architecture/api-and-protocol.md` (protokol sürümleme, alan
numarası tekrar kullanılmaz, bilinmeyen enum güvenli işlenir).
