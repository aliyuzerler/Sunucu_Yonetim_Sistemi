# agent/

Node Agent — ayrı Go ikilileri: `server-agent` (yetkisiz, outbound mTLS) ve
`server-privd` (ayrıcalıklı yerel broker). 16 fazlık yol haritasının tüm
node-tarafı aileleri buradadır: Linux (systemd, dnf/apt, firewall, Docker/Compose,
web hosting/TLS, DB/dosya, e-posta, KVM, restic yedekleme, imzalı güncelleme)
ve Windows Server (SCM, Event Log, Win32 metrikler, named pipe broker).

## Güvenlik modeli (ADR-0008 + ADR-0003)

1. Özel anahtar `server-agent` tarafından LOKALDE üretilir; node dışına çıkmaz.
2. Kayıt: tek kullanımlık token (control-plane `enroll-token` CLI veya UI)
   + CSR → sunucu, node kimliğini URI SAN'a yazıp sertifika imzalar.
3. Sunucu CA'sı operatör tarafından aktarılır (`ca.crt`); enroll yanıtındaki
   CA ile pinlenir (MITM kontrolü).
4. Run modu: outbound mTLS stream; hello/heartbeat/facts/metrics/capability
   raporu + tipli operasyonlar (expiry/idempotency/correlation alanlarıyla).
5. Privileged işlemler YALNIZCA yerel broker `server-privd` üzerinden yapılır:
   - Unix socket `/run/server-control/privd.sock` (0660, grup erişimi) veya
     Windows named pipe; TCP yok.
   - SO_PEERCRED / SID-SDDL ile allowlist; default deny.
   - Tipli JSON istekler; ikililer **argv** ile çalışır, shell string'i yok.
   - Her istek (reddedilenler dahil) JSONL audit dosyasına yazılır.
6. **Her operasyon ailesi broker'da varsayılan KAPALI opt-in bayrağı ister;**
   kapalıyken ilgili işlemler tipli `unsupported_operation` döner
   (sessiz fallback yok, kapasite beyan edilmez):

| Bayrak (`server-privd`) | Aile | Faz |
|---|---|---|
| `--docker-socket <yol>` | `docker.*` (remove/deploy preview-only — ADR-0011) | 07 |
| `--web-enable` | `web.*` + `tls.*` (nginx/PHP-FPM; anahtar node'dan çıkmaz — ADR-0013) | 08/09 |
| `--db-enable` · `--fs-enable` | `db.*` · `fs.*` (node-local DB; site-docroot scoped fs — ADR-0014) | 10 |
| `--mail-enable` | `mail.*` (Postfix/Dovecot/Rspamd — ADR-0015) | 11 |
| `--vm-enable` | `vm.*` (KVM/QEMU/libvirt — ADR-0016) | 12 |
| `--backup-enable --backup-source <kökler>` | `backup.*` (restic — ADR-0018) | 14 |
| `--update-enable --update-trust-key <genel-anahtar>` | `update.*` (imzalı; SCP-SIG-V1 — ADR-0020) | 15 |

Salt-okuma yüzeyler opt-in istemez: `logs.search` (journalctl arama,
node-tarafı redaksiyon) ve `windows.eventlog.read` yetenek setindedir;
firewall ailesi (`fw.*`) tüm fazlar boyunca preview-only'dir (ADR-0010).

## Paketler

| Paket | Sorumluluk |
| --- | --- |
| `internal/agent` | mTLS koşu döngüsü, executor registry, capability probe, facts/metrics zarfları |
| `internal/broker` | Broker protokolü + istemci + sunucu (Unix socket / Windows pipe, UID/SID allowlist, audit, sınırlar) |
| `internal/osdetect` | OS/distro/sürüm (os-release, Windows), çekirdek, systemd, paket yöneticisi tespiti |
| `internal/metrics` | CPU/RAM/disk/ağ/uptime — procfs + statfs (Windows: Win32), kaynak + timestamp |
| `internal/svcops` | systemd adapter: list/status/start/stop/restart (argv, validate, timeout) |
| `internal/pkgops` | Paket yönetimi: dnf/yum + apt-get (argv, allowlist) |
| `internal/fwops` | Firewall işlemleri — preview-only, mutasyon yok (ADR-0010) |
| `internal/dockerx` · `internal/composeops` | Docker Engine + Compose (inspect/loglar; remove/deploy preview-only — ADR-0011) |
| `internal/webops` | nginx + PHP-FPM site yaşam döngüsü: marker'lı config, atomik yazım + rollback, dosya-koruyucu silme (ADR-0012) |
| `internal/tlsops` | TLS anahtar üretimi, sertifika sahneleme, nginx reload — anahtar yalnız node'da (ADR-0013) |
| `internal/dbops` | Node-local PostgreSQL/MariaDB-MySQL: kullanıcı/DB yaşam döngüsü (korumalı adlar tipli red) |
| `internal/fsops` | Scoped dosya yöneticisi (yalnız site docroot'ları; path traversal/symlink karşı testli) |
| `internal/mailops` | Postfix/Dovecot/Rspamd: map + userdb yönetimi, `postfix check`/`doveconf -n` kapısı, byte-düzeyi rollback (ADR-0015) |
| `internal/vmops` | KVM/QEMU/libvirt: preflight, VM yaşam döngüsü, disk (yalnız `sc-` birimleri), qcow2 snapshot (ADR-0016) |
| `internal/logops` | journalctl log arama — salt-okuma, node-tarafı redaksiyon (ADR-0017) |
| `internal/backupops` | restic yedekleme/geri yükleme: init/backup/verify, izole restore (ADR-0018) |
| `internal/updateops` | İmzalı update: stage (sha256+imza+health) → atomik swap → rollback (SCP-SIG-V1; ADR-0020) |
| `internal/winops` · `internal/winevent` · `internal/winscm` | Windows adaptörleri: Win32 işlemleri, Event Log okuma, SCM servis yönetimi (ADR-0009) |

## Komutlar

```bash
# Control plane tarafında token üret:
control-plane enroll-token --scope group:<grup-uuid> --ttl 15m

# Agent tarafında (ca.crt dosyasını önce aktarın):
server-agent enroll --server <cp-host>:9090 --ca-file ca.crt --token <TOKEN>
server-agent run --server <cp-host>:9090 [--broker-socket /run/server-control/privd.sock]

# Broker (root) — aileler yukarıdaki tablodaki bayraklarla açılır:
server-privd --allow-uid <agent-uid> \
  --socket /run/server-control/privd.sock \
  --group server-control-broker \
  --audit-file /var/log/server-control/privd-audit.jsonl

# Geliştirme:
make agent-build && make agent-test && make agent-vet
```

systemd kurulumu ve sertleştirme ayrıntıları: `deploy/agent/README.md`;
Windows kurulumu: `deploy/agent-windows/README.md`; üretim paketleri ve
güncelleme hattı: `deploy/INSTALL-UPDATE.md`.

## Sınırlar

- İdempotency cache sınırlıdır (256 girişi / 30 dk retention).
- Yeniden bağlanma: exponential backoff + jitter (≤30s üst sınır).
- Metrik aralığı sunucu tarafından önerilir (HelloAck.metrics_interval_seconds,
  varsayılan 60s; agent tarafı 10s–10dk aralığına kıskanır). Sunucu, node başına
  en hızlı 10 saniyede bir anlık görüntü kabul eder (rate limit).
- Tüm operasyon aileleri uygulandı (Faz 04–15). Yeni aile eklerken disiplin
  aynıdır: broker opt-in bayrağı + capability beyanı + tipli op + audit +
  argv disiplini (serbest metin shell yok).
