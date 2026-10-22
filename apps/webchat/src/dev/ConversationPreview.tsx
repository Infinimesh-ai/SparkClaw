// A separate development entry: production App, gateway state, drafts and
// repositories are never mounted. All example interactions stay in memory.
import { StrictMode, useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { X } from "lucide-react";
import type { Message, Session } from "../api/types";
import { ActionIcon } from "../components/ActionIcon";
import { ComposerSurface } from "../components/composer";
import { MessageBubble } from "../components/messages";
import { NotificationCenter } from "../components/notificationCenter";
import { SessionSidebar } from "../components/sidebar";
import { TaskSearch, WorkbenchWelcome } from "../components/workbench";
import { useMessageScroll } from "../hooks/useMessageScroll";
import type { VoiceInputModel } from "../hooks/useVoiceInput";
import { dictionaries } from "../i18n";
import { applyAppearance } from "../lib/appearance";
import "../styles/app.css";

type PreviewConversation = Session & { messages: Message[]; draft: string };

const language = "zh";
const text = dictionaries[language];
const disabledVoice: VoiceInputModel = {
  state: "disabled",
  disabled: true,
  active: false,
  level: 0,
  elapsedMs: 0,
  partialText: "",
  partialFrozen: false,
  silenceMode: "standard",
  errorCode: "speech_model_unavailable",
  errorDetail: "",
  retryable: false,
  hasPendingTranscript: false,
  devices: [],
  selectedDeviceId: "",
  deviceFallback: false,
  previewState: "idle",
  previewLevel: 0,
  previewErrorCode: "",
  refreshDevices: async () => {},
  selectDevice: () => {},
  setSilenceMode: () => {},
  togglePreview: () => {},
  stopPreview: async () => {},
  toggle: () => {},
  retry: () => {},
  insertPending: () => {},
  cancel: async () => {}
};

function exampleMessage(session: string, index: number, role: Message["role"], content: string): Message {
  return {
    id: `${session}-message-${index}`,
    session_id: session,
    role,
    content,
    created_at: `2026-10-21T16:${String(20 + index).padStart(2, "0")}:00.000Z`,
    ...(role === "assistant" ? { run_id: `${session}-example-run-${index}` } : {})
  };
}

function initialConversations(): PreviewConversation[] {
  return [
    {
      id: "preview-plan",
      title: "安排明天的工作",
      created_at: "2026-10-21T16:20:00.000Z",
      updated_at: "2026-10-21T16:24:00.000Z",
      draft: "",
      messages: [
        exampleMessage("preview-plan", 0, "user", "帮我安排一下明天的工作。上午想留两小时做方案，下午还要处理邮件。"),
        exampleMessage("preview-plan", 1, "assistant", "可以，把需要专注的工作留给上午，再把零散沟通集中到下午。\n\n### 上午：完成方案\n- 09:30–11:30，完善目标、结构和关键示例。\n- 开始前切换到 `focus` 状态，先完成一个可以评审的版本。\n\n### 下午：集中收尾\n- 留出半小时处理邮件，优先回复需要你决定的问题。\n- 下班前用十分钟记录进展，写下明天的第一步。"),
        exampleMessage("preview-plan", 2, "user", "那会议放在什么时候比较合适？"),
        exampleMessage("preview-plan", 3, "assistant", "建议放在下午 14:00 左右，给上午的方案留出完整时间。\n\n### 一点安排建议\n- 把议题和需要决定的问题提前发给参会人。\n- 控制在 30 分钟内，结束时明确下一步和负责人。\n\n这样上午能专注推进，下午也有余地处理新的反馈。")
      ]
    },
    {
      id: "preview-writing",
      title: "把项目进展说清楚",
      created_at: "2026-10-21T15:20:00.000Z",
      updated_at: "2026-10-21T15:24:00.000Z",
      draft: "",
      messages: [
        exampleMessage("preview-writing", 0, "user", "项目进展邮件怎么写得简短一点，又不漏掉重点？"),
        exampleMessage("preview-writing", 1, "assistant", "可以只保留三个部分，让读者很快知道项目在哪里。\n\n### 建议结构\n- **已经完成**：写结果，不必逐项描述过程。\n- **接下来做什么**：给出最近的一个里程碑。\n- **需要谁配合**：明确问题、负责人和时间。\n\n标题也可以直接一点，例如：`项目进展｜本周完成与下周安排`。")
      ]
    },
    {
      id: "preview-reading",
      title: "整理阅读笔记",
      created_at: "2026-10-20T15:20:00.000Z",
      updated_at: "2026-10-20T15:24:00.000Z",
      draft: "",
      messages: [
        exampleMessage("preview-reading", 0, "user", "读完一篇文章，怎样留下真正有用的笔记？"),
        exampleMessage("preview-reading", 1, "assistant", "先用自己的话回答三个问题，比复制很多段落更容易再次使用。\n\n- 作者最重要的判断是什么？\n- 哪个例子改变了你的理解？\n- 你准备把它用在什么地方？\n\n最后补上一句简短的行动，例如：`下次写方案时，先解释要解决的问题。`")
      ]
    }
  ];
}

function ConversationPreview() {
  useEffect(() => {
    const dispose = applyAppearance();
    // Optional visual-review overrides affect this development page only.
    const params = new URLSearchParams(window.location.search);
    const theme = params.get("theme");
    const size = params.get("text-size");
    if (theme === "light" || theme === "dark") document.documentElement.dataset.resolvedTheme = theme;
    if (size === "small" || size === "default" || size === "large") document.documentElement.dataset.textSize = size;
    return dispose;
  }, []);
  const [conversations, setConversations] = useState(initialConversations);
  const [selected, setSelected] = useState("preview-plan");
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const [notificationsOpen, setNotificationsOpen] = useState(false);
  const [editingSession, setEditingSession] = useState("");
  const [titleDraft, setTitleDraft] = useState("");
  const [notice, setNotice] = useState("");
  const composerInputRef = useRef<HTMLTextAreaElement | null>(null);
  const messageScroll = useMessageScroll(selected);
  const active = conversations.find(conversation => conversation.id === selected)!;
  const showHome = active.messages.length === 0;

  function selectConversation(conversation: PreviewConversation) {
    setSelected(conversation.id);
    setSearchOpen(false);
    setNotice("");
  }

  function createConversation() {
    const now = new Date().toISOString();
    const conversation: PreviewConversation = {
      id: `preview-${crypto.randomUUID()}`,
      title: "新对话示例",
      created_at: now,
      updated_at: now,
      draft: "",
      messages: []
    };
    setConversations(current => [conversation, ...current]);
    setSelected(conversation.id);
    setNotice("");
    requestAnimationFrame(() => composerInputRef.current?.focus());
  }

  function updateDraft(value: string) {
    setConversations(current => current.map(conversation => conversation.id === selected ? { ...conversation, draft: value } : conversation));
  }

  function send() {
    const content = active.draft.trim();
    if (!content) return;
    const now = new Date().toISOString();
    const id = crypto.randomUUID();
    const user: Message = { id: `preview-user-${id}`, session_id: selected, role: "user", content, created_at: now };
    const assistant: Message = {
      id: `preview-assistant-${id}`,
      session_id: selected,
      role: "assistant",
      run_id: `preview-run-${id}`,
      created_at: now,
      content: "### 示例回复\n已收到你输入的内容。这条回复仅用于预览，没有调用模型或执行工具。\n\n- 可以继续发送消息，观察真实对话组件的排版。\n- 也可以切换侧栏会话，查看不同长度的内容。"
    };
    setConversations(current => current.map(conversation => conversation.id === selected
      ? { ...conversation, title: conversation.messages.length ? conversation.title : content.slice(0, 24), updated_at: now, draft: "", messages: [...conversation.messages, user, assistant] }
      : conversation));
    messageScroll.resume();
  }

  function rename(id: string) {
    const title = titleDraft.trim();
    if (!title) return;
    setConversations(current => current.map(conversation => conversation.id === id ? { ...conversation, title } : conversation));
    setEditingSession("");
  }

  function remove(id: string) {
    const remaining = conversations.filter(conversation => conversation.id !== id);
    if (!remaining.length) { createConversation(); setConversations(current => current.filter(conversation => conversation.id !== id)); return; }
    setConversations(remaining);
    if (selected === id) setSelected(remaining[0].id);
  }

  // App navigation is in component state, not URL routes. Leave the isolated
  // preview and return to the actual application rather than imitating it.
  const returnToApplication = () => window.location.assign("/");

  return <main className={`shell workbench glassWorkbench ${showHome ? "homePresentation" : ""} ${sidebarCollapsed ? "sidebarCollapsed" : ""}`}>
    <SessionSidebar text={text} language={language} page="chat" ownerProfile={null}
      sessions={conversations} activeSession={selected} searchOpen={searchOpen}
      editingSession={editingSession} sessionTitleDraft={titleDraft}
      onCreateSession={createConversation} onSelectSession={selectConversation}
      onStartRename={conversation => { setEditingSession(conversation.id); setTitleDraft(conversation.title); }}
      onCancelRename={() => setEditingSession("")} onRenameSubmit={rename}
      onTitleDraftChange={setTitleDraft} onDeleteSession={remove}
      onNavigate={returnToApplication} onSearch={() => setSearchOpen(true)}
      onToggleSidebar={() => setSidebarCollapsed(current => !current)} />

    <section className={`workspace ${showHome ? "homeWorkspace" : ""}`}>
      <header className="topbar">
        <button className="iconButton sidebarToggle" onClick={() => setSidebarCollapsed(current => !current)} aria-label="切换侧栏"><ActionIcon name="panel-left" /></button>
        <span className="workspaceLabel">对话预览 · 示例内容</span>
        <div className="topbarActions">
          <NotificationCenter notifications={[]} unreadCount={0} open={notificationsOpen} toast={null}
            language={language} text={text} onToggle={() => setNotificationsOpen(current => !current)}
            onDismissToast={() => setNotificationsOpen(false)} onRead={async () => {}} onReadAll={async () => {}} />
          <button className="iconButton rightSidebarToggle" disabled title="浏览器在对话预览中不可用" aria-label="浏览器在对话预览中不可用"><ActionIcon name="panel-right" /></button>
        </div>
      </header>
      {notice && <div className="noticeBanner" role="status"><span>{notice}</span><button className="iconButton" onClick={() => setNotice("")} title="关闭提示" aria-label="关闭提示"><X size={15} /></button></div>}
      <section className={`chatColumn ${showHome ? "homeChat" : ""}`}>
        <div ref={messageScroll.listRef} className="messageList" aria-label="示例对话">
          {active.messages.length ? active.messages.map(message => <MessageBubble key={message.id}
            message={message} streamStatuses={message.id === "preview-plan-message-3" ? [{ id: "example-completed", type: "tool_completed", text: "示例状态 · 已完成" }] : []} text={text} language={language} />)
            : <WorkbenchWelcome language={language} description />}
        </div>
        {messageScroll.away && <div className="messageScrollAction"><button type="button" className="messageLatest" onClick={messageScroll.resume}>回到最新</button></div>}
        <ComposerSurface text={text} language={language} activeSession={selected} activeInput={active.draft}
          activeAttachments={[]} busy={false} voice={disabledVoice} composerInputRef={composerInputRef}
          canCompose canSend filesEnabled={false} mailEnabled={false}
          onInputChange={updateDraft} onSend={send}
          onUploadDocument={() => setNotice("文件上传在此预览中不可用。")}
          onChooseDocument={() => setNotice("文件选择在此预览中不可用。")}
          onOpenAttachment={() => setNotice("附件在此预览中不可用。")}
          onRemoveAttachment={() => setNotice("附件在此预览中不可用。")}
          onOpenModels={showHome ? returnToApplication : undefined} />
      </section>
    </section>
    {searchOpen && <TaskSearch language={language} sessions={conversations} onSelect={selectConversation} onClose={() => setSearchOpen(false)} />}
  </main>;
}

const host = document.getElementById("root");
if (import.meta.env.DEV && host) {
  createRoot(host).render(<StrictMode><ConversationPreview /></StrictMode>);
} else if (host) {
  host.textContent = "此对话预览仅在开发模式下可用。";
}
