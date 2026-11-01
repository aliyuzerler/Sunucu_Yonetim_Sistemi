-- Faz 02: kimlik, oturum ve RBAC taban tabloları.
-- Tasarım kararları (ADR-0007):
--   * Parolalar yalnızca argon2id PHC dizisi olarak saklanır; düz metin asla.
--   * CSRF token'ı oturum token'ından türetilir (sha256 türetme);
--     veritabanında saklanmaz. Mutating isteklerde X-CSRF-Token başlığı
--     zorunludur; SameSite=Strict cookie ile birlikte savunma sağlar.
--   * Oturum token'ları SHA-256 özeti ile saklanır; çalınan DB yedekleri
--     doğrudan oturum ele geçirmeye yeterli olmaz.
--   * İlk yönetici kurulumu tek kullanımlık, kısa ömürlü token iledir;
--     varsayılan parola tanımlı değildir.
--   * RBAC: role→permission kataloğu ve kullanıcı bağlamı; scope 'all',
--     'group' veya 'node' olabilir. Yetkisizlik VARSAYILAN'dır (default-deny).

CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username      TEXT NOT NULL CHECK (username ~ '^[a-z0-9_.-]{3,32}$'),
    display_name  TEXT NOT NULL DEFAULT '' CHECK (char_length(display_name) <= 128),
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    password_hash TEXT NOT NULL CHECK (char_length(password_hash) <= 512),
    password_algo TEXT NOT NULL DEFAULT 'argon2id',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at TIMESTAMPTZ
);

-- Kullanıcı adı büyük/küçük harf duyarsız benzersiz.
CREATE UNIQUE INDEX users_username_lower_idx ON users (lower(username));

CREATE TABLE roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE CHECK (name ~ '^[a-z0-9_-]{2,64}$'),
    description TEXT NOT NULL DEFAULT '',
    is_system   BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE permissions (
    id   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE CHECK (name ~ '^[a-z]+:[a-z_]+$')
);

CREATE TABLE role_permissions (
    role_id       UUID NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    permission_id UUID NOT NULL REFERENCES permissions (id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

-- Kullanıcı→rol bağlamı; scope_type='all' için scope_id NULL'dur.
CREATE TABLE user_role_bindings (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role_id    UUID NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    scope_type TEXT NOT NULL CHECK (scope_type IN ('all', 'group', 'node')),
    scope_id   UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- scope_id NULL dahil tam satır tekrarını engeller (PostgreSQL 15+).
    UNIQUE NULLS NOT DISTINCT (user_id, role_id, scope_type, scope_id),
    CHECK ((scope_type = 'all') = (scope_id IS NULL))
);

CREATE INDEX user_role_bindings_user_idx ON user_role_bindings (user_id);

-- İlk yönetici kurulumu: tek kullanımlık token özeti.
CREATE TABLE admin_bootstrap_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash  TEXT NOT NULL UNIQUE CHECK (char_length(token_hash) = 64),
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (consumed_at IS NULL OR consumed_at >= created_at)
);

CREATE TABLE sessions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE CHECK (char_length(token_hash) = 64),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at   TIMESTAMPTZ,
    CHECK (expires_at > created_at)
);

CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

-- Yetki kataloğu (Faz 02 kapsamı; yeni fazlar yeni izin ekler).
INSERT INTO permissions (name) VALUES
    ('users:read'),
    ('users:write'),
    ('nodes:read'),
    ('nodes:write'),
    ('tasks:read'),
    ('tasks:write'),
    ('audit:read');

INSERT INTO roles (name, description, is_system) VALUES
    ('admin',    'Tam yetkili yönetici',                       true),
    ('operator', 'Operasyon ekibi: node ve görev yönetimi',    true),
    ('viewer',   'Salt okunur görüntüleyici',                  true);

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p WHERE r.name = 'admin';

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('nodes:read', 'nodes:write', 'tasks:read', 'tasks:write', 'audit:read')
WHERE r.name = 'operator';

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('nodes:read', 'tasks:read', 'audit:read')
WHERE r.name = 'viewer';
