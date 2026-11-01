# Faz 00 — Mimari Gözden Geçirme Raporu

- **Tarih:** 2026-11-01
- **Kapsam:** `server-control-zcode-kit` dokümantasyon tabanının tamamının denetimi (kod yazımı kapsam dışıdır)
- **Denetçi:** ZCode (Faz 00 uygulama oturumu)
- **Girdi:** `agents.md`, `README.md`, `START_HERE.md`, `docs/` (vision, architecture, technology, security, ui-ux, operations, decisions, references, phases), `rules/`, `skills/`, `prompts/` (phase-start ×16, phase-test ×16, workflow ×4)

## 1. Denetim yöntemi

1. Kit içindeki tüm dosyalar listelendi; doküman, kural, skill ve prompt dosyaları satır satır okundu.
2. `agents.md` bölüm 1'deki kaynak öncelik sırasına göre dokümanlar arasında çelişki taraması yapıldı (güvenlik → ADR → mimari → teknoloji → faz promptu).
3. Faz 00 kabul kriterleri tek tek kanıt dosyalarıyla eşleştirildi (bkz. bölüm 4).
4. Eksik mimari belgeleri tamamlandı; tutarsızlıklar kök dosyada düzeltildi (bölüm 2).
5. 16 fazın bağımlılık haritası çıkarıldı (`docs/phases/README.md`).

## 2. Bulgular ve çözümler

| ID | Önem | Bulgu | Çözüm | Durum |
|---|---|---|---|---|
| F-01 | Orta | `docs/architecture/system-architecture.md` Failure model bölümündeki görev durum listesinde `accepted` durumu eksikti; `docs/architecture/task-lifecycle.md` durum makinesi `queued → dispatched → accepted → running → …` içeriyordu. İki doküman çelişiyordu. | `system-architecture.md` durum listesi `accepted` eklenerek task-lifecycle dokümanıyla hizalandı ve çapraz referans eklendi. | Düzeltildi |
| F-02 | Orta | `docs/vision/scope.md` MVP tanımı "Docker read-only/limited lifecycle" içeriyor; faz yol haritası ise Docker'ı Faz 07'ye koyuyordu. MVP ile faz eşlemesi dokümante değildi. | `scope.md`'ye hizalama notu eklendi: MVP = Faz 00–04; Docker sınırlı yaşam döngüsü Faz 07'de, güvenlik incelemesinden sonra teslim edilir. Ürün kararı değiştirilmedi, eşleme netleştirildi. | Düzeltildi |
| F-03 | Düşük | `docs/ui-ux/design-system.md` sol menü Operations grubunda "Tasks & Activity" öğesi yoktu; `docs/ui-ux/navigation.md` içeriyordu. | `design-system.md` menü tanımı `navigation.md` ile hizalandı. | Düzeltildi |
| F-04 | Orta | Faz 00 kabul kriteri "çoklu-node stratejisi ADR ile kaydedilmeli" — ADR-0002 (bağlantı modeli) ve ADR-0004 (tek ilk dağıtım) parçalı kapsıyordu ancak strateji tek ADR'de toplanmamıştı. | **ADR-0006** yazıldı: tek Control Plane ile başla, sözleşmeler çoklu-node hazır, ölçekleme yolu ölçüme bağlı, node başına güvenlik izolasyonu. | Tamamlandı |
| F-05 | Düşük | `docs/decisions/` altında ADR dizini, süreç tanımı ve şablon yoktu; çelişki çözümünde referans zinciri eksik kalıyordu. | `docs/decisions/README.md` eklendi (süreç, durum değerleri, şablon, ADR-0001…0006 dizini). | Tamamlandı |
| F-06 | Orta | "16 fazın bağımlılık sırası" yalnızca dolaylı tanımlıydı (her fazda "önceki faz PASS"); fazlar arası gerçek teknik bağımlılıklar haritasızdı. | `docs/phases/README.md` içine bağımlılık tablosu eklendi: yürütme sırası + teknik bağımlılık + çıktının beslediği fazlar; giriş/çıkış kriterleri tanımlandı. | Tamamlandı |

## 3. Doğrulanan tutarlılıklar (düzeltme gerektirmeyenler)

- **Ürün kapsamı / non-goals:** `product-vision.md` ürün sınırı ile `scope.md` non-goals tutarlı (SaaS faturalandırma, domain registrar, rastgele root shell, kayıtsız OS yönetimi her ikisinde de dışta). Roller (Owner/Infra Admin/Operator/Auditor) yalnızca `product-vision.md`'de tanımlı, başka dokümanda çelişen rol yok.
- **Mimari bileşenler:** Control Plane (modüler monolit), Agent (ayrı ikili, outbound), privileged broker (lokal IPC, allowlist), adapter arayüzleri (14 domain arayüzü), capability kaydı ve task lifecycle beş mimari doküman arasında tutarlı; `agents.md` ve `rules/architecture-rules.md` ile aynı sınırları tanımlıyor.
- **Güvenlik:** `security-boundaries.md` güven bölgeleri, `threat-model.md` varlıklar/düşmanlar/yollar, `rules/security-rules.md` ve ADR-0003 aynı temel değişmezleri içeriyor (serbest shell yok, mTLS, default deny, secret redaction, audit).
- **Teknoloji tabanı:** `technology-stack.md` (Go 1.27.x, React 19.3, Node 26 LTS, Vite 8.3, Postgres 18.x, chi v5, pgx, gRPC) ↔ `official-sources.md` (2026-11-01 kaynak doğrulaması) ↔ `version-policy.md` (pinleme politikası) tutarlı.
- **Bağımlılık/lisans politikası:** `dependency-policy.md` SBOM + lisans taraması zorunlu tutuyor; `open-source-references.md` GPL/AGPL uyarıları (1Panel, HestiaCP, Cockpit) çalışma referansı sınırını çiziyor. Çelişki yok.
- **Kural/skill/prompt zinciri:** 5 kural dosyası ve 8 skill, mimari dokümanlarla çelişmiyor; 16 fazın uygulama ve test promptları birebir eşleşiyor.

## 4. Faz 00 kabul kriterleri ↔ kanıt eşlemesi

| Kabul kriteri | Kanıt |
|---|---|
| Ürün kapsamı, non-goals, roller, başarı ölçütleri çelişkisiz | Bölüm 3; `product-vision.md`, `scope.md` (+ F-02 hizalama notu) |
| Control Plane, Agent, privileged broker, adapter, capability, task lifecycle net | `system-architecture.md`, `agent-architecture.md`, `capability-system.md`, `task-lifecycle.md` (+ F-01 düzeltmesi) |
| Linux-first + Windows faz kararı ve çoklu-node stratejisi ADR ile kayıtlı | ADR-0005 + **ADR-0006** |
| API/protocol versioning, güvenlik sınırları, threat model, veri varlıkları | `api-and-protocol.md`, `security-boundaries.md`, `threat-model.md` (Assets), `data-model.md` |
| 16 fazın bağımlılık sırası + giriş/çıkış kriterleri | `docs/phases/README.md` bağımlılık tablosu; her fazın "Ön koşullar / Kapsam / Çıkış raporu" bölümleri |

## 5. Açık kararlar (bilinçli olarak sonraki faza bırakılanlar)

1. **Paket yöneticisi seçimi (npm vs pnpm):** `development-environment.md` Faz 01'e bırakmış — planlı açık karar; Faz 01'de seçim ve gerekçesi kaydedilecek.
2. **Ürün lisansı:** seçilmemiş; dağıtım başlamadan karar verilmeli (Faz 15'ten önce). Açık risk olarak izleniyor.
3. **ACME uygulaması (lego vs Certbot adapter):** `technology-stack.md` uyumluluk incelemesinden sonra karar diyor — Faz 09'a bırakıldı.

## 6. Ortam ve operasyon riskleri

| Risk | Etki | Önlem |
|---|---|---|
| Geliştirme sandbox'ında Go, Docker ve PostgreSQL 18 kurulu değil (Node 24 var; doküman tabanı Node 26 LTS) | Faz 01'de Go toolchain ve compose tabanlı Postgres kurulumu gerekecek; sandbox'ta Docker yoksa ilgili testler NOT RUN/BLOCKED işaretlenir, kullanıcı localinde doğrular | Faz 01 planında toolchain kurulumu ve ortam matrisi yer alacak; test raporları ortam farklarını açıkça yazacak |
| GitHub erişim token'ı sohbet üzerinden paylaşıldı | Token ifşası riski | Token repoya yazılmadı; yalnızca push komutunda geçici kullanıldı. **Kullanıcıya öneri: Faz 00 onayından sonra token'ı revoke edip ince kapsamlı (fine-grained), kısa ömürlü token kullanın** |
| Doküman dili karma (mimari İngilizce, faz işleyişi Türkçe) | Yeni katılımcıda okuma yükü | Dil politikası bilinçli: ürün/teknik doküman İngilizce, faz işleyişi ve raporlar Türkçe; UI kopyaları i18n anahtarına bağlı (design-system) |

## 7. Sonuç

Faz 00 kabul kriterlerinin tamamı karşılandı (bkz. `phase-00-test-report.md`). **Faz tamamlandı:** Evet — kod yok, dokümantasyon sözleşmesi tutarlı ve ADR zinciri kuruldu. Sonraki adım: bağımsız Faz 00 test promptunun ayrı oturumda çalıştırılması ve sahip onayı; onaydan sonra Faz 01'e geçilir.
