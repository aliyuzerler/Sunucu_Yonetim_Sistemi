# Server Control — Sunucu Yönetim Sistemi

Self-hosted, genişletilebilir altyapı kontrol düzlemi: Linux/Windows sunucuları, Docker iş yükleri, web hosting, DNS/SSL, veritabanları, e-posta, KVM sanal makineleri ve bulut sağlayıcılarını tek bir cPanel benzeri arayüzden yöneten modüler çok sunuculu platform. Kendi kod tabanımızla geliştirilir; cPanel'in bilgi mimarisinden ilham alınır, marka/varlık kopyalanmaz.

## Mevcut durum

| | |
|---|---|
| Tamamlanan fazlar | **Faz 00–15 (16/16)** — her faz bağımsız test raporuyla kabul edildi (`docs/reviews/phase-NN-test-report.md`) |
| Güncel sürüm | **v0.16.0-rc1** (tag `v0.16.0-rc1`, commit `09a38b1`) — Faz 15: eklenti sistemi, imzalı güncelleme, paketleme, üretim sertleştirmesi |
| CI | **19/19 job YEŞİL** (run 41537489729; tek workflow `.github/workflows/ci.yml`: kalite kapıları, çok-distro agent matrisi, paketleme, sürüm denetimi) |
| Doğrulama | backend 20/20 paket `-race` (gerçek PostgreSQL) · agent süit `-race` + `GOOS=windows` vet · vitest 54/54 · Playwright 47/47 · secret-scan temiz · govulncheck + npm audit · CycloneDX SBOM |
| Sıradaki adım | Sürüm kararları: lisans seçimi + GA kapatıcıları — `docs/release/RELEASE-v0.16.0-rc1.md` |

Faz durumu, kanıtlar ve riskler için: [`docs/project-status.md`](docs/project-status.md) · Sürüm geçmişi: [`CHANGELOG.md`](CHANGELOG.md).

## Depo yapısı

```
agents.md            ZCode için ana çalışma talimatı (her oturumda okunur)
docs/                Ürün, mimari, teknoloji, güvenlik, UI/UX, operasyon, ADR, faz kapsamları
docs/reviews/        Faz inceleme/test raporları + ekran görüntüleri (screenshots/phase-00..15)
docs/decisions/      ADR kayıtları (0001–0020) + süreç/dizin
docs/phases/         16 fazın kapsam dokümanları + bağımlılık tablosu
docs/release/        Sürüm notları (PRR + GA kapatıcıları)
docs/operations/     Geliştirme ortamı, dağıtım modeli, yedekleme, gözlemlenebilirlik
prompts/             Faz başlatma (phase-start), bağımsız test (phase-test), iş akışı promptları
rules/               Zorunlu kural setleri (agent, architecture, backend, frontend, security)
skills/              ZCode uzmanlık modülleri (backend, frontend, linux-agent, windows-agent, docker, …)
backend/control-plane/   Go modüler monolit: REST API + gRPC (mTLS) agent sunucusu + CLI'lar
agent/               Node Agent — `server-agent` (yetkisiz) + `server-privd` (ayrıcalıklı broker); Linux + Windows
proto/ + gen/proto/  Protobuf sözleşmeleri + üretilen Go kodu (buf lint/breaking CI kapısı)
web/                 React 19 + strict TypeScript + Tailwind 4 arayüzü (vitest + Playwright)
packaging/           systemd unit şablonları (agent kurulumu)
scripts/             Paketleme (package-linux.sh, release-package.sh), SBOM (sbom.py), secret-scan
deploy/              PostgreSQL 18 compose + .env.example + agent kurulum rehberleri + INSTALL-UPDATE.md
.github/workflows/   CI: kalite kapıları, çok-distro agent testleri, release-package, release-audit, secret-scan
```

## Teknik kararlar (özet)

- Backend ve Agent: **Go** (1.27.3) · Frontend: **React 19 + TypeScript (strict)** + Vite
- Veritabanı: **PostgreSQL 18** — kalıcı task/outbox PostgreSQL üzerinde; broker/cache (NATS/Valkey) yalnızca ölçülmüş ihtiyaçta (ADR-0004)
- Agent–Control Plane: **outbound mTLS gRPC**; agent özel anahtarı node'dan çıkmaz (ADR-0002, ADR-0008)
- **Serbest metin shell yok:** tipli, izin listeli operasyonlar; ayrıcalıklı işler lokal broker'da (ADR-0003)
- Control Plane: Linux-first modüler monolit (ADR-0001); çoklu-node stratejisi ADR-0006, ölçekleme kanıtı Faz 14
- Doğrulanmış dağıtımlar: **Ubuntu Server 24.04 LTS, Debian 13** (agent CI matrisi), **Rocky/AlmaLinux 9** (Faz 06 + `agent-rhel`); Windows Server Agent Faz 05 (ADR-0009)
- Sürüm detayları ve lisanslar: [`docs/technology/technology-stack.md`](docs/technology/technology-stack.md)

## Faz işleyişi (nasıl çalışılır)

1. Her faz için iki ayrı prompt vardır: `prompts/phase-start/phase-NN.md` (uygulama) ve `prompts/phase-test/phase-NN-test.md` (bağımsız doğrulama).
2. **Test promptunu geliştirme promptuyla aynı anda çalıştırma.** Testi, uygulamayı yazan oturumdan bağımsız ayrı bir görev/oturum olarak çalıştır.
3. Faz kabulü: tüm kabul kriterleri PASS + kritik/yüksek bulgu yok. Aksi halde `prompts/workflow/fix-failed-tests.md` ile düzeltme yapılır.
4. Faz geçişleri ve teknik bağımlılıklar: [`docs/phases/README.md`](docs/phases/README.md) — bağımlılık tablosuna bakın.
5. Her faz sonunda `docs/project-status.md`, `CHANGELOG.md` ve ilgili ADR'ler güncellenir; rapor GitHub'a push'lanır.
6. Çelişki durumunda öncelik sırası `agents.md` §1'dedir; çelişki sessizce çözülmez, ADR önerisi yazılır.

16 fazın tamamı bu süreçle tamamlandı; sonraki değişiklikler için de aynı disiplin (plan → küçük test edilebilir adımlar → kalite kapıları → bağımsız doğrulama → rapor) geçerlidir (`prompts/workflow/`).

| Faz | Konu |
|---|---|
| 00 | Mimari ve proje sözleşmesini kesinleştirme ✅ |
| 01 | Monorepo, geliştirme ortamı, temel UI/API ✅ |
| 02 | Kimlik, RBAC, sunucu envanteri, iş/olay altyapısı ✅ |
| 03 | Agent protokolü, kayıt, mTLS, heartbeat ve capability ✅ |
| 04 | Linux Agent, sistem metrikleri, servis ve temel OS yönetimi ✅ |
| 05 | Windows Server Agent ve Windows adaptörleri ✅ |
| 06 | Rocky/AlmaLinux ve ek Linux dağıtımı adaptörleri ✅ |
| 07 | Docker Engine ve Compose yönetimi ✅ |
| 08 | Web hosting, Nginx, PHP-FPM ve uygulama dağıtımı ✅ |
| 09 | Domain, DNS, reverse proxy ve SSL/ACME ✅ |
| 10 | Veritabanları ve güvenli dosya yöneticisi ✅ |
| 11 | E-posta servisi yönetimi ✅ |
| 12 | KVM/QEMU/libvirt sanallaştırma ve VPS yönetimi ✅ |
| 13 | İzleme, log, alarm ve güvenlik merkezi ✅ |
| 14 | Yedekleme, geri yükleme, bulut ve çoklu sunucu ölçekleme ✅ |
| 15 | Eklenti sistemi, güncelleme, paketleme ve üretim sertleştirmesi ✅ |

## Bileşen durumu

| Bileşen | Durum |
|---|---|
| Control Plane (Go REST + gRPC mTLS) | Kimlik/RBAC/oturum + ilk-admin bootstrap, node envanteri, görev motoru (outbox, idempotency), telemetri, redaksiyonlu audit, DNS/SSL/ACME, web hosting, DB/dosya yöneticisi, e-posta, KVM VM, yedekleme/bulut, izleme/alarm/güvenlik merkezi, eklenti sözleşmesi + imzalı güncelleme — 111 tipli operasyon, 92 capability, migration `0001–0019` |
| Agent (`server-agent` + `server-privd`) | Linux (Ubuntu/Debian/Rocky/Alma) + Windows Server; tüm operasyon aileleri broker'da **varsayılan kapalı opt-in** bayrağıyla açılır; kapalıyken tipli `unsupported_operation` (fail-closed) |
| Protokol | `proto/servercontrol/agent/v1` — Enroll + Connect (bidi stream); buf lint/breaking CI kapısı |
| Paketleme ve sürüm | deb/rpm/zip + SHA256SUMS + SCP-SIG-V1 ed25519 imzaları (`sigtool`); CycloneDX SBOM; node'da stage→health→atomik swap→rollback — `deploy/INSTALL-UPDATE.md` |

## Hızlı başlangıç (geliştirme)

Gereksinimler: **Go 1.27.3**, **Node.js 26.13.0 LTS**, **Docker Engine + Compose**.

```bash
cp deploy/.env.example deploy/.env          # yerel değerleri düzenleyin (git'e girmez)
make compose-up                             # PostgreSQL 18 (localhost'a bağlı)
export CP_DATABASE_URL="postgres://serverctl:...@localhost:5432/server_control?sslmode=disable"
make migrate-up                             # explicit migration
make backend-test && make backend-race      # Go testler
make agent-build && make agent-test         # agent süitleri
go run ./cmd/control-plane bootstrap        # tek kullanımlık ilk yönetici tokenı (backend/control-plane altında)
go run ./cmd/control-plane                  # API: http://127.0.0.1:8080 (backend/control-plane altında)
# Token ile: curl -X POST http://127.0.0.1:8080/api/v1/bootstrap/admin \
#   -H 'Content-Type: application/json' \
#   -d '{"token":"<TOKEN>","username":"<ad>","password":"<güçlü-parola>"}'
cd web && npm ci && npm run dev             # UI: http://localhost:5173 (giriş yapın)
make web-e2e                                # Playwright smoke + ekran görüntüleri (mock API)
```

Oturum açma zorunludur; ilk yönetici hesabı yukarıdaki bootstrap akışıyla oluşturulur (varsayılan parola yoktur — ADR-0007).

CLI yardımcıları (`backend/control-plane` altında): `control-plane enroll-token` (node kayıt tokenı), `control-plane ca`, `control-plane verify-update`, `cmd/sigtool` (ed25519 imza anahtarı — sürüm imzaları).

Üretim kurulumu, yükseltme ve geri alma: [`deploy/INSTALL-UPDATE.md`](deploy/INSTALL-UPDATE.md) (imzalı deb/rpm/zip, staging → atomik swap → rollback). Node agent kurulumu: [`deploy/agent/README.md`](deploy/agent/README.md) ve [`deploy/agent-windows/README.md`](deploy/agent-windows/README.md).

Tüm komutlar: `make help` · Detay: [`docs/operations/development-environment.md`](docs/operations/development-environment.md) ve [`deploy/README.md`](deploy/README.md).

- Zorunlu kalite kapıları: `gofmt` (boş çıktı), `go vet`, `go test -race`; frontend `lint`, `typecheck`, `test`, `build` — CI'da zorlanır.
- `.env.example` yalnızca placeholder içerir; gerçek `.env` gitignored. Repo asla secret/gerçek sunucu verisi içermez.

## Lisans notu

Bu depo üçüncü taraf kodu içermez; bağımlılıklar `docs/technology/dependency-registry.md` altında lisanslarıyla listelenir. Ürün lisansı henüz seçilmemiştir; dağıtımdan önce karar verilmelidir. Bu doküman hukuki görüş değildir.
