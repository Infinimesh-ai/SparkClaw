// Match the host Store's initial-question title length, keeping local labels
// on one line and slicing Unicode code points rather than UTF-16 code units.
export function initialQuestionTitle(content) {
  const characters = Array.from(content.trim().replace(/\s+/gu, " "));
  return characters.slice(0, 42).join("") + (characters.length > 42 ? "..." : "");
}

export function migrateConversationTitles(db) {
  db.exec("ALTER TABLE conversations ADD COLUMN title_pending INTEGER NOT NULL DEFAULT 0 CHECK(title_pending IN (0,1))");
  // Old releases stored only the label, without its origin. Infer pending
  // titles once during migration; normal writes use the explicit flag.
  const defaults = ["", "新对话", "New conversation", "New SparkClaw Session"];
  const conversations = db.prepare(`SELECT id FROM conversations WHERE hidden=0 AND title IN (${defaults.map(() => "?").join(",")})`).all(...defaults);
  const firstQuestion = db.prepare("SELECT content FROM messages WHERE conversation_id=? AND role='user' ORDER BY rowid LIMIT 1");
  const rename = db.prepare("UPDATE conversations SET title=?,title_pending=0 WHERE id=?");
  const pending = db.prepare("UPDATE conversations SET title_pending=1 WHERE id=?");
  for (const conversation of conversations) {
    const title = initialQuestionTitle(firstQuestion.get(conversation.id)?.content ?? "");
    if (title) rename.run(title, conversation.id);
    else pending.run(conversation.id);
  }
}
