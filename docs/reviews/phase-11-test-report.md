# Faz 11 Bağımsız Test Raporu — E-posta servisi yönetimi

- **Test tarihi:** 2026-11-02
- **Denetlenen commit:** 59535e6 (Faz 11 implementasyonu + CI-36..41 düzeltmeleri; taban 4df83c3 = Faz 10 ACCEPTED/READY, tag v0.11.0)
- **Test eden rolü:** bağımsız denetçi (uygulama ajanı varsayımları alınmadı; kanıt file:line + komut çıktısı istendi)
- **Yerel doğrulama (taze koşu, 59535e6):** agent `gofmt -l` temiz + `go vet ./...` temiz + `go test ./...` **17/17 paket ok** (mailops/broker/agent ayrıca **`-race`** ok) + `GOOS=windows go vet` + `go build` ok; backend `go vet ./...` temiz + `go test ./...` **17/17 paket ok**; web `vitest` **35/35** + `npm run build` ok + **Playwright 30/30** (6 mock spec; Faz 11 dahil 4 yeni test) + `scripts/secret-scan.sh` temiz.
- **CI:** yerel ortamda Postfix/Dovecot olmadığından gerçek-mail lifecycle (EXPECTED_MAIL=1) yerelde **NOT RUN**'dır; CI agent-mail job'ı koşar. **Beş koşuluk gerçek-CI serisi (run 41393010479 → 41396671925) toplam 6 bulgu üretti — hepsi gerçek kanıt değeri taşıdı, düzeltildi ve birim regresyonlarla güvenceye alındı:**
  - **CI-36 (e2e gerçek mod, ORTA):** `mail.spec.ts` 1. testi gerçek modda skip edilmiyordu — gerçek modda agent yokken mail capability'si de yoktur (panel DÜRÜST olarak görünmez) ve testin oluşturduğu `mail-01` node'u smoke spec'inin gerçek-modda `/e2e/reset` almayan "Henüz kayıtlı sunucu yok" boş-liste ön koşulunu bozuyordu. Düzeltme: gerçek modda skip (diğer 3 mail testiyle aynı disiplin).
  - **CI-37 (ORTA, tanı altyapısı):** gerçek-host testi teslimden ÖNCE `postfix reload` u ZORUNLU kılar (postfix testten önce başlatıldığı için managed blok canlı config'e sonradan yazılır); teslim zaman aşımında `postqueue -p` + `postconf -n` (virtual satırları) + `/var/log/mail.log` son 40 satırı dürüst tanı olarak raporlanır — sonraki bulguların kök-nedeni bu tanılarla 1 koşuda çözüldü.
  - **CI-38 (YÜKSEK, gerçek-delivery kanıtının birinci değeri):** `domains` map dosyası KEY-ONLY satır içeriyordu; `postmap` "expected format: key whitespace value" uyarısıyla satırı derlenmiş hash map'e ALMIYOR → `virtual_mailbox_domains` lookup'ı domaini görmüyor → mesaj virtual transport yerine SMTP relay'e düşüyor → `dsn=5.4.4 status=bounced (Host or domain name not found)`. Düzeltme: yönetilen biçim `domain OK` (key+value; domain sınıfı yalnız key üyeliğine bakar), okuma tarafı `readDomainNames()` key-only parse; birim regresyon biçimi güvenceye alıyor. Sessiz yapısal bozulma sınıfı — panel kullanıcısına hata döndürmezdi, gerçek teslim CI'da yakalandı.
  - **CI-39 (YÜKSEK, ikinci değer):** CI-38 sonrası mesaj virtual transport'a gitti (`relay=virtual` ✓) ama `virtual_mailbox_maps lookup: Permission denied` → `status=deferred (mail system configuration error)`: postfix virtual/trivial-rewrite map'leri **postfix OS kullanıcısı** olarak okur; yönetilen dizin 0750 root:root'tu. Düzeltme: BaseDir 0755 (+ AÇIK `os.Chmod` — MkdirAll önceden-varolan dizine dokunmaz), postmap çıktısı `.db` dosyalarına 0644 sabitlenir; birim regresyon izinleri güvenceye alıyor. `dovecot-users` 0600 kalır (auth-worker root okur; IMAP bu fazda kapsamda değil — mail.md dürüst not).
  - **CI-40 (YÜKSEK, üçüncü değer):** map lookup'ları geçti, virtual agent `create maildir file .../Maildir/tmp/...: Permission denied` (UID/GID=5000/5000): `ensureMaildir`'in `MkdirAll`'ı **ara bileşenleri** (domain/, localpart/) root:root 0700 yaratıyordu — chown yalnız Maildir yapraklarına yapılıyordu. Düzeltme: vmailBase→Maildir **zincirinin tümü** + cur/new/tmp 5000:5000 chown; `TestMailboxCreateChownsWholeChain` regresyonu. Yine sessiz-bozulma sınıfı: hata yalnız gerçek teslimde görünüyordu.
  - **CI-41 (DÜŞÜK, test-akışı):** kontrollü teslim kanıtı geçti (test 63s→4.6s); kalan tek bulgu akış sırası: adım 10 `DomainDelete`, alias hâlâ varken **koruyucu çelişki reddine** takıldı (davranış tasarım gereği DOĞRU). Teste 9.5 `AliasDelete` adımı eklendi.
  - **NİHAİ KOŞU (run 41396671925, commit 59535e6 = tag v0.12.0): CI 14/14 YEŞİL** — gerçek kanıt satırları agent-mail job'ından: `--- PASS: TestRealMailLifecycle` (gerçek Postfix+Dovecot: domain → mailbox ({SSHA} userdb, düz-metin userdb'ye yazılmaz) → **sendmail → virtual transport → Maildir/new KONTROLLÜ TESLİM** → alias → quota → config check → içeriksiz queue → mailbox+domain delete), `--- PASS: TestRealMailSyntaxRollback` (gerçek `postfix check` kapısı + rollback); e2e gerçek-backend, backend PostgreSQL 18 entegrasyonu, secret-scan temiz.
- **Kapsam:** yalnız Faz 11 (`prompts/phase-test/phase-11-test.md`); sonraki faz başlatılmadı.

## Zorunlu kabul kontrolleri

### Kriter 1 — Mail modülü varsayılan kurulumda zorunlu olmamalı; preflight ve port kontrolü yapmalı → **PASS**

| Kanıt | Konum |
|---|---|
| `--mail-enable` flag VARSAYILAN `false`; runner yalnız flag verilirse bağlanır | `agent/cmd/server-privd/daemon_unix.go:48` (registerPlatformFlags), `:95` (mailRunnerOrZero) |
| Kapalıyken HER 16 mail op'u tipli `unsupported_operation` reddi — sessiz kalabalık yok; negatif kanıt: gate testi 16/16 op | `agent/internal/broker/exec_unix.go` (`isMailOp` gate); `broker/mail_test.go:TestMailFamilyDisabledByDefault` (PASS — IT-01 düzeltmesi) |
| Modül paket KURMAZ: runner'da paket yöneticisi çağrısı YOK (yalnız fixed-argv `postfix/postmap/postqueue/postconf/doveconf/doveadm/rspamadm/systemctl`); preflight salt rapor | `agent/internal/mailops/runner_linux.go:Preflight` (yan etkisiz); `mutate_linux.go` yürütme yüzeyi |
| Preflight: 7 ikili + sürüm (dolduğunda), **port kontrolü** {25,465,587,143,993} — kernel bind denemesi, EADDRINUSE→occupied, diğer hata→unknown (dürüst üç-durum) | `mailops.go:PreflightPorts`; `runner_linux.go:probePort/isAddrInUse` |
| Ek dürüstlük: vmail/varlık/wiring/managed-dir + PTR best-effort ("bilinmiyor" asla kanıt gibi sunulmaz) + notlar ("modül kurulum YAPMAZ") | `runner_linux.go:Preflight` (probePTR, notes); e2e `mail.spec.ts` kriter-1 testi PASS |

### Kriter 2 — Domain/mailbox/alias/quota işlemleri typed ve auditli olmalı → **PASS**

| Kanıt | Konum |
|---|---|
| 16 typed op spec: sınıflar (read_only/idempotent/destructive), `RequiredPerm` mail:read/mail:control, timeout/payload sınırları; registry 67→**83** doğrulaması | `backend/control-plane/internal/tasks/ops_faz11.go` (buildFaz11Registry); `service_test.go` (`!= 83` PASS) |
| Şemalar agent ile AYNA + iki taraflı strict doğrulama: domain/local/adres allowlist RE'leri, kota 10 MiB..1 TiB, kapalı-enum limit anahtarları | `mailops.go` (ValidDomain/ValidLocal/ValidAddress/ValidQuota/ValidLimitKey) ↔ `ops_faz11.go` (mailValid*); broker `protocol.go` mail case'leri |
| Audit: görev motoru her task'i auditler (actor/op/payload/result); broker her isteği (red dahil) JSONL auditler; mail işlemleri de bu zincirden geçer | mevcut `internal/audit` + `tasks/service.go`; `broker/server.go:handleCreds` (AuditEntry) |
| Üç-katman doğrulama kanıtı: CP validator + broker strict şema + runner RE; broker mail negatifleri (18) + pozitifleri (12) + hata-kategori eşlemesi | `broker/mail_test.go` (IT-01 düzeltmesi — PASS), `ops_faz11_test.go` (PASS), `mailops_test.go` (PASS) |
| Mailbox şifresi: node'da üretilir (crypto/rand 32), userdb'de YALNIZ `{SSHA}` hash; düz metin TEK SEFERLİK reveal (dbcred kind="mailbox") | `mailops.go:GeneratePassword/HashSSHA`; `dbops_test`... `mailops_test.go:TestMailboxCreateFullFlow` (plaintext-userdb negatifi PASS); `dbcred/dbcred.go` (OpMailBoxCreate seal) |

### Kriter 3 — SPF, DKIM, DMARC, PTR/reverse DNS gereksinimleri UI'da açıklanmalı → **PASS**

| Kanıt | Konum |
|---|---|
| Deliverability kartı: 4 kayıt türü için açıklama + ÖRNEK DNS kaydı (SPF/DKIM/DMARC TXT örnekleri) | `web/src/components/MailPanel.tsx` (data-testid="mail-deliverability") |
| Dürüst dış-DNS beyanı: "bu panel dış DNS'i yönetmez" + PTR'nin IP sahibinden ayarlanabileceği + preflight PTR'sinin best-effort olduğu notu | `MailPanel.tsx` (intro + ptrNote); `i18n/tr.ts` + `en.ts` `nodedetail.mail.deliverability.*` (67 yeni anahtar ×2) |
| DKIM imzalama yönetimi bu fazda kapsam dışı — UI AÇIKÇA söyler (sahte kanıt yok) | `i18n/tr.ts` (dkim anahtarı metni); ADR-0015 sınırlar |
| Negatif/negatif-değil kanıtları: e2e + vitest içerik varlığını doğrular; gerçek modda agent yokken panel DÜRÜST görünmez | `web/e2e/mail.spec.ts` (kriter-3 testi PASS); `src/__tests__/NodeDetailMail.test.tsx` (PASS) |

### Kriter 4 — Mail servis config değişiklikleri syntax check ve rollback olmadan uygulanmamalı → **PASS**

| Kanıt | Konum |
|---|---|
| Map mutasyonları: atomic write → `postmap` → **`postfix check`** → başarısızlıkta byte-düzeyi geri alma + yeniden derleme → tipli `ErrSyntaxCheck` | `mailops/mutate_linux.go:applyMap/restoreMap`; `broker/exec_unix.go` (ErrSyntaxCheck→executor_error) |
| Userdb mutasyonları: atomic write → **`doveconf -n`** kapısı → geri alma | `mutate_linux.go:applyUsersFile` |
| limits.set: main.cf bellek-içi backup → `postconf -e` → `postfix check` → başarısızlıkta restore; reload best-effort + dürüst `reloaded` bayrağı (geçerli config daemon-down nedeniyle geri ALINMAZ) | `mutate_linux.go:LimitsSet/restoreMainCF/reloadBestEffort` |
| Negatif kanıt (hermetik): check-fail → dosyalar byte-identical restore (domain/vmailbox/userdb/main.cf dört yol) | `mailops_test.go:TestDomainCreateSyntaxFailRollsBack, TestMailboxCreateDoveconfFailRollsBack, TestLimitsSetSyntaxFailRestoresMainCF` (PASS) |
| Pozitif kanıt (GERÇEK host): `/bin/false` check ikilisiyle create reddi + önceki baytların bütünlüğü — gerçek `postfix check` kapısı CI'da | `mailops/realhost_test.go:TestRealMailSyntaxRollback` — CI run 41396671925 **PASS** |
| Okuma görünümü: `mail.config.check` üç bileşenin (postfix/dovecot/rspamd) durumunu değişiklik yapmadan raporlar | `mutate_linux.go:ConfigCheck`; e2e/vitest rozet testleri PASS |

### Kriter 5 — Spam/abuse limitleri ve mail queue görüntüleme bulunmalı; hassas içerik loglanmamalı → **PASS**

| Kanıt | Konum |
|---|---|
| Kapalı-enum limitler: 6 Postfix tunable (message_size_limit/mailbox_size_limit/smtpd_client_message_rate_limit/smtpd_client_recipient_rate_limit/smtpd_client_connection_count_limit/anvil_rate_time_unit) aralık doğrulamalı; bilinmeyen anahtar tipli red (CP + broker + runner üç katman) | `mailops.go:limitKeys/ValidLimitKey`; `ops_faz11.go:mailLimitKeys`; `protocol.go:OpMailLimitsSet`; negatifler `mail_test.go` (evil_key/out-of-range PASS) |
| UI limit formu: yalnız kapalı anahtarlar listelenir; uygula butonu check+rollback etiketli | `MailPanel.tsx` (limits bölümü) |
| Kuyruk: `postqueue -j` ayrıştırması — queue_id/queue_name/size/age/sender/neden(≤200); **alıcı adresleri ve gövde ayrıştırma aşamasında DÜŞÜRÜLÜR**; tavan 200 kayıt | `mailops/mutate_linux.go:QueueList/queueJSONLine`; `protocol.go` limit 0..200 |
| Hassas-içerik negatif kanıtı: sahte postqueue çıktısında alıcı adresi → sonuçta YOK; UI kuyruk notu ("mesaj gövdesi ve alıcı adresleri hiçbir zaman gösterilmez"); audit/result'ta şifre asla düz metin (dbcred seal) | `mailops_test.go:TestQueueListDropsContent` (PASS); `MailPanel.tsx` (note); `dbcred_test.go:TestScrubMailBoxCreateSealsAndStrips` (PASS) |

## Bulgular

| Kimlik | Önem | Özet | Durum |
|---|---|---|---|
| IT-01 | ORTA (test boşluğu) | Mail ailesi broker katmanı (kapı/şema/yürütme) test edilmemiş birleştirilmişti — db/fs ailelerindeki üç-katman kanıt mail için eksikti; kötü payload'ın broker'da durduğu yalnız CP validator testlerinden çıkarılabiliyordu | **Bu denetim koşusunda düzeltildi:** `broker/mail_test.go` (kapı 16/16 + 18 negatif + 12 pozitif + hata-kategori eşlemesi) — `-race` PASS |
| IT-02 | DÜŞÜK | `dovecot-users` 0600 root-owned: passwd-file doğrulaması auth-worker root okur; IMAP oturumu (login yolu) bu fazda test edilmez — kalıntı risk mail.md'de dürüstçe belgeli | Belgelendi (ADR-0015 + mail.md sınırlar) |
| IT-03 | BİLGİ | `QueueList` neden alanı yalnız İLK alıcının delay_reason'undan alınır (çok-alıcılı deferred kayıtlarda diğer nedenler görünmez) — amaç tanı edinilebilirliği, eksiksizlik değil; içerik-disiplin ile tutarlı | Belgelendi (kod yorumu + rapor) |
| IT-04 | POZİTİF | Bağımsız koşu + gerçek-CI döngüsü 6 gerçek bulgu yakaladı (CI-36..41): üçü SAHTE-KANIT engelleyen sessiz-bozulma sınıfından (key-only map, map izinleri, Maildir zinciri sahipliği) — bu hatalar panel/şema testlerinde ASLA görünmezdi; yalnız gerçek teslim kanıtı yüzeye çıkardı | Düzeltildi + birim regresyonlarla güvenceye alındı |
| IT-05 | BİLGİ | `postfix --version` CI adımında beklenen hatayı üretir (postfix cmd sendmail-amaçlı değildir) — `|| postconf mail_version` fallback'i çalışıyor, zararsız log gürültüsü | Belgelendi (değişiklik gerekmez) |

## Test komutları ve kısa çıktılar

```
agent:  gofmt -l . → (boş); go vet ./... → (temiz)
        go test -race ./internal/mailops/ ./internal/broker/ ./internal/agent/ → ok ×3
        go test ./... → 17/17 paket ok
        GOOS=windows go vet ./... && GOOS=windows go build ./... → ok
backend: go vet ./... → (temiz); go test ./... → 17/17 paket ok
web:    npx vitest run → Test Files 8 passed (8), Tests 35 passed (35)
        npm run build → ✓ built; npx playwright test → 30 passed (1.1m)
güvenlik: bash scripts/secret-scan.sh → temiz ✓
CI (gerçek host): run 41396671925 → 14/14 job success
        agent-mail: --- PASS: TestRealMailLifecycle (kontrollü teslim dahil)
                    --- PASS: TestRealMailSyntaxRollback
```

## Regresyon sonucu

- Agent 17/17 paket (Faz 04-10 süitleri dahil) — mail eklemeleri mevcut davranışı kırmadı; broker/dbfs/web/mail `-race` ok.
- Backend 17/17 paket; registry 83-op testi, scrubber birimleri, httpapi rota regresyonu ok.
- vitest 35/35 (8 süit), Playwright 30/30 (6 spec) — Faz 02-10 e2e akışları yeşil.
- GOOS=windows vet+build temiz (mail ailesi linux-only; protocol sabitleri platform-nötr).
- secret-scan temiz; buf CI'da (proto değişikliği bu fazda YOK — protokol additive değil, yalnız task şemaları).

## Güvenlik değerlendirmesi

- **Opt-in kapı + tipli redler:** kapalı aile her op'ta `unsupported_operation`; peer çekirdek-kimlikli UID allowlist (değişmedi).
- **Şifre disiplini korunur:** node-üretimi, `{SSHA}`-yalnız saklama, persist-öncesi seal (kind="mailbox"), tek-seferlik reveal, anahtarsız redaksiyon — birim kanıtlı.
- **Sessiz-bozulma sınıfı CI'da ele geçirildi:** key-only map, map okuma izinleri ve Maildir zinciri sahipliği hataları yalnız GERÇEK teslimde görünüyordu; beş-koşuluk gerçek-CI serisi bunları düzeltip birim regresyonlarla kalıcılaştırdı — CI'nın varlık gerekçesi bu sınıfın kanıtı.
- **Girdi saldırı yüzeyi:** tüm domain/local/adres/limit girdileri üç katmanda allowlist'li; shell interpolasyonu yok (fixed argv); yol/sahte-map girdisi payload'dan kabul edilmiyor.
- **Kalıntı riskler (dürüst):** DKIM imzalama yönetimi yok (UI açıklıyor); rspamd politika düzenlemesi yok; IMAP oturum yolu bu fazda test edilmez (IT-02); PTR/SPF doğrulaması dış DNS'tedir (UI dürüst beyan).

## Faz kabul kararı

**ACCEPTED** — 5/5 zorunlu kriter PASS (her biri file:line + komut/CI kanıtlı); kritik/yüksek açık bulgu yok (IT-01 bu koşuda düzeltildi; kalanlar düşük/bilgi).

## Sonraki faza geçiş

**READY** — tüm zorunlu kriterler PASS, kritik/yüksek bulgu yok; CI 14/14 yeşil (run 41396671925, commit 59535e6 = tag v0.12.0).
