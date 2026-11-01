# agent/

Node Agent — ayrı Go ikilileri: `server-agent` (yetkisiz, outbound mTLS) ve
`server-privd` (ayrıcalıklı yerel broker). Faz 03 protokolü + Faz 04 Linux
OS yönetimi + Faz 05 Windows Server adaptörleri (SCM, Event Log, Win32
metrikler; `docs/architecture/windows-support.md`).

## Güvenlik modeli (ADR-0008 + ADR-0003)

1. Özel anahtar `server-agent` tarafından LOKALDE üretilir; node dışına çıkmaz.
2. Kayıt: tek kullanımlık token (control-plane `enroll-token` CLI veya UI)
   + CSR → sunucu, node kimliğini URI SAN'a yazıp sertifika imzalar.
3. Sunucu CA'sı operatör tarafından aktarılır (`ca.crt`); enroll yanıtındaki
   CA ile pinlenir (MITM kontrolü).
4. Run modu: outbound mTLS stream; hello/heartbeat/**facts/metrics**/capability
   raporu + tipli operasyonlar (expiry/idempotency/correlation alanlarıyla).
5. **Faz 04:** privileged işlemler (systemd servis start/stop/restart/list/status)
   YALNIZCA yerel broker `server-privd` üzerinden yapılır:
   - Unix socket `/run/server-control/privd.sock` (0660, grup erişimi), TCP yok.
   - SO_PEERCRED ile UID allowlist; default deny.
   - Tipli JSON istekler; systemctl **argv** ile çalışır, shell string'i yok.
   - Birim adı allowlist'i: `^[A-Za-z0-9][A-Za-z0-9@._-]{0,246}\.service$`.
   - Her istek (reddedilenler dahil) JSONL audit dosyasına yazılır.

## Paketler

| Paket | Sorumluluk |
| --- | --- |
| `internal/osdetect` | OS/distro/sürüm (os-release), çekirdek, systemd, paket yöneticisi tespiti |
| `internal/metrics` | CPU/RAM/disk/ağ/uptime — procfs + statfs, kaynak + timestamp |
| `internal/svcops` | systemd adapter: list/status/start/stop/restart (argv, validate, timeout) |
| `internal/broker` | Broker protokolü + istemci + sunucu (UID allowlist, audit, sınırlar) |
| `internal/agent` | mTLS koşu döngüsü, executor registry, capability probe, facts/metrics zarfları |

## Komutlar

```bash
# Control plane tarafında token üret:
control-plane enroll-token --scope group:<grup-uuid> --ttl 15m

# Agent tarafında (ca.crt dosyasını önce aktarın):
server-agent enroll --server <cp-host>:9090 --ca-file ca.crt --token <TOKEN>
server-agent run --server <cp-host>:9090 [--broker-socket /run/server-control/privd.sock]

# Broker (root):
server-privd --allow-uid <agent-uid> \
  --socket /run/server-control/privd.sock \
  --group server-control-broker \
  --audit-file /var/log/server-control/privd-audit.jsonl

# Geliştirme:
make agent-build && make agent-test && make agent-vet
```

systemd kurulumu ve sertleştirme ayrıntıları: `deploy/agent/README.md`.

## Faz sınırları

- İdempotency cache sınırlıdır (256 girişi / 30 dk retention).
- Yeniden bağlanma: exponential backoff + jitter (≤30s üst sınır).
- Metrik aralığı sunucu tarafından önerilir (HelloAck.metrics_interval_seconds,
  varsayılan 60s; agent tarafı 10s–10dk aralığına kıskanır). Sunucu, node başına
  en hızlı 10 saniyede bir anlık görüntü kabul eder (rate limit).
- Paket yönetimi, firewall, dosya sistemi vb. işlemler sonraki fazlardır;
  erken uygulama kapsam kaymasıdır.
