-- Faz 03: agent protokolü — enrollment token'ları ve node sertifikaları.
-- Tasarım (ADR-0008):
--   * Kayıt token'ı tek kullanımlık, kısa ömürlü; yalnızca SHA-256 özeti
--     saklanır; kapsamı 'node' (mevcut node'a devir) veya 'group' (yeni node)
--     olabilir; replay koruması consumed_at + satır kilidi iledir.
--   * node_certificates asla özel anahtar içermez; parmak izi benzersizdir;
--     revoke edilmiş sertifika mTLS el sıkışmasında reddedilir.

ALTER TABLE nodes
    ADD COLUMN IF NOT EXISTS protocol_version TEXT
        CHECK (protocol_version IS NULL OR char_length(protocol_version) <= 32);

CREATE TABLE enrollment_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash  TEXT NOT NULL UNIQUE CHECK (char_length(token_hash) = 64),
    scope_type  TEXT NOT NULL CHECK (scope_type IN ('node', 'group')),
    node_id     UUID REFERENCES nodes (id) ON DELETE CASCADE,
    group_id    UUID REFERENCES node_groups (id) ON DELETE CASCADE,
    created_by  TEXT NOT NULL DEFAULT '' CHECK (char_length(created_by) <= 128),
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((scope_type = 'node') = (node_id IS NOT NULL)),
    CHECK ((scope_type = 'group') = (group_id IS NOT NULL)),
    CHECK (consumed_at IS NULL OR consumed_at >= created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

-- Bekleyen token'lar için operasyonel sorgu indeksi.
CREATE INDEX enrollment_tokens_pending_idx ON enrollment_tokens (expires_at)
    WHERE consumed_at IS NULL AND revoked_at IS NULL;

CREATE TABLE node_certificates (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id            UUID NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    fingerprint_sha256 TEXT NOT NULL UNIQUE CHECK (char_length(fingerprint_sha256) = 64),
    serial             TEXT NOT NULL CHECK (char_length(serial) <= 64),
    not_before         TIMESTAMPTZ NOT NULL,
    not_after          TIMESTAMPTZ NOT NULL,
    status             TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
    revoked_at         TIMESTAMPTZ,
    revoked_by         TEXT CHECK (revoked_by IS NULL OR char_length(revoked_by) <= 128),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (node_id, serial),
    CHECK (not_after > not_before),
    CHECK ((status = 'active') = (revoked_at IS NULL))
);

CREATE INDEX node_certificates_node_idx ON node_certificates (node_id);
