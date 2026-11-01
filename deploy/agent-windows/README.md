# Windows Agent Kurulumu (disposable VM doğrulama prosedürü)

> ⚠️ Bu prosedür **yalnızca disposable/test Windows Server VM'i** üzerinde
> çalıştırılmalıdır. Üretim sunucusunda riskli değişiklik yapılmaz
> (phase-05.md kapsam dışı maddesi).

## 1. Derleme

```cmd
:: Linux geliştirme makinesinde çapraz derleme:
set GOOS=windows& set GOARCH=amd64
go build -o server-agent.exe ./cmd/server-agent
go build -o server-privd.exe ./cmd/server-privd
```

## 2. Agent hesabı ve SID

Komut istemi (cmd) ile:

```cmd
:: Yerel yetkisiz hesap
net user serveragent /add /passwordchg:no
wmic useraccount where name='serveragent' get sid
:: → S-1-5-21-… (bu SID'i aşağıda --allow-sid olarak kullanın)
```

## 3. Broker kurulumu (server-privd, LocalSystem)

```cmd
mkdir C:\ProgramData\server-control

sc create server-privd start= auto binPath= "C:\Program Files\ServerControl\server-privd.exe ^
  --pipe \\.\pipe\server-control-privd ^
  --allow-sid S-1-5-21-… ^
  --sddl D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;S-1-5-21-…) ^
  --audit-file C:\ProgramData\server-control\privd-audit.jsonl"
sc description server-privd "Server Control privileged broker (named pipe, SID allowlist)"
sc start server-privd
```

- SDDL varsayılanı `D:P(A;;GA;;;SY)(A;;GA;;;BA)` — agent SID'i `--sddl`
  ile eklenmelidir (yukarıdaki örnekte ekli).
- Audit JSONL: her istek (reddedilenler dahil) yanıt-ÖNCE yazılır.

## 4. Agent kurulumu (server-agent, yetkisiz hesap)

```cmd
:: mTLS kaydı önce CLI'dan (token + CA dosyası hazırken):
server-agent.exe enroll --server <control-plane>:9090 --token <TOKEN> ^
  --dir C:\ProgramData\server-control-agent

:: SCM servisi: svc-run argümanı agent'ı native Windows servisi yapar
sc create server-agent start= auto obj= .\serveragent password= <ŞİFRE> ^
  binPath= "C:\Program Files\ServerControl\server-agent.exe svc-run ^
  --server <control-plane>:9090 ^
  --dir C:\ProgramData\server-control-agent ^
  --broker-endpoint \\.\pipe\server-control-privd"
sc start server-agent
```

## 5. Doğrulama listesi (sahip kontrolü)

1. `sc query server-agent` → RUNNING; control-plane UI'da node aktif.
2. UI → Node detay → capability çipleri: `windows.scm.service.*` ve
   `windows.eventlog.read` **available**.
3. Salt-okuma SCM: servis listesi görünür; `status` döner.
4. Event Log: `System` kanalından son olaylar listelenir.
5. Negatif: `--allow-sid`'den farklı bir kullanıcıyla pipe'a bağlanan
   istek `peer_denied` alır ve audit dosyasına düşer.
6. Kontrol işlemi: test ortamındaki önemsiz bir serviste start→stop→
   restart; audit kayıtları eşleşir.
7. **PowerShell yürütme yüzeyi OLMADIĞINI doğrulayın:** hiçbir yetenek,
   komut yürütme kabulü sunmaz (ADR-0009).

## 6. Kaldırma

```cmd
sc stop server-agent & sc delete server-agent
sc stop server-privd & sc delete server-privd
rmdir /s /q C:\ProgramData\server-control
net user serveragent /delete
```
