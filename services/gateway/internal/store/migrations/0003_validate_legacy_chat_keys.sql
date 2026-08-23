DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM external_chat_sessions AS target
    JOIN weixin_chat_sessions AS source ON source.id = target.id
    WHERE target.binding_id = source.binding_id
      AND target.external_chat_id = source.external_user_id
      AND target.external_thread_id = ''
    GROUP BY target.binding_id, target.external_chat_id, target.external_thread_id
    HAVING count(*) > 1
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = '23514',
      MESSAGE = 'external chat session adoption produced a duplicate canonical natural key';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM external_chat_messages AS target
    JOIN weixin_chat_messages AS source ON source.id = target.id
    WHERE source.external_message_id <> ''
      AND target.chat_session_id = source.chat_session_id
      AND target.external_message_id = source.external_message_id
    GROUP BY target.chat_session_id, target.external_message_id
    HAVING count(*) > 1
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = '23514',
      MESSAGE = 'external chat message adoption produced a duplicate canonical natural key';
  END IF;
END
$$;
