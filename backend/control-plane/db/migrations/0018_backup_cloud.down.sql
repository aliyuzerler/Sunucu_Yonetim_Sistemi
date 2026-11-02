-- Faz 14 geri alma: bulut sağlayıcı kayıtları, restore tatbikatları,
-- yedek kayıtları ve Faz 14 izinleri kaldırılır. Veri kaybı bilinçlidir:
-- yedek kayıtları yalnız KAYIT'tır (dosyalar/repostalar silinmez).

DROP TABLE IF EXISTS restore_drills;
DROP TABLE IF EXISTS backups;
DROP TABLE IF EXISTS cloud_providers;

DELETE FROM role_permissions WHERE permission_id IN (
    SELECT id FROM permissions WHERE name IN
        ('backup:read', 'backup:control', 'cloud:read', 'cloud:control'));
DELETE FROM permissions WHERE name IN
    ('backup:read', 'backup:control', 'cloud:read', 'cloud:control');
