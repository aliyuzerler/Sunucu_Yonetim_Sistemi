-- Faz 15 geri alma: yalnız bu fazın tabloları ve izinleri kaldırılır.
DELETE FROM role_permissions
WHERE permission_id IN (SELECT id FROM permissions
                         WHERE name IN ('extensions:read', 'extensions:control',
                                        'updates:read', 'updates:control'));

DELETE FROM permissions
WHERE name IN ('extensions:read', 'extensions:control', 'updates:read', 'updates:control');

DROP TABLE IF EXISTS update_artifacts;
DROP TABLE IF EXISTS extension_grants;
DROP TABLE IF EXISTS extensions;
