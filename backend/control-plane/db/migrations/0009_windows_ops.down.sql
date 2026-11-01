-- 0009_windows_ops geri alınması.
DELETE FROM role_permissions WHERE permission_id IN (SELECT id FROM permissions WHERE name = 'eventlog:read');
DELETE FROM permissions WHERE name = 'eventlog:read';
