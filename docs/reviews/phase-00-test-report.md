# Faz 00 — Bağımsız Test Promptu Öz-Denetim Raporu

- **Tarih:** 2026-11-01
- **Rol:** Test eden (bağımsız doğrulama) — `prompts/phase-test/phase-00-test.md` kriterlerine göre
- **Not:** Bu rapor, uygulama oturumu içinde "taze gözle" yürütülen öz-denetimdir. Kit kuralı gereği test promptunun **ayrı bir oturumda** tekrar çalıştırılması önerilir; sahip onayıyla birlikte faz kapısı kapanır.

## Zorunlu kabul kontrolleri

| # | Kriter | Sonuç | Kanıt |
|---|---|---|---|
| K1 | Ürün kapsamı, non-goals, kullanıcı rolleri ve başarı ölçütleri çelişkisiz | **PASS** | `product-vision.md` (roller, başarı ölçütleri) ↔ `scope.md` (non-goals) çelişki taraması temiz; MVP↔faz eşlemesi F-02 hizalama notuyla netleşti. Rol/başarı ölçütü başka dokümanda çelişkili biçimde geçmiyor (grep doğrulaması). |
| K2 | Control Plane, Agent, privileged broker, adapter, capability ve task lifecycle netleşmeli | **PASS** | `system-architecture.md` (bileşenler + veri akışı + failure model), `agent-architecture.md` (süreç düzeni, yaşam döngüsü), `capability-system.md` (kayıt alanları, 14 adapter arayüzü), `task-lifecycle.md` (durum makinesi, outbox/SKIP LOCKED). F-01 düzeltmesi sonrası iki dokümandaki durum listeleri birebir aynı: `grep accepted` her ikisinde eşleşiyor. |
| K3 | Linux-first + Windows Agent faz kararı ve çoklu-node stratejisi ADR ile kayıtlı | **PASS** | ADR-0005 (Linux-first, Windows Faz 05) + **ADR-0006** (tek Control Plane ile başla, çoklu-node hazır sözleşmeler, ölçekleme yolu, node başına izolasyon). ADR dizini `docs/decisions/README.md` güncel. |
| K4 | API/protocol versioning, güvenlik sınırları, threat model ve veri varlıkları dokümante | **PASS** | `api-and-protocol.md` (`/api/v1`, OpenAPI CI kontrolü, proto sürüm alanları), `security-boundaries.md` (5 güven bölgesi, çekirdek kurallar, destructive eylemler), `threat-model.md` (Assets/Adversaries/High-risk paths/Mitigations), `data-model.md` (14 varlık, şifreleme, indeksleme). |
| K5 | 16 fazın bağımlılık sırası ve giriş/çıkış kriterleri tanımlı | **PASS** | `docs/phases/README.md` bağımlılık tablosunda 16 satır (`grep -c "^| [0-9]"` = 16); giriş/çıkış kriterleri tablo üstünde tanımlı; her faz dokümanında Ön koşullar + kabul kriterleri + çıkış raporu mevcut. |

## Test yaklaşımı uygulaması

| Adım | Sonuç | Not |
|---|---|---|
| Kod incelemesi (mimari sınır, yetki, hata yönetimi, redaction, audit) | **N/A** | Faz 00 kapsam dışı: kod yazılmaz (faz promptu açıkça yasaklıyor). Depoda kaynak kod yok — `backend/ agent/ proto/ web/ deploy/` mevcut değil. Bu bilinçli bir kapsam sonucudur, atlanmış test değildir. |
| Otomatik testler (unit/integration/contract/lint/build) | **N/A** | Çalıştırılabilir bileşen yok. Doküman bütünlüğü kontrolleri manuel grep/diff ile yapıldı. |
| Pozitif senaryolar | **PASS** | Kabul kriterleri ↔ kanıt eşlemesi (inceleme raporu §4); tutarlılık taraması 6 doküman grubunda tamamlandı. |
| Negatif senaryolar | **PASS** | Gizli anahtar/token taraması: `ghp_…`/`github_pat_`/AKIA/`PRIVATE KEY` desenleri → **temiz**. Fazlar arası çelişki taraması → 3 bulgu bulundu ve düzeltildi (F-01…F-03); kalan tarama temiz. |
| Regresyon | **N/A** | Önceki faz yok (Faz 00 ilk faz). |
| Ortam kısıtı | — | Bu fazda mock/disposable ortam gerektiren işlem yok; üretim sunucusuna dokunulmadı. |

## Bulgular

| Önem | Bulgu | Durum |
|---|---|---|
| Kritik | — | Yok |
| Yüksek | — | Yok |
| Orta | F-01 (durum listesi çelişkisi), F-02 (MVP↔faz eşlemesi), F-04 (çoklu-node ADR eksik), F-06 (bağımlılık haritası eksik) | Hepsinde çözüm uygulandı ve doğrulandı |
| Düşük | F-03 (menü hizalaması), F-05 (ADR dizini eksik) | Çözüldü |

## Komutlar (kanıt örnekleri)

```
grep -n "accepted" docs/architecture/system-architecture.md   # F-01 düzeltmesi kanıtı
grep -n "Alignment note" docs/vision/scope.md                 # F-02 hizalama notu
grep -c "^| [0-9]" docs/phases/README.md                      # 16 (bağımlılık tablosu)
ls prompts/phase-start | wc -l && ls prompts/phase-test | wc -l  # 16 16
grep -rqE "ghp_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16}|BEGIN (RSA|EC) PRIVATE KEY" .  # eşleşme yok
```

## Güvenlik değerlendirmesi

- Repoda secret/token/private key bulunmuyor; GitHub token'ı yalnızca push anında geçici olarak kullanıldı, repoya yazılmadı.
- `.gitignore` ile `.env`, key dosyaları ve derleme çıktıları ileride yanlışlıkla commitlenmeye karşı korundu.
- Güvenlik doküman zinciri (threat model ↔ boundaries ↔ ADR-0003 ↔ security-rules) tutarlı; release blocker tanımı yerinde.

## Faz kabul kararı

**ACCEPTED** — tüm zorunlu kriterler PASS, kritik/yüksek bulgu yok.

## Sonraki faza geçiş

**READY** — Faz 01 (`prompts/phase-start/phase-01.md`) başlatılabilir. Koşul: sahip onayı ve (önerilen) bağımsız oturumda test promptunun tekrar çalıştırılması.
