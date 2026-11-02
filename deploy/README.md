# deploy/ — Dağıtım ve Geliştirme Ortamı

## Geliştirme (Faz 01)

`docker-compose.yml` yalnızca **PostgreSQL 18** kaldırır. Control Plane ve UI lokalde çalışır:

```bash
# 1) Ortam değişkenleri
cp .env.example .env   # değerleri düzenleyin (.env gitignored)

# 2) Veritabanı
docker compose up -d
docker compose ps      # healthy bekleyin

# 3) Migration (explicit, otomatik çalışmaz)
cd ../backend/control-plane
export CP_DATABASE_URL="postgres://serverctl:...@localhost:5432/server_control?sslmode=disable"
go run ./cmd/migrate status
go run ./cmd/migrate up

# 4) Control Plane
go run ./cmd/control-plane
# healthz:  http://127.0.0.1:8080/healthz
# readyz:   http://127.0.0.1:8080/readyz  (DB up → ready)
#
# Faz 09: DNS/SSL için ek ortam değişkenleri (hepsi OPSİYONEL; ayarsız
# ilgili aile dürüstçe tipli hata verir — sessiz fallback yoktur):
#   CP_SECRETS_KEY        base64 32 bayt AES-256 anahtarı (sağlayıcı
#                         kimlik bilgileri + sertifika materyali için
#                         ZORUNLU; üretimi: openssl rand -base64 32)
#   CP_ACME_ACCOUNT_KEY   ACME hesap anahtarı dosya yolu (yoksa P-256
#                         üretir, 0600)
#   CP_ACME_DIRECTORY_URL ACME dizin URL'si — test için LE STAGING:
#                         https://acme-staging-v02.api.letsencrypt.org/directory
#   CP_ACME_CONTACT       mailto:ops@example.com
# Ayrıntılar: docs/architecture/dns-ssl.md + ADR-0013.

# 5) UI (ayrı terminal)
cd ../../web && npm install && npm run dev
```

## Agent: web hosting + TLS sertifikaları (Faz 08/09)

`server-privd` gerçek sistem mutasyonlarını (web hosting: kullanıcı/config/reload;
TLS: anahtar üretimi, sertifika sahneleme, nginx reload) yalnız opt-in ile yapar:

```bash
server-privd --web-enable ...   # VARSAYILAN KAPALI — her iki aile için tek kapı

### Faz 10: veritabanı ve dosya yöneticisi kapıları

```
server-privd --db-enable   # VARSAYILAN KAPALI — node-local PostgreSQL/MariaDB-MySQL yönetimi
server-privd --fs-enable   # VARSAYILAN KAPALI — scoped dosya yöneticisi (yalnız site docroot'ları)
server-privd --mail-enable # VARSAYILAN KAPALI — e-posta servisi yönetimi (Postfix/Dovecot/Rspamd; Faz 11)
server-privd --vm-enable   # VARSAYILAN KAPALI — sanallaştırma yönetimi (KVM/QEMU/libvirt; Faz 12)
server-privd --backup-enable --backup-source /etc/server-control   # VARSAYILAN KAPALI — restic node yedeği (Faz 14)
server-privd --update-enable --update-trust-key <GENEL-ANAHTAR>    # VARSAYILAN KAPALI — imzalı güncelleme ailesi (Faz 15; ADR-0020)
```

- db ailesi yalnız node-local unix soketlerine dokunur; panelin kendi PostgreSQL'i yapısal olarak erişilemez (ADR-0014). Korumalı adlar (sistem + panel DB'leri) tipli red ile korunur.
- DB kullanıcı şifresi node'da üretilir, control-plane'de AES-256-GCM seal edilir ve TEK SEFERLİK reveal edilir — `CP_SECRETS_KEY` ayarlı OLMALI (ayarsızsa şifre kalıcı saklanmaz ve dürüstçe redakte edilir).
- Dosya yöneticisi kökleri yalnız web hosting site docroot'larıdır (`/var/www/server-control/<site>/html`); upload/indirme 512 KiB ile sınırlıdır.
- E-posta ailesi (Faz 11, ADR-0015): `--mail-enable` verilmeden tüm mail.* işlemleri tipli red döner. Modül paket KURMAZ; `mail.preflight` yalnız rapor verir. Yönetilen yollar: `/etc/server-control/mail/` (map + userdb, 0600), marker-blokle `/etc/postfix/main.cf` ve `/etc/dovecot/conf.d/99-server-control.conf`, Maildir kökü `/var/vmail` (vmail kullanıcısı 5000:5000 ZORUNLU — yoksa mailbox işlemleri tipli red döner). Her mutasyon `postfix check`/`doveconf -n` kapısından geçer; başarısızlıkta byte-düzeyi rollback. Mailbox şifreleri db.user.create disiplinindedir (node'da üretilir, kind="mailbox" ile seal edilir, TEK SEFERLİK reveal — `CP_SECRETS_KEY` gerekir). Deliverability (SPF/DKIM/DMARC/PTR) dış DNS bağımlılığıdır ve UI'da açıklanır.
```

Kapalıyken ilgili operasyonlar tipli `unsupported_operation` döndürür (sessiz
kalabalık yok). TLS özel anahtarı yalnız bu broker'da doğar ve node'dan asla
çıkmaz (ADR-0013).

## Güvenlik notları

- Sanallaştırma ailesi (Faz 12, ADR-0016): `--vm-enable` verilmeden tüm vm.* işlemleri tipli red döner. Modül paket KURMAZ; `vm.preflight` yalnız rapor verir (KVM/CPU flags/bellek/pool/köprü). Gereksinimler: qemu-kvm, qemu-utils, libvirt-daemon-system + libvirt-clients, çalışan libvirtd, en az bir AKTİF storage pool ve bir köprü/libvirt ağı. Disk görüntü yolu payload'da ASLA taşınmaz; silme yalnız `sc-` önekli yönetilen birimlere dokunur (yabancı diskte tipli red). Snapshot'lar iç snapshot'tır ve yalnız qcow2'de çalışır (gerçek format qemu-img ile doğrulanır). VNC yalnız 127.0.0.1'e bağlanır; konsol akışı bu fazda yoktur. VM oluşturmak guest OS yönetimi sağlamaz — in-guest modüller için guest içine agent gerekir.
- Yedekleme ailesi (Faz 14, ADR-0018): node tarafı `--backup-enable` ile opt-in'dir; kapalıyken tüm backup.* işlemleri tipli red döner. restic node paket yöneticisinden kurulur (modül kurulum YAPMAZ). Repo yolu node'da sabittir: `/var/backups/server-control/restic` (0700); restic parola dosyası `/etc/server-control/backup/restic-password` (0600) — parola dosyasının repo dizini İÇİNDE olması tipli olarak reddedilir (kriter 3: anahtar/parola depo ile aynı yerde tutulmaz). `--backup-source` virgüllü kök listesi yedek yüzeyini daraltır (varsayılan `/etc/server-control`); görev payload'ı bu köklerin altına girmeyen yolu reddeder. Restore yalnız izole dizine (`/var/backups/server-control/restore/rest-<rastgele>`) yapılır. Control Plane yedeği için `CP_BACKUP_DIR` (varsayılan `/var/backups/server-control/control-plane`) tanımlı dizine 0700 izinleriyle yazılır; `CP_SECRETS_KEY` bu dizine ASLA yazılmaz ve her doğrulama pass'i artifact'i anahtar sızması için tarar. "Backup ve restore tatbikatı" — yedek alınca bütünlük doğrulaması kaydedilir; periyodik izole restore tatbikatı önerilir (UI'dan tek tıkla çalışır).
- İzleme/log/alarm/güvenlik merkezi (Faz 13, ADR-0017): agent tarafında yeni kapı YOKTUR — `logs.search` varsayılan yetenek setindedir ve salt-okumadır; agent kullanıcısının `systemd-journal` grubunda olması önerilir (yoksa tipli `ErrPermission` döner, sessiz boş sonuç DEĞİL). Control Plane'de `GET /metrics` scrape yüzeyi açıktır: `CP_METRICS_TOKEN` tanımlayın (Bearer zorunlu, fail-closed); tanımlı değilken yalnız sınırlı secretsiz toplam seriler servis edilir — yine de ters proxy ile kısıtlamanız önerilir. Alarm evaluator'u CP process'i içinde her 60 sn'de bir çalışır (node işlemi çalıştırmaz). `CP_SECRETS_KEY` tanımsızsa güvenlik merkezi bunu `warn` dürüst kontrol olarak raporlar.
- PostgreSQL portu yalnızca `127.0.0.1`'e bağlanır.
- İmaj, tag + sha256 digest ile sabitlenmiştir (sürüm politikası); güncellerken digest'i yenileyin ve değişikliği kaydedin.
- `.env` dosyası repoya girmez; `.env.example` yalnızca placeholder içerir.
- Bu compose dosyası üretim dağıtımı değildir; üretim topolojisi Faz 15'te paketlenir.

## Faz 15: imzalı güncelleme hattı (ADR-0020)

- **Güven kökü:** `CP_EXTENSION_TRUST_KEYS` / `CP_UPDATE_TRUST_KEYS` (Control Plane) ve
  `--update-trust-key` (broker) — virgüllü base64 ed25519 genel anahtar(lar) (`sigtool keygen`).
  Anahtar yapılandırılmazsa ilgili yüzey KAPALIDIR (fail-closed).
- **Paketler:** `make package VERSION=... DIST=dist` → deb/rpm/zip + `SHA256SUMS`;
  imza için `RELEASE_PRIV_B64` ile `make package-sign` (SCP-SIG-V1). CI `release-package`
  job'ı kurulum testlerini debian:13 + rockylinux:9'da uçtan uca kanıtlar.
- **Node update akışı:** artifact `/var/lib/server-control/updates/incoming/` altına tek dosya
  olarak yerleştirilir → `update.stage` (sha256 + imza + health-check + rollback kopyası +
  atomik swap + post-swap health) → aktivasyon `linux.systemd.service.restart` →
  `update.status` doğrulama; sorun halinde `update.rollback`.
- **Tam rehber:** `deploy/INSTALL-UPDATE.md` (kurulum → yükseltme → düşürme sınırları → kaldırma).
