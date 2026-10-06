CREATE TABLE IF NOT EXISTS workbench_drafts (
    owner_id text NOT NULL,
    session_id text NOT NULL DEFAULT '',
    content text NOT NULL DEFAULT '',
    attachment_ids jsonb NOT NULL DEFAULT '[]',
    revision bigint NOT NULL CHECK (revision > 0),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (owner_id, session_id)
);
