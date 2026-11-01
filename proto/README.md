# proto/

Agent ↔ Control Plane protokol sözleşmeleri (Protocol Buffers).

## Yerleşim

- `servercontrol/agent/v1/agent.proto` — Enroll + Connect (bidi stream)
  sözleşmesi v1: zarflar `request_id`, `node_id`, `correlation_id`,
  `issued_at`, `expires_at`, `idempotency_key` taşır (api-and-protocol.md).
- `buf.yaml` — lint (STANDARD; stream zarf adlandırma istisnası gerekçeli) +
  breaking (FILE) kuralları.
- `buf.gen.yaml` — pinli yerel Go plugin'leriyle `../gen/proto` altına üretim.
- `../gen/proto/` — üretilen Go kodu (ayrı modül; backend ve agent
  `replace` ile bağlar). Üretim kodu elle DÜZENLENMEZ.

## Komutlar

```bash
make proto-tools   # pinli buf + protoc-gen-go + protoc-gen-go-grpc kurulumu
make proto         # buf lint + buf generate
make proto-check   # proto + git diff --exit-code (CI kırılma kapısı)
```

## Sürümleme kuralları

- Alan numaraları ASLA yeniden kullanılmaz; silinen alanlar `reserved` olur.
- Yeni mesaj/alan ekleme geriye uyumludur; `buf breaking` CI'da denetler.
- Bilinmeyen enum değerleri her iki tarafta güvenli işlenir.
- Protokol sürümü `Hello`/`EnrollRequest` içinde açık alan olarak taşınır;
  uyumsuz sürüm HATA döndürür (sessiz dönüşüm yok).
