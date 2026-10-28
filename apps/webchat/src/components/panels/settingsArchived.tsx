// Settings view for conversations archived on this device: find, restore, or delete them.
import { useState } from "react";
import { ArchiveRestore, CircleAlert, Trash2 } from "lucide-react";
import type { Session } from "../../api/types";
import type { Language } from "../../i18n";
import { useArchivedConversations } from "../../hooks/useArchivedConversations";
import { shortId } from "../../lib/format";
import { ActionIcon } from "../ActionIcon";

export type ArchivedConversation = Pick<Session, "id" | "title"> & { source?: string };

// Every whitespace-separated keyword must appear in the title or the id.
function matches(conversation: ArchivedConversation, query: string) {
  const haystack = `${conversation.title}\n${conversation.id}`.toLocaleLowerCase();
  return query.toLocaleLowerCase().split(/\s+/).filter(Boolean).every((keyword) => haystack.includes(keyword));
}

export function ArchivedConversationsSettings({ conversations, ownerID, language, busy = false, onDelete }: {
  conversations: ArchivedConversation[];
  ownerID?: string;
  language: Language;
  busy?: boolean;
  // Asks for confirmation itself; the conversation leaves the list once it is deleted.
  onDelete?: (id: string) => void | Promise<void>;
}) {
  const zh = language === "zh";
  const { archived, save } = useArchivedConversations(ownerID);
  const [query, setQuery] = useState("");
  const [error, setError] = useState("");
  const archivedConversations = conversations.filter((conversation) => conversation.source !== "mcp" && archived.includes(conversation.id));
  const visible = archivedConversations.filter((conversation) => matches(conversation, query));
  const searchLabel = zh ? "搜索已归档对话" : "Search archived conversations";

  function restore(conversation: ArchivedConversation) {
    if (!save(archived.filter((id) => id !== conversation.id))) {
      setError(zh ? "无法保存归档状态，请检查浏览器存储后重试。" : "Could not save the archive state. Check browser storage and try again.");
      return;
    }
    setError("");
  }

  return <article className="settingsBlock clientSettings archivedConversationSettings">
    <label className="settingsPageSearch archivedConversationSearch">
      <ActionIcon name="search" />
      <input type="search" value={query} onChange={(event) => setQuery(event.target.value)}
        aria-label={searchLabel} placeholder={zh ? "按关键词搜索对话…" : "Search conversations by keyword…"} />
    </label>
    {error && <p className="credentialValidationFeedback error" role="alert">
      <span className="credentialValidationIcon" aria-hidden="true"><CircleAlert size={18} strokeWidth={1.75} /></span>
      <span className="credentialValidationCopy">{error}</span>
    </p>}
    {!archivedConversations.length ? <p className="muted">{zh ? "暂无归档对话" : "No archived conversations"}</p>
      : !visible.length ? <p className="muted" role="status">{zh ? "没有匹配的归档对话，试试其他关键词。" : "No matching archived conversations. Try another keyword."}</p>
      : <div className="clientList" role="list" aria-label={zh ? "已归档对话" : "Archived conversations"}>
        {visible.map((conversation) => <div className="clientItem archivedConversationItem" role="listitem" key={conversation.id}>
          <div>
            <strong title={conversation.title}>{conversation.title}</strong>
            <small>{shortId(conversation.id)}</small>
          </div>
          <span className="archivedConversationActions">
            <button type="button" className="ghost" onClick={() => restore(conversation)}
              aria-label={`${zh ? "取消归档" : "Unarchive conversation"}: ${conversation.title}`} title={zh ? "取消归档" : "Unarchive"}>
              <ArchiveRestore size={14} />
            </button>
            {onDelete && <button type="button" className="reject" disabled={busy} onClick={() => void onDelete(conversation.id)}
              aria-label={`${zh ? "删除对话" : "Delete conversation"}: ${conversation.title}`} title={zh ? "删除" : "Delete"}>
              <Trash2 size={14} />
            </button>}
          </span>
        </div>)}
      </div>}
  </article>;
}
