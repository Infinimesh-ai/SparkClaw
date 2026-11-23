-- Each client shows only its own conversations (docs/client-link-layer-design.md 4.4).
-- A conversation created through the link layer records the device that
-- created it; conversations from the host WebChat and the message channels
-- keep an empty origin. Every existing conversation predates device
-- ownership and is deleted here (design premise 7), except the MCP binding
-- sessions, which are binding-owned state rather than conversation history.

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS origin_device_id text NOT NULL DEFAULT '';
ALTER TABLE session_tombstones ADD COLUMN IF NOT EXISTS origin_device_id text NOT NULL DEFAULT '';

DROP INDEX IF EXISTS sessions_owner_revision_idx;
DROP INDEX IF EXISTS session_tombstones_owner_revision_idx;
CREATE INDEX IF NOT EXISTS sessions_owner_device_revision_idx ON sessions (owner_id, origin_device_id, revision);
CREATE INDEX IF NOT EXISTS session_tombstones_owner_device_revision_idx ON session_tombstones (owner_id, origin_device_id, revision);

-- The device of a deleted conversation travels with its tombstone, so only
-- that device learns of the deletion.
CREATE OR REPLACE FUNCTION sparkclaw_session_tombstone() RETURNS trigger AS $$
BEGIN
  INSERT INTO session_tombstones (session_id, owner_id, origin_device_id, revision, deleted_at)
  VALUES (OLD.id, OLD.owner_id, OLD.origin_device_id, nextval('session_revision_seq'), now())
  ON CONFLICT (session_id) DO UPDATE SET owner_id = EXCLUDED.owner_id, origin_device_id = EXCLUDED.origin_device_id,
    revision = EXCLUDED.revision, deleted_at = EXCLUDED.deleted_at;
  RETURN OLD;
END
$$ LANGUAGE plpgsql;

-- Ownership never changes once a conversation exists.
CREATE OR REPLACE FUNCTION sparkclaw_session_keep_origin() RETURNS trigger AS $$
BEGIN
  NEW.origin_device_id := OLD.origin_device_id;
  RETURN NEW;
END
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS sessions_keep_origin ON sessions;
CREATE TRIGGER sessions_keep_origin BEFORE UPDATE ON sessions
  FOR EACH ROW EXECUTE FUNCTION sparkclaw_session_keep_origin();

-- Delete every existing conversation with the same closure as an owner's
-- session deletion (store/session_postgres.go sessionDeleteStatements). The
-- sessions themselves go last, so the subquery names the same set each time.

DELETE FROM workbench_drafts WHERE session_id IN (SELECT id FROM sessions WHERE source <> 'mcp');
DELETE FROM reminder_deliveries WHERE reminder_id IN (SELECT id FROM reminders WHERE session_id IN (SELECT id FROM sessions WHERE source <> 'mcp'));
DELETE FROM run_feedback WHERE session_id IN (SELECT id FROM sessions WHERE source <> 'mcp');
DELETE FROM approvals WHERE session_id IN (SELECT id FROM sessions WHERE source <> 'mcp');
DELETE FROM document_records WHERE session_id IN (SELECT id FROM sessions WHERE source <> 'mcp');
DELETE FROM memory_candidates WHERE session_id IN (SELECT id FROM sessions WHERE source <> 'mcp');
DELETE FROM memories WHERE source_run_id IN (SELECT id FROM agent_runs WHERE session_id IN (SELECT id FROM sessions WHERE source <> 'mcp'));
DELETE FROM episode_summaries WHERE session_id IN (SELECT id FROM sessions WHERE source <> 'mcp');
DELETE FROM artifact_objects WHERE session_id IN (SELECT id FROM sessions WHERE source <> 'mcp');
DELETE FROM weixin_chat_messages WHERE chat_session_id IN (SELECT id FROM weixin_chat_sessions WHERE linked_session_id IN (SELECT id FROM sessions WHERE source <> 'mcp'));
DELETE FROM external_chat_messages WHERE chat_session_id IN (SELECT id FROM external_chat_sessions WHERE linked_session_id IN (SELECT id FROM sessions WHERE source <> 'mcp'));
DELETE FROM weixin_chat_sessions WHERE linked_session_id IN (SELECT id FROM sessions WHERE source <> 'mcp');
DELETE FROM external_chat_sessions WHERE linked_session_id IN (SELECT id FROM sessions WHERE source <> 'mcp');
DELETE FROM browser_login_blocks WHERE session_id IN (SELECT id FROM sessions WHERE source <> 'mcp');
DELETE FROM tool_calls WHERE session_id IN (SELECT id FROM sessions WHERE source <> 'mcp');
DELETE FROM model_calls WHERE session_id IN (SELECT id FROM sessions WHERE source <> 'mcp');
DELETE FROM reminders WHERE session_id IN (SELECT id FROM sessions WHERE source <> 'mcp');
DELETE FROM messages WHERE session_id IN (SELECT id FROM sessions WHERE source <> 'mcp');
DELETE FROM agent_runs WHERE session_id IN (SELECT id FROM sessions WHERE source <> 'mcp');
DELETE FROM audit_events WHERE session_id IN (SELECT id FROM sessions WHERE source <> 'mcp');
DELETE FROM events WHERE session_id IN (SELECT id FROM sessions WHERE source <> 'mcp');
DELETE FROM sessions WHERE source <> 'mcp';

-- No client has seen a device-scoped cursor yet, so the tombstones of the
-- deleted conversations announce nothing to anyone.
DELETE FROM session_tombstones;
