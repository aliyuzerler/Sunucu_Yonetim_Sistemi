-- Faz 12: KVM/QEMU/libvirt sanallaştırma yönetimi (ADR-0016).
-- Yalnız YENİ izinler seed edilir (CI-04 dersi: 0002 kataloğu admin'e
-- zaten tüm izinleri verdi; CROSS JOIN pkey çakışması üretir).
--
-- vm ailesi node'da --vm-enable ile OPT-IN'dir (varsayılan KAPALI);
-- disk görüntü yolu payload'da ASLA yoktur, vm.delete yalnız sc- önekli
-- yönetilen birimleri kaldırır, snapshot qcow2 kapısı node'dadır.
-- Migration 0015'in db_credentials kind CHECK genişletmesine dokunmaz.

INSERT INTO permissions (name) VALUES
    ('vm:read'), ('vm:control');

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('vm:read', 'vm:control')
WHERE r.name = 'admin';

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('vm:read')
WHERE r.name = 'operator';
