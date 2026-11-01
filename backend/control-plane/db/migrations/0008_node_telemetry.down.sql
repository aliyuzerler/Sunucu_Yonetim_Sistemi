-- 0008_node_telemetry geri alınması.
DROP INDEX IF EXISTS node_metrics_collected_idx;
DROP INDEX IF EXISTS node_metrics_node_time_idx;
DROP TABLE IF EXISTS node_metrics;
DROP TABLE IF EXISTS node_system_facts;

DELETE FROM role_permissions WHERE permission_id IN (SELECT id FROM permissions WHERE name IN ('service:read', 'service:control'));
DELETE FROM permissions WHERE name IN ('service:read', 'service:control');
