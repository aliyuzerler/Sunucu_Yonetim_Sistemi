# Fazlar ve Prompt Dizini

## Bağımlılık sırası (Faz 00 denetimi, ADR-0006 ile uyumlu)

- **Yürütme sırası lineerdir:** her faz, bir önceki fazın test raporu PASS olmadan başlatılmaz.
- **Teknik bağımlılıklar** aşağıdaki tabloda faz bazında gösterilir; bir faz, bağlı olduğu fazların tümü ACCEPTED olmadan kapsam dahil edilemez.
- Giriş kriteri her fazda: önceki faz test raporu PASS + ilgili ADR/mimari/güvenlik belgeleri okunmuş + mock/disposable ortam planlı.
- Çıkış kriteri her fazda: tüm kabul kriterleri PASS, kritik/yüksek bulgu yok, `docs/project-status.md` + `CHANGELOG.md` güncel, test promptu ayrı çalıştırılmış.

| Faz | Konu | Teknik bağımlılık | Çıktının beslediği fazlar |
|---|---|---|---|
| 00 | Mimari ve proje sözleşmesi | — (dokümantasyon tabanı) | tümü |
| 01 | Monorepo, geliştirme ortamı, temel UI/API | 00 | 02 ve sonrası |
| 02 | Kimlik, RBAC, envanter, task/outbox | 01 | 03, 04 ve sonrası |
| 03 | Agent protokolü, kayıt, mTLS, capability | 02 | 04, 05, 07, 12 |
| 04 | Linux Agent, facts, metrik, servis yönetimi | 03 | 05, 06, 07, 08, 12, 13 |
| 05 | Windows Server Agent | 03, 04 | 13 (Windows metrikleri opsiyonel) |
| 06 | Rocky/AlmaLinux adaptörleri | 04 | 08 (distro kapsamı) |
| 07 | Docker Engine ve Compose | 04 | 13 (konteyner metrikleri) |
| 08 | Web hosting (Nginx/PHP-FPM), uygulama dağıtımı | 04 (06 önerilir) | 09, 10, 11 |
| 09 | Domain, DNS, reverse proxy, SSL/ACME | 08 | 11 |
| 10 | Veritabanları, güvenli dosya yöneticisi | 08 | 14 (DB yedekleme) |
| 11 | E-posta servisi | 08, 09 | — |
| 12 | KVM/QEMU/libvirt sanallaştırma | 04 | 14 (VM yedekleme) |
| 13 | İzleme, log, alarm, güvenlik merkezi | 04 (05/07 opsiyonel) | 14 |
| 14 | Yedekleme, bulut, çoklu-node ölçekleme | 10, 12, 13 | 15 |
| 15 | Eklenti, güncelleme, paketleme, sertleştirme | 00–14 | — |

## Faz dizini

- **Faz 00: Mimari ve proje sözleşmesini kesinleştirme** — [Uygulama](../prompts/phase-start/phase-00.md) · [Test](../prompts/phase-test/phase-00-test.md) · [Kapsam](phase-00.md)
- **Faz 01: Monorepo, geliştirme ortamı, temel UI/API** — [Uygulama](../prompts/phase-start/phase-01.md) · [Test](../prompts/phase-test/phase-01-test.md) · [Kapsam](phase-01.md)
- **Faz 02: Kimlik, RBAC, sunucu envanteri ve görev altyapısı** — [Uygulama](../prompts/phase-start/phase-02.md) · [Test](../prompts/phase-test/phase-02-test.md) · [Kapsam](phase-02.md)
- **Faz 03: Agent protokolü, kayıt, mTLS ve capability** — [Uygulama](../prompts/phase-start/phase-03.md) · [Test](../prompts/phase-test/phase-03-test.md) · [Kapsam](phase-03.md)
- **Faz 04: Linux Agent ve temel OS yönetimi** — [Uygulama](../prompts/phase-start/phase-04.md) · [Test](../prompts/phase-test/phase-04-test.md) · [Kapsam](phase-04.md)
- **Faz 05: Windows Server Agent ve native adaptörler** — [Uygulama](../prompts/phase-start/phase-05.md) · [Test](../prompts/phase-test/phase-05-test.md) · [Kapsam](phase-05.md)
- **Faz 06: Ek Linux dağıtımı adaptörleri** — [Uygulama](../prompts/phase-start/phase-06.md) · [Test](../prompts/phase-test/phase-06-test.md) · [Kapsam](phase-06.md)
- **Faz 07: Docker Engine ve Compose yönetimi** — [Uygulama](../prompts/phase-start/phase-07.md) · [Test](../prompts/phase-test/phase-07-test.md) · [Kapsam](phase-07.md)
- **Faz 08: Web hosting ve uygulama dağıtımı** — [Uygulama](../prompts/phase-start/phase-08.md) · [Test](../prompts/phase-test/phase-08-test.md) · [Kapsam](phase-08.md)
- **Faz 09: Domain, DNS, reverse proxy ve SSL** — [Uygulama](../prompts/phase-start/phase-09.md) · [Test](../prompts/phase-test/phase-09-test.md) · [Kapsam](phase-09.md)
- **Faz 10: Veritabanları ve güvenli dosya yöneticisi** — [Uygulama](../prompts/phase-start/phase-10.md) · [Test](../prompts/phase-test/phase-10-test.md) · [Kapsam](phase-10.md)
- **Faz 11: E-posta servisi yönetimi** — [Uygulama](../prompts/phase-start/phase-11.md) · [Test](../prompts/phase-test/phase-11-test.md) · [Kapsam](phase-11.md)
- **Faz 12: KVM/QEMU/libvirt sanallaştırma ve VPS** — [Uygulama](../prompts/phase-start/phase-12.md) · [Test](../prompts/phase-test/phase-12-test.md) · [Kapsam](phase-12.md)
- **Faz 13: İzleme, log, alarm ve güvenlik merkezi** — [Uygulama](../prompts/phase-start/phase-13.md) · [Test](../prompts/phase-test/phase-13-test.md) · [Kapsam](phase-13.md)
- **Faz 14: Yedekleme, geri yükleme, bulut ve çoklu sunucu ölçekleme** — [Uygulama](../prompts/phase-start/phase-14.md) · [Test](../prompts/phase-test/phase-14-test.md) · [Kapsam](phase-14.md)
- **Faz 15: Eklenti sistemi, güncelleme, paketleme ve üretim sertleştirmesi** — [Uygulama](../prompts/phase-start/phase-15.md) · [Test](../prompts/phase-test/phase-15-test.md) · [Kapsam](phase-15.md)
