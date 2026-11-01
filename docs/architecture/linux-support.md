# Linux Desteği ve Uyumluluk Matrisi (Faz 06)

Bu belge, Faz 06 Linux dağıtım adaptörlerinin **destek matrisini**, yanlış
pozitif engel kurallarını ve firewall politikasını tanımlar. Windows tarafı
için `docs/architecture/windows-support.md` geçerlidir.

## 1. Distro/ana sürüm destek matrisi

Destek, `agent/internal/osdetect.SupportedDistros` matrisiyle birebir aynıdır;
aşağıdaki tablo **açık listeyi** yansıtır (kriter: desteklenen majör sürümler
açıkça listelenmeli).

| Distro | Desteklenen majörler | Aile | Paket aracı | Servis yöneticisi | Firewall backend | Durum kanıtı |
| --- | --- | --- | --- | --- | --- | --- |
| Ubuntu | **22, 24** | deb | apt/dpkg (dpkg-query) | systemd | ufw / nftables / iptables | ubuntu-24.04 runner + e2e |
| Debian | **12, 13** | deb | apt/dpkg (dpkg-query) | systemd | nftables / iptables | debian:13 container (CI) |
| Rocky Linux | **9, 10** | rhel | dnf/rpm (rpm -qa) | systemd | firewalld / nftables | rockylinux:9 privileged systemd container (CI `agent-rhel`) |
| AlmaLinux | **9, 10** | rhel | dnf/rpm (rpm -qa) | systemd | firewalld / nftables | almalinux:9 privileged systemd container (CI `agent-rhel`) |
| (bilinen diğer) | — | debian-like / rhel-like | — | — | — | `unknown` — destek iddiası YOK |

- Matris dışı majör (ör. Rocky 8, Ubuntu 26): `support_state=untested` —
  **desteklenmiyor olarak raporlanır** (kriter: uyumsuz/denenmemiş sürüm
  desteklenmiyor gösterilir). Kod çalışmayabilir demek değil; iddia yok demek.
- Matris dışı distro (ör. SLES): `support_state=unknown`.

## 2. Yanlış pozitif engel kuralları (detection)

1. **Aile etiketi yalnız ID'den gelir** (`os-release` `ID=` alanı:
   rocky/almalinux/ubuntu/debian). `ID_LIKE` yalnız ID bilinmiyorken kaba bir
   `debian-like`/`rhel-like` etiketi üretir — **asla** `supported` üretmez.
2. **Majör ayrıştırma katıdır**: `VERSION_ID` "9.5"→"9", "24.04"→"24";
   boş/sayısal olmayan değer asla eşleşmez (`majorOf`).
3. Fixture seti yanlış pozitif senaryolarını sabitler:
   `distro_test.go` — ID_LIKE=rhel olan bilinmeyen distro, boş/geçersiz
   VERSION_ID, Rocky 8 (matris dışı majör).
4. Gerçek kanıt: CI `agent-rhel` job'ı disposable privileged systemd
   container'larında `TestRealHostSmoke`'u çalıştırır — `EXPECTED_OS_ID`
   ortam değişkeni olmadan test SESSİZCE ATLANMAZ (gate konmuşsa host
   eşleşmezse TEST BAŞARISIZ olur; sessiz skip kanıt üretemez).

## 3. Firewall backend farklılıkları ve PREVIEW-ONLY politikası

- Tespit sırası (salt-okuma): **firewalld** (`firewall-cmd --state`=running)
  → **ufw** (`ufw status`=active) → **nftables** (`nft list ruleset` okunur)
  → **iptables** (`iptables-save` okunur) → **none**. Fark dürüstçe
  raporlanır (`Status.Reason`).
- Kuralların listelenmesi backend'e göre farklılaşır ve sınırlıdır
  (firewalld: aktif zon başına `--list-all`, ≤16 zon; ufw/nft/iptables:
  doğrudan liste; 64 KiB + 400 satır sınırı).
- **Kural değişikliği yüzeyi YOKTUR (ADR-0010):**
  `linux.firewall.rule.preview` parametrelere karşılık gelen argv komutlarını
  **veri olarak** döndürür, hiçbir şey çalıştırmaz (`Preview.Executed`
  değişmez `false`'dur). firewalld ve ufw için komut şablonları vardır;
  nftables/iptables kural sözdizimi host'a özgü olduğundan bu backend'ler
  Faz 06'da **yalnız raporlanır**, önizleme reddedilir
  (`ErrPreviewUnsupported`).

## 4. Paket envanteri — salt-okuma

- `linux.pkg.list`: deb ailesinde `dpkg-query -W -f=…`, rhel ailesinde
  `rpm -qa --qf=…` (sabit argv; sınırlı çıktı 1 MiB; limit 1..500).
- **Kurulum/kaldırma/güncelleme yüzeyi YOKTUR** — mutasyon ayrı bir fazda,
  onay akışıyla tasarlanacaktır (linux-support.md kapsam kararı).

## 5. Bilinen sınırlar / dürüst boşluklar

1. Gerçek Rocky/Alma kanıtı CI'daki disposable container'lardan gelir
   (privileged systemd); sahibin kalıcı VM doğrulaması ek değerdir.
2. Rocky 10 / Alma 10 matriste yer alır (desteklenen majör olarak beyan);
   CI kanıtı şu an 9 container'larındadır — 10 container job'a eklendiğinde
   kanıt eşitlenir.
3. nftables/iptables için kural önizlemesi yoktur (rapor-only).
4. Firewall audit: operasyonlar control-plane audit'ine yazılır; broker
   JSONL audit'i her istek için üretilir (ret dahil).
