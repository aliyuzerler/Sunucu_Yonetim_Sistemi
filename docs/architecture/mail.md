# E-posta servisi yönetimi (Faz 11 — ADR-0015)

Postfix (MTA) + Dovecot (IMAP/POP3 userdb) + Rspamd (spam, kapsamı
sınırlı) tabanlı **isteğe bağlı** mail modülü. Aile `mail.*`, agent
tarafında `--mail-enable` ile açılır; modül paket KURMAZ.

## Bileşenler ve veri akışı

```
Control Plane (tasks: mail.* → RBAC mail:read/mail:control)
   → Agent (MailExecutor relay) → Privileged broker (--mail-enable)
       → mailops.Runner (fixed argv; /etc/server-control/mail)
           → Postfix hash maps + main.cf marker blok
           → Dovecot passwd-file userdb + conf.d marker parçası
```

## Yönetilen yollar

| Yol | Amaç | İzin |
| --- | --- | --- |
| `/etc/server-control/mail/` | yönetilen dizin | 0755 (postfix kullanıcısı map okur — CI-39) |
| `/etc/server-control/mail/domains` | virtual_mailbox_domains map (satır: `domain OK`) | 0644 |
| `/etc/server-control/mail/vmailbox` | virtual_mailbox_maps (adres → Maildir) | 0644 |
| `/etc/server-control/mail/valias` | virtual_alias_maps | 0644 |
| `/etc/server-control/mail/dovecot-users` | Dovecot passwd-file userdb ({SSHA} hash + kota) | 0600 |
| `/etc/postfix/main.cf` | yalnız marker-blok (`virtual_*`) | dokunulmadan korunur |
| `/etc/dovecot/conf.d/99-server-control.conf` | passdb/userdb parçası | 0644 |
| `/var/vmail/<domain>/<local>/Maildir` | sanal posta kutuları (vmail:5000) | 0700 |

## Operasyonlar (16)

Okuma: `mail.preflight`, `mail.status`, `mail.domain.list`,
`mail.mailbox.list`, `mail.alias.list`, `mail.config.check`,
`mail.queue.list`, `mail.limits.get`.

Mutasyon: `mail.domain.create/delete`, `mail.mailbox.create/delete`,
`mail.alias.create/delete`, `mail.quota.set`, `mail.limits.set`.

## Güvenlik değişmezleri

1. **Opt-in:** `--mail-enable` verilmeden her mail.* işlemi tipli
   `unsupported_operation` reddi.
2. **Preflight kurulum yapmaz:** ikili sürüm/port/vmail/wiring taraması
   salt rapordur (kriter 1).
3. **Syntax check + rollback:** her mutasyon apply → check → (başarısızsa)
   byte-düzeyi geri alma; reload best-effort ve `reloaded` bayrağıyla
   dürüst (kriter 4).
4. **Şifre disiplini:** node'da üretilir, `{SSHA}` hash'i userdb'de, düz
   metin TEK SEFERLİK reveal (`db_credentials`, kind=`mailbox`;
   AES-256-GCM seal; `CP_SECRETS_KEY` yoksa redaksiyon kazanır).
5. **Kuyruk gizliliği:** `postqueue -j` ayrıştırması gövdeyi ve alıcı
   adreslerini düşürür; sonuçta yalnız id/boyut/yaş/gönderici/neden (≤200
   karakter) kalır (kriter 5).
6. **Kapalı-enum limitler:** 6 Postfix tunable, aralık doğrulamalı;
   bilinmeyen anahtar tipli red.

## Dovecot userdb satır biçimi (dokümante format)

```
user@domain:{SSHA}base64:uid:gid::/var/vmail/d/u/Maildir::userdb_quota_rule=*:bytes=N
```

Extra alanlar boşlukla ayrılır (Dovecot dokümantasyonu); kota değerindeki
':' güvenlidir. Şema: `{SSHA}` = SHA1(parola+salt)+salt (RFC 2307).

## Deliverability (dış DNS — panel yönetmez)

| Kayıt | Örnek | Not |
| --- | --- | --- |
| SPF | `example.com. IN TXT "v=spf1 mx -all"` | gönderici beyanı |
| DKIM | `selector._domainkey IN TXT "v=DKIM1; ..."` | imzalama bu fazda kapsam dışı |
| DMARC | `_dmarc IN TXT "v=DMARC1; p=quarantine; ..."` | politika + raporlama |
| PTR | IP sahibi (ISS/barındırıcı) ayarlar | hostname eşleşmesi zorunlu |

## Bilinen sınırlar (dürüst kapsam)

- Rspamd: yalnız tespit + configtest + servis durumu (politika düzenleme
  yok).
- DKIM imzalama anahtarı üretimi/kaydı bu fazda yok (UI açıklar).
- Quota Dovecot userdb üzerinden uygulanır; Postfix `mailbox_size_limit`
  global bir değerdir ve `mail.limits.set` kapalı enum'undadır.
- Dovecot userdb 0600 root-owned'dır: passwd-file doğrulaması auth-worker
  tarafından root olarak okunur; IMAP oturumu bu fazda test edilmez.
  Postfix tarafı için map dosyaları 0644 + dizin 0755 zorunludur
  (CI-39: postfix virtual agent'ı 'postfix' kullanıcısı olarak okur).
- domains map satırları `domain OK` biçimindedir: postmap key-only satırı
  derlenmiş hash'e ALMAZ — bu, virtual_mailbox_domains'in sessizce bozulması
  demektir (CI-38; regresyon birim testi biçimi güvenceye alır).
- PTR/SPF doğrulaması panel dışıdır; preflight PTR sorgusu best-effort.
