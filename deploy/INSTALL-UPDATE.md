# Kurulum, Güncelleme ve Geri Alma Rehberi (RC1)

Bu rehber Faz 15 çıktısıdır: imzalı artifact hattı (SCP-SIG-V1, ADR-0020),
temiz kurulum → yükseltme → geri alma sınırları → kaldırma adımları ve
kaldırma sonrası veri sözleşmesi. Tüm doğrulamalar fail-closed'dur.

## 1. Güven kökü (bir kez)

Sürüm artifact'ları ed25519 ile imzalanır (SCP-SIG-V1: `scp-sign-v1\n<ad>\n<boyut>\n<sha256>\n`).

```bash
# Sürüm yöneticisinde (özel anahtar ASLA depoya/panel'e yazılmaz):
sigtool keygen --priv release-priv.b64
# → "GENEL (base64)" satırını kaydedin; bu, TÜM doğrulayıcıların güven köküdür.
```

| Nerede | Nasıl verilir |
| --- | --- |
| Control Plane (eklenti manifest) | `CP_EXTENSION_TRUST_KEYS=<genel-anahtar>` |
| Control Plane (update yayın) | `CP_UPDATE_TRUST_KEYS=<genel-anahtar>` |
| Node broker (update ailesi) | `server-privd --update-trust-key <genel-anahtar>` |

Anahtar yapılandırılmazsa ilgili yüzey KAPALIDIR: eklenti kaydı ve update
yayını 503 döner, node'da update görevleri `ErrTrustKeyMissing` ile reddedilir.
Bu bir hata değil, tasarlanmış fail-closed davranıştır.

## 2. İmzalı paket üretimi (sürüm yöneticisi)

```bash
make package VERSION=0.16.0-rc1 DIST=dist        # deb/rpm/zip + SHA256SUMS
RELEASE_PRIV_B64=$(cat release-priv.b64) make package-sign VERSION=0.16.0-rc1 DIST=dist
# dist/SHA256SUMS.sig — her artifact için "<sha256> <imza> <ad>" satırları
```

CI'daki `release-package` job'ı aynı hattı geçici anahtarla uçtan uca kanıtlar:
debian:13 clean install → upgrade → uninstall; rockylinux:9 install → erase;
zip yapısı + checksum + imza doğrulaması.

## 3. Control Plane yükseltmesi (operatör prosedürü)

CP kendi kendini güncellemeZ (ADR-0020 madde 4). Sıra:

1. **Yedekle:** panelde `backup.cp.run` → `backup.cp.verify` → `backup.cp.restore_drill`
   (doğrulamasız yedek "success" olamaz; tatbikat İZOLE veritabanında koşar).
2. **Doğrula:** `control-plane verify-update --manifest update-manifest.json --key <genel-anahtar>`
   → "İmza GEÇERLİ" çıkmayan artifact ASLA kurulmaz.
3. Dur → yeni ikiliği yerleştir → migration'lar (`make migrate-up`; her
   migration'ın up/down + downgrade sınırları migration dosyasındadır) → başlat.
4. `GET /healthz` + `GET /readyz` + panel girişini doğrula.

Geri alma: önceki ikiliğe dönüş + `migrate down` (yalnız belgelenmiş
sınırlara kadar — veri şeması geriye uyumsuz adımlarda GERİ ALMA YAPILMAZ;
bunun yerine ileri yama yayımlanır).

## 4. Agent kurulumu (temiz host)

```bash
# Debian/Ubuntu
sudo dpkg -i server-control-agent_0.16.0-rc1_amd64.deb   # ya da apt/dpkg doğrulamalı
sha256sum -c SHA256SUMS                                    # checksum
# RPM (RHEL ailesi)
sudo rpm -ivh server-control-agent_0.16.0-rc1_amd64.rpm
```

Paket kurar: `/usr/local/bin/server-agent` + `/usr/local/bin/server-privd`,
systemd unit'leri, `server-control` sistem kullanıcısı ve
`/var/lib/server-control/updates/{incoming,staged,rollback}` dizinleri.
Enrollment: `deploy/agent/README.md` (CA init → enroll-token → `server-agent enroll`).

### Update ailesini açma (opt-in, VARSAYILAN KAPALI)

```ini
# server-control-broker.service drop-in:
ExecStart=/usr/local/bin/server-privd --allowed-uid server-control \
  --update-enable --update-trust-key <GENEL-ANAHTAR>
```

## 5. Agent güncelleme akışı (kriter 2)

1. Artifact'ı node'a yerleştirin: `/var/lib/server-control/updates/incoming/`
   (tek dosya — birden fazla dosya tipli `ErrAmbiguousInput` reddidir).
2. Panelde artifact üst verisi yayımlı olmalı (`Güncellemeler` → yayımlı;
   iptal edilmiş artifact için görev oluşturulamaz).
3. `update.stage` görevi gönderin (payload: `update_id`, `version`, `sha256`,
   `signature` — **yol ALANI YOK**). Node sırayla:
   sha256 → ed25519 imza → staged ikiliğe `--version` health-check →
   aktif ikiliğin rollback kopyası → atomik swap → swap sonrası health-check.
   Herhangi bir adım başarısızsa: tipli red, swap otomatik geri alınır,
   artifact staging'e ASLA giremez.
4. Aktivasyon: `linux.systemd.service.restart` (server-control-agent) ya da
   operatör restart. `update.status` ile doğrulayın.
5. Sorun olursa: `update.rollback` — önceki ikiliğe geri döner + health-check.

**Windows:** RC'de stage/verify/runners aynı şema ile Linux'ta kanıtlıdır;
Windows'ta ikilik değişimi çalışmakta olan .exe için SCM dur→değiştir→başlat
operatör prosedürüdür (dürüst sınırlama — ADR-0020 madde 3; Windows artifact
imza/checksum kanıtı CI'dadır).

## 6. Yükseltme / düşürme sınırları ve kaldırma (kriter 4)

- **Yükseltme:** aynı sürümün yeniden kurulması idempotenttir (deb postinst
  yeniden çalışır); veritabanı migration'ları ileriye uyumludur.
- **Düşürme:** paket yöneticisi eski sürümü kurabilir, ANCAK (a) DB şeması
  geriye uyumsuz migration içeren sürümlere dönüş `migrate down` sınırına
  takılır ve (b) update manifest'inin `min_prev` alanı uyumsuz düşüşü
  staging'de reddeder. Düşürmeden önce `backup.cp.run` + tatbikat ZORUNLUDUR.
- **Kaldırma:** `dpkg -r server-control-agent` / `rpm -e server-control-agent`
  ikilikleri + unit'leri kaldırır; `/var/lib/server-control` VERİSİ KORUNUR
  (rollback kopyaları + update staging + agent önbelleği) — kasıtlı tasarım;
  tam temizlik: `rm -rf /var/lib/server-control /etc/server-control`.
