-- Faz 01 baseline: platform metadata tablosu.
-- Amaç: migration altyapısının uçtan uca kanıtlanması ve readyz/destek
-- tanılamalarında şema taban bilgisinin sorgulanabilir olması.
-- Ürün şemaları (users, nodes, tasks...) Faz 02'de ayrı migration'larla gelir.

CREATE TABLE IF NOT EXISTS platform_metadata (
    key        TEXT PRIMARY KEY,
    value      JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO platform_metadata (key, value)
VALUES ('schema_baseline', jsonb_build_object('phase', 1, 'migration', '0001_baseline'))
ON CONFLICT (key) DO NOTHING;
