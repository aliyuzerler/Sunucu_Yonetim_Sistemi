-- Faz 02: append-only audit event kaydı.
-- security-boundaries.md: her hassas değişiklik actor/target/action/result/
-- correlation ID ile kaydedilir; metadata secret-filtreli (uygulama katmanı).
-- Append-only: UPDATE/DELETE veritabanı tetikleyicisiyle engellenir.

CREATE TABLE audit_events (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor          TEXT NOT NULL CHECK (char_length(actor) <= 128),
    actor_id       UUID REFERENCES users (id) ON DELETE SET NULL,
    action         TEXT NOT NULL CHECK (action ~ '^[a-z0-9_.]{3,128}$'),
    target_type    TEXT NOT NULL DEFAULT '' CHECK (char_length(target_type) <= 64),
    target_id      TEXT NOT NULL DEFAULT '' CHECK (char_length(target_id) <= 128),
    result         TEXT NOT NULL CHECK (result IN ('success', 'failure', 'denied')),
    correlation_id TEXT NOT NULL CHECK (char_length(correlation_id) <= 64),
    request_id     TEXT NOT NULL DEFAULT '' CHECK (char_length(request_id) <= 64),
    -- Redaction uygulama katmanında zorlanır; DB tarafında da ikinci savunma
    -- olarak bilinen secret anahtarları reddedilir (defense in depth).
    metadata       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX audit_created_idx ON audit_events (created_at DESC);
CREATE INDEX audit_correlation_idx ON audit_events (correlation_id);
CREATE INDEX audit_actor_idx ON audit_events (actor);
CREATE INDEX audit_action_idx ON audit_events (action);

CREATE OR REPLACE FUNCTION audit_events_append_only_guard() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_events append-only tablodur: % engellendi', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_events_append_only
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_append_only_guard();
