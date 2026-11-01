-- Faz 08 geri alma: web hosting izinlerini kaldırır.
DELETE FROM role_permissions WHERE permission_id IN (
  SELECT id FROM permissions WHERE name IN ('web:read', 'web:control'));
DELETE FROM permissions WHERE name IN ('web:read', 'web:control');
