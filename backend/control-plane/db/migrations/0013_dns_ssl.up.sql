-- Faz 09: Domain/DNS sağlayıcı kayıtları ve SSL sertifika yaşam döngüsü.
-- Yalnız YENİ izinler seed edilir (CI-04 dersi: 0002 kataloğu admin'e
-- zaten tüm izinleri verdi; CROSS JOIN pkey çakışması üretir).
--
-- dns_providers.credentials_encrypted: AES-256-GCM ciphertext
-- (internal/secrets, CP_SECRETS_KEY). Anahtar ASLA PostgreSQL'de yaşamaz;
-- düz metin kimlik bilgisi hiçbir okuma yoluna çıkmaz (yalnız anahtar
-- ADLARI credential_keys alanında tutulur).
--
-- certificates: yönetilen TLS sertifikalarının yaşam döngüsü durumu
-- (certmgmt store SQL'i ile aynı ad — CI-22 dersi: migration ve store
-- şemaları birebir hizalı olmalı; 0006'daki node_certificates agent
-- mTLS kimlik sertifikalarıdır, ayrıdır). TLS özel anahtarı bu tabloda
-- ASLA tutulmaz (node'da doğar; yalnız CSR — public materyal — saklanır);
-- sertifika/zincir PEM'i GCM sealed saklanır.

CREATE TABLE dns_providers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL,
    capabilities JSONB NOT NULL,
    credentials_encrypted BYTEA NOT NULL,
    credential_keys JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE certificates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    sans JSONB NOT NULL DEFAULT '[]'::jsonb,
    provider_id UUID REFERENCES dns_providers(id) ON DELETE SET NULL,
    node_id UUID,
    csr_pem TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'issuing', 'issued', 'renewing',
                         'renewal_failed', 'revoked', 'expired')),
    cert_pem_enc BYTEA,
    chain_pem_enc BYTEA,
    not_after TIMESTAMPTZ,
    fingerprint TEXT,
    last_error TEXT,
    last_error_class TEXT
        CHECK (last_error_class IS NULL OR last_error_class IN
               ('rate_limit', 'challenge', 'order_invalid', 'csr_invalid',
                'network', 'acme_other')),
    retry_after_secs INT NOT NULL DEFAULT 0,
    issued_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX certificates_state_idx ON certificates (state);
CREATE INDEX certificates_not_after_idx ON certificates (not_after);

INSERT INTO permissions (name) VALUES
    ('dns:read'), ('dns:control'), ('cert:read'), ('cert:control');

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('dns:read', 'dns:control', 'cert:read', 'cert:control')
WHERE r.name = 'admin';

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('dns:read', 'cert:read')
WHERE r.name = 'operator';
