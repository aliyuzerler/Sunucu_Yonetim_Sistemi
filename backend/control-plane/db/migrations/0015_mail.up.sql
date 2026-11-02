-- Faz 11: e-posta servisi yönetimi (Postfix/Dovecot/Rspamd — ADR-0015).
-- Yalnız YENİ izinler seed edilir (CI-04 dersi: 0002 kataloğu admin'e
-- zaten tüm izinleri verdi; CROSS JOIN pkey çakışması üretir).
--
-- Mailbox şifreleri db_credentials tablosunda kind='mailbox' olarak
-- yaşar: db.user.create ile AYNI disiplin (node'da üretilir, AES-256-GCM
-- seal, TEK SEFERLİK reveal). Postgres CHECK kısıtı genişletilir —
-- mevcut satırlara dokunulmaz, down migration kısıtı geri alır.

ALTER TABLE db_credentials DROP CONSTRAINT db_credentials_kind_check;
ALTER TABLE db_credentials
    ADD CONSTRAINT db_credentials_kind_check
    CHECK (kind IN ('postgres', 'mysql', 'mailbox'));

INSERT INTO permissions (name) VALUES
    ('mail:read'), ('mail:control');

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('mail:read', 'mail:control')
WHERE r.name = 'admin';

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('mail:read')
WHERE r.name = 'operator';
