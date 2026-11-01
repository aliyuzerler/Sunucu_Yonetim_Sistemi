-- 0005_audit geri alınması.
DROP TABLE IF EXISTS audit_events;
DROP FUNCTION IF EXISTS audit_events_append_only_guard();
