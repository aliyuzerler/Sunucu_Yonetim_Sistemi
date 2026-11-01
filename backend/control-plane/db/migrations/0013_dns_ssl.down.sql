-- Faz 09 geri alma: ssl_certificates + dns_providers ve yalnız bu fazın
-- seed ettiği izinler kaldırılır.
DROP INDEX IF EXISTS ssl_certificates_not_after_idx;
DROP INDEX IF EXISTS ssl_certificates_state_idx;
DROP TABLE IF EXISTS ssl_certificates;
DROP TABLE IF EXISTS dns_providers;

DELETE FROM role_permissions WHERE permission_id IN (
    SELECT id FROM permissions WHERE name IN
        ('dns:read', 'dns:control', 'cert:read', 'cert:control'));
DELETE FROM permissions WHERE name IN
    ('dns:read', 'dns:control', 'cert:read', 'cert:control');
