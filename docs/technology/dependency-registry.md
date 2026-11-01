# Dependency Registry (Faz 01)

`docs/technology/dependency-policy.md` gereği her doğrudan bağımlılığın kaydı:
sürüm, kaynak, SPDX lisans, amaç. Sürümler kilitlidir (go.mod / package-lock.json / image digest).
Lisans kimlikleri paket manifestosundan alınmıştır; yayın öncesi (Faz 15 paketleme)
SBOM (SPDX/CycloneDX) üretimi ve dosya düzeyi lisans denetimi yapılacaktır.

Doğrulama tarihi: **2026-11-01** (sürümler güncel stable çizgiler; kit tabanı `official-sources.md` ile uyumlu). Faz 02 eklentileri aynı gün doğrulandı.

## Backend (Go 1.27.3) — module: backend/control-plane

| Bağımlılık | Sürüm | Kaynak | Lisans | Amaç |
|---|---|---|---|---|
| `github.com/go-chi/chi/v5` | v5.5.0 | github.com/go-chi/chi | MIT | HTTP router (teknoloji tabanı kararı) |
| `golang.org/x/crypto` | v0.58.0 | go.googlesource.com/crypto | BSD-3-Clause | argon2id parola özeti (ADR-0007) |
| `golang.org/x/sys` | v0.48.0 | go.googlesource.com/sys | BSD-3-Clause | Faz 04: broker SO_PEERCRED (unix.GetsockoptUcred); Faz 05: Windows yüzeyi (svc/mgr SCM, registry, LazyProc Win32/iphlpapi/advapi32); Faz 06: yeni doğrudan bağımlılık YOK (pkgops/fwops stdlib exec + x/sys olmayan saf argv/parse) — doğrudan bağımlılık (önceden indirect'ti) |
| `google.golang.org/grpc` | v1.85.0 | go.googlesource.com/grpc | Apache-2.0 | Agent transport (ADR-0008) |
| `google.golang.org/protobuf` | v1.36.12 | go.googlesource.com/protobuf | BSD-3-Clause | Protobuf runtime |
| `gopkg.in/yaml.v3` | v3.0.1 | github.com/go-yaml/yaml (gopkg.in yönlendirmesi) | MIT | Faz 07: compose dosyası ayrıştırma (agent-local saf analiz — composeops); girdi ≤64 KiB + services ≤64 + findings ≤100 sınırlı; yaml.v3 alias/billion-laughs koruması + bakımcı durumu: bakım modunda ama de-facto endüstri standardı, son güvenlik danışmanlığı işlendi (GO-2022-0603 düzeltmesi v3.0.1 içinde) |
| `gen/proto` (üretim kodu) | pinli buf v1.75.0 + protoc-gen-go v1.36.12 + protoc-gen-go-grpc v1.6.1 | buf.build / go.googlesource.com | Apache-2.0 / BSD-3-Clause | `make proto-tools` ile Go proxy checksum'lu kurulum; üretim CI'da diff kapısıyla doğrulanır |
| `github.com/jackc/pgx/v5` | v5.12.0 | github.com/jackc/pgx | MIT | PostgreSQL erişimi (pgxpool) |
| `golang.org/x/text` | v0.41.0 | go.googlesource.com/text | BSD-3-Clause | pgx geçiş bağımlılığı |

Standart kütüphane tercih edilir (slog, net/http, crypto/rand, embed); başka bağımlılık eklenmedi.

## Frontend (Node 26.13.0 LTS, npm) — web/

| Bağımlılık | Sürüm | Lisans | Amaç |
|---|---|---|---|
| `react` / `react-dom` | 19.3.1 | MIT | UI çatısı |
| `vite` | 8.3.5 | MIT | Build aracı + dev sunucu |
| `@vitejs/plugin-react` | 6.1.2 | MIT | React HMR/transform |
| `typescript` | 5.9.3 | Apache-2.0 | Strict tip kontrolü. **Not:** TS 7.0.2 yayında; typescript-eslint 8.72.1 `<6.1.0` istediği için 5.9.3 pinlendi. TS 7 desteği çıkınca değerlendirilir. |
| `tailwindcss` + `@tailwindcss/vite` | 4.3.3 | MIT | Tasarım token'ları + utility CSS |
| `vitest` | 5.0.5 | MIT | Test çalıştırıcı |
| `jsdom` | 30.1.3 | MIT | Test DOM ortamı |
| `@testing-library/react` | 16.3.3 | MIT | Bileşen testleri |
| `@testing-library/jest-dom` | 7.0.1 | MIT | Matcher eklentisi |
| `@testing-library/user-event` | 14.6.8 | MIT | Etkileşim testleri |
| `eslint` | 10.14.0 | MIT | Lint |
| `@eslint/js` | 10.0.1 | MIT | ESLint önerilen kurallar |
| `typescript-eslint` | 8.72.1 | MIT | TS lint kuralları |
| `eslint-plugin-react-hooks` | 7.1.1 | MIT | Hooks kuralları |
| `eslint-plugin-react-refresh` | 0.5.7 | MIT | HMR güvenlik kuralı |
| `globals` | 17.13.0 | MIT | Ortam global tanımları |
| `@types/react` / `@types/react-dom` | 19.3.0 | MIT | React tipleri |
| `@types/node` | 26.6.6 | MIT | Node tipleri (config dosyaları) |
| `@playwright/test` | 1.57.0 | playwright.dev (github.com/microsoft/playwright) | Apache-2.0 | E2E smoke testleri + ekran görüntüleri (dev-only) |

## Altyapı imajları

| İmaj | Pin | Amaç |
|---|---|---|
| `postgres:18-alpine` | sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5 (2026-11-01) | Geliştirme DB + CI servis konteyneri |

## Bilinen riskler / notlar

- `npm audit` (2026-11-01): 12 × **moderate**, tamamı eslint toolchain'i (dev-only, üretime çıkmaz). Kaynak: `minimatch` ve `@eslint-community/eslint-utils` danışmanlıkları. Her faz başında yeniden taranır (dependency-policy §7).
- `@vitejs/plugin-react` 6.x oxc-transform tabanlıdır; rolldown/babel geçiş peer'ları opsiyoneldir ve kullanılmaz.
- Faz 08 (2026-11-02): **yeni harici bağımlılık YOK** — web hosting adaptörü
  (`agent/internal/webops`) tamamen stdlib'tir (useradd/nginx/systemctl
  fixed argv exec + /proc/net.tcp parse + atomic rename). nginx/php-fpm
  çalışma zamanı bağımlılığı node tarafında opsöneldir (CI'da `apt-get`
  nginx + php8.3-fpm; sürüm kilitleri node'un kendi paket yöneticisindedir).
- Otomatik SBOM üretimi Faz 15 paketleme fazında eklenir; bu dosya her fazda elle güncel tutulur.
