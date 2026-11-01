-- Faz 08: Web hosting (nginx + PHP-FPM site yaşam döngüsü). Yalnız YENİ
-- izinler seed edilir (CI-04 dersi: 0002 kataloğu admin'e zaten tüm
-- izinleri verdi; CROSS JOIN pkey çakışması üretir). site create/update/
-- disable/delete web:control altındadır (gerçek mutasyon — ADR-0012);
-- operator salt-okuma alır (en az ayrıcalık).

INSERT INTO permissions (name) VALUES ('web:read'), ('web:control');

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('web:read', 'web:control')
WHERE r.name = 'admin';

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name = 'web:read'
WHERE r.name = 'operator';
