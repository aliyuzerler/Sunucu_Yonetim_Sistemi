-- Faz 04: node telemetri — agent'ın raporladığı sistem bilgileri ve metrik
-- anlık görüntüleri. node_facts operatör tarafından yönetilen sürümlenmiş
-- metadata iken node_system_facts agent'ın bildirdiği platform gerçeğidir
-- (node başına tek satır, upsert). node_metrics zaman serisidir.

CREATE TABLE node_system_facts (
    node_id UUID PRIMARY KEY REFERENCES nodes (id) ON DELETE CASCADE,
    os_id TEXT NOT NULL DEFAULT '',
    os_version_id TEXT NOT NULL DEFAULT '',
    os_pretty_name TEXT NOT NULL DEFAULT '',
    os_home_url TEXT NOT NULL DEFAULT '',
    kernel_release TEXT NOT NULL DEFAULT '',
    arch TEXT NOT NULL DEFAULT '',
    systemd_version TEXT NOT NULL DEFAULT '',
    package_manager TEXT NOT NULL DEFAULT '',
    systemd_available BOOLEAN NOT NULL DEFAULT false,
    source TEXT NOT NULL DEFAULT '',
    collected_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE node_metrics (
    id BIGSERIAL PRIMARY KEY,
    node_id UUID NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    collected_at TIMESTAMPTZ NOT NULL,
    source TEXT NOT NULL DEFAULT '',
    cpu JSONB NOT NULL,
    memory JSONB NOT NULL,
    disks JSONB NOT NULL,
    networks JSONB NOT NULL,
    uptime_seconds DOUBLE PRECISION NOT NULL DEFAULT 0
);

-- Detay sorgusu: node'un en yeni anlık görüntüleri.
CREATE INDEX node_metrics_node_time_idx ON node_metrics (node_id, collected_at DESC);

-- Retention: varsayılan 7 gün. Temizlik uygulama tarafında periyodik çalışır.
CREATE INDEX node_metrics_collected_idx ON node_metrics (collected_at);

-- Faz 04 yetkileri: servis işlemleri okuma ve KONTROLLÜ yürütme.
-- admin tüm yeni izinleri alır; operator yalnızca okuma alır (least
-- privilege — start/stop/restart bilinçli olarak atanır).
INSERT INTO permissions (name) VALUES
    ('service:read'),
    ('service:control');

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p WHERE r.name = 'admin';

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name = 'service:read'
WHERE r.name = 'operator';
