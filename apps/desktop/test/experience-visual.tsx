import { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { Bell, Globe2, PanelLeft, Plus, Search } from "lucide-react";
import { api } from "../../webchat/src/api/client";
import type { Approval, Client, Memory, Message, OwnerProfile, PublicConfig, Schedule, ToolCall } from "../../webchat/src/api/types";
import type { EmailMessage } from "../../webchat/src/api/email";
import { dictionaries } from "../../webchat/src/i18n";
import { setTextSize, setTheme, type TextSize, type Theme } from "../../webchat/src/lib/appearance";
import { ComposerSurface } from "../../webchat/src/components/composer";
import { MessageBubble } from "../../webchat/src/components/messages";
import { InspectorColumn, type PanelTab } from "../../webchat/src/components/inspector";
import { WorkspaceSettingsSidebar } from "../../webchat/src/components/settingsSidebar";
import { workbenchCopy } from "../../webchat/src/components/workbench";
import { ScheduleBar } from "../../webchat/src/components/schedules";
import { NotificationCenter } from "../../webchat/src/components/notificationCenter";
import { EmailMessageCard } from "../../webchat/src/components/emailMessage";
import type { VoiceInputModel } from "../../webchat/src/hooks/useVoiceInput";
import "../../webchat/src/styles/app.css";
import "../../webchat/src/styles/local-workbench.css";

// Sanitized production-component fixture. No Gateway, microphone, installed
// client profile, live mail, or transport is used by this visual qualification.
const timestamp = "2026-10-18T02:00:00Z", noop = () => {}, done = async () => {};
const text = dictionaries.zh, copy = workbenchCopy.zh;
const paragraph = "阅读连续性验证：中文正文、English terms 与数字 2026 应保持舒适行距。滚动查看长回答时，输入区、来源与附件仍然可访问。以下内容只使用固定测试数据，保留事实、风险、限制和完整文字。";
const longText = Array.from({ length: 24 }, () => paragraph).join("\n\n");
const mixedText = "# 阅读与输入保持一致\n\n中文与 English typography，16px / 28px。路径、网址和标识符可复制：\n\nhttps://example.invalid/" + "long-path-segment/".repeat(12) + "\n\n- 首项包含完整的信息与来源\n- 第二项保留风险和限制\n- Third item has a `copyable_identifier`\n\n## 数据与代码\n\n| 字段 | Value |\n| --- | --- |\n| 样例 | 保留全部数据 |\n\n```text\n" + "long_identifier_".repeat(12) + "\n```";
const config = {
  tool_policy: { risk_counts: { read: 8, dangerous: 2 }, definition_count: 10, definition_approval_required_tools: ["files.remove"], configured_approval_required_tools: ["browser.submit"], denied_tools: [], operator_controls: { web_access: true, workspace_files: true, shell_commands: false, file_changes: "ask", external_actions: "ask" } },
  model: { capacity_profile: "fixture", mock: true, ...Object.fromEntries(["fast", "deep", "embedding", "guard"].map(name => [name, { name, model: `${name}-fixture`, base_url: "", capacity_physical_model: "fixture", context_tokens: 8192, output_budgets: {} }])) },
  gateway: { bind: "127.0.0.1", port: 0, remote_access: "disabled", rate_limit: { enabled: false, requests_per_minute: 0, burst: 0 } },
  workspaces: { default_root: "/fixture/工作区/long-folder-name" }, sandbox: { enabled: true }, state: { backend: "memory" }, storage: { artifact_backend: "filesystem" }, memory: { enabled: true }, tools: { notifications: { channels: {} }, reminders: { enabled: true, default_channel: "web" } }
} as unknown as PublicConfig;
const owner: OwnerProfile = { id: "fixture", display_name: "测试工作区", email: "fixture@example.invalid", created_at: timestamp, updated_at: timestamp };
const clients: Client[] = [{ id: "fixture", name: "当前设备 · 中文 English", created_at: timestamp, last_seen_at: timestamp }, { id: "second", name: "另一个设备名称应完整可查找", created_at: timestamp }];
const memories: Memory[] = [{ id: "memory", kind: "preference", content: paragraph, source_run_id: "fixture", created_at: timestamp }];
const approvals: Approval[] = [{ id: "approval", source: "tool", session_id: "fixture", run_id: "fixture", tool_call_id: "call", tool: "workspace.file.write", risk: "reversible", status: "pending", summary: "保存项目报告", reason: "需要确认写入目标与本次操作范围。", resources: ["/fixture/报告/总结.txt"], arguments: { path: "/fixture/报告/总结.txt", scope: "single_operation" }, created_at: timestamp }];
const calls: ToolCall[] = [{ id: "call", session_id: "fixture", run_id: "fixture", tool: "workspace.read", risk: "read", status: "completed", arguments: { path: "/fixture/说明.txt" }, observation_summary: paragraph, started_at: timestamp, completed_at: timestamp }];
const schedules: Schedule[] = [{ id: "schedule", title: "每周检查项目进展并整理中文与 English 摘要", text: paragraph, due_time: "2026-10-19T01:00:00Z", timezone: "Asia/Shanghai", recurrence: "FREQ=WEEKLY;BYDAY=MO", status: "pending", updated_at: timestamp, editable: true, cancelable: true, endpoint: { kind: "web", status: "active", conversation_label: "项目讨论" } }];
const voice = { state: "idle", disabled: false, active: false, devices: [], selectedDeviceId: "default", silenceMode: "manual", previewState: "idle", previewLevel: 0, partialText: "", elapsedMs: 0, level: 0, refreshDevices: done, stopPreview: done, togglePreview: done, toggle: done, selectDevice: noop, setSilenceMode: noop, cancel: noop, retry: done } as unknown as VoiceInputModel;
api.integrations = async () => ({ integrations: [] });
api.emailRenderPreview = async () => ({ state: "ready", content: [{ kind: "paragraph", children: [{ kind: "text", text: paragraph }] }, { kind: "table", children: [{ kind: "row", children: ["项目", "Chinese / English", "可横向滚动的完整内容"].map(value => ({ kind: "header_cell", children: [{ kind: "text", text: value }] })) }, { kind: "row", children: Array.from({ length: 3 }, () => ({ kind: "cell", children: [{ kind: "text", text: paragraph }] })) }] }, { kind: "code_block", text: "const filename = 'fixture';" }] } as never);

type Scene = "empty" | "short" | "long" | "mixed" | "attachments" | "schedules" | "mail" | "files" | "notifications" | PanelTab;
const settings = ["settings", "appearance", "devices", "models-tools", "permissions", "connections", "memory", "approvals", "timeline"];
function Fixture() {
  const [scene, setScene] = useState<Scene>("long"), [input, setInput] = useState("继续讨论，保留这份草稿。");
  const [appearanceRevision, setAppearanceRevision] = useState(0);
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  useEffect(() => {
    Object.assign(window, { experienceVisual: {
      setScene, setInput,
      setAppearance(theme: Theme, size: TextSize) {
        setTheme(theme);
        setTextSize(size);
        setAppearanceRevision(revision => revision + 1);
      }
    } });
  }, []);
  const isSettings = settings.includes(scene), tab = scene as PanelTab;
  const message: Message = { id: "assistant", session_id: "fixture", role: "assistant", content: scene === "long" ? longText : scene === "mixed" ? mixedText : paragraph, created_at: timestamp };
  const mail = { id: "mail", mailbox_id: "fixture", from: "同事 <fixture@example.invalid>", to: ["reader@example.invalid"], cc: [], subject: "项目进度与待确认事项", sent_at: timestamp, arrived_at: timestamp, direction: "inbound", summary: paragraph, body_available: true, body_text: paragraph, attachments: [{ id: "file", name: "项目报告-Chinese-English.pdf", size: 4096, available: false }] } as unknown as EmailMessage;
  return <div className={`shell workbench localWorkbench ${isSettings ? "settingsPageMode" : ""}`}>
    {isSettings ? <WorkspaceSettingsSidebar text={text} language="zh" tab={tab} pendingApprovalCount={1} pendingCandidateCount={0} onTabChange={setScene} onBack={() => setScene("long")} /> : <aside className="sidebar">
      <div className="brandRow"><span className="brand"><strong className="workbenchBrandName">Spark<span>X</span></strong></span><button className="iconButton" aria-label="收起侧栏"><PanelLeft size={18} /></button></div>
      <button className="sidebarScheduleLink" onClick={() => setScene("schedules")}>日程</button><div className="conversationListHeader">最近任务<div className="conversationListActions"><button className="conversationHeaderButton" aria-label="搜索"><Search size={18} /></button><button className="conversationHeaderButton" aria-label="新建"><Plus size={18} /></button></div></div>
      <div className="sessionList">{["阅读与输入对齐", "包含中文与 English 的很长会话标题用于键盘可见性验证", "项目进度"].map((title, index) => <article className={`sessionItem ${index === 0 ? "active" : ""}`} key={title}><button className="sessionSelect" title={title}><span className="sessionTitle">{title}</span></button><div className="sessionActions"><button className="miniIconButton" aria-label="更多">···</button></div></article>)}</div>
      <div className="sidebarFooter"><button className="sidebarAccountTrigger" onClick={() => setScene("settings")}><span className="workspaceAvatar">测</span><span className="sidebarAccountIdentity"><strong>测试工作区</strong><small>fixture@example.invalid</small></span></button></div>
    </aside>}
    <main className={isSettings ? "settingsPageMain" : "workspace desktopWorkbench"}>
      {isSettings ? <div className="settingsPageContent"><header className="settingsPageHeader"><h1>{copy.settingsTitles[tab]}</h1><p>{copy.settingsDescriptions[tab]}</p></header><InspectorColumn key={`${scene}-${appearanceRevision}`} settingsPage showTabs={false} tab={tab} onTabChange={setScene} text={text} language="zh" pendingApprovalCount={1} pendingCandidateCount={0} toolCalls={calls} approvals={approvals} candidates={[]} memories={memories} traceRun={null} traceList={[]} traceLoading={false} ready={null} modelCalls={[]} auditEvents={[]} artifacts={[]} episodes={[]} evalRuns={[]} runtimeConfig={config} ownerProfile={owner} clients={clients} currentClientID="fixture" onReloadClients={async () => clients} connectors={[]} notificationBindings={[]} onOpenTrace={noop} setError={noop} surfaceError={noop} refreshGlobal={done} refreshActiveSession={done} setEvalRuns={noop} setNotificationBindings={noop} setConnectors={noop} setRuntimeConfig={noop} setOwnerProfile={noop} onLanguageChange={noop} onOpenSchedules={() => setScene("schedules")} /></div> : <>
        <header className="topbar"><span className="workspaceLabel">工作区</span><div className="topbarActions"><NotificationCenter notifications={[{ id: "notification", notification_id: "notification", source: "fixture", kind: "document_mention", deep_link: "https://example.invalid/fixture", occurred_at: timestamp, created_at: timestamp, updated_at: timestamp }]} unreadCount={1} open={scene === "notifications"} toast={null} text={text} language="zh" onToggle={() => setScene(scene === "notifications" ? "long" : "notifications")} onDismissToast={noop} onRead={done} onReadAll={done} /><button className="iconButton" aria-label="打开浏览器"><Globe2 size={18} /></button></div></header>
        {scene === "schedules" ? <section className="workbenchPage schedulePage"><header className="workbenchPageHeader"><div><h1>日程</h1><p>下次运行、时区与执行状态</p></div><button className="primaryButton">创建日程</button></header><ScheduleBar schedules={schedules} open loading={false} busyId="" language="zh" text={text} onToggle={noop} onRefresh={noop} onEdit={done} onDelete={done} /></section> : scene === "mail" ? <div className="emailPopup" style={{ width: "100%", height: "100%", display: "flex", flexDirection: "column" }}><header className="emailPopupHeader"><h2>邮件</h2><button className="primaryButton">撰写邮件</button></header><div className="emailPanes"><aside className="emailCategoryPane"><div className="emailCategoryTabs">{["收件箱", "重要邮件", "已发送"].map(label => <button key={label}><Bell size={18} /><span>{label}</span></button>)}</div></aside><div className="emailConversationPane"><button className="emailConversationRow selected"><span className="emailThreadAvatar">同</span><span className="emailThreadCopy"><span className="emailThreadMeta"><strong>同事</strong><time>10:00</time></span><b>项目进度与待确认事项</b><small>{paragraph}</small></span></button></div><div className="emailDetailPane"><div className="emailTimeline"><EmailMessageCard mail={mail} viewed={false} text={text} language="zh" busy={false} onReanalyze={noop} onError={noop} onReply={noop} /></div></div></div></div> : scene === "files" ? <section className="workbenchPage"><header className="workbenchPageHeader"><h1>文件</h1></header>{["项目总结.pdf", "very-long-filename-with-Chinese-中文文档说明.xlsx"].map(name => <div className="localFileRow" key={name}><label><input type="checkbox" />{name}<small>PDF · 24 KB · 2026-10-18</small></label><button>预览</button><button>下载</button></div>)}</section> : <section className={`chatColumn ${scene === "empty" ? "homeChat" : ""}`}><div className="messageList">{scene === "empty" ? <div className="workbenchWelcome"><h1>今天可以帮你做什么？</h1></div> : <><MessageBubble message={{ ...message, id: "user", role: "user", content: "请保留原有界面，优化阅读与输入体验。" }} text={text} language="zh" streamStatuses={[]} onFeedback={done} /><MessageBubble message={message} text={text} language="zh" streamStatuses={[]} onFeedback={done} /></>}</div><ComposerSurface text={text} language="zh" activeSession="fixture" activeInput={input} activeAttachments={scene === "attachments" ? [{ name: "项目报告.pdf", rel_path: "fixture/report.pdf", content_type: "application/pdf" }] : []} busy={false} voice={voice} composerInputRef={inputRef} onInputChange={setInput} onUploadDocument={done} onChooseDocument={done} onOpenAttachment={done} onRemoveAttachment={noop} onSend={noop} /></section>}
      </>}
    </main>
  </div>;
}
createRoot(document.getElementById("root")!).render(<Fixture />);
