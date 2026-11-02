-- Faz 12 geri alınması: yalnız Bu migration'ın seed ettiği izinler
-- kaldırılır (role_permissions önce — FK güvenliği; CI-04 dersi).
DELETE FROM role_permissions WHERE permission_id IN
    (SELECT id FROM permissions WHERE name IN ('vm:read', 'vm:control'));

DELETE FROM permissions WHERE name IN ('vm:read', 'vm:control');
