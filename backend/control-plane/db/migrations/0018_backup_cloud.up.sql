-- Faz 14: yedekleme/geri yükleme ve ilk bulut sağlayıcı bağdaştırıcısı.
--
-- backups: KONTROL PANELİ (control_plane) ve NODE veri yedekleri AYRI kayıt
-- sınıflarıdır (kriter 1) — scope sütunu yapısal ayrımı taşar; node yedeği
-- için node_id ZORUNLUDUR (CHECK), kontrol paneli yedeğinde node_id YASAKTIR.
-- verify_state: bir yedek, bütünlük doğrulaması kaydedilmeden tamamlanmış
-- sayılmaz (kriter 2); doğrulama zamanı + özeti saklanır.
--
-- restore_drills: her yedek türü için İZOLE hedefte restore tatbikatı kaydı
-- (kriter 2) — CP yedeği için geçici/izole veritabanı adı, node yedeği için
-- yönetilen izole restore dizini. Canlı veriye dokunan restore BU fazda yoktur.
--
-- cloud_providers: ilk bulut sağlayıcı bağdaştırıcı kaydı (kriter 5) —
-- dns_providers ile aynı kimlik bilgisi disiplini: AES-256-GCM ciphertext
-- (internal/secrets, CP_SECRETS_KEY); anahtar ASLA PostgreSQL'de yaşamaz;
-- düz metin kimlik bilgisi hiçbir okuma yoluna çıkmaz. kind sütunu kapalı
-- enum'dur: her sağlayıcı kendi scope/izin matrisiyle ayrı test edilir.
--
-- Şifreleme anahtarı ayrımı (kriter 3) yapısal olarak korunur: yedek deposu
-- yolu CP_BACKUP_DIR'dir; CP_SECRETS_KEY ortamdan gelir ve bu dizine ASLA
-- yazılmaz (uygulama içi doğrulama: internal/backup.ValidateKeySeparation).
-- Node tarafında restic deposu /var/backups/server-control altında, restic
-- parola dosyası /etc/server-control/backup altında tutulur — ayrı kökler.

CREATE TABLE backups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scope TEXT NOT NULL CHECK (scope IN ('control_plane', 'node')),
    node_id UUID REFERENCES nodes (id) ON DELETE CASCADE,
    kind TEXT NOT NULL DEFAULT '' CHECK (char_length(kind) <= 64),
    status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'success', 'failed')),
    verify_state TEXT NOT NULL DEFAULT 'unverified'
        CHECK (verify_state IN ('unverified', 'verified', 'failed')),
    verify_at TIMESTAMPTZ,
    verify_detail TEXT NOT NULL DEFAULT '' CHECK (char_length(verify_detail) <= 512),
    size_bytes BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    sha256 TEXT NOT NULL DEFAULT '' CHECK (sha256 = '' OR char_length(sha256) = 64),
    snapshot_id TEXT NOT NULL DEFAULT '' CHECK (char_length(snapshot_id) <= 128),
    storage_path TEXT NOT NULL DEFAULT '' CHECK (char_length(storage_path) <= 512),
    task_id UUID,
    detail TEXT NOT NULL DEFAULT '' CHECK (char_length(detail) <= 1024),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT backups_node_scope CHECK ((scope = 'node') = (node_id IS NOT NULL))
);

CREATE INDEX backups_scope_created_idx ON backups (scope, created_at DESC);
CREATE INDEX backups_node_idx ON backups (node_id, created_at DESC);

CREATE TABLE restore_drills (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    backup_id UUID NOT NULL REFERENCES backups (id) ON DELETE CASCADE,
    node_id UUID REFERENCES nodes (id) ON DELETE CASCADE,
    target TEXT NOT NULL CHECK (char_length(target) <= 256),
    status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'success', 'failed')),
    detail TEXT NOT NULL DEFAULT '' CHECK (char_length(detail) <= 1024),
    tables_restored INT NOT NULL DEFAULT 0 CHECK (tables_restored >= 0),
    rows_restored BIGINT NOT NULL DEFAULT 0 CHECK (rows_restored >= 0),
    task_id UUID,
    actor TEXT NOT NULL DEFAULT '' CHECK (char_length(actor) <= 128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX restore_drills_backup_idx ON restore_drills (backup_id, created_at DESC);
CREATE INDEX restore_drills_node_idx ON restore_drills (node_id, created_at DESC);

CREATE TABLE cloud_providers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE CHECK (char_length(name) BETWEEN 3 AND 64),
    kind TEXT NOT NULL CHECK (kind IN ('s3')),
    endpoint TEXT NOT NULL CHECK (char_length(endpoint) <= 256),
    region TEXT NOT NULL DEFAULT '' CHECK (char_length(region) <= 64),
    credentials_encrypted BYTEA NOT NULL,
    credential_keys JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Faz 14 izinleri — YALNIZCA yeni izinler seed edilir (CI-04 dersi).
INSERT INTO permissions (name) VALUES
    ('backup:read'), ('backup:control'), ('cloud:read'), ('cloud:control');

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('backup:read', 'backup:control', 'cloud:read', 'cloud:control')
WHERE r.name = 'admin';

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('backup:read', 'cloud:read')
WHERE r.name = 'operator';
