-- Faz 03: görevlerin yürütme hedefi — control_plane (dahili) veya node (agent).
-- agent.echo gibi node operasyonları stream üzerinden node'a dağıtılır.

ALTER TABLE tasks
    ADD COLUMN IF NOT EXISTS execution_target TEXT NOT NULL DEFAULT 'control_plane'
        CHECK (execution_target IN ('control_plane', 'node'));

-- Node-hedefli kuyruk sorgusu için kısmi indeks.
CREATE INDEX IF NOT EXISTS tasks_node_queued_idx ON tasks (node_id, created_at)
    WHERE execution_target = 'node' AND status = 'queued';
