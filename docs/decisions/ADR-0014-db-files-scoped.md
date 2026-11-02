# ADR-0014: Veritabanı Yönetimi (Node-Local) + Site-Kapsamlı, Yarışsız Dosya Yöneticisi

- Status: Accepted (Faz 10)
- Context: Faz 10 kabul kriterleri: (1) panelin kendi PostgreSQL'i ile yönetilen müşteri DB instance'larının ayrılması, (2) database/user/grant işlemlerinin typed/scoped/auditli olması ve şifrenin asla tekrar düz metin gösterilmemesi, (3) DB-native tutarlılıkla backup/restore, (4) dosya yöneticisinde canonical path/symlink/traversal/race/boyut/MIME kapıları, (5) dosya işlemlerinin yalnız atanmış site/root alanında çalışması.

## Context

- Panel PostgreSQL 18 üzerinde çalışır (ADR-0004). Yönetilecek müşteri veritabanları ise node'lardaki MariaDB/MySQL ve PostgreSQL instance'larıdır. Bu ikisinin birbirine karışması hem güvenlik hem işlemsel açıdan kabul edilemez.
- DB kullanıcı şifresi bir secret'tır: payload'a konursa tasks.payload (JSONB), log'a/audit'e sızarsa kalıcı ihlal olur; task sonucuna konursa tasks.result'ta sonsuza dek düz metin durur ve "tekrar göstermeme" kriteri ihlal edilir.
- Dosya yöneticisi klasik olarak raw-path kabul eder → traversal/symlink-escape; root kullanıcısıyla çalıştığından tek bir hata host geneline açılır. Ayrıca site kullanıcısı docroot sahibi olduğundan dosya swap'ları (TOCTOU) gerçek bir saldırı yüzeyidir.
- Broker istek satırı Faz 04'ten beri 16 KiB ile sınırlıydı; dosya yükleme bunun üstünde gövde taşır.

## Decision

1. **Yapısal panel-DB ayrımı (kriter 1):** `db.*` payload şemasında host/port/DSN alanı YOKTUR; dbops yalnız node-local unix soketlerine (`/run/postgresql`, `/run/mysqld/mysqld.sock`) bağlanır. Panelin kendi PostgreSQL'i agent'ın erişebileceği hiçbir yolda temsil edilmez. Ayrıca korumalı-ad listesi (sistem veritabanları + `server_control`/`srvctl*`/`serverctl*` konvansiyonları) ve korumalı kullanıcı listesi broker + runner katmanlarında tipli red ile korunur. Kalıntı risk (dürüst kayıt): operatör panel DB'sini egzotik bir adla managed node'a koyarsa koruma isim disiplinine düşer — deploy dokümanı panel DB'sinin managed instance'larda barındırılmamasını söyler.
2. **Şifre yaşam döngüsü (kriter 2):** Şifre NODE'da üretilir (crypto/rand, 32 alnum — tek tırnaklı SQL literalinde güvenli charset). Task sonucu kalıcılaşmadan ÖNCE `agentsrv.ResultScrubber` (internal/dbcred) şifreyi sonuçtan AYRIŞTIRIR: AES-256-GCM seal (CP_SECRETS_KEY, Faz 09 disiplini) → `db_credentials` tablosu; tasks.result/audit/outbox'ta asla düz metin yaşamaz. Gösterim TEK SEFERLİKTİR: `POST /db-credentials/{id}/reveal` tek race-free UPDATE (WHERE revealed_at IS NULL RETURNING) ile bir kez çözüp işaretler; ikinci çağrı tipli 409. CP_SECRETS_KEY ayarsızsa seal yapılamaz → şifre redakte edilir ve hiçbir yüzeye çıkmaz (redaksiyon retention'ı yener; fail-closed). db.user.delete kullanıcının kimlik satırlarını düşürür.
3. **DB-native tutarlılık (kriter 3):** Backup `pg_dump` (tek MVCC snapshot) / `mysqldump --single-transaction --quick --no-tablespaces` (InnoDB tutarlı snapshot) ile akıtılır (streaming; bellek sınırsız büyümez). Her yedek dosyası `-- server-control managed db backup` marker'ıyla başlar; restore YALNIZ marker'lı, yönetilen dizindeki, isim-allowlist'li dosyayı kabul eder (`ErrForeignBackup`) — keyfi dosya restore'u imkânsız. Disk kapasitesi (≥64 MiB boş) ve dosya sayısı (≤200/kind) dürüst tipli sınırlarla korunur. Yarım restore operasyonel risk olarak belgelenmiştir (restore tekrarı yakınsar).
4. **Yarışsız scoped dosya yöneticisi (kriter 4+5):** Kökler YALNIZ webops site docroot'larıdır (webops.DocRoot; site adı allowlist'i tek doğruluk kaynağı) — raw path payload'a ASLA konmaz. Dolaşım `openat` + `O_NOFOLLOW` zinciriyle fd-göreli yapılır (ara bileşen symlink'i tipli red; final bileşen hiçbir işlemde izlenmez). Açılan final nesnenin GERÇEK yolu `/proc/self/fd/<n>` üzerinden kanıtlanır ve kanonik docroot içinde olduğu doğrulanır — site kullanıcısının yürütme sırasındaki dizin swap'ları (TOCTOU) kapsam dışına çıkan her nesneyi tipli `ErrScope` ile düşürür. Boyut kapısı 512 KiB (indirme+yükleme, dürüst `ErrTooLarge`), uzantı denylist'i (.php/.phtml/.phar/.sh/.key/.pem/…), içerik magic-byte denetimi (`#!`, ELF, MZ, class) ve DetectContentType denylist'i defense-in-depth'tir; asıl güvenlik sınırı kök hapsi ve O_NOFOLLOW'dur. chmod ≤ 0777 (setuid/setgid/sticky yasak), chown'un HEDEFİ payload'da YOKTUR — site kullanıcısı (webops.SiteUser) türetilir.
5. **Opt-in kapılar:** `db.*` ailesi `--db-enable`, `fs.*` ailesi `--fs-enable` ile açılır (ikisi de VARSAYILAN KAPALI; ADR-0012 deseninin genelleştirmesi). Kapalıyken tipli `unsupported_operation`; capability'ler unavailable raporlanır.
6. **Broker istek satırı 16 KiB → 1 MiB:** fs.upload gövdesi (base64 ≤ 768 KiB) tek JSONL satırında taşınır. Sınır kasıtlı yükseltildi: peer çekirdek-kimlikli UID allowlist'li agent'tır, eşzamanlı yürütme semaforu sınırlıdır ve 1 MiB tavanı görev motorundaki MaxPayload tavanıyla hizalıdır.
7. **Registry + RBAC:** 16 yeni op (11 db + 5 fs); registry 51→67. Yeni izinler `db:read`/`db:control`/`files:read`/`files:control` — migration 0014 yalnız yeni izinleri seed eder (CI-04 dersi); operator salt-okuma. db.user.create `idempotent` (mevcut kullanıcı → no-op, şifre DÖNDÜRÜLMEZ), database/user delete + restore `destructive` + retry-güvensiz, backup `non_idempotent` + retry-safe.

## Alternatifler

- **Uzak DB instance yönetimi (host/port + kimlikli):** reddedildi — panel-DB ayrımını zayıflatır, secret yüzeyini büyütür. Node-local soket modeli faz kapsamını da dürüst tutar.
- **Şifreyi panelde üretip payload'da taşımak:** reddedildi — tasks.payload JSONB'de düz metin kalıcılaşır.
- **Şifreyi task sonucunda bırakmak:** reddedildi — "tekrar düz metin" kriterini doğrudan ihlal eder; GET /tasks sonsuza dek şifre döndürürdü.
- **Raw-path + chroot benzeri filtre dosya yöneticisi:** reddedildi — filtre tabanlı kapsam symlink/race ile delinir; türetme-tabanlı kök (site adı → docroot) yapısal olarak traversal'sızdır.
- **Chunked upload protokolü (16 KiB satırını korumak için):** ertelendi — karmaşıklığı protocol+state yıkımına karşı 1 MiB kasıtlı sınırına tercih edildi; chunking gerçekten gerekirse ayrı ADR ile eklenir.

## Consequences

- (+) Panel DB'si ile müşteri DB'leri yapısal ayrı; şifre hiçbir kalıcı yüzeyde düz metin değil; dosya kapsamı yapısal.
- (+) Backup/restore marker disipliniyle keyfi dosya erişimine kapalı; DB-native tutarlılık gerçek araçlarla kanıtlı (CI agent-dbfs).
- (−) Uzak/DBA düzeyi işler (replication, PITR, tablo düzeyi yönetim) kapsam dışı — sonraki fazlar.
- (−) 512 KiB dosya sınırı büyük varlıklar için gerçek bir sınırdır (dürüst tipli hata; chunking gerekiyorsa ADR açılır).
- (−) Restore yarıda kalırsa hedef veritabanı kısmi veri içerebilir; operatör yeniden restore ile yakınsar (belgelendi).
- Takip: db.instance.list şimdilik varsayılan soketleri tarar; çoklu-socket PG kümeleri sonraki faza.
