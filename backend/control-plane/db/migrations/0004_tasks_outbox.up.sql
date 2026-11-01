-- Faz 02: kalıcı task + outbox altyapısı.
-- task-lifecycle.md: queued → dispatched → accepted → running →
-- succeeded | failed | partial | cancelled | expired | unknown.
-- Task ve outbox kaydı TEK transaction içinde oluşturulur (kabul kriteri).
-- Worker kiralaması FOR UPDATE SKIP LOCKED ile yapılır (kabul kriteri).

CREATE TABLE tasks (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operation         TEXT NOT NULL CHECK (operation ~ '^[a-z0-9_.]{3,128}$'),
    operation_version TEXT NOT NULL CHECK (char_length(operation_version) <= 32),
    node_id           UUID REFERENCES nodes (id) ON DELETE SET NULL,
    payload           JSONB NOT NULL DEFAULT '{}'::jsonb,
    status            TEXT NOT NULL DEFAULT 'queued'
                      CHECK (status IN ('queued', 'dispatched', 'accepted', 'running',
                                        'succeeded', 'failed', 'cancelled', 'expired',
                                        'unknown', 'partial')),
    priority          INTEGER NOT NULL DEFAULT 0,
    actor             TEXT NOT NULL CHECK (char_length(actor) <= 128),
    correlation_id    TEXT NOT NULL CHECK (char_length(correlation_id) <= 64),
    idempotency_key   TEXT CHECK (idempotency_key IS NULL OR (char_length(idempotency_key) BETWEEN 8 AND 128)),
    result            JSONB,
    error_category    TEXT CHECK (error_category IS NULL OR char_length(error_category) <= 64),
    attempts          INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts      INTEGER NOT NULL DEFAULT 3 CHECK (max_attempts BETWEEN 1 AND 10),
    retry_safe        BOOLEAN NOT NULL DEFAULT false,
    deadline_at       TIMESTAMPTZ,
    lease_owner       TEXT CHECK (lease_owner IS NULL OR char_length(lease_owner) <= 128),
    lease_expires_at  TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX tasks_status_created_idx ON tasks (status, created_at);
CREATE INDEX tasks_node_idx ON tasks (node_id);
CREATE INDEX tasks_correlation_idx ON tasks (correlation_id);
-- Idempotency: aynı key ile ikinci task oluşturulamaz (kısmi benzersiz indeks).
CREATE UNIQUE INDEX tasks_idempotency_key_idx ON tasks (idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE TABLE task_attempts (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id        UUID NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    attempt_no     INTEGER NOT NULL CHECK (attempt_no >= 1),
    status         TEXT NOT NULL CHECK (status IN ('started', 'succeeded', 'failed')),
    error_category TEXT CHECK (error_category IS NULL OR char_length(error_category) <= 64),
    -- Sınırlı ve temizlenmiş çıktı (secret redaction hizmet katmanında yapılır).
    output         TEXT CHECK (output IS NULL OR char_length(output) <= 4096),
    started_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at    TIMESTAMPTZ,
    UNIQUE (task_id, attempt_no)
);

CREATE INDEX task_attempts_task_idx ON task_attempts (task_id, attempt_no);

-- Outbox: task ile aynı transaction'da yazılır; worker işler.
CREATE TABLE outbox_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type  TEXT NOT NULL CHECK (char_length(aggregate_type) <= 64),
    aggregate_id    UUID NOT NULL,
    event_type      TEXT NOT NULL CHECK (char_length(event_type) <= 64),
    payload         JSONB NOT NULL DEFAULT '{}'::jsonb,
    attempts        INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at    TIMESTAMPTZ,
    last_error      TEXT CHECK (last_error IS NULL OR char_length(last_error) <= 512),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX outbox_unprocessed_idx ON outbox_events (next_attempt_at)
    WHERE processed_at IS NULL;
