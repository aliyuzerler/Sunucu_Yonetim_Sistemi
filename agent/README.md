# agent/

Node Agent — ayrı Go ikilisi (`server-agent`), outbound mTLS gRPC (Faz 03).

## Güvenlik modeli (ADR-0008)

1. Özel anahtar `server-agent` tarafından LOKALDE üretilir; node dışına çıkmaz.
2. Kayıt: tek kullanımlık token (control-plane `enroll-token` CLI veya UI)
   + CSR → sunucu, node kimliğini URI SAN'a yazıp sertifika imzalar.
3. Sunucu CA'sı operatör tarafından aktarılır (`ca.crt`); enroll yanıtındaki
   CA ile pinlenir (MITM kontrolü).
4. Run modu: outbound mTLS stream; hello/heartbeat/capability raporu +
   tipli operasyonlar (expiry/idempotency/correlation alanlarıyla).

## Komutlar

```bash
# Control plane tarafında token üret:
control-plane enroll-token --scope group:<grup-uuid> --ttl 15m

# Agent tarafında (ca.crt dosyasını önce aktarın):
server-agent enroll --server <cp-host>:9090 --ca-file ca.crt --token <TOKEN>
server-agent run --server <cp-host>:9090

# Geliştirme:
make agent-build && make agent-test && make agent-vet
```

## Faz sınırları

- Faz 03'te yalnızca `agent.echo` capability'si vardır (typed echo; host'a
  dokunmaz). OS işlemleri, `server-privd` privileged broker ve metrikler
  Faz 04+ kapsamıdır; erken uygulama kapsam kaymasıdır.
- İdempotency cache sınırlıdır (256 girişi / 30 dk retention).
- Yeniden bağlanma: exponential backoff + jitter (≤30s üst sınır).
