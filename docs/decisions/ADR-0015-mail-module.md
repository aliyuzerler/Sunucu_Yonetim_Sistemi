# ADR-0015: E-posta servisi yönetimi (opt-in mail ailesi)

Durum: Kabul edildi — Faz 11
Tarih: 2026-11-02

## Bağlam

Faz 11, Postfix + Dovecot + Rspamd tabanlı isteğe bağlı bir e-posta modülü
ister. Kabul kriterleri: (1) modül varsayılan kurulumda zorunlu DEĞİL,
preflight ve port kontrolü yapmalı; (2) domain/mailbox/alias/quota
işlemleri tipli ve auditli; (3) SPF/DKIM/DMARC/PTR gereksinimleri UI'da
açıklanmalı; (4) config değişiklikleri syntax check + rollback olmadan
uygulanmamalı; (5) spam/abuse limitleri ve içeriksiz kuyruk görüntüleme.

Önceki fazların kalıpları geçerlidir: web hosting gerçek mutasyonları
broker'da `--web-enable` kapısının arkasında yapar (ADR-0012); veritabanı
ve dosya yöneticisi aileleri kendi opt-in kapılarıyla gelir (ADR-0014);
şifreler node'da üretilir, control-plane'e gitmeden önce seal edilir ve
yalnız bir kez gösterilir (ADR-0014 dbcred disiplini).

## Karar

1. **Aile `mail.*`, `--mail-enable` ile OPT-IN (varsayılan KAPALI).**
   Kapalıyken tüm işlemler tipli `unsupported_operation` reddi döner —
   sessiz kalabalık yok (ADR-0012/0014 genelleştirmesi). Modül HİÇBİR
   paket kurmaz: `mail.preflight` yalnız rapor verir (kriter 1); kurulum
   operatörün işidir.

2. **Dosya tabanlı yapılandırma, veritabanı bağımsız.** Yönetilen dizin
   `/etc/server-control/mail/` altında üç Postfix hash map'i (domains,
   vmailbox, valias) ve bir Dovecot passwd-file userdb (dovecot-users,
   0600 — yalnız hash) yaşar. `main.cf` yalnız marker-blokle
   (`# BEGIN/END server-control mail (managed)`) dokunulur; Dovecot
   conf.d'ye marker'lı `99-server-control.conf` yazılır. Mail verisinin
   PostgreSQL/MySQL'e bağlanması ZORUNLU DEĞİLDİR — küçük kurulumlarda
   dosya tabanı yeterlidir ve modül dbops ailesinden bağımsızdır.

3. **Her mutasyon: apply → syntax check → rollback.** Değişiklik önce
   uygulanır (atomic rename), `postmap` derler, `postfix check`
   (ve userdb için `doveconf -n`) kapısından geçirir; başarısızlıkta
   önceki baytlar geri yazılır ve tipli `ErrSyntaxCheck` döner (kriter 4).
   Reload best-effort'tur: geçerli bir config, daemon çalışmıyordu diye
   geri alınmaz; sonuçta `reloaded=false` dürüstçe raporlanır.

4. **Şifre disiplini = db.user.create disiplini.** Mailbox şifresi
   node'da üretilir (crypto/rand, 32 alfanumerik), `{SSHA}` hash olarak
   userdb'ye yazılır; düz metin yalnız görev sonucunda TEK SEFER döner.
   agentsrv ResultScrubber sonuç kalıcılaşmadan ÖNCE şifreyi AES-256-GCM
   ile seal edip `db_credentials` tablosuna `kind="mailbox"` olarak
   ayırır; reveal tek seferliktir (mevcut dbcred store + REST ucu
   paylaşılır — migration 0015 yalnız CHECK kısıtını genişletir).
   `CP_SECRETS_KEY` yoksa redaksiyon kazanır (fail-closed).

5. **Kuyruk içeriksizdir.** `mail.queue.list` `postqueue -j` çıktısından
   yalnız kuyruk kimliği/boyut/yaş/gönderici/neden (200 karakter tavan)
   alır; mesaj gövdesi ve alıcı adresleri ayrıştırma aşamasında DÜŞÜRÜLÜR
   (kriter 5). Limit anahtarları kapalı enum'dur (6 Postfix tunable,
   aralık doğrulamalı) — bilinmeyen anahtar tipli red.

6. **Deliverability dış DNS bağımlılığıdır.** SPF/DKIM/DMARC/PTR
   gereksinimleri UI'da örnek kayıtlarla AÇIKLANIR (kriter 3); backend
   hot-path'te DNS çağrısı yapmaz. PTR sorgusu preflight'ta best-effort
   ve dürüst "bilinmiyor" durumundadır. DKIM imzalama anahtarı yönetimi
   BU FAZDA kapsam dışıdır (UI dürüstçe söyler).

7. **Rspamd kapsamı dürüstçe sınırlıdır:** tespit (preflight), config
   testi (`rspamadm configtest`, kuruluysa) ve servis durumu. Politika
   düzenleme bu fazda yoktur.

## Sonuçlar

- Windows agent'ta mail ailesi yoktur (linux-only runner); GOOS=windows
  derleme etkilenmez (protocol.go'daki sabitler platform-nötrdür).
- Registry 67→83 operasyon; agent capability 51→67; RBAC'a `mail:read`
  ve `mail:control` eklenir (migration 0015 yalnız YENİ izinleri seed
  eder — CI-04 dersi).
- Yeni harici Go bağımlılığı YOKTUR; Postfix (IPL-1.0), Dovecot
  (LGPL-2.1) ve Rspamd (Apache-2.0) çalışma-zamanı sistem bağımlılığı
  olarak dependency-registry'e işlenir.
- CI'ya `agent-mail` job'ı eklenir (gerçek Postfix+Dovecot lifecycle +
  sendmail ile kontrollü teslim; `EXPECTED_MAIL=1` kapısı — CI-17 dersi).
- Kalan risk: vmail kullanıcısı/uid-gid operatör hazırCommitliği (tip
  hatası dürüst reddedilir); Postfix main.cf müdahalesi marker-blok
  dışına çıkmaz; DKIM imzalama eksikliği UI'da belgelenir.
