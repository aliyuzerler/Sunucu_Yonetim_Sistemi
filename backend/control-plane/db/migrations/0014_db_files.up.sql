-- Faz 10: veritabanı yönetimi + scoped dosya yöneticisi.
-- Yalnız YENİ izinler seed edilir (CI-04 dersi: 0002 kataloğu admin'e
-- zaten tüm izinleri verdi; CROSS JOIN pkey çakışması üretir).
--
-- db_credentials: db.user.create şifrelerinin yaşam döngüsü (kriter 2 —
-- "password hiçbir zaman tekrar düz metin gösterilmemeli"):
--   - Şifre NODE'da üretilir (agent, crypto/rand; asla payload'da taşınmaz).
--   - Görev sonucu kalıcılaşmadan ÖNCE agentsrv ResultScrubber şifreyi
--     sonuçtan AYRIŞTIRIR ve yalnız AES-256-GCM sealed blob olarak burada
--     saklar (internal/secrets, CP_SECRETS_KEY — anahtar PostgreSQL'de
--     asla yaşamaz; Faz 09 dns_providers disiplini).
--   - Reveal TEK SEFERLİKTİR (revealed_at): ikinci reveal tipli red.
--   - tasks.result sütununda şifre ASLA durmaz; yalnız credential_ref +
--     fingerprint (SHA-256 ilk 16 hex) kalır.
-- Panelin kendi PostgreSQL veritabanı (CP_DATABASE_URL) bu tabloyla
-- yönetilmez: db.* ailesi yalnız node-local unix soketlerine dokunur ve
-- panel veritabanı adları dbops korumalı-ad listesindedir (ADR-0014).

CREATE TABLE db_credentials (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id UUID NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('postgres', 'mysql')),
    username TEXT NOT NULL,
    host TEXT NOT NULL DEFAULT '',
    password_encrypted BYTEA NOT NULL,
    fingerprint TEXT NOT NULL,
    revealed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (node_id, kind, username, host)
);

CREATE INDEX db_credentials_node_idx ON db_credentials (node_id);

INSERT INTO permissions (name) VALUES
    ('db:read'), ('db:control'), ('files:read'), ('files:control');

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('db:read', 'db:control', 'files:read', 'files:control')
WHERE r.name = 'admin';

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('db:read', 'files:read')
WHERE r.name = 'operator';
