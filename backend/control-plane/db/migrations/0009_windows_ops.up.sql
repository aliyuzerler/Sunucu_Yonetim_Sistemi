-- Faz 05: Windows agent operasyonları — SCM servis işlemleri mevcut
-- service:read/service:control izinlerini KULLANIR (aynı risk sınıfı);
-- yalnızca YENİ izin eventlog:read eklenir (olay günlüğü okuma, allowlist'li
-- kanallar: Application/System/Setup — Security tasarımdan hariç, ADR-0009).
INSERT INTO permissions (name) VALUES
    ('eventlog:read');

-- DİKKAT: yalnızca YENİ izin verilir; 0002 admin'e tüm kataloğu zaten
-- verdi — CROSS JOIN pkey çakışması üretir (CI bulgusu CI-04).
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name = 'eventlog:read'
WHERE r.name = 'admin';

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name = 'eventlog:read'
WHERE r.name = 'operator';
