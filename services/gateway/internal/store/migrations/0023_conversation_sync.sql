-- Backend-authoritative conversation sync (docs/client-link-layer-design.md 4.1).
-- sessions.revision is a global monotonic change cursor; messages.seq is
-- monotonic within a conversation. Triggers keep both current so every
-- existing write path (host WebChat, link layer) stays unchanged.
CREATE SEQUENCE IF NOT EXISTS session_revision_seq;

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS revision bigint NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS last_seq bigint NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS preview text NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS archived boolean NOT NULL DEFAULT false;

ALTER TABLE messages ADD COLUMN IF NOT EXISTS seq bigint NOT NULL DEFAULT 0;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS client_message_id text;

CREATE UNIQUE INDEX IF NOT EXISTS messages_session_client_message_id_idx
  ON messages (session_id, client_message_id) WHERE client_message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS messages_session_seq_idx ON messages (session_id, seq);
CREATE INDEX IF NOT EXISTS sessions_owner_revision_idx ON sessions (owner_id, revision);

-- Deleted conversations leave a tombstone so other clients learn of the
-- deletion through sync.changes.
CREATE TABLE IF NOT EXISTS session_tombstones (
  session_id text PRIMARY KEY,
  owner_id text NOT NULL,
  revision bigint NOT NULL,
  deleted_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS session_tombstones_owner_revision_idx ON session_tombstones (owner_id, revision);

-- Backfill existing history.
WITH ordered AS (
  SELECT id, row_number() OVER (PARTITION BY session_id ORDER BY created_at, id) AS n
  FROM messages
)
UPDATE messages AS m SET seq = ordered.n FROM ordered WHERE m.id = ordered.id;

UPDATE sessions AS s SET
  last_seq = COALESCE((SELECT max(seq) FROM messages WHERE session_id = s.id), 0),
  preview = COALESCE((SELECT left(content, 200) FROM messages WHERE session_id = s.id ORDER BY seq DESC LIMIT 1), ''),
  revision = nextval('session_revision_seq');

CREATE OR REPLACE FUNCTION sparkclaw_message_assign_seq() RETURNS trigger AS $$
BEGIN
  UPDATE sessions
     SET last_seq = last_seq + 1,
         preview = left(NEW.content, 200)
   WHERE id = NEW.session_id
  RETURNING last_seq INTO NEW.seq;
  RETURN NEW;
END
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION sparkclaw_session_assign_revision() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'INSERT'
     OR NEW.title IS DISTINCT FROM OLD.title
     OR NEW.updated_at IS DISTINCT FROM OLD.updated_at
     OR NEW.last_seq IS DISTINCT FROM OLD.last_seq
     OR NEW.archived IS DISTINCT FROM OLD.archived
     OR NEW.hidden IS DISTINCT FROM OLD.hidden
     OR NEW.owner_id IS DISTINCT FROM OLD.owner_id THEN
    NEW.revision := nextval('session_revision_seq');
  END IF;
  RETURN NEW;
END
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION sparkclaw_session_tombstone() RETURNS trigger AS $$
BEGIN
  INSERT INTO session_tombstones (session_id, owner_id, revision, deleted_at)
  VALUES (OLD.id, OLD.owner_id, nextval('session_revision_seq'), now())
  ON CONFLICT (session_id) DO UPDATE SET owner_id = EXCLUDED.owner_id, revision = EXCLUDED.revision, deleted_at = EXCLUDED.deleted_at;
  RETURN OLD;
END
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS messages_assign_seq ON messages;
CREATE TRIGGER messages_assign_seq BEFORE INSERT ON messages
  FOR EACH ROW EXECUTE FUNCTION sparkclaw_message_assign_seq();

DROP TRIGGER IF EXISTS sessions_assign_revision ON sessions;
CREATE TRIGGER sessions_assign_revision BEFORE INSERT OR UPDATE ON sessions
  FOR EACH ROW EXECUTE FUNCTION sparkclaw_session_assign_revision();

DROP TRIGGER IF EXISTS sessions_tombstone ON sessions;
CREATE TRIGGER sessions_tombstone AFTER DELETE ON sessions
  FOR EACH ROW EXECUTE FUNCTION sparkclaw_session_tombstone();
