-- Faz 11 geri alma: mailbox credential türünü ve mail izinlerini kaldırır.

DELETE FROM role_permissions WHERE permission_id IN (
    SELECT id FROM permissions WHERE name IN ('mail:read', 'mail:control'));
DELETE FROM permissions WHERE name IN ('mail:read', 'mail:control');

-- kind='mailbox' satırları kaldırılmadan kısıt daraltılamaz; faz geri
-- alınırken mailbox credential'ları da faz kapsamındadır.
DELETE FROM db_credentials WHERE kind = 'mailbox';

ALTER TABLE db_credentials DROP CONSTRAINT db_credentials_kind_check;
ALTER TABLE db_credentials
    ADD CONSTRAINT db_credentials_kind_check
    CHECK (kind IN ('postgres', 'mysql'));
