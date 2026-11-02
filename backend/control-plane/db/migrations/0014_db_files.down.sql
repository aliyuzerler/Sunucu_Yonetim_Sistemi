-- Faz 10 geri alımı: db_credentials + Faz 10 izinleri kaldırılır.

DROP INDEX IF EXISTS db_credentials_node_idx;
DROP TABLE IF EXISTS db_credentials;

DELETE FROM role_permissions WHERE permission_id IN (
    SELECT id FROM permissions WHERE name IN
        ('db:read', 'db:control', 'files:read', 'files:control'));
DELETE FROM permissions WHERE name IN
    ('db:read', 'db:control', 'files:read', 'files:control');
