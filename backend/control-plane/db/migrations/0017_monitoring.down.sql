-- Faz 13 geri alma: alarm tabloları ve yeni izinler kaldırılır.
DROP INDEX IF EXISTS alerts_rule_idx;
DROP INDEX IF EXISTS alerts_status_idx;
DROP INDEX IF EXISTS alerts_active_fingerprint_idx;
DROP TABLE IF EXISTS alerts;
DROP TABLE IF EXISTS alert_rules;

DELETE FROM role_permissions WHERE permission_id IN (
    SELECT id FROM permissions WHERE name IN ('logs:read', 'alerts:read', 'alerts:control', 'security:read')
);
DELETE FROM permissions WHERE name IN ('logs:read', 'alerts:read', 'alerts:control', 'security:read');
