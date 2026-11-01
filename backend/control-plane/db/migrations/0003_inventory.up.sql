-- Faz 02: node envanteri — grup, etiket, OS facts ve capability tabloları.
-- data-model.md temelli: UUID kimlik, UTC zaman damgaları, JSONB yalnızca
-- sürümlenmiş metadata için (node_facts.facts), etiket ayrı tabloda.

CREATE TABLE node_groups (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE CHECK (name ~ '^[a-z0-9_.-]{1,64}$'),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 256),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE nodes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    display_name  TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 128),
    hostname      TEXT CHECK (hostname IS NULL OR char_length(hostname) <= 253),
    os_family     TEXT NOT NULL DEFAULT 'unknown' CHECK (os_family IN ('linux', 'windows', 'unknown')),
    os_name       TEXT CHECK (os_name IS NULL OR char_length(os_name) <= 128),
    os_version    TEXT CHECK (os_version IS NULL OR char_length(os_version) <= 128),
    arch          TEXT CHECK (arch IS NULL OR char_length(arch) <= 32),
    agent_version TEXT CHECK (agent_version IS NULL OR char_length(agent_version) <= 64),
    -- Agent kaydı Faz 03'te geldiğinden 'pending' başlangıç durumudur.
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active', 'offline', 'retired')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ
);

CREATE INDEX nodes_status_idx ON nodes (status);
CREATE INDEX nodes_created_at_idx ON nodes (created_at);

CREATE TABLE node_group_members (
    node_id    UUID NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    group_id   UUID NOT NULL REFERENCES node_groups (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (node_id, group_id)
);

CREATE INDEX node_group_members_group_idx ON node_group_members (group_id);

CREATE TABLE node_tags (
    node_id UUID NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    tag     TEXT NOT NULL CHECK (tag ~ '^[a-z0-9_.:-]{1,64}$'),
    PRIMARY KEY (node_id, tag)
);

CREATE INDEX node_tags_tag_idx ON node_tags (tag);

-- OS facts: sürümlenmiş JSONB metadata (facts_version ile şema uyumu izlenir).
CREATE TABLE node_facts (
    node_id       UUID PRIMARY KEY REFERENCES nodes (id) ON DELETE CASCADE,
    facts         JSONB NOT NULL,
    facts_version INTEGER NOT NULL DEFAULT 1 CHECK (facts_version >= 1),
    observed_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Capability kayıtları (capability-system.md alanlarıyla hizalı).
CREATE TABLE node_capabilities (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id            UUID NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    capability_id      TEXT NOT NULL CHECK (capability_id ~ '^[a-z0-9_.]{3,128}$'),
    version            TEXT NOT NULL CHECK (char_length(version) <= 32),
    available          BOOLEAN NOT NULL DEFAULT false,
    reason_unavailable TEXT CHECK (reason_unavailable IS NULL OR char_length(reason_unavailable) <= 256),
    adapter_id         TEXT CHECK (adapter_id IS NULL OR char_length(adapter_id) <= 128),
    adapter_version    TEXT CHECK (adapter_version IS NULL OR char_length(adapter_version) <= 64),
    observed_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (node_id, capability_id)
);

CREATE INDEX node_capabilities_node_idx ON node_capabilities (node_id);
