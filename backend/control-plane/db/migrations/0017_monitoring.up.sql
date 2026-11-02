-- Faz 13: izleme ve alarm modülü (monitoring.md).
-- alert_rules: operatör tanımlı eşik kuralları; node_id NULL = tüm düğümler.
-- alerts: yaşam döngüsü izlenen alarm örnekleri; parmak izi DEDUPE kısmi
-- tekil indeksle garanti edilir (aktif alarm başına TEK satır).

CREATE TABLE alert_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    node_id UUID REFERENCES nodes (id) ON DELETE CASCADE,
    metric TEXT NOT NULL,
    dimension TEXT NOT NULL DEFAULT '',
    op TEXT NOT NULL,
    threshold DOUBLE PRECISION NOT NULL,
    duration_seconds INT NOT NULL DEFAULT 0,
    severity TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT alert_rules_metric CHECK (metric IN ('cpu', 'memory', 'disk', 'metrics_freshness')),
    CONSTRAINT alert_rules_op CHECK (op IN ('gt', 'lt')),
    CONSTRAINT alert_rules_severity CHECK (severity IN ('info', 'warning', 'critical'))
);

CREATE TABLE alerts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id UUID NOT NULL REFERENCES alert_rules (id) ON DELETE CASCADE,
    node_id UUID REFERENCES nodes (id) ON DELETE CASCADE,
    dimension TEXT NOT NULL DEFAULT '',
    fingerprint TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open',
    severity TEXT NOT NULL,
    value DOUBLE PRECISION NOT NULL DEFAULT 0,
    occurrences INT NOT NULL DEFAULT 1,
    title TEXT NOT NULL DEFAULT '',
    detail TEXT NOT NULL DEFAULT '',
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    acknowledged_at TIMESTAMPTZ,
    acknowledged_by TEXT,
    silenced_until TIMESTAMPTZ,
    silenced_by TEXT,
    resolved_at TIMESTAMPTZ,
    resolved_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT alerts_status CHECK (status IN ('open', 'acknowledged', 'silenced', 'resolved')),
    CONSTRAINT alerts_severity CHECK (severity IN ('info', 'warning', 'critical'))
);

-- DEDUPE: aynı (kural, node, dimension) parmak izi için en fazla BİR aktif
-- alarm. Çözülmüş alarmlar geçmiş olarak kalır; yeni ihlal yeni örnek açar.
CREATE UNIQUE INDEX alerts_active_fingerprint_idx ON alerts (fingerprint) WHERE status <> 'resolved';
CREATE INDEX alerts_status_idx ON alerts (status, last_seen_at DESC);
CREATE INDEX alerts_rule_idx ON alerts (rule_id);

-- Faz 13 izinleri — YALNIZCA yeni izinler seed edilir (CI-04 dersi);
-- mevcut katalog asla yeniden verilmez.
INSERT INTO permissions (name) VALUES
    ('logs:read'), ('alerts:read'), ('alerts:control'), ('security:read');

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('logs:read', 'alerts:read', 'alerts:control', 'security:read')
WHERE r.name = 'admin';

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p
  ON p.name IN ('logs:read', 'alerts:read', 'security:read')
WHERE r.name = 'operator';
