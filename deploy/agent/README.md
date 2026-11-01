# Faz 04 — Linux Agent kurulumu (server-agent + server-privd)

Bu dizin, Ubuntu 24.04 ve Debian 13 için iki ayrı kimlik/systemd unit'i içerir:

| Bileşen | Kimlik | Sorumluluk | Ağ |
| --- | --- | --- | --- |
| `server-agent` | `server-agent` kullanıcısı (yetkisiz) | Outbound mTLS stream: hello/heartbeat/facts/metrics/capability + tipli operasyon yürütme | Yalnız outbound (control plane:9090) + yerel broker socket |
| `server-privd` | `root` (broker) | Yalnız allowlist'li systemd servis işlemleri | Yalnız Unix socket `/run/server-control/privd.sock` (0660) |

Güvenlik modeli (docs/architecture/agent-architecture.md, ADR-0003):

- Agent asla root çalışmaz; privileged işlemler typed JSON isteklerle broker'a gider.
- Broker TCP dinlemez; socket izni 0660, dizin 0750'dir ve SO_PEERCRED ile yalnız
  `--allow-uid` listesindeki UID'ler kabul edilir (default deny).
- systemctl **argv** ile çalıştırılır (`exec.Command("systemctl", "start", unit)`);
  girdiler asla shell string'ine birleştirilmez. Birim adları
  `^[A-Za-z0-9][A-Za-z0-9@._-]{0,246}\.service$` allowlist'i ile doğrulanır.
- Her broker isteği (reddedilenler dahil) `/var/log/server-control/privd-audit.jsonl`
  dosyasına (0600) JSONL olarak yazılır.
- Faz 07 (opsiyonel): `server-privd --docker-socket /var/run/docker.sock` ile
  Docker Engine ailesi açılır. Daemon soketine erişim yalnız bu broker'dadır
  (unix; TCP yok — ADR-0011). remove/compose.deploy işlemleri PREVIEW-ONLY'dir:
  hiçbir silme/deploy yürütülmez, yalnız Engine çağrısı metin olarak döner.
  Bayrak boş geçilirse docker ailesi kapalıdır (operasyonlar
  `unsupported_operation` döner, kabiliyet beyan edilmez).

## Kurulum adımları (disposable VM'de doğrulayın)

```bash
# 1. Derleme (repo kökünde)
cd agent
go build -o /usr/local/bin/server-agent ./cmd/server-agent
go build -o /usr/local/bin/server-privd ./cmd/server-privd

# 2. Kimlikler
useradd -r -s /usr/sbin/nologin server-agent
groupadd -r server-control-broker
usermod -aG server-control-broker server-agent
mkdir -p /var/lib/server-control-agent /var/log/server-control
chown server-agent: /var/lib/server-control-agent
chmod 0750 /var/log/server-control

# 3. Unit'ler
cp deploy/agent/server-agent.service deploy/agent/server-privd.service /etc/systemd/system/
# server-privd.service içindeki --allow-uid değerini güncelleyin:
sed -i "s/--allow-uid 982/--allow-uid $(id -u server-agent)/" /etc/systemd/system/server-privd.service
systemctl daemon-reload
systemctl enable --now server-privd

# 4. Kayıt + çalıştırma (CA sertifikası operatör tarafından dağıtılır)
cp ca.crt /var/lib/server-control-agent/ca.crt
sudo -u server-agent server-agent enroll \
  --server control-plane.example.internal:9090 \
  --token <TEK-KULLANIMLIK-TOKEN>
systemctl enable --now server-agent
```

## Doğrulama

```bash
# Socket izinleri
ls -l /run/server-control/privd.sock          # 0660, grp=server-control-broker
# Broker audit akışı
tail -f /var/log/server-control/privd-audit.jsonl
# Yetkisiz UID reddi (farklı kullanıcıdan):
runuser -u nobody -- socat - UNIX-CONNECT:/run/server-control/privd.sock # peer_denied beklenir
```

## Riskler ve sınırlar

- `systemctl restart` non_idempotent sınıfındadır ve retry-güvenli değildir;
  kuyruk tekrarları operatör kararına bırakılır.
- Broker audit dosyası rotate edilmez; journald izleme veya logrotate önerilir.
- Gerçek host testleri bu fazda sandbox'ta YAPILAMADI (Docker/VM yok);
  unit + entegrasyon testleri fake systemctl/fixture'larla koşuldu ve CI'da
  iki distro matrisinde (ubuntu-24.04, debian-13) doğrulanır. Canlı doğrulama
  adımları sahibine bırakılır (bkz. docs/reviews/phase-04-test-report.md).
