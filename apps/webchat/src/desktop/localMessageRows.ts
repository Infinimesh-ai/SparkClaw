import type { Message } from "../api/types";
import { isTaskInProgress } from "../lib/conversationActivity";
import type { LocalConversationContent } from "./clientStore";

export type LocalMessageRow = { key: string; scrollAnchorId: string; message: Message; inProgress: boolean };

// Both the ephemeral answer and its durable replacement render through the
// same MessageBubble with the same key. Persistent message IDs remain intact.
export function localMessageRows(content: LocalConversationContent, sessionId: string, pendingTasks = content.tasks.filter(task => isTaskInProgress(task.status))): LocalMessageRow[] {
  const delivered = new Set<string>();
  const rows = content.messages.map(message => {
    const requestId = message.role === "assistant" ? message.request_id : undefined;
    if (requestId) delivered.add(requestId);
    const key = requestId ? `execution:${requestId}` : message.id;
    return { key, scrollAnchorId: key, message: { ...message, session_id: sessionId }, inProgress: false };
  });
  for (const task of pendingTasks) {
    if (delivered.has(task.request_id)) continue;
    const key = `execution:${task.request_id}`;
    rows.push({ key, scrollAnchorId: key, inProgress: true,
      message: { id: `progress:${task.id}`, session_id: sessionId, role: "assistant", created_at: task.created_at,
        content: task.progress_projection?.content ?? task.progress_content ?? "" } });
  }
  return rows;
}
