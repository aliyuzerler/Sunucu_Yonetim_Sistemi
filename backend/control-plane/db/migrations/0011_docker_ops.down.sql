-- Faz 07 geri alma: docker izinlerini kaldırır.
DELETE FROM role_permissions WHERE permission_id IN (
  SELECT id FROM permissions WHERE name IN ('docker:read', 'docker:control'));
DELETE FROM permissions WHERE name IN ('docker:read', 'docker:control');
