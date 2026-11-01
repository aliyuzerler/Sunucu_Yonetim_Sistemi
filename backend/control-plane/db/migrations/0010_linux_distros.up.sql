-- Faz 06: Linux dağıtım adaptörleri (Rocky/Alma). İki bölüm:
-- (1) node_system_facts'a distro sınıflandırma kolonları (additive);
-- (2) YENİ salt-okuma izinleri: pkg:read, firewall:read (aynı risk sınıfı —
--     kural değişikliği yüzeyi bu fazda yoktur, ADR-0010).

ALTER TABLE node_system_facts ADD COLUMN IF NOT EXISTS distro_family TEXT NOT NULL DEFAULT '';
ALTER TABLE node_system_facts ADD COLUMN IF NOT EXISTS distro_like TEXT NOT NULL DEFAULT '';
ALTER TABLE node_system_facts ADD COLUMN IF NOT EXISTS support_state TEXT NOT NULL DEFAULT '';
ALTER TABLE node_system_facts ADD COLUMN IF NOT EXISTS support_reason TEXT NOT NULL DEFAULT '';

-- DİKKAT: yalnızca YENİ izinler seed edilir; 0002 admin'e tüm kataloğu
-- zaten verdi — CROSS JOIN pkey çakışması üretir (CI bulgusu CI-04).
INSERT INTO permissions (name) VALUES ('pkg:read'), ('firewall:read');

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('pkg:read', 'firewall:read')
WHERE r.name = 'admin';

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('pkg:read', 'firewall:read')
WHERE r.name = 'operator';
