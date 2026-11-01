-- Faz 07: Docker Engine ve Compose yönetimi. Yalnız YENİ izinler seed
-- edilir (CI-04 dersi: 0002 kataloğu admin'e zaten tüm izinleri verdi;
-- CROSS JOIN pkey çakışması üretir). Silme yüzeyi Faz 07'de yoktur
-- (ADR-0011 preview-only); operator salt-okuma alır (en az ayrıcalık).

INSERT INTO permissions (name) VALUES ('docker:read'), ('docker:control');

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('docker:read', 'docker:control')
WHERE r.name = 'admin';

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name = 'docker:read'
WHERE r.name = 'operator';
