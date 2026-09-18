import { useState } from "react";
import { createRoot } from "react-dom/client";
import { api } from "./api/client";
import type { EmailConversation, EmailDraft, EmailMessage } from "./api/email";
import { EmailPopup } from "./components/emailPopup";
import { dictionaries } from "./i18n";
import "./styles/app.css";

const conversation: EmailConversation = {
  id: "launch",
  version: 4,
  title: "产品发布内容最终确认",
  participants: ["陈晓雯", "我"],
  summary: "陈晓雯确认发布文章已经定稿，预计下周二上午上线，并希望在上线前收到最终链接。你已确认时间可行，并请她补充官网与公众号的发布链接。",
  summary_state: "ready",
  unseen_count: 1,
  last_activity_at: "2026-09-17T07:38:00Z",
  concerns: []
};

const messages: EmailMessage[] = [
  {
    id: "launch-1", version: 1, mailbox_id: "gmail-main", receiving_address: "me@example.com", direction: "inbound",
    from: "陈晓雯 <xiaowen@example.com>", to: ["me@example.com"], cc: [], subject: "Re: 产品发布内容最终确认",
    arrived_at: "2026-09-16T08:15:00Z", sent_at: "2026-09-16T08:15:00Z", viewed: true, original_available: true, attachments: [],
    body_text: "你好，\n\n发布文章已经完成最终校对，我们计划下周二上午正式上线。请确认这个时间是否可行；上线前我会再发一次最终链接给你。\n\n谢谢！"
  },
  {
    id: "launch-2", version: 2, mailbox_id: "gmail-main", receiving_address: "me@example.com", direction: "outbound",
    from: "我 <me@example.com>", to: ["xiaowen@example.com"], cc: [], subject: "Re: 产品发布内容最终确认",
    arrived_at: "2026-09-16T09:02:00Z", sent_at: "2026-09-16T09:02:00Z", viewed: true, original_available: false, attachments: [],
    body_text: "晓雯你好，\n\n下周二上午没问题。麻烦上线前把官网和公众号的最终链接都发我一份，我会再做一次快速确认。"
  },
  {
    id: "launch-3", version: 3, mailbox_id: "gmail-main", receiving_address: "me@example.com", direction: "inbound",
    from: "陈晓雯 <xiaowen@example.com>", to: ["me@example.com"], cc: [], subject: "Re: 产品发布内容最终确认",
    arrived_at: "2026-09-17T07:38:00Z", sent_at: "2026-09-17T07:38:00Z", viewed: false, original_available: true, attachments: [],
    body_text: "好的，我会在周一下午把两个最终链接一起发给你。若发布时间有调整，也会第一时间同步。"
  }
];

let previewReplyDraft: EmailDraft = {
  id: "preview-reply", version: 1, mailbox_id: "gmail-main", mode: "reply", reply_mail_id: "launch-3",
  to: ["xiaowen@example.com"], cc: [], subject: "Re: 产品发布内容最终确认",
  body: "晓雯你好，\n\n收到，谢谢。周一下午发来后我会尽快完成最终确认；如果发布时间有调整，也请及时同步。",
  state: "draft"
};

Object.assign(api, {
  emailProviders: async () => ({ providers: [{ provider: "gmail", display_name: "Gmail", enabled: true, default: true, account: "default", state: "ready", version: 1 }] }),
  emailSyncStatus: async () => ({ version: 8, backlog: 0, pending_count: 0, mailboxes: [{ id: "gmail-main", version: 8, provider: "gmail", address: "me@example.com", active_binding: true, intake_enabled: true, state: "active" }] }),
  emailConversations: async () => ({ version: 4, conversations: [conversation, { ...conversation, id: "budget", title: "Q4 预算调整", summary: "财务已更新第四季度预算表，等待你确认。", unseen_count: 0, participants: ["财务团队", "我"] }, { ...conversation, id: "interview", title: "候选人面试时间", summary: "面试时间已调整到周五下午。", unseen_count: 0, participants: ["招聘团队", "我"] }], counts: { total: 3, unseen: 1 } }),
  emailInteractionMails: async () => ({ version: 1, messages: [] }),
  emailNotifications: async () => ({ version: 1, messages: [] }),
  emailPending: async () => ({ version: 1, messages: [] }),
  emailConversation: async () => ({ version: 4, conversation }),
  emailMessages: async () => ({ version: 4, messages }),
  emailPresentations: async (_kind: "mail" | "conversation", ids: string[], language: "en" | "zh") => ({ items: ids.map((id) => ({
    target_kind: "mail" as const, target_id: id, presentation_language: language, state: "ready" as const,
    analysis_revision: "preview-v1", revision: 1,
    summary: ({
      "launch-1": "陈晓雯确认文章已完成最终校对，计划下周二上午上线，并请你确认时间是否可行。",
      "launch-2": "你确认下周二上午可行，并请她在上线前补充官网和公众号的最终链接。",
      "launch-3": "陈晓雯会在周一下午发送两个最终链接，如发布时间调整也会及时同步。"
    } as Record<string, string>)[id] ?? "邮件摘要"
  })) }),
  ensureEmailPresentations: async () => ({ items: [] }),
  emailComposeCapabilities: async () => ({ compose: true, reply: true, reply_all: true, cc: true, max_to: 100 }),
  polishEmailReply: async () => ({ ...previewReplyDraft }),
  saveEmailDraft: async (draft: object) => {
    previewReplyDraft = { ...previewReplyDraft, ...draft, id: "preview-reply", version: previewReplyDraft.version + 1, state: "draft" };
    return { ...previewReplyDraft };
  },
  sendEmailDraft: async () => {
    previewReplyDraft = { ...previewReplyDraft, version: previewReplyDraft.version + 1, state: "sent" };
    return { ...previewReplyDraft };
  },
  markEmailViewed: async (ids: string[]) => ({ version: 4, mail_ids: ids }),
  reanalyzeEmail: async () => ({ scheduled: true })
});

function Preview() {
  const [selection, setSelection] = useState("launch");
  return <EmailPopup text={dictionaries.zh} language="zh" selection={selection} onSelect={setSelection} onClose={() => {}} />;
}

createRoot(document.getElementById("root")!).render(<Preview />);
