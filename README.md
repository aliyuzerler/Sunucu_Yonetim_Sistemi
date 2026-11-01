# Server Control — Sunucu Yönetim Sistemi

Self-hosted, genişletilebilir altyapı kontrol düzlemi: Linux/Windows sunucuları, Docker iş yükleri, web hosting, DNS/SSL, veritabanları, e-posta, KVM sanal makineleri ve bulut sağlayıcılarını tek bir cPanel benzeri arayüzden yöneten modüler çok sunuculu platform. Kendi kod tabanımızla geliştirilir; cPanel'in bilgi mimarisinden ilham alınır, marka/varlık kopyalanmaz.

## Mevcut durum

| | |
|---|---|
| Tamamlanan faz | **Faz 00** — Mimari ve proje sözleşmesi (rapor: `docs/reviews/phase-00-architecture-review.md`) |
| Bekleyen onay | Bağımsız `phase-00-test` çalıştırması + sahip onayı |
| Sıradaki faz | **Faz 01** — Monorepo, geliştirme ortamı, temel UI/API |
| Kaynak kod | Henüz yok (Faz 01'de `backend/control-plane`, `agent`, `proto`, `web`, `deploy` iskeletleri kurulacak) |

Faz durumu, kanıtlar ve riskler için: [`docs/project-status.md`](docs/project-status.md) · Sürüm geçmişi: [`CHANGELOG.md`](CHANGELOG.md).

## Depo yapısı

```
agents.md            ZCode için ana çalışma talimatı (her oturumda okunur)
docs/                Ürün, mimari, teknoloji, güvenlik, UI/UX, operasyon, ADR, faz kapsamları
docs/reviews/        Faz inceleme ve test raporları
docs/decisions/      ADR kayıtları (0001–0006) + süreç/dizin
docs/phases/         16 fazın kapsam dokümanları + bağımlılık tablosu
prompts/             Faz başlatma (phase-start), bağımsız test (phase-test), iş akışı promptları
rules/               Zorunlu kural setleri (agent, architecture, backend, frontend, security)
skills/              ZCode uzmanlık modülleri (backend, frontend, linux-agent, docker, …)
backend/, agent/,    Faz 01'de kurulacak monorepo klasörleri
proto/, web/, deploy/
```

## Teknik kararlar (özet)

- Backend ve Agent: **Go** (1.27.x) · Frontend: **React 19 + TypeScript (strict)** + Vite
- Veritabanı: **PostgreSQL 18** — kalıcı task/outbox PostgreSQL üzerinde; broker/cache (NATS/Valkey) yalnızca ölçülmüş ihtiyaçta (ADR-0004)
- Agent–Control Plane: **outbound mTLS gRPC**; agent özel anahtarı node'dan çıkmaz (ADR-0002)
- **Serbest metin shell yok:** tipli, izin listeli operasyonlar; ayrıcalıklı işler lokal broker'da (ADR-0003)
- Control Plane: Linux-first modüler monolit (ADR-0001); çoklu-node stratejisi ADR-0006
- İlk doğrulama hedefleri: **Ubuntu Server 24.04 LTS ve Debian 13**; Windows Agent Faz 05 (ADR-0005)
- Sürüm detayları ve lisanslar: [`docs/technology/technology-stack.md`](docs/technology/technology-stack.md)

## Faz işleyişi (nasıl çalışılır)

1. Her faz için iki ayrı prompt vardır: `prompts/phase-start/phase-NN.md` (uygulama) ve `prompts/phase-test/phase-NN-test.md` (bağımsız doğrulama).
2. **Test promptunu geliştirme promptuyla aynı anda çalıştırma.** Testi, uygulamayı yazan oturumdan bağımsız ayrı bir görev/oturum olarak çalıştır.
3. Faz kabulü: tüm kabul kriterleri PASS + kritik/yüksek bulgu yok. Aksi halde `prompts/workflow/fix-failed-tests.md` ile düzeltme yapılır.
4. Faz geçişleri ve teknik bağımlılıklar: [`docs/phases/README.md`](docs/phases/README.md) — bağımlılık tablosuna bakın.
5. Her faz sonunda `docs/project-status.md`, `CHANGELOG.md` ve ilgili ADR'ler güncellenir; rapor GitHub'a push'lanır.
6. Çelişki durumunda öncelik sırası `agents.md` §1'dedir; çelişki sessizce çözülmez, ADR önerisi yazılır.

| Faz | Konu |
|---|---|
| 00 | Mimari ve proje sözleşmesini kesinleştirme ✅ |
| 01 | Monorepo, geliştirme ortamı, temel UI/API |
| 02 | Kimlik, RBAC, sunucu envanteri, iş/olay altyapısı |
| 03 | Agent protokolü, kayıt, mTLS, heartbeat ve capability |
| 04 | Linux Agent, sistem metrikleri, servis ve temel OS yönetimi |
| 05 | Windows Server Agent ve Windows adaptörleri |
| 06 | Rocky/AlmaLinux ve ek Linux dağıtımı adaptörleri |
| 07 | Docker Engine ve Compose yönetimi |
| 08 | Web hosting, Nginx, PHP-FPM ve uygulama dağıtımı |
| 09 | Domain, DNS, reverse proxy ve SSL/ACME |
| 10 | Veritabanları ve güvenli dosya yöneticisi |
| 11 | E-posta servisi yönetimi |
| 12 | KVM/QEMU/libvirt sanallaştırma ve VPS yönetimi |
| 13 | İzleme, log, alarm ve güvenlik merkezi |
| 14 | Yedekleme, geri yükleme, bulut ve çoklu sunucu ölçekleme |
| 15 | Eklenti sistemi, güncelleme, paketleme ve üretim sertleştirmesi |

## Geliştirme ortamı (Faz 01'de kurulacak)

- Go 1.27.x, Node.js 26 LTS, Docker Engine + Compose, PostgreSQL 18 (geliştirme volume'ü, asla üretim DB değil)
- Zorunlu komutlar: `go test ./...`, `go vet ./...`, `go test -race ./...`; frontend `typecheck`, `lint`, `test`, `build`
- `.env.example` yalnızca placeholder içerir; gerçek `.env` gitignored. Repo asla secret/gerçek sunucu verisi içermez.

## Lisans notu

Bu depo üçüncü taraf kodu içermez; bağımlılıklar `docs/technology/` altında lisanslarıyla listelenir. Ürün lisansı henüz seçilmemiştir; dağıtımdan önce karar verilmelidir. Bu doküman hukuki görüş değildir.
