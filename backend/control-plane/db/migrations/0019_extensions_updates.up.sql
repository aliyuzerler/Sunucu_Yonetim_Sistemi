-- Faz 15: eklenti sözleşmesi, imzalı güncelleme kayıt defteri ve sürüm yönetimi.
--
-- extensions: imzalı eklenti manifestleri (SCP-EXT-MANIFEST-V1, ADR-0019).
-- Manifest kendisi JSONB olarak saklanır; imza ed25519 (kanonik JSON, imza
-- alanı hariç). state yalnız pending|approved|revoked: kayıt HİÇBİR izin
-- vermez (kriter 1 — varsayılan izin YOK); yetki tamamen extension_grants
-- tablosundaki açık onaylardan gelir (kim/ne zaman/geri alındı mı).
-- delegated_ops yalnız MEVCUT kayıtlı tip işlerinin alt kümesi olabilir
-- (kayıt anında servis doğrular); API uyumluluk aralığı manifest'tedir ve
-- servis her okumada yeniden değerlendirir (compatible alanı hesaplanır).
--
-- extension_grants: TEK TEK onaylanmış izinler. granted_by yönetici kimliği;
-- revoked_at NULL = aktif. (extension_id, action) tekil — aynı izin için tek
-- satır; geri alma + yeniden onay aynı satırı canlandırır (audit izi
-- audit_events'te, burada yalnız son durum).
--
-- update_artifacts: imzalı sürüm artifact'larının ÜST VERİSİ (ADR-0020).
-- Control Plane ikili/zip dağıtMAZ — yalnız hedef platform + sürüm + sha256 +
-- imza kaydeder; yayınlama anında imza fail-closed doğrulanır (güven kökü
-- CP_UPDATE_TRUST_KEYS). (target, version) tekil. state published|revoked:
-- revoked artifact için update.stage görevi oluşturulamaz (ops_faz15
-- validator'u reddeder). Agent tarafında gerçek doğrulama yerelde yapılır:
-- sha256 + imza (broker --update-trust-key) + staging + health + rollback.
--
-- Şema isimleri kanunla uyumlu: internal/extensions + internal/updates.

CREATE TABLE extensions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE CHECK (name ~ '^[a-z0-9][a-z0-9-]{1,63}$'),
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 128),
    vendor TEXT NOT NULL CHECK (char_length(vendor) BETWEEN 1 AND 128),
    version TEXT NOT NULL CHECK (char_length(version) BETWEEN 5 AND 64),
    api_version TEXT NOT NULL CHECK (char_length(api_version) BETWEEN 3 AND 128),
    min_cp_version TEXT NOT NULL CHECK (char_length(min_cp_version) BETWEEN 5 AND 64),
    artifact_sha256 TEXT NOT NULL CHECK (artifact_sha256 ~ '^[0-9a-f]{64}$'),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'approved', 'revoked')),
    manifest JSONB NOT NULL,
    signature TEXT NOT NULL CHECK (char_length(signature) BETWEEN 64 AND 256),
    delegated_ops JSONB NOT NULL DEFAULT '[]'::jsonb,
    capabilities JSONB NOT NULL DEFAULT '[]'::jsonb,
    registered_by TEXT NOT NULL CHECK (char_length(registered_by) BETWEEN 1 AND 128),
    registered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE extension_grants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    extension_id UUID NOT NULL REFERENCES extensions (id) ON DELETE CASCADE,
    action TEXT NOT NULL CHECK (action ~ '^[a-z]+:[a-z]+$'),
    risk TEXT NOT NULL CHECK (risk IN ('low', 'medium', 'high')),
    granted_by TEXT NOT NULL CHECK (char_length(granted_by) BETWEEN 1 AND 128),
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ,
    CONSTRAINT extension_grants_unique UNIQUE (extension_id, action)
);

CREATE INDEX extension_grants_ext_idx ON extension_grants (extension_id);

CREATE TABLE update_artifacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    target TEXT NOT NULL CHECK (target IN ('agent_linux_amd64', 'agent_windows_amd64', 'control_plane')),
    version TEXT NOT NULL CHECK (char_length(version) BETWEEN 5 AND 64),
    file_name TEXT NOT NULL CHECK (char_length(file_name) BETWEEN 1 AND 128),
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 1073741824),
    sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    signature TEXT NOT NULL CHECK (char_length(signature) BETWEEN 64 AND 256),
    min_prev TEXT CHECK (min_prev IS NULL OR char_length(min_prev) BETWEEN 5 AND 64),
    api_version TEXT NOT NULL CHECK (char_length(api_version) BETWEEN 1 AND 32),
    notes TEXT NOT NULL DEFAULT '' CHECK (char_length(notes) <= 512),
    state TEXT NOT NULL DEFAULT 'published' CHECK (state IN ('published', 'revoked')),
    manifest JSONB NOT NULL,
    published_by TEXT NOT NULL CHECK (char_length(published_by) BETWEEN 1 AND 128),
    published_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT update_artifacts_unique UNIQUE (target, version)
);

CREATE INDEX update_artifacts_target_idx ON update_artifacts (target, version);

-- Faz 15 izinleri — YALNIZCA yeni izinler seed edilir (CI-04 dersi).
INSERT INTO permissions (name) VALUES
    ('extensions:read'), ('extensions:control'),
    ('updates:read'), ('updates:control');

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('extensions:read', 'extensions:control', 'updates:read', 'updates:control')
WHERE r.name = 'admin';

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('extensions:read', 'updates:read')
WHERE r.name = 'operator';
