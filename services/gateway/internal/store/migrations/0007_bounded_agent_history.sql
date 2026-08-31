CREATE INDEX IF NOT EXISTS idx_messages_session_recent
  ON messages(session_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_tool_calls_session_recent_terminal
  ON tool_calls(session_id, started_at DESC, id DESC)
  WHERE completed_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_episode_summaries_session_recent
  ON episode_summaries(session_id, created_at DESC, id ASC);
