# ADR-0010: Firewall kural değişikliği — preview-only

**Tarih:** 2026-11-01 · **Durum:** Kabul edildi · **Faz:** 06

## Bağlam

Faz 06 firewall adaptörü gereği backend farklılıklarının doğru tanınması ve
otomatik kural değişikliklerinin güvenli bir önizleme gerektirmesi istenir
(`docs/phases/phase-06.md`). Firewall kural mutasyonu; yanlış payload ile
 SSH erişiminin kesilmesi, üretim trafiğinin engellenmesi gibi yüksek
etkili, geri dönüşü operatör müdahalesine bağlı riskler taşır. Agent'ın
mevcut güvenlik duruşu (ADR-0003 typed operations, allowlist-only) ile
"serbest kural mutasyonu" çelişir.

## Karar

Faz 06'da **firewall kural mutasyonu yürütücü yüzeyi YOKTUR**. Sağlanan
yüzeyler:

1. `linux.firewall.status` — backend tespiti, salt-okuma.
2. `linux.firewall.rules.list` — aktif backend'den sınırlı kural listesi,
   salt-okuma.
3. `linux.firewall.rule.preview` — typed payload'a karşılık gelen argv
   komutlarını **veri olarak** döndürür; `Preview.Executed` her zaman
   `false`'tur. Hiçbir komut çalıştırılmaz.

Komut şablonları yalnız firewalld ve ufw için üretilir; nftables/iptables
kural sözdizimi host'a özgü olduğundan bu backend'lerde önizleme reddedilir
(`ErrPreviewUnsupported`) — tahminle yanlış komut üretmek yerine dürüst ret.

## Alternatifler

- **Onaylı mutasyon akışı (dual-control):** önizleme → operatör onayı →
  agent yürütür. Güçlü ama ayrı bir tasarım (onay kaydının audit/rollback
  semantiği) gerektirir; sonraki fazlara ertelendi.
- **Sınırlı mutasyon (yalnız port ekle/çıkar):** kapsamı küçültür ama
  yine yanlış zone/interface seçimiyle kilitleme riski taşır; Faz 06 için
  gerek yoktur (kriter önizleme güvenliğini ister, mutasyonu değil).

## Sonuçlar

- Faz 06 kabul kriteri ("otomatik kural değişikliği güvenli preview
  gerektirmeli") **daha güçlü bir politikayla** karşılanır: değişiklik
  otomatik değildir; yalnız önizleme üretilir.
- Kontrol düzlemi izin modeli salt-okuma kalır: `firewall:read` (mutasyon
  izni ileride `firewall:control` olarak ayrı tasarlanır).
- UI önizleme butonu "çalıştırmaz" etiketiyle işaretlenir; döndürülen
  komutlar operatör dokümantasyonuna gider.
- Gelecek mutasyon fazı bu ADR'yi güncelleyerek (yeni ADR) onay akışını
  tanımlar.
