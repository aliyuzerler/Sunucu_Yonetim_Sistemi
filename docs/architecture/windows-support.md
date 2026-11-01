# Windows Desteği ve Sürüm/OS Matrisi (Faz 05)

Bu belge, Windows Server ajanının **destek matrisini**, yetenek sınırlarını
ve **PowerShell yürütme yüzeyinin neden bulunmadığını** açıklar
(ADR-0009). Linux tarafı için `docs/architecture/agent-architecture.md`
geçerli kalır.

## 1. Destek matrisi

| Bileşen | Windows Server 2019 | Windows Server 2022 | Windows Server 2025 | Mimari |
| --- | --- | --- | --- | --- |
| `server-agent.exe` (outbound mTLS, servis) | ✓ | ✓ | ✓ | amd64 |
| `server-privd.exe` (yerel broker, named pipe) | ✓ | ✓ | ✓ | amd64, LocalSystem |
| `windows.scm.service.list/status` | ✓ | ✓ | ✓ | SCM API (x/sys), broker üzerinden |
| `windows.scm.service.start/stop/restart` | ✓ | ✓ | ✓ | SCM API, LocalSystem, audit'li |
| `windows.eventlog.read` (Application/System/Setup) | ✓ | ✓ | ✓ | wevtutil argv, sabit şablon |
| `windows.eventlog.read` (Security kanalı) | ✗ | ✗ | ✗ | **Tasarımdan hariç** (yönetici ister + hassas) |
| Host metrics (CPU/RAM/disk/ağ/uptime) | ✓ | ✓ | ✓ | Win32 API (x/sys), bağımlılık yok |
| System facts | ✓ | ✓ | ✓ | Registry (CurrentVersion) |
| PowerShell yürütme | **✗ — kalıcı ret** | ✗ | ✗ | ADR-0003/0009 |

- Go sürümü: `agent/go.mod` (1.27.x), yalnız amd64 derlenir.
- CI doğrulaması: `agent-windows` job (windows-2025) — SCM salt-okuma,
  gerçek named pipe + SID allowlist (izin/ret), wevtutil System sondası,
  Win32 metrics smoke, registry facts. **Kontrol (start/stop/restart)
  işlemleri mock'la sınanır; CI runner'da gerçek servis kontrolü yapılmaz.**

## 2. Güvenlik modeli (Linux ile birebir)

- **Broker = ayrı ikili, ayrı kimlik.** Agent yetkisiz hesap; broker
  LocalSystem. İletişim yalnız `\\.\pipe\server-control-privd`:
  SDDL `D:P(A;;GA;;;SY)(A;;GA;;;BA)` + agent hesabının SID'i.
- **Çağırıcı kimliği çekirdek garantili:** `ImpersonateNamedPipeClient` +
  thread token SID (PID yeniden kullanım yarışı yok). Agent, pipe'ı
  `SECURITY_IDENTIFICATION` ile açar → broker kimliği görebilir, asla
  agent kimliğine hareket edemez.
- **Allowlist üç katman:** control-plane validator → agent executor →
  broker `validateRequest`. `--allow-sid` açık liste; boş liste = tam kapalı;
  her ret auditlenir (JSONL, yanıt-öncesi).
- **Shell yok:** SCM işlemleri yapılandırılmış API; wevtutil argv sabit
  şablon; XPath/PS sorgu metni girdiden asla üretilmez.

## 3. Bilinen sınırlar / dürüst boşluklar

1. Security kanalı okunmaz (yönetici ayrıcalığı + hassasiyet); yetenek
   raporunda neden belirtilir.
2. Paket yönetimi kapsamda değil; facts `msi|unknown` bildirir, kurulum
   yapan hiçbir yetenek yoktur.
3. Pipe deadline'ları kapanış zamanlayıcısıdır (sync pipe'ta kernel
   deadline yok) — çağrı timeout'larıyla sınırlıdır.
4. Broker audit dosyası rotasyonu operatöre aittir (Event Forwarding veya
   zamanlanmış görev + rotasyon; Linux'taki journald/logrotate eşdeğeri).
5. Gerçek VM doğrulaması (disposable Windows Server) sahibine aittir:
   `deploy/agent-windows/README.md` prosedürü.

## 4. Yetenek kimlikleri (capability IDs)

| Yetenek | Kaynak | Kullanılabilirlik |
| --- | --- | --- |
| `windows.scm.service.*` (5) | broker pipe sondası | broker erişilebilir + SID izinli |
| `windows.eventlog.read` | wevtutil System sondası | kanal okunabilirse |
| `agent.echo` | daima | ✓ |
