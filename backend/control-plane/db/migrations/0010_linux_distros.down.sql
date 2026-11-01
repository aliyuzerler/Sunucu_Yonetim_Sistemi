-- Faz 06 geri alma: kolonlar ve izinler.
ALTER TABLE node_system_facts DROP COLUMN IF EXISTS support_reason;
ALTER TABLE node_system_facts DROP COLUMN IF EXISTS support_state;
ALTER TABLE node_system_facts DROP COLUMN IF EXISTS distro_like;
ALTER TABLE node_system_facts DROP COLUMN IF EXISTS distro_family;

DELETE FROM role_permissions WHERE permission_id IN (
    SELECT id FROM permissions WHERE name IN ('pkg:read', 'firewall:read'));
DELETE FROM permissions WHERE name IN ('pkg:read', 'firewall:read');
