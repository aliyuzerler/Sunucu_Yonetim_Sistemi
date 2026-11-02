# Changelog

## Unreleased

## [0.13.0] — 2026-11-02 — Faz 12: KVM/QEMU/libvirt sanallaştırma ve VPS

### Added
- **VM adaptörü (`agent/internal/vmops`, yalnız stdlib):** KVM/QEMU/libvirt — 16 typed işlem: `vm.preflight` (KVM varlığı vmx/svm+/dev/kvm, CPU flags, MemTotal/MemAvailable, storage pool'lar tip/boyut/boş alan/hedef, host köprüleri + libvirt ağları; KURULUM YAPMAZ — kriter 1), `vm.status`, `vm.list`, `vm.info` (uuid/vcpu/bellek/diskler/ağlar + guest_agent kanalı + konsol METADATA), `vm.create`, `vm.clone`, `vm.start`, `vm.stop` (graceful + auditli force), `vm.reboot`, `vm.delete`, `vm.disk.list` (format_verified + qemu-img check), `vm.disk.add`, `vm.snapshot.list/create/revert/delete`.
- **Opt-in kapı:** `--vm-enable` (VARSAYILAN KAPALI; kapalıyken tipli unsupported) — ADR-0012/0014/0015 genelleştirmesi; broker Config.VM + VMRunner hermetik arayüz; agent VmExecutor relay-only (MaxPayload 8 KiB); unix capability 67→83.
- **Kaynak limitleri (kriter 2):** kapalı aralıklar (vCPU 1..64, RAM 128..262144 MiB, disk 1..4096 GiB) üç katmanda doğrulanır; runner host gerçeklerini de denetler (MemTotal, pool boş alanı, köprü/ağ varlığı — ErrInsufficient); create/clone/disk.add başarısızlığında birim rollback'i; graceful stop sınırlı pencerede bekler, zaman aşımında tipli ErrConflict — node kendi kendine destroy'a yükseltmez (force=auditli açık karar).
- **Yol disiplini (kriter 3):** disk görüntü yolu payload'da ASLA yok — yollar libvirt çıktısından (dumpxml/vol-path) türetilir ve yalnız virsh/qemu-img argv'si olur; host mount (losetup/qemu-nbd/guestfish) yapısal olarak yok; managed `sc-` namespace: delete yalnız sc- birimlerini kaldırır, yabancı diskte tipli ErrForeignVolume ile TÜM silme reddedilir (VM tanımlı kalır); ad allowlist'leri traversal/enjeksiyonu reddeder; XML üretimi `xml.EscapeText` ile kaçırılır.
- **Snapshot/format (kriter 4):** iç snapshot'lar qcow2 kapısıyla — GERÇEK format `qemu-img info --output=json` ile doğrulanır (raw → tipli ErrDiskFormat; XML iddiası ≠ gerçek → ErrFormatMismatch); `vm.disk.list` format_verified + `qemu-img check` raporlar; revert non-idempotent/retry-güvensiz (ara durum tekrar revert'te ikinci kez diskard edilir).
- **Guest dürüstlüğü (kriter 5):** VM oluşturmak guest OS yönetimi SAĞLAMAZ — guest-agent kanalı domain XML'inde tanımlıdır ve `vm.info` varlığını raporlar; in-guest modüller agent kurulana dek unsupported (UI'da her zaman görünen dürüst not); VNC yalnız 127.0.0.1'e bağlanır, konsol yalnız metadata'dır (akış/proxy bu fazda yok).
- **UI:** VmPanel (preflight rozetleri + dürüst notlar, VM listesi + satır aksiyonları, limit ipuçlu create formu, clone formu, bilgi + disk doğrulama tablosu, yönetilen disk ekleme, snapshot yönetimi, force silme onayı) TR/EN +87 anahtar; start/stop/delete/create/clone/snapshot sonrası otomatik liste tazeleme.
- **Testler:** vmops 15 hermetik birim test (sahte STATEFUL virsh/qemu-img ikilileri: preflight parse, create XML şekli + define-rollback, traversal/limit negatifleri, graceful stop zaman aşımı + force, yabancı birim kapısı, kısmi silme kurtarma, qcow2 kapısı + format uyuşmazlığı, snapshot/disk/clone yaşam döngüleri, dir-olmayan havuz redleri, ErrUnavailable; ×count=2 stabil) + realhost kanıt testleri (EXPECTED_VM=1; libvirt yoksa CI'da BAŞARISIZ — CI-17 dersi); CP ops_faz12 validator + registry şekil testleri (99 op); broker üç-katman negatifleri (16 kapı + 20 şema negatifi + hata kategori eşlemesi); vitest VmPanel süiti (5 test; 40/40); Playwright `vm.spec.ts` (4 test; 34/34) + 4 ekran görüntüsü `docs/reviews/screenshots/phase-12/`.
- **CI:** yeni `agent-vm` job — runner'da GERÇEK libvirt+QEMU ile tam lifecycle kanıtı (pool → create → disk.add → gerçek qemu-img format doğrulaması → snapshot create/revert → start running → force stop → clone → delete; yönetilen birim silimi diskten doğrulanır) + GERÇEK yabancı-birim red kanıtı; KVM yoksa TCG (boot'suz SeaBIOS VM "running" kalır — determinist); EXPECTED_VM=1 sessiz atlanamaz; 15 job.
- **Dokümanlar:** ADR-0016 + `docs/architecture/virtualization.md` (güvenlik modeli tablosu, yaşam döngüsü/rollback, host gereksinimleri, test kanıtı) + deploy README (--vm-enable semantiği) + dependency-registry (yeni harici Go bağımlılığı YOK; libvirt LGPL-2.1-or-later / QEMU GPL-2.0-or-later çalışma-zamanı sistem bağımlılığı) + ADR index (ADR-0015 satırı Faz 11'den eksikti — bu fazda eklendi; IT-03).

## [0.12.0] — 2026-11-02 — Faz 11: E-posta servisi yönetimi

### Added
- **Mail adaptörü (`agent/internal/mailops`, yalnız stdlib):** Postfix + Dovecot + Rspamd tabanlı isteğe bağlı modül — 16 typed işlem: `mail.preflight` (ikili/sürüm/port/vmail/wiring taraması; KURULUM YAPMAZ — kriter 1), `mail.status`, `mail.domain.list/create/delete`, `mail.mailbox.list/create/delete`, `mail.alias.list/create/delete`, `mail.quota.set`, `mail.config.check` (postfix check + doveconf -n + rspamadm configtest), `mail.queue.list` (postqueue -j; gövde ve alıcı adresleri ayrıştırmada DÜŞÜRÜLÜR — kriter 5), `mail.limits.get/set` (6 kapalı-enum Postfix tunable, aralık doğrulamalı).
- **Opt-in kapı:** `--mail-enable` (VARSAYILAN KAPALI; kapalıyken tipli unsupported) — ADR-0012/0014 genelleştirmesi; broker Config.Mail + MailRunner hermetik arayüz; agent MailExecutor relay-only (MaxPayload 8 KiB); unix capability 51→67.
- **Syntax check + rollback değişmezi (kriter 4):** her mutasyon atomic write → postmap → `postfix check`/`doveconf -n` kapısı → başarısızlıkta byte-düzeyi geri alma → tipli ErrSyntaxCheck; reload best-effort ve dürüst `reloaded` bayrağı. Yönetilen yollar: `/etc/server-control/mail/` (domains/vmailbox/valias hash map'leri + dovecot-users 0600), marker-blok `/etc/postfix/main.cf`, marker parça `/etc/dovecot/conf.d/99-server-control.conf`, Maildir kökü `/var/vmail` (vmail 5000:5000 — yoksa tipli red).
- **Şifre disiplini:** mailbox şifresi node'da üretilir (crypto/rand 32 alnum), userdb'de YALNIZ {SSHA} hash; düz metin görev sonucunda bir kez döner ve agentsrv ResultScrubber AES-256-GCM seal edip db_credentials'e kind="mailbox" olarak ayırır (migration 0015 CHECK genişletmesi); TEK SEFERLİK reveal mevcut REST ucundan; CP_SECRETS_KEY ayarsızsa dürüst redaksiyon (kriter 2 — typed domain/mailbox/alias/quota işlemleri görev motoru + broker audit izinde).
- **Deliverability rehberi (kriter 3):** MailPanel'de SPF/DKIM/DMARC/PTR açıklama kartları + örnek DNS kayıtları + "panel dış DNS yönetmez" dürüst notu; DKIM imzalama yönetimi ve Rspamd politika düzenlemesi bu fazda dürüstçe kapsam dışı.
- **UI:** NodeDetail E-posta bölümü (MailPanel: preflight rozetleri + notlar, servis özeti, domain/mailbox/alias yönetimi, kota formu, tek-seferlik şifre reveal, içeriksiz kuyruk tablosu, kapalı-enum limit formu, config check rozetleri); TR/EN sözlük (+67 anahtar); DbFsPanel credential listesi kind="mailbox" filtresiyle mail paneline bırakıldı.
- **Testler:** mailops 22 hermetik birim test (sahte ikililerle tüm akışlar: apply/rollback/replay/izin/port/maskeli şifre; gofmt+vet temiz) + realhost kanıt testleri (EXPECTED_MAIL=1); CP ops_faz11 validator testleri + 83-op registry testi + mailbox scrubber birimleri; vitest MailPanel süiti (5 test); Playwright `mail.spec.ts` (4 test) + 4 ekran görüntüsü `docs/reviews/screenshots/phase-11/`.
- **CI:** yeni `agent-mail` job — runner'da GERÇEK Postfix+Dovecot ile tam lifecycle + sendmail→virtual→Maildir KONTROLLÜ TESLİM kanıtı + gerçek syntax-rollback kanıtı (EXPECTED_MAIL=1 sessiz atlanamaz — CI-17 dersi); 14 job.
- **CI gerçek koşu bulguları (run 41393010479→41396671925, beş koşuluk seri — her biri GERÇEK kanıt değeri):** CI-36 (e2e gerçek mod): mail.spec 1. test gerçek modda skip edilmeliydi — gerçek modda agent yok (panel dürüstçe görünmez) ve mail-01 node'u smoke'ın boş-liste ön koşulunu bozuyordu. CI-37: gerçek-host testi teslimden ÖNCE `postfix reload` u ZORUNLU kılıyor + zaman aşımında postqueue/postconf/mail.log tanıları basıyor (kök-neden CI'da görülebilir). CI-38: domains map KEY-ONLY satırları postmap derlenmiş hash'e ALMIYOR → domain virtual sınıfa girmiyor → SMTP+DNS bounce; yönetilen biçim `domain OK` (key+value) + key-only parse. CI-39: postfix virtual agent map'leri 'postfix' KULLANICISI olarak okur — BaseDir 0755 + .db 0644 sabitlendi. CI-40: MkdirAll ara dizinleri root yaratıyor — Maildir ZİNCİRİ (vmailBase→domain→localpart→Maildir→cur/new/tmp) tamamen 5000:5000 chown ediliyor. CI-41: test akışına AliasDelete-before-DomainDelete adımı (koruyucu çelişki reddi tasarım gereği doğruydu). BEŞİNCİ KOŞU 14/14 YEŞİL: run 41396671925 (commit 59535e6 = tag v0.12.0) — TestRealMailLifecycle + TestRealMailSyntaxRollback PASS (sendmail → virtual transport → Maildir KONTROLLÜ TESLİM GERÇEK kanıtı).
- **Dokümanlar:** ADR-0015 + `docs/architecture/mail.md` (yönetilen yollar, satır biçimi, güvenlik değişmezleri, dürüst sınırlar) + deploy README (--mail-enable semantiği) + dependency-registry (yeni harici Go bağımlılığı YOK; Postfix IPL-1.0 / Dovecot LGPL-2.1 / Rspamd Apache-2.0 çalışma-zamanı sistem bağımlılığı olarak kaydedildi).


## [0.11.0] — 2026-11-02 — Faz 10: Veritabanları ve güvenli dosya yöneticisi

### Added
- **Veritabanı yönetim adaptörü (`agent/internal/dbops`, yalnız stdlib):** PostgreSQL + MariaDB/MySQL — 11 typed işlem: `db.instance.list` (soket taraması + sürüm kanıtı; tespit, mutasyon yok), `db.database.list/create/delete`, `db.user.list/create/delete`, `db.grant.set` (desired-state: önce REVOKE sonra kapalı matris {readonly, readwrite, all}), `db.backup`, `db.backup.list`, `db.restore`. Tümü **yalnız node-local unix soketlerine** dokunur (`runuser -u postgres` peer auth / mysql root socket); payload şemalarında host/port/DSN ALANI YOKTUR — **panelin kendi PostgreSQL'i yapısal olarak erişilemez** (kriter 1). Tüm tanımlayıcılar katı allowlist RE ile doğrulanır (quoting belt + RE suspenders); sistem/panel veritabanları (`server_control`, `srvctl*`, template/`mysql`…) ve süper-user hesapları broker + runner katmanlarında **korumalı-ad tipli red** ile korunur (kriter 1).
- **Şifre yaşam döngüsü (kriter 2):** şifre NODE'da üretilir (crypto/rand, 32 alnum), task sonucunda bir kez taşınır ve `agentsrv.ResultScrubber` kalıcılaşmadan ÖNCE şifreyi **AES-256-GCM seal** edip `db_credentials` tablosuna ayırır (`internal/dbcred`; CP_SECRETS_KEY — Faz 09 disiplini). tasks.result / audit / outbox'ta asla düz metin yaşamaz; REST listesi hiçbir materyal döndürmez; **`POST /db-credentials/{id}/reveal` TEK SEFERLİKTİR** (race-free `WHERE revealed_at IS NULL RETURNING`; 2. çağrı tipli 409, audit'li). CP_SECRETS_KEY ayarsızsa şifre redakte edilir ve hiçbir yüzeye çıkmaz (redaksiyon retention'ı yener — fail-closed). `db.user.delete` kimlik satırlarını düşürür.
- **DB-native backup/restore (kriter 3):** `pg_dump` (tek MVCC snapshot) / `mysqldump --single-transaction --quick --no-tablespaces` (InnoDB tutarlı snapshot) akıtılarak (streaming) yazılır; her yedek dosyası **marker'lı** ilk satırla başlar, sha256+bytes raporlanır; ≥64 MiB boş yer + ≤200 dosya/kind dürüst tipli sınırlar; yarım dosya silinir (asla diskte kalmaz). Restore **yalnız marker'lı, yönetilen dizindeki, isim-allowlist'li** dosyayı kabul eder (`ErrForeignBackup` — keyfi dosya restore'u imkânsız).
- **Scoped dosya yöneticisi (`agent/internal/fsops`, yalnız stdlib + x/sys):** 5 işlem — `fs.list`, `fs.download`, `fs.upload`, `fs.chmod`, `fs.chown`. **Kökler yalnız webops site docroot'larıdır** (`webops.DocRoot`; raw path payload'a ASLA konmaz — host geneli browse yapısal olarak imkânsız, kriter 5). **Yarışsız dolaşım:** `openat` zinciri + her bileşende `O_NOFOLLOW` (ara symlink tipli red; ENOTDIR-ELOOP ayrımı Fstatat ile dürüst); açılan final nesnenin çekirdek-gerçek yolu **`/proc/self/fd` kapsam kanıtı** ile doğrulanır (TOCTOU swap'ları `ErrScope`); symlink'ler listelenir ama hiçbir işleme konu olmaz; **512 KiB** indirme/yükleme tavanı (`ErrTooLarge`); uzantı denylist (.php/.phtml/.phar/.sh/.key/.pem/…) + **magic-byte** (`#!`, ELF, MZ, class) + DetectContentType denylist'i (DetectContentType shebang'ı text/plain sayar — kendi magic denetimi eklendi); chmod ≤ 0777 (setuid/setgid/sticky yasak) fd-üzerinden; **chown hedefi payload'da yok** — site kullanıcısı türetilir (`webops.SiteUser`; yoksa `ErrNoSiteUser`), `webops.DocRoot` ihracıyla webops/fsops tek doğruluk kaynağında birleştirildi (kriter 4).
- **Broker:** db/fs aileleri allowlist'e strict şema doğrulamasıyla eklendi (16 op; korumalı-ad kontrolleri derinlemesine savunma olarak broker'da da); `--db-enable` / `--fs-enable` bayrakları (her biri VARSAYILAN KAPALI — ADR-0012 deseninin genelleştirmesi; kapalıyken tipli unsupported); **istek satırı sınırı 16 KiB → 1 MiB** (fs.upload base64 ≤ 768 KiB; kasıtlı, gerekçeli — peer çekirdek-kimlikli, semafor sınırlı); DBRunner/FsRunner arayüzleri (hermetik test); unix registry 32→**51 capability** (35→51 toplam).
- **Control plane:** `ops_faz10.go` (16 op spec + mirror validator'lar; registry 51→**67 operasyon** testi); RBAC `db:read`/`db:control`/`files:read`/`files:control` (migration **0014** — yalnız yeni izinler seed edilir (CI-04 dersi); admin hepsi, operator salt-okuma); `db_credentials` tablosu (UNIQUE (node,kind,username,host), upsert reveal penceresini resetler); agentsrv `ResultScrubber` (persist-öncesi secret redaksiyon kancası); REST `GET /nodes/{id}/db-credentials` (db:read) + `POST /db-credentials/{id}/reveal` (db:control, tek seferlik, audit'li).
- **UI:** NodeDetail'de **Veritabanları** bölümü (instance/database/user/backup listeleri, create/delete formları, grant matrisi, backup/restore) ve **Dosya yöneticisi** bölümü (kök+yol listesi, yükleme (dosya seçici + mod + overwrite), indirme (blob kaydet), chmod, chown); **şifre kartı**: task sonucunda düz şifre ASLA görünmez — yalnız credential_id + parmak izi; "Şifreyi göster (bir kez)" ile tek-seferlik reveal ve "ikinci kez gösterilemez" uyarısı; TR/EN sözlükler; db./fs. capability'ye göre bölüm gating.
- **Testler:** dbops birim (SQL üretim matrisi, korumalı adlar, yedek marker mekanikleri sahte ikililerle, dosya sınırı) + fsops **güvenlik negatif testleri** (traversal, symlink escape — dosya+dizin+iç, mutlak yol, boyut, MIME/magic, mod bitleri, olmayan site kökü, temp kalıntısızlık) + dbcred scrub birim testleri (her başarısızlık yolunda redaksiyon kazanır: anahtarsız/provenance-eksik/seal-hatası) + PG yaşam döngüsü (upsert → tek reveal → ikinci red → kullanıcı silme temizliği) + broker db/fs şema negatifleri + kapı-off tipli redleri; vitest `NodeDetailDbFs.test.tsx` (panel gating + şifre-gizlilik sözleşmesi + reveal akışı); Playwright `db-files.spec.ts` (3 test) + 4 ekran görüntüsü `docs/reviews/screenshots/phase-10/`; mock'a 16 op katalog + dbfs- şablonu + db/fs görev simülasyonları + **tek-seferlik reveal mock'u** (409 dahil).
- **CI:** yeni `agent-dbfs` job — runner'da GERÇEK PostgreSQL + MariaDB ile tam DB lifecycle (create → user+grant → backup → restore → delete; korumalı-ad gerçek redi; gerçek pg_dump çıktı doğrulaması) ve GERÇEK docroot üzerinde FS lifecycle (upload/download/chmod/chown + gerçek symlink-escape reddi); `EXPECTED_DB=1`/`EXPECTED_FS=1` gate'leri sessiz atlanamaz (CI-17 dersi). Toplam **13 job**.
- **Docs:** **ADR-0014** (panel-DB yapısal ayrımı, şifre seal+tek-reveal, marker'lı DB-native backup, openat+O_NOFOLLOW+proc-kanıtı dosya hapsi, 1 MiB broker sınırı gerekçesi, chunking erteleme kararı), `docs/architecture/db-files.md` (yerleşim, işlem tablosu, güvenlik modelleri, dürüst sınırlar), dependency-registry notu (**yeni harici bağımlılık YOK**), deploy README (`--db-enable`/`--fs-enable` + CP_SECRETS_KEY zorunluluğu), ADR dizini.

### Fixed
- **Faz 09'dan taşın hata (Faz 10'da saptandı):** `/dns-providers` ve `/certificates` REST route'ları Faz 09 implementasyonunda server.go'ya **hiç kaydedilmemişti** (yalnız import + Options/api alanları eklenmişti; constructor ataması da yoktu) — gerçek backend'de 404 döndürüyorlardı. e2e dns-ssl spec'i gerçek modda `test.skip` olduğu için CI yakalamamıştı. Düzeltme: route kayıtları + constructor alan atamaları tamamlandı; **regresyon testi** (`routes_regression_test.go`) rota varlığını auth katmanı üzerinden güvenceye aldı. Rapor: `docs/reviews/phase-10-test-report.md`.

### Fixed (CI gerçek koşusundan — run 41368133074)
- **CI-28 (gerçek-host kanıtı — restore izin hatası):** PostgreSQL restore'u `runuser -u postgres -- psql -f <path>` ile yapılıyordu; yedek dosyası root'a ait 0700 dizinde (0600) olduğundan postgres OS kullanıcısı dosyayı OKUYAMADI (TestRealDBLifecycle exit 1 — CI'nın varlık sebebi tam buydu). Düzeltme: dosya root tarafından açılır ve STDIN üzerinden psql'e akıtılır; izinler gevşetilmez (yedek 0600 kalır), veri node dışına çıkmaz.
- **CI-29:** `broker/dbfs_test.go`'da build tag eksikti — GOOS=windows vet `dbops.Instance` undefined (dbops.Instance linux-only). `//go:build unix` eklendi.
- **CI-30 (flake):** vitest DbFsPanel testinde `db-credentials` kartı async yüklendiği halde `waitFor` olmadan erişiliyordu — 3 koşuda 1 flake. `waitFor` eklendi; 6/6 stabil doğrulandı. Ders (IT-02): async yüklenebilecek HER kart waitFor ile beklenir.
- gofmt: son düzenlemelerden sonra üç dosya (ops_faz10.go, dbops/runner_linux.go, fsops/realhost_test.go) format dışı kalmıştı — CI gofmt kapısı yakaladı; `gofmt -l` her iki modülde temiz.

- **CI-31 (Windows gerçek koşusu — flake):** `TestPipeBrokerAllowAndDeny` deny yolu "No process is on the other end of the pipe" — server `writeResponse`→anında `Close` yarışı pipe instance'ını istemci okumadan yıkıyordu. `respond()`'a 50ms close-grace (red yolları nadir; unix'te zararsız).
- **CI-32 (gerçek-host kanıtı — PG DROP ROLE bağımlılığı):** grant→delete doğal yaşam döngüsünde `DROP ROLE`, role'un başka veritabanındaki yetkileri nedeniyle bağımlılık hatasıyla düşüyordu ("role cannot be dropped because some objects depend on it"). PostgreSQL standart temizlik prosedürü uygulandı: her bağlanılabilir veritabanında `DROP OWNED BY` (yetkiler + varsayılan ACL'ler; rol nesne sahibiyse tipli `ErrConflict`), ardından `DROP ROLE`. `pgDatabases` + `queryDB` (-d <db>) yardımcıları eklendi.
- **CI-33 (gerçek koşu — `-race` altında patlayan fd hatası):** `fs.list`'te final=="" yolunda parentFD ve dirFD AYNI fd'yi çift kapatıyordu — fd-reuse ile ilgisiz bir fd kapanıp EBADF üretiyordu (yerelde sessiz, CI `-race` + zamanlama altında `TempDir cleanup: bad file descriptor`). fd sahipliği tek yerde toplandı (tek close + koşullu parentFD close) ve `-count=3` ile doğrulandı.
- **CI-34 (gerçek-host kanıtı):** MySQL/MariaDB'de hiç grant verilmemiş kullanıcı için `REVOKE ... ON db.*` ERROR 1141 fırlatıyor — desired-state'in İLK uygulaması bozuluyordu (ilk iki koşuda postgres aşaması erken düştüğü için yüzeye çıkmamıştı). Runner artık `information_schema.SCHEMA_PRIVILEGES` probe'uyla mevcut db-yetkisi yoksa REVOKE'u atlar ("yetkisiz" istenen durum zaten sağlıdır); kaçışlı grantee literal'i allowlist-RE doğrulamasıyla birlikte kullanılır. Hermetik iki-alt-test (grants-exist / no-grants) eklendi.
- **CI-35 (gerçek-host kanıtı — probe literal kaçışı):** CI-34 probe'unun grantee literal'inde iç '@' tırnağı kaçırılmıştı ('''user'@'host''' — bozuk SQL → istemci exit 1). Değer 'user'@'host' için doğru literal '''user''@''host''' (her içerik tırnağı ikiye katlanır); hermetik test beklentisi de gerçek SQL biçimine güncellendi.


### Security
- Panelin kendi PostgreSQL'i ile yönetilen müşteri instance'ları **şema düzeyinde** ayrıdır (host/port/DSN alanı yok; yalnız node-local soket).
- DB şifreleri node'da üretilir, GCM bütünlük kanıtlı seal ile saklanır, **tekrar asla düz metin gösterilmez** (tek-seferlik reveal; audit'li; 2. deneme tipli 409).
- Dosya kapsamı filtre değil **türetme** ile çalışır: kök = site adı → docroot; openat+O_NOFOLLOW ve /proc fd kanıtıyla TOCTOU/symlink-escape tipli reddedilir.
- Restore yalnız marker'lı yönetilen dosyayı kabul eder — keyfi dosya/arbitrary-path restore'u yapısal olarak yoktur.
- db.* mutasyon yüzeyi `--db-enable`, fs.* `--fs-enable` ile opt-in; her iki aile de kapalıyken tipli red verir.

## [0.10.0] — 2026-11-02 — Faz 09: Domain, DNS, reverse proxy ve SSL

### Added
- DNS sağlayıcı adaptör katmanı (`internal/dns`, yalnız stdlib + pgx): `Provider` arayüzü (Capabilities/Zones/Records/ApplyChange — kayıt CRUD + kabiliyet bildirimi, kriter 1), kapalı kayıt türü kümesi {A, AAAA, CNAME, TXT, MX}, TTL 60–86400 sağlayıcı-beyanlı sınırlar, wildcard `*.sub` desteği, tür-bazlı değer doğrulaması (IPv4/IPv6 ayrıştırıcı, CNAME/MX hedef allowlist'i). Bu fazda desteklenen tür dürüstçe **yalnız `mock`** (in-memory; DNS-01 TXT saklar) — Cloudflare/Route53 bilinçli olarak kapsam dışı, bilinmeyen tür `ErrKindUnknown` ile reddedilir (tahmin yok).
- Sağlayıcı kimlik bilgileri **AES-256-GCM şifreli at-rest** (`internal/secrets`): `dns_providers.credentials_encrypted` yalnız seal'lı blob; anahtar `CP_SECRETS_KEY` (base64 32 bayt) ile operatörce sağlanır, PostgreSQL'de asla yaşamaz, ayarsızda tipli red. REST okumaları ve UI kimlik bilgisi DEĞERİ asla döndürmez (write-only form; submit sonrası alan sıfırlanır) — kriter 5.
- DNS kayıt değişikliği disiplini (kriter 2): `dns.record.diff` **apply öncesi zorunlu önizleme** (before/after anlık görüntüsü), `dns.record.apply` idempotent upsert + TTL capability doğrulaması + before/after'lı task sonucu (denetim izi), `dns.record.delete` non_idempotent + retry-güvensiz; tümü control-plane hedefli typed operasyon (worker Exec) — task/RBAC/audit çerçevesi aynen.
- ACME sertifika yaşam döngüsü (`internal/acmex`, `golang.org/x/crypto/acme` RFC 8555 — YENİ modül YOK): **DNS-01 only** orchestration (TXT'ler sağlayıcı adaptörü üzerinden `_acme-challenge.<domain>`), CSR node'dan gelir, hata sınıflandırma yüzeyi `rate_limit` (429 + Retry-After) / `challenge` / `order_invalid` / `csr_invalid` / `network` / `acme_other` — hepsi sertifika kaydına yazılır ve UI'da görünür (kriter 3). **İptal hesap anahtarıyla** (RFC 8555 §7.6) elle JWS imzalanarak yapılır; `alreadyRevoked` idempotent kabul edilir. Hesap anahtarı control-plane host'unda dosya (0600, PostgreSQL'de asla).
- Sertifika durum makinesi (`internal/certmgmt`): `pending → issuing → issued → (renewing → issued | renewal_failed) → revoked/expired` kapalı kümesi; başarısızlık `last_error` + sınıf + `retry_after_secs` olarak kayda işlenir; `expired` hem okumada türetilir hem saatlik watcher ile kalıcılaşır; 30 gün altı `expiring_soon` rozeti. **Başarısız yenileme canlı materyali bozmaz** — `renewal_failed` kaydında sealed PEM korunur (kriter 4 depo katmanı).
- **TLS özel anahtarı node'da doğar, node'dan asla çıkmaz** (ADR-0013; ADR-0002 ilkesinin genelleştirmesi): agent `internal/tlsops` (yalnız stdlib) — `web.cert.request` (ECDSA P-256 anahtar üretimi, privkey 0600, yalnız CSR döner), `web.cert.install` (public PEM: parse/expiry/SAN-coverage/**çift eşleşme** doğrulaması → sahne yazımı → rollback kopyası → atomik rename → **nginx -t kapısı** → kapı geçmeden reload yok; kapı başarısızsa önceki sertifika geri yüklenir ve sistem yeniden doğrulanır), `web.cert.status` (diskteki sertifikaların not_after/parmak izi/anahtar varlığı). Kurulum alanı `/etc/nginx/ssl/srvctl/<domain>/` (privkey.pem 0600, fullchain.pem 0644).
- Broker: TLS ailesi allowlist'e strict şema doğrulamasıyla eklendi (3 op); `--web-enable` kapısı web ailesiyle PAYLAŞILIR (ADR-0012 genelleştirmesi; kapalıyken tipli red); unix registry 32→**35 capability**; `server-privd` TLS runner wiring.
- Control plane: 8 operasyon spec'i (`ops_faz09.go`: 5 dns + 3 cert; +3 node cert op), RBAC `dns:read`/`dns:control`/`cert:read`/`cert:control` (migration 0013 — yalnız yeni izinler, CI-04 dersi), registry 40→**51 operasyon** testi, REST `/dns-providers` (GET/POST/DELETE — kimlik bilgisi asla geri gelmez; audit'li) ve `/certificates` (GET liste/detay), config'e `CP_SECRETS_KEY`/`CP_ACME_*` doğrulaması, main wiring + sertifika süre izleyicisi.
- UI: **DnsPage** (sağlayıcı tablosu kabiliyet rozetleriyle + write-only ekleme formu + silme onayı; bölge/kayıt tarayıcı görev poll'lü; kayıt formu TTL doğrulamalı; **diff önizleme kartı** before/after ile; apply/delete görev zinciri) ve **SslPage** (sertifika tablosu durum rozetleri + rate_limit/Retry-After gösterimi + expiring_soon; **üç adımlı üretim sihirbazı** web.cert.request→cert.issue→web.cert.install görsel adım rozetleriyle; yenile + gerekçe-seçimli iptal); TR/EN sözlükler; nav'da DNS/SSL artık gerçek sayfa (ComingSoon kaldırıldı).
- Testler: vitest `DnsSsl.test.tsx` (6 test — kimlik bilgisi gizliliği, write-only form reset'i, sınıflandırılmış hata görünürlüğü), Playwright `dns-ssl.spec.ts` (3 test) + 6 ekran görüntüsü `docs/reviews/screenshots/phase-09/`; mock'a Faz 09 op kataloğu (11 op) + DNS/cert REST + zengin görev sonuçları + demo `renewal_failed` sertifika.
- CI: `agent-web` job'a **Faz 09 TLS gerçek lifecycle kanıtı** eklendi (anahtar üretimi + self-signed kurulum + GERÇEK nginx -t kapısı + reload + eşleşmeyen anahtar reddi ile canlı sertifika koruması; `EXPECTED_TLS=1` gate — CI-17 dersi, sessiz atlama yasak). Toplam 12 job.
- Docs: **ADR-0013** (control-plane'de DNS adaptörü, DNS-01 orchestration, anahtar-node'da, hesap-imzalı revoke, CSR-yeniden-kullanım trade-off'u), `docs/architecture/dns-ssl.md` (yerleşim, op tablosu, dürüst destek matrisi, durum makinesi, güvenlik modeli, config), dependency-registry (x/crypto/acme notu — YENİ harici bağımlılık YOK), ADR dizini, deploy README (`CP_SECRETS_KEY`/`CP_ACME_*` + `--web-enable` TLS kapsamı).
- E2E/PG entegrasyon testleri: sahte ACME sunucusu (`internal/acmex/fakeca` — test çifti, JWS doğrulamasız, açıkça belgelenmiş) ile issue/rate-limit/challenge/revoke uçtan uca; certmgmt PG akışı (issue→renewal_failed rate_limit→başarılı yenileme→revoke + watcher expired geçişi) TEST_DATABASE_URL ile CI'da koşar.

### Security
- TLS özel anahtarları yalnız node'da doğar ve asla iletilmez; kurulum öncesi çift-eşleşme kanıtı zorunlu (yanlış node'un sertifikası diskte hiçbir değişiklik olmadan tipli reddedilir).
- Sağlayıcı kimlik bilgileri GCM bütünlük kanıtlı şifreli; okuma yüzeyi yok (write-only sözleşme testle doğrulanır).
- nginx reload yalnız `nginx -t` kapısından geçtikten sonra; başarısız kurulum/yenileme canlı sertifikayı ve sistem tutarlılığını korur (rollback + yeniden doğrulama).
- cert.issue/renew/revoke rate limit'i gizlemez: sınıf + Retry-After kayda ve UI'ya işlenir; rate limit klasik "sessiz yeniden deneme" ile maskelenmez (MaxRetry=2 üst sınırı).

## [0.9.0] — 2026-11-02 — Faz 08: Web hosting ve uygulama dağıtımı

### Added
- Web hosting adaptörü (`agent/internal/webops`, yalnız stdlib): nginx + PHP-FPM site yaşam döngüsü — site create/update/disable/delete **typed işlemler olarak gerçek yürütülür** (broker-tekil, `--web-enable` opt-in; ADR-0012). Site başına sistem kullanıcısı izolasyonu (`sc-web-<site>`, nologin, `--system --user-group`), docroot 0750 (sahip site kullanıcısı, grup www-data) — site kullanıcıları birbirinin dosyasını okuyamaz.
- Config disiplini: ilk satır `MarkerHeader`; markersız dosya `ErrForeignConfig` ile reddedilir (**mevcut config üzerine kör yazım imkânsız** — kriter 5). Yazım temp+rename **atomiktir**; önceki içerik daima `.rollback` kopyası olarak tutulur (kriter 3). Reload YALNIZ `nginx -t` (syntax) VE `nginx -T` (include kanıtı) geçince sinyallenir; kapı başarısızsa rollback geri yüklenir, reload yapılmaz.
- PHP-FPM pool + sürüm matrisi: kapalı küme {8.2, 8.3, 8.4, 8.5} — matris dışı `ErrPHPUnsupported`, kurulu değilse `ErrPHPNotInstalled` (tahmin yok; kriter 4). Pool site kullanıcısı altında çalışır; socket ACL yalnız www-data'ya (0660); `disable_functions` havuzu arındırılır; reload `php<v>-fpm.service` birimine.
- Port çakışması/edge-proxy yönetimi: `/proc/net/tcp{,6}` + inode eşlemesiyle dinleyici **sahiplik** tespiti — nginx'e ait portta vhost serbest, Apache (apache2/httpd) sahipliği tipli `ErrPortConflict` + edge-proxy önerisi, sahipsiz dinleyici güvenli tarafta red. Apache opsiyonel driver olarak tasarlandı (adapter ayrı fazta).
- Node.js/Python dağıtımı: `backend=proxy` — nginx reverse-proxy **yalnız loopback** hedefe (127.0.0.1:port, [::1]:port, unix:/run/...); dış hedef SSRF guard ile reddedilir. Uygulama runtime yönetimi dürüst olarak sonraki faza bırakıldı (web-hosting.md §Uygulama dağıtımı).
- site.delete **dosya-koruyucu**: docroot ve kullanıcı verisi ASLA silinmez (`docroot_preserved=true`); yalnız broker'ın kendi marker'lı config/pool dosyaları (önce `.rollback`) + türetilen sistem kullanıcısı kaldırılır (ADR-0012).
- Broker: web ailesi allowlist'e strict şema doğrulamasıyla eklendi (site adı allowlist, kapalı backend enum, php matrisi, proxy hedef kalıpları); `WebRunner` arayüzü (hermetik testler için); `server-privd --web-enable` bayrağı (VARSAYILAN KAPALI — gerçek mutasyon opt-in); kapalıyken tipli `unsupported_operation`.
- Control plane: 7 operasyon spec'i (`ops_faz08.go`); YENİ izinler `web:read` / `web:control` — yalnız yeni izinler seed edilir (migration 0012, CI-04 dersi); operator salt-okuma; create/update/disable idempotent + retry-safe, delete `non_idempotent` + retry-güvensiz; registry 33→**40 operasyon** testi.
- UI: NodeDetail Web hosting bölümü — site listesi tablosu, PHP sürüm matrisi paneli, backend'e göre alan gösteren create/update formu (static/php/proxy), durum/devre dışı bırak/sil ("document root KORUNUR" etiketi); görev sonucu poll edilip `web-report` kartında gösterilir; TR/EN sözlükler.
- E2E: Playwright `web-hosting.spec.ts` (3 test) + `docs/reviews/screenshots/phase-08/` ekran görüntüleri (4 adet) repoda; mock'a web host şablonu (`web-` öneki) ve 7-op katalog + zengin görev sonuçları eklendi.
- CI: `agent-web` job — runner'da GERÇEK nginx + php8.3-fpm ile tam lifecycle kanıtı (create→update→disable→delete, rollback dosyası varlığı, docroot korunması); `EXPECTED_WEB=1` gate'i sessiz atlanamaz (CI-17 dersi). Toplam 12 job.
- Docs: ADR-0012 (broker-tekil opt-in yürütme, dosya-koruyucu delete, marker config disiplini), `docs/architecture/web-hosting.md` (destek matrisi + güvenlik modeli + dürüst sınırlar), dependency-registry notu (yeni harici bağımlılık YOK), deploy README (`--web-enable` semantiği), ADR dizini güncellendi.

### Fixed (bağımsız test koşusunda)
- IT-01: disabled site `update` akışında eski `.disabled` config yedeksiz siliniyordu — silmeden önce `disabledPath + .rollback` kopyası yazılır ve sonuçta raporlanır; byte-düzeyi regresyon testi (TestUpdateDisabledSiteKeepsRollback). Rapor: `docs/reviews/phase-08-test-report.md` (5/5 kriter PASS, ACCEPTED/READY; CI 12/12 run 41346840245).

### Fixed (CI agent-web job kanıtından)
- CI-19: `ensureSiteUser` kullanıcı home dizinini `paths()` sırası yüzünden **confDir'den** türetiyordu — `useradd --create-home` nginx config dizini altına site dizini açıyordu (gerçek runner kanıtı: `/etc/nginx/conf.d/srvctl-citest` dizini + `rm: Is a directory`). Home artık `webRoot/<site>`; regresyon testi useradd argv'sini kesin doğrular.

### Security
- Web ailesi VARSAYILAN KAPALI: gerçek sistem mutasyonu (useradd/config yazımı/reload) yalnız `--web-enable` ile açılır; operatör bilinçli seçim yapar (ADR-0012).
- Site adı tek tohumdur: kullanıcı/yol/docroot yalnız allowlist'li addan türetilir; docroot override yoktur (path traversal yapısal olarak imkânsız).
- Kör yazım yasağı marker kanıtıyla zorlanır; syntax check + include kanıtı olmadan reload yok; her değişiklikte rollback kopyası.
- proxy hedefi yalnız loopback/unix (SSRF guard); argv disiplini (useradd/nginx/systemctl sabit vektör); tüm broker istekleri JSONL audit.

## [0.8.0] — 2026-11-02 — Faz 07: Docker Engine ve Compose yönetimi

### Added
- Docker Engine adaptörü (`agent/internal/dockerx`, yalnız stdlib): Engine API istemcisi unix socket üzerinden — `/_ping` Api-Version pazarlığı, pencere [1.24, 1.47]; daemon daha eskiyse tipli `ErrUnsupportedAPI` (tahmin yok), daha yeniyse 1.47 clamp; istekler `/v{maj}.{min}/` önekli; çıktı sınırlı (liste 1 MiB, log 64 KiB + dürüst truncation; çoklamalı log akışı ayrıştırıcı — TTY ham akış fallback'i ile).
- Docker işlem ailesi (15 op): container list/inspect/logs/start/stop/restart, image/network/volume listeleri — gerçek yürütme; **container/image/volume/network remove + compose.deploy PREVIEW-ONLY (ADR-0011)**: Engine `DELETE` çağrısı ve CLI eşdeğeri metin olarak döner, `Executed=false` değişmezi; `*dockerx.Client`'ın remove metodu YOKTUR (derleyici düzeyinde invariant). Inspect çıktısında `Config.Env` KASITLI dışlanır (secret redaksiyonu).
- Compose analizi (`agent/internal/composeops`, agent-local SAF): `docker.compose.validate` şema doğrulama + 14+ policy kuralı (privileged, tehlikeli cap_add, host network/pid/ipc, docker.sock bind, hassas dizin bind, public port, gizli-anahtar görünümlü env ANAHTARI — değer asla kopyalanmaz, root user, devices, ayrıcalıklı port, env_file, no-restart, writable-rootfs); `docker.compose.deploy` yalnız sıralı aksiyon planı döndürür (`Executed=false`). Girdi sınırları: 64 KiB, NUL reddi, services ≤64, findings ≤100; depends_on bilinmeyen/döngü reddi.
- Broker: docker ailesi allowlist'e strict şema doğrulamasıyla eklendi (ref allowlist `..` reddiyle, tail/timeout sınırları); `DockerRunner` arayüzünde remove metodu yok (preview-only interface invariant); `server-privd --docker-socket` bayrağı (boş = docker ailesi kapalı); agent `--docker-socket` probe bayrağı (agent sokete BAĞLANMAZ).
- Control plane: 15 operasyon spec'i (`ops_faz07.go`); YENİ izinler `docker:read` (liste/inspect/logs/önizleme/compose), `docker:control` (start/stop/restart) — yalnız yeni izinler seed edilir (migration 0011, CI-04 dersi); operator salt-okuma; restart `non_idempotent` + retry-güvensiz; registry 18→**33 operasyon** testi.
- UI: NodeDetail Docker bölümü (docker capability'li node'larda) — list butonları, container ref formsu (inspect/logs/start/stop/restart, istemci ref allowlist), silme önizlemesi ("çalıştırmaz" etiketi), compose YAML alanı; compose görev sonucu poll edilip **bulgular severity renkleriyle gösterilir**; deploy planı aksiyon listesi + "hiçbir kaynak oluşturulmadı" notu; TR/EN sözlükler.
- E2E: Playwright `docker.spec.ts` (3 test) + `docs/reviews/screenshots/phase-07/` ekran görüntüleri repoda; mock'a docker host şablonu (`docker-` öneki), 15-op katalog, tek-görev GET ucu ve compose sonuç raporu simülasyonu eklendi.
- CI: `agent-docker` job — runner'ın kendi daemon'ı üzerinde disposable busybox container ile GERÇEK lifecycle kanıtı (list/inspect/logs/stop/start/restart); `EXPECTED_DOCKER=1` gate'i sessiz atlanamaz (CI-17 dersi).
- Docs: ADR-0011 (broker-tekil erişim + preview-only silme/deploy), `docs/architecture/docker-support.md` (destek matrisi + policy kural kataloğu + dürüst sınırlar), dependency-registry'ye `gopkg.in/yaml.v3 v3.0.1` (MIT), ADR dizini güncellendi.

### Security
- Docker daemon erişimi YALNIZ privileged broker'da (root, unix socket; TCP yüzeyi yok — ADR-0011); agent yalnız probe için socket VARLIĞINA bakar, bağlanmaz.
- Silme ve compose deploy yürütme yüzeyi kodda YOKTUR (preview-only; onay akışlı yürütme ayrı faz + yeni ADR).
- Üç katmanlı doğrulama docker ailesine taşındı: control-plane validator → agent executor → broker `validateRequest` (hepsi strict JSON + kapalı enum + ref allowlist).
- Secret redaksiyonu: inspect env yok; compose bulgularında yalnız anahtar adları; log 64 KiB sınırı.
- Yeni bağımlılık: `gopkg.in/yaml.v3 v3.0.1` (MIT) — tek yeni bağımlılık; lisans/SDK kaydı dependency-registry'de.

### Fixed (bağımsız test koşusunda)
- CI-18 (orta, test altyapısı): `TestCapabilityProbing` docker ailesi probe gerekçesi için tek dal varsayıyordu; docker'lı runner'larda socket VAR → dürüst gerekçe "broker_unreachable" oluyordu ve test kırmızıydı (run 41340951932). Test iki dürüst dalı da kabul edecek şekilde düzeltildi (`fdeb311`); unavailable SONUCU her ortamda değişmez. Doğrulama: CI run 41341230025 **11/11 success** (agent-docker gerçek lifecycle kanıtı dahil). Rapor: `docs/reviews/phase-07-test-report.md` (ACCEPTED/READY).

## [0.7.0] — 2026-11-01 — Faz 06: Ek Linux dağıtımı adaptörleri (Rocky Linux + AlmaLinux)

### Added
- Distro sınıflandırma + AÇIK destek matrisi (`agent/internal/osdetect`): `SupportedDistros` — ubuntu 22/24, debian 12/13, **rocky 9/10, almalinux 9/10**; aile yalnız `ID=`'den gelir, `ID_LIKE` yalnız kaba `debian-like`/`rhel-like` etiketi üretir ve ASLA `supported` döndürmez; katı majör ayrıştırma (9.5→9, 24.04→24); matris dışı majör `untested`, bilinmeyen distro `unknown` — yanlış pozitif yok (kriter). Rocky/Alma os-release fixture'ları + yanlış-pozitif test seti.
- Salt-okuma paket envanteri (`agent/internal/pkgops`): `linux.pkg.list` — deb ailesi `dpkg-query -W -f=…`, rhel ailesi `rpm -qa --qf=…` (SABİT argv; 1 MiB çıktı sınırı; limit 1..500; kontrol-karakter yankı koruması). Kurulum/kaldırma/güncelleme yüzeyi YOK (belgeli kapsam kararı).
- Firewall adaptörü (`agent/internal/fwops`): dürüst backend tespiti (firewalld→ufw→nftables→iptables→none, `Reason` ile), backend'e göre sınırlı salt-okuma kural listesi (firewalld aktif zon başına `--list-all` ≤16 zon; 64 KiB/400 satır), zon adı katı allowlist. **`linux.firewall.rule.preview` YALNIZ ÖNİZLEME (ADR-0010):** komutları veri olarak döndürür, hiçbir şey çalıştırmaz; firewalld/ufw şablonları, nftables/iptables dürüst ret (`ErrPreviewUnsupported`).
- Broker: 4 yeni op allowlist'e eklendi (strict şema doğrulaması — preview payload'ı kapalı enum'lar + sınırlı int'ler); unix registry 10 capability; `SurveyExecutor` broker-arka yüzeyli; server-privd Pkg/Firewall runner'ları bağladı.
- Control plane: 4 salt-okuma operasyon spec'i (`ops_faz06.go`); YENİ izinler `pkg:read`, `firewall:read` (yalnız yeni izinler seed edilir — migration 0010, CI-04 dersi); `node_system_facts` +4 kolon (distro_family/distro_like/support_state/support_reason).
- Protokol: `SystemFactsReport` +4 additive alan (13–16) — buf lint + deterministik üretim; sözleşme geriye dönük uyumlu.
- UI: destek durumu rozeti (destekleniyor / desteklenmiyor(?) + gerekçe), dağıtım ailesi fact'i, linux node'larda salt-okuma dağıtım işlemleri bölümü (paket limit formu, firewall durum/kurallar butonları, kural önizleme formu + "çalıştırmaz" etiketi); TR/EN sözlükler; 2 yeni vitest paketi (4 test).
- E2E: Playwright `linux-distro.spec.ts` (3 test) + `docs/reviews/screenshots/phase-06/` ekran görüntüleri repoda; mock `/e2e/reset` önkoşul ucu + `/e2e` proxy'si (spec'ler sıra-bağımsız).
- CI: `agent-rhel` job — disposable **privileged systemd container**'lar (rockylinux:9, almalinux:9) içinde GERÇEK kanıt: `TestRealHostSmoke` (os-release/matris/dnf/systemd; gate'li ama mismatch'te SESSİZ ATLANMAZ), gerçek rpm envanteri ayrıştırma, gerçek firewalld durum+kural+önizleme sondağı.
- Docs: ADR-0010 (firewall preview-only), `docs/architecture/linux-support.md` (açık distro/majör matrisi + yanlış pozitif engel kuralları + dürüst sınırlar).

### Security
- Kural değişikliği yüzeyi YOKTUR (ADR-0010): önizleme `Executed=false` değişmezi; firewalld/ufw komut şablonları sabit, zon adı `^[A-Za-z0-9_.-]{0,32}$` allowlist'li.
- Paket envanteri salt-okuma; çıktı sınırlı (500/1 MiB); alan yankısı kontrol-karakter korumalı.
- Üç katmanlı doğrulama yeni op'lara taşındı: control-plane validator → agent executor → broker `validateRequest` (hepsi strict JSON + kapalı enum).
- Gerçek-host smoke testleri gate'siz sessiz atlanamaz: gate konmuşsa host eşleşmezse test BAŞARISIZ olur (kanıt dürüstlüğü).

### Fixed (bağımsız test koşusunda)
- IT-01 (orta, test altyapısı): e2e `openRocky01`/`openWin01` yardımcıları node listesi yerleşmeden `row.count()` yapıyordu → ara sıra çift `rocky-01`/`win-01` oluşturuluyor ve strict mode ihlali flaky kırmızı üretiyordu (yerelde ~1/3 koşu; bağımsız Faz 06 test koşusunda yakalandı, yeniden üretildi). Satır sayımından önce `Yükleniyor…` göstergesinin kaybolması bekleniyor (`linux-distro.spec.ts`, `windows-node.spec.ts`); doğrulama: 6 ardışık tam koşu 14/14 = **84/84 yeşil**. Kanıt ve değerlendirme: `docs/reviews/phase-06-test-report.md` (ACCEPTED/READY).

## [0.6.0] — 2026-11-01 — Faz 05: Windows Server Agent ve native adaptörler

### Added
- Windows agent (`windows/amd64`): aynı agent çekirdeği (outbound mTLS, hello/heartbeat/capability/facts/metrics, tipli operasyonlar) — platform-ayrık adaptörler build tag'leriyle (`detect_windows.go`, `collect_windows.go`, `registry_windows.go`, `daemon_windows.go`); gRPC sözleşmesi DEĞİŞMEDİ (buf breaking gate ile korumalı).
- SCM adaptörü (`agent/internal/winscm`): list/status/start/stop/restart `golang.org/x/sys/windows/svc` + `svc/mgr` yapılandırılmış API ile (sc.exe çıktı ayrıştırma YOK); en az ayrıcalıklı SCM erişimi (ENUMERATE|CONNECT); stop→state poll→start; restart non-idempotent + retry-güvensiz; servis KEY adı allowlist `^[A-Za-z0-9][A-Za-z0-9 ._-]{0,79}$` (sondaki nokta/boşluk reddi).
- Event Log okuyucu (`agent/internal/winevent`): `wevtutil query-events <kanal> /count:N /rd:true /format:xml` SABİT argv şablonu; kanal kapalı enum (Application/System/Setup — Security tasarımdan hariç), seviye istemci tarafında filtre, çıktı `encoding/xml` ile sınırlı olaylara ayrıştırılır (`winops.ParseWevtutilXML`, tüm GOOS'larda fixture-testli); XPath/PowerShell YOK.
- Windows broker: `server-privd` LocalSystem olarak yalnız `\\.\pipe\server-control-privd` dinler (SDDL `D:P(A;;GA;;;SY)(A;;GA;;;BA)` + agent SID); çağırıcı kimliği `ImpersonateNamedPipeClient` + thread-token SID ile çekirdek garantili; agent pipe'ı `SECURITY_IDENTIFICATION` ile açar; `--allow-sid` açık allowlist (default deny), her ret audit'li. Unix-dışı taşıyıcılar `identifyPeer`'de reddedilir (Faz 04 IN-01 bulgusu bu vesileyle giderildi).
- Çapraz-platform şema paketi (`agent/internal/winops`): SCM/eventlog payload şemaları, doğrulayıcılar, wevtutil XML ayrıştırıcı — her GOOS'ta birim testli.
- Windows metrics: GetSystemTimes (CPU pencere %), GlobalMemoryStatusEx, GetDriveType+GetDiskFreeSpaceEx+GetVolumeInformationW (yalnız FIXED hacimler), GetIfTable (MIB_IFROW sabit offset ayrıştırma, fixture-testli), GetTickCount64; facts registry'den (ProductName/DisplayVersion/CurrentBuild+UBR) — YENİ harici bağımlılık YOK (x/sys zaten direct).
- Agent/ hizmet olarak Windows: `server-agent svc-run` (svc.Handler; StartPending→Running→StopPending) ve `server-privd` SCM farkındalığı; `deploy/agent-windows/README.md` sc.exe kurulum + doğrulama listesi + kaldırma.
- Control plane: 6 yeni operasyon (`windows.scm.service.list/status/start/stop/restart` — okuma `service:read`, kontrol `service:control`; `windows.eventlog.read` — YENİ `eventlog:read` izni); migration `0009_windows_ops` (yalnız YENİ izin seed'i — CI-04 dersi); şemalar `agent/internal/winops` ile ayna.
- UI: NodeDetail Windows-farkındalıklı — SCM başlığı/etiketi/placeholder, `{service}` payload'ı, istemci SCM allowlist doğrulaması; Windows node'larda Event Log kartı (kanal seçimi — Security enum'da yok, seviye, olay sayısı 1-200); TR/EN sözlükler; 3 yeni vitest.
- CI: `agent-windows` job (windows-2025: gerçek SCM salt-okuma, gerçek named pipe + SID izin/ret, wevtutil System sondası, Win32 metrics smoke, registry facts); linux agent job'una `GOOS=windows` çapraz derleme kontrolü.

### Changed
- `agent/internal/osdetect` ve `internal/metrics` platform-ayrık dosyalara bölündü (Facts/Collector/Snapshot tipleri paylaşımlı kaldı; `applyCPU` ortak pencere mantığı).
- `broker.Client` platform-ayrık dial (`dial_unix.go`/`dial_windows.go`); `broker.Config` per-OS (AllowedUIDs / AllowedSIDs).
- `e2e/mock-api.mjs`: windows telemetri şablonu (`win-` öneki / os_family=windows) + windows operasyon kataloğu.
- `docs/decisions/README.md`: ADR-0009 dizine eklendi.

### Security
- PowerShell yürütme yüzeyi YOK (kalıcı tasarım reddi — ADR-0009); hiçbir yetenek komut yürütme kabulü sunmaz.
- Named pipe: yalnız SYSTEM/Administrators/agent SID; çağırıcı kimliği çekirdek doğrulamalı (PID yeniden kullanım yarışı yok); agent kimliği IDENTIFICATION düzeyinde — broker asla agent kimliğine hareket edemez.
- Üç katmanlı allowlist Windows'a taşındı: sunucu validator → agent executor → broker validateRequest.
- Windows testleri CI'da unprivileged kullanıcı ile koşar; gerçek servis kontrolü mock'la sınanır; gerçek host doğrulaması disposable VM prosedürüyle sahibine belgelendi.

### Fixed (bağımsız test koşusunda)
- CI-13 (Yüksek, test altyapısı): gerçek `wevtutil` çıktısı `<Events>` köküyle SARMALAMAZ — art arda `<Event>` kök öğeleri basar. `winops.ParseWevtutilXML` sarmal varsaydığından windows-2025 CI smoke sondası `parse=0` üretti (run 41319001191, `TestReadSmoke`). Ayrıştırıcı `xml.Decoder` akış çözümüne geçirildi: hem sarmalanmış hem ham şekil kabul edilir; bozuk öğede temkinli durup o ana dek çözülen olayları döndürür. Gerçek wevtutil şekli kalıcı fixture olarak eklendi (`TestParseWevtutilXMLUnwrapped`).

## [0.5.0] — 2026-11-01 — Faz 04: Linux Agent ve temel OS yönetimi

### Added
- Ayrıcalık ayrımı: `server-agent` (yetkisiz) + `server-privd` (root broker) ikilileri; broker yalnızca Unix socket (`/run/server-control/privd.sock`, 0660, dizin 0750) dinler, TCP yok; SO_PEERCRED ile UID allowlist (default deny); tipli JSON istekler; her istek (reddedilenler dahil) JSONL audit'e yazılır (`deploy/agent/` systemd unitleri + sertleştirme).
- OS tespiti (`agent/internal/osdetect`): /etc/os-release (ID/VERSION_ID/PRETTY_NAME/HOME_URL), çekirdek sürümü (procfs), systemd varlık+sürüm (procfs işaretleri + `systemctl --version` argv), paket yöneticisi (apt/dpkg/dnf/yum/unknown); kaynak provenance'ı ile raporlanır.
- Metrikler (`agent/internal/metrics`): CPU (pencere bazlı %, çekirdek, load 1/5/15), bellek, disk (gerçek FS filtre + cihaz dedupe + statfs kullanım), ağ (lo hariç), uptime — procfs/statfs kaynağı ve collection timestamp'i ile; protokol v1'e geriye dönük `SystemFactsReport` + `MetricsReport` zarf yükleri (field 14/15) ve `HelloAck.metrics_interval_seconds` (sunucu önerisi 60s; agent 10s–10dk kıskacı) eklendi.
- Servis işlemleri (`agent/internal/svcops`): systemd list/status/start/stop/restart — birim adı allowlist'i `^[A-Za-z0-9][A-Za-z0-9@._-]{0,246}\.service$`, exec.Command argv (shell string'i YOK), işlem başına timeout, sınırlı çıktı; broker üzerinden `linux.systemd.service.*` operasyonları.
- Capability beyanı: broker erişilebilirliği rapor zamanında probe edilir; servis kabiliyetleri erişilemezse `reason_unavailable` ile bildirilir (runtime contract, capability-system.md).
- Control plane: migration `0008_node_telemetry` (node_system_facts upsert + node_metrics zaman serisi + indeksler + retention 7g), `internal/telemetry` servisi (doğrulama+sınır+prune), agentsrv `SystemFactsReport`/`MetricsReport` işleyicileri (node başına 10s hız sınırı; metrics veri-düzlemi olduğu için tek tek audit'lenmez), REST `/nodes/{id}/facts/system`, `/nodes/{id}/metrics/latest`, `/nodes/{id}/metrics` (nodes:read).
- Görev motoru: 5 yeni node-hedefli operasyon (`linux.systemd.service.list/status/start/stop/restart`) — payload validator'ları iki tarafta (sunucu + agent), `OperationSpec.RequiredPerm` ile RBAC katmanı: okuma `service:read`, start/stop/restart `service:control` (tasks:write üstüne); restart `non_idempotent` + retry-güvensiz.
- RBAC: `service:read` ve `service:control` izinleri (migration 0008 seed; admin=hepsi, operator=yalnız okuma — least privilege).
- UI: Node detay görünümü — sistem bilgileri kartı, metrik kartı (progressbar'lı disk kullanımı, kaynak+toplanma zamanı), kabiliyet rozetleri (nedeniyle), tipli servis eylem formları (istemci tarafı birim doğrulama; gerçek denetim sunucu+agent+broker'da); TR/EN sözlükler.
- CI: `agent` job iki distro matrisi (ubuntu-24.04 runner + debian:13 container) — Faz 04 "iki distro test matrisi" teslimatı.
- Testler: osdetect fixture matrisi (Ubuntu 24.04 + Debian 13), metrics parser testleri, svcops fake-systemctl argv/timeout/eksik-systemd testleri, broker socket entegrasyonu (UID reddi, allowlist, shell-meta birim reddi, audit zorunluluğu, hız sınırlı eşzamanlılık), agentsrv telemetry entegrasyon testi (facts upsert + metrics hız sınırı), tasks validator birim testleri, httpapi uç testleri, NodeDetail vitest (4 test), E2E telemetry spec (2 test) + 3 ekran görüntüsü.

### Changed
- `agent.DefaultRegistry(brokerSocket)` imzası; `Run(ctx, store, addr, executors, RunOptions{FactsRoot})`; `server-agent run --broker-socket` bayrağı (varsayılan `/run/server-control/privd.sock`).
- `docs/technology/dependency-registry.md`: `golang.org/x/sys` v0.48.0 indirect→direct (BSD-3-Clause); yeni HARİCİ bağımlılık eklenmedi.
- `agent/README.md` + `deploy/agent/README.md` kurulum ve güvenlik modeliyle güncellendi.

### Security
- İşlem girdileri asla shell string'ine birleştirilmez: broker tipli JSON, systemctl argv; birim adı regex allowlist'i iki uçta + broker'da üç kez doğrulanır (sunucu validator, agent executor, broker validateRequest).
- Broker sadece root tarafından yazılabilir socket üzerinden erişilir; SO_PEERCRED allowlist dışındaki her UID `peer_denied` ile reddedilir ve auditlenir; allowlist boşsa tamamen kapalı.
- Agent process'i systemd sertleştirmesiyle çalışır (NoNewPrivileges, ProtectSystem=strict, boş CapabilityBoundingSet); broker yalnız AF_UNIX'e kısıtlıdır.
- Metrik yutumu node başına 10s hız sınırıyla korunur; snapshot alan sınırları (≤64 disk/ağ, kaynak adı ≤128) sunucu tarafında zorlanır.
- `service:control` izni ayrı tutulur: görev oluşturma yetkisi olan operatör varsayılan olarak servis durduramaz/yeniden başlatamaz.

## [0.4.0] — 2026-11-01 — Faz 03: Agent protokolü, kayıt, mTLS ve capability

### Added
- Protokol sözleşmesi: `proto/servercontrol/agent/v1/agent.proto` — `EnrollService` + `AgentService.Connect` (bidi stream); zarflar request_id/node_id/correlation_id/issued_at/expires_at/idempotency_key taşır; `buf.yaml` lint/breaking; üretilen kod `gen/proto` (ayrı modül); `make proto-tools/proto/proto-check` (CI kırılma kapısı).
- Enrollment (ADR-0008): tek kullanımlık, ≤1h, node/group kapsamlı token'lar (yalnızca SHA-256 özeti saklanır); tüketim + node devri/oluşumu + sertifika kaydı TEK transaction'da (FOR UPDATE + replay koruması); plaintext token yalnızca üretim anında bir kez döner.
- mTLS kimlik: operatör CA'sı (`control-plane ca init`, anahtar 0600, DB'de asla değil); agent anahtarı LOKALDE üretilir, yalnızca CSR iletilir; leaf sertifikaya sunucu tarafında URI SAN node kimliği yazılır; gRPC sunucusu istemci sertifikası + DB revocation + node durumu denetimi yapar; her zarfta node_id ↔ sertifika kimliği eşleşmesi zorunlu.
- Stream: Hello→HelloAck (v1 anlaşması), heartbeat (last_seen/active), capability raporu → node_capabilities upsert; süresi geçmiş zarflar atılır.
- Görev dağıtımı: tasks tablosuna `execution_target` (control_plane|node); node-hedefli görevler canlı stream üzerinden SKIP LOCKED + 60s lease ile dağıtılır; `OperationResult` (SUCCEEDED/FAILED/REJECTED) yalnızca dispatched durumda ve doğru node'dan uygulanır.
- Agent ikilisi (`server-agent`, ayrı Go modülü): lokal ECDSA anahtar (0600) + CSR, CA pinlemeli enroll (MITM guard), outbound mTLS koşu döngüsü (backoff+jitter), allowlist executor'lar (`agent.echo`) ve sınırlı idempotency cache; bilinmeyen operasyon REJECTED.
- HTTP API + UI: `/api/v1/agent-enrollments` (list/issue/revoke) ve "Agent Kaydı" sayfası (tek gösterim paneli, durum listesi, iptal); `enrollment` menü öğesi Faz 03'ten aktife alındı.
- CI: `proto` job (buf lint + üretim + git diff kapısı) ve `agent` job (vet/build/test-race).
- Testler: certs birim testleri; agent birim testleri; agentsrv PostgreSQL entegrasyon testleri (enroll akışları, sertifikasız/yanlış-node bağlantı reddi, uçtan uca dispatch); E2E'ye Agent Kaydı senaryosu (6/6).

### Changed
- `docs/decisions/ADR-0008-agent-protocol-mtls.md` ve ADR dizini; `docs/technology/dependency-registry.md` (grpc v1.85.0, protobuf v1.36.12, pinli buf/plugin araç zinciri); `docs/operations/development-environment.md` (CA/enroll/agent akışları); proto/ ve agent/ README'leri gerçek içerikle dolduruldu; README bileşen tablosu.

### Security
- Özel anahtar node'dan asla çıkmaz; CSR dışındaki malzeme reddedilir; sertifikada kimlik sunucu tarafından atanır (agent kimliğini seçemez); token/sertifika hash'leri DB'de; revoke edilmiş sertifika bağlantı kuruluşunda reddedilir; zarf expiry her iki yönde uygulanır; payload üst sınırları (64KiB) ve allowlist doğrulamaları her iki uçta.

## [0.3.0] — 2026-11-01 — Faz 02: Kimlik, RBAC, node envanteri, görev altyapısı ve E2E

### Added
- Kimlik ve oturum (ADR-0007): argon2id parola özeti; sunucu taraflı oturumlar (token yalnızca SHA-256 özeti saklanır); `HttpOnly SameSite=Strict` cookie; oturum token'ından türetilen CSRF token'ı (mutating isteklerde `X-CSRF-Token` zorunlu); IP/kullanıcı bazlı giriş hız sınırlama; uniform hata ile kullanıcı adı sızıntısının önlenmesi.
- İlk yönetici bootstrap: `control-plane bootstrap` CLI ile tek kullanımlık, kısa ömürlü token (≤1h, yalnızca özeti saklanır); `POST /api/v1/bootstrap/admin` tek transaction'da ilk yönetici + global admin rolü oluşturur; yarış testiyle tek admin kanıtı; varsayılan parola yok.
- RBAC: sabit izin kataloğu (users/nodes/tasks/audit × read/write), `all`/`group`/`node` kapsamlı rol bağlamaları, default-deny policy (pure fonksiyon + SQL store), her uçta `requirePermission`, reddedilen isteklerin audit'i.
- Node envanteri: nodes/node_groups/node_tags/node_facts (sürümlenmiş JSONB)/node_capabilities tabloları; oluşturma, güncelleme, etiket, grup ataması, facts upsert, capability upsert; keyset sayfalama ve izin filtreleri.
- Görev altyapısı: tipli operasyon kaydı (`noop.echo`, `inventory.node.tag.set` — allowlist, payload şema doğrulaması); task + outbox TEK transaction'da; worker `FOR UPDATE SKIP LOCKED` kiralaması, lease kurtarma, süre aşımı taraması, outbox teslimi (exponential backoff); attempt kayıtları; idempotency key (kısmi benzersiz indeks + replay/çatışma semantiği).
- Audit: append-only `audit_events` (trigger ile UPDATE/DELETE engeli), actor/target/action/result/correlation ID, secret redaction (recall-first, test edildi), keyset sayfalama.
- REST `/api/v1`: bootstrap, auth (login/session/csrf/logout), nodes (+tags/facts/capabilities), node-groups, tasks (+cancel, task-operations), audit, users (salt-okunur), overview özeti.
- Frontend: giriş sayfası + auth gate/provider; Sunucular (oluşturma + grup + etiket), Görevler (tipli kayıt, oluştur/iptal/yenile), Denetim Kaydı (filtre + "daha fazla"), Kullanıcılar (salt-okunur), gerçek sayılarla dürüst Genel Bakış; TR/EN sözlük genişletmesi.
- Playwright E2E katmanı: 5 smoke senaryosu (giriş, node oluşturma, görev yaşam döngüsü, denetim kaydı, EN yerel ayar); lokalde mock API (`web/e2e/mock-api.mjs`), CI'da gerçek Control Plane (`E2E_REAL=1`); ekran görüntüleri `docs/reviews/screenshots/phase-02/` altında repoya commit edildi.
- CI `e2e` job'ı: PostgreSQL 18 servis + Go build + `migrate up` + gerçek bootstrap + Playwright (chromium) + artifact yükleme (`actions/upload-artifact` SHA-pinli, doğrulanmış).
- `docs/decisions/ADR-0007-identity-sessions-bootstrap.md`; ADR dizin güncellemesi; `internal/testdb` ortak PostgreSQL test altyapısı; Makefile `web-e2e`/`web-e2e-install`/`web-e2e-real` hedefleri.

### Changed
- Migration seti 0002–0005 ile genişledi (kimlik, envanter, görev, denetim).
- `docs/ui-ux/navigation.md` — Operasyonlar grubuna "Audit Log" eklendi (Faz 02 denetim görünürlüğü).
- `docs/technology/dependency-registry.md` — `golang.org/x/crypto v0.58.0` (BSD-3-Clause), `@playwright/test 1.57.0` (Apache-2.0, dev-only) kayıtları.
- `docs/operations/development-environment.md` — bootstrap akışı, E2E komutları ve yeni ortam değişkenleri.
- `docs/project-status.md` — Faz 02 kanıtları ve açık işler.

### Security
- Oturum/CSRF token'larının veritabanında düz metin bulunmaması; audit ve loglarda secret redaction; audit tablosunun append-only olması; bootstrap token üretiminin yalnızca host CLI'sında olması; default-deny RBAC ve katalog dışı izin reddi; payload `DisallowUnknownFields` + boyut limitleri; login rate limit + uniform hata.

### Fixed (CI doğrulama turunda)
- CI-1: Paralel test paketlerinin migration advisory lock yarışı — `migrate` artık sınırlı süre bekler (30s), sonra yüksek sesle hata verir; lock testi iptal yolunu da kapsar.
- CI-2: `PGRecorder.Record` merkezî secret redaction uygular (defense in depth).
- CI-3: CI e2e job'ı `playwright install --with-deps` takılması — runner imajındaki system Chrome kullanılır (`E2E_BROWSER_CHANNEL=chrome`); job'lara `timeout-minutes` eklendi.
- CI-4: Her test paketi `internal/testutil.UniqueDB` ile kendi izole disposable veritabanını kullanır (per-test DB'ler test sonunda DROP WITH FORCE ile kaldırılır).

## [0.2.0] — 2026-11-01 — Faz 01: Monorepo, geliştirme ortamı, temel UI/API

### Added
- Monorepo yerleşimi: `backend/control-plane/`, `agent/` (Faz 03 rezerve), `proto/` (Faz 03 rezerve), `web/`, `deploy/`.
- Go Control Plane iskeleti: `GET /healthz`, `GET /readyz` (DB ping, degraded durumu), `GET /api/v1/version`; chi router; güvenlik başlıkları; istek-ID yayılımı; JSON hata zarfı; gövde limiti; graceful shutdown; maskelemeli yapılandırma (`config.RedactDatabaseURL`).
- Explicit migration altyapısı: gömülü SQL dosyaları, advisory-lock, transactional up/down, `migrate status`; `0001_baseline` migration'ı; PostgreSQL 18 entegrasyon testleri (CI servis konteyneri; `TEST_DATABASE_URL` yoksa açık SKIP).
- React 19.3 + TypeScript 5.9 (strict) + Tailwind 4 token kabuğu: `navigation.md` uyumlu sol menü, üst bar, TR/EN yerel anahtarları (derleme zamanı anahtar eşleşmesi), dürüst boş durumlar, erişilebilir durum rozetleri.
- `deploy/docker-compose.yml`: PostgreSQL 18-alpine sha256 digest ile pinli, port `127.0.0.1`'e bağlı; `.env.example` yalnızca placeholder.
- CI (`.github/workflows/ci.yml`): backend fmt/vet/build/test-race (+ PostgreSQL 18 servis), frontend npm ci/lint/typecheck/test/build, secret-scan; actionlar SHA ile pinli.
- Kök `Makefile` (backend/web/compose/migrate hedefleri) ve `scripts/secret-scan.sh`.
- `docs/technology/dependency-registry.md` — pinli sürümler + SPDX lisansları + risk notları.
- `docs/reviews/phase-01-test-report.md` — Faz 01 öz-denetim raporu.

### Changed
- `docs/operations/development-environment.md` — Faz 01 kararları (npm, Go 1.27.3, Node 26.13.0, nihai komut adları).
- `docs/project-status.md`, `CHANGELOG.md`, kök `README.md` — Faz 01 durumu ve hızlı başlangıç.

### Security
- `.gitignore` secret desenleri; CI secret taraması; `CP_DATABASE_URL` loglarda maskelenir; query string'ler asla loglanmaz; API güvenlik başlıkları (CSP, nosniff, DENY, no-store).

## [0.1.0] — 2026-11-01 — Faz 00: Mimari ve proje sözleşmesi

### Added
- `docs/reviews/phase-00-architecture-review.md` — tam dokümantasyon denetimi, bulgular F-01…F-06 ve çözümleri.
- `docs/reviews/phase-00-test-report.md` — bağımsız test promptu kriterlerine göre öz-denetim raporu.
- `docs/decisions/ADR-0006-multi-node-strategy.md` — çoklu-node stratejisi: ilk dağıtım tek Control Plane, çoklu-node hazır sözleşmeler, ölçüme bağlı ölçekleme yolu.
- `docs/decisions/README.md` — ADR süreci, şablonu ve ADR-0001…0006 dizini.
- `docs/phases/README.md` içine 16 fazın bağımlılık sırası tablosu (teknik bağımlılık + giriş/çıkış kriteri tanımları).
- `.gitignore` — gizli dosya ve derleme çıktılarının repoya girmemesi için temel kural seti.

### Changed
- `docs/architecture/system-architecture.md` — Failure model görev durum listesine `accepted` eklendi; task-lifecycle dokümanıyla hizalandı (F-01).
- `docs/vision/scope.md` — MVP ↔ faz eşleme notu eklendi (MVP = Faz 00–04; Docker sınırlı yaşam döngüsü Faz 07) (F-02).
- `docs/ui-ux/design-system.md` — Operations menü grubuna "Tasks & Activity" eklendi; navigation.md ile hizalandı (F-03).
- `docs/project-status.md` — Faz 00 durumu, kanıtlar, açık kararlar ve ortam riskleriyle güncellendi.
- `README.md` — proje deposu olarak yeniden düzenlendi; faz işleyişi ve bağımsız test promptu gereksinimi korundu.

### Notes
- Faz 00'da kod, bağımlılık veya migration yoktur; kit taban dokümantasyonu (`PACKAGE-MANIFEST.json`, `SHA256SUMS`) değişmeden korunmuştur.
