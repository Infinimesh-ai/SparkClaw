import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { FileDown, LogOut, Plus, Settings } from "lucide-react";
import { api } from "../api/client";
import { InspectorColumn, type PanelTab } from "../components/inspector";
import { WorkspaceSettingsSidebar } from "../components/settingsSidebar";
import { dictionaries, initialLanguage, LANGUAGE_STORAGE_KEY, type Language } from "../i18n";
import { applyAppearance } from "../lib/appearance";
import { clientStore, type LocalConversation, type LocalConversationContent } from "./clientStore";
import { desktopCapability } from "./capability";
import "../styles/local-workbench.css";

const emptyContent: LocalConversationContent = { messages: [], tasks: [], files: [] };

// Desktop R3 has its own data path. It never mounts App's legacy shared-session
// hooks. Server-dependent features become available only as their phases pass.
export function LocalWorkbench() {
  const store = clientStore()!;
  const desktop = desktopCapability()!;
  const [language, setLanguage] = useState<Language>(initialLanguage);
  const zh = language === "zh";
  const [conversations, setConversations] = useState<LocalConversation[]>([]);
  const [selected, setSelected] = useState("");
  const selectedRef = useRef(selected); selectedRef.current = selected;
  const readGeneration = useRef(0);
  const [content, setContent] = useState(emptyContent);
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [settings, setSettings] = useState(false);
  const [tab, setTab] = useState<PanelTab>("devices");
  const [currentClientID, setCurrentClientID] = useState("");

  const surfaceError = useCallback((err: unknown) => {
    setError(err instanceof Error ? err.message : zh ? "本地保存失败，请重试。" : "Local save failed. Try again.");
  }, [zh]);
  const reload = useCallback(async () => { setConversations(await store.list()); }, [store]);
  const select = useCallback(async (id: string) => {
    selectedRef.current = id; setSelected(id); setContent(emptyContent); setDraft("");
    const generation = ++readGeneration.current;
    const next = await store.read(id);
    if (selectedRef.current === id && readGeneration.current === generation) setContent(next);
  }, [store]);
  useEffect(() => {
    applyAppearance();
    let active = true;
    void store.list().then((rows) => { if (active) setConversations(rows); }).catch(surfaceError)
      .finally(() => { if (active) setLoading(false); });
    void desktop.localConnection().then((status) => { if (active) setCurrentClientID(status.client_id ?? ""); }).catch(surfaceError);
    return () => { active = false; ++readGeneration.current; };
  }, [desktop, store, surfaceError]);

  async function action(operation: () => Promise<void>) {
    if (busyRef.current) return;
    busyRef.current = true;
    setBusy(true); setError(""); setNotice("");
    try { await operation(); } catch (err) { surfaceError(err); } finally { busyRef.current = false; setBusy(false); }
  }
  async function create() {
    await action(async () => {
      const conversation = await store.create(zh ? "新对话" : "New conversation");
      await reload(); await select(conversation.id);
    });
  }
  async function save(event: FormEvent) {
    event.preventDefault();
    const text = draft.trim(); const id = selected;
    if (!text || !id) return;
    await action(async () => {
      await store.enqueue(id, text);
      if (selectedRef.current === id) {
        setDraft(""); setContent(await store.read(id));
      }
      await reload();
      setNotice(zh ? "已保存到本机，尚未提交执行。" : "Saved on this device. Execution has not been submitted.");
    });
  }
  async function saveFile(file?: File) {
    if (!file || !selected) return;
    const id = selected;
    await action(async () => {
      if (file.size > 64 * 1024 * 1024) throw new Error(zh ? "文件超过 64 MiB。请选择较小的文件。" : "File exceeds 64 MiB. Choose a smaller file.");
      await store.saveFile(id, file.name, new Uint8Array(await file.arrayBuffer()));
      if (selectedRef.current === id) setContent(await store.read(id));
      setNotice(zh ? "文件已验证并保存到本机。" : "File verified and saved on this device.");
    });
  }
  async function logout() { await desktop.logout!(); }
  function changeLanguage(next: Language) {
    setLanguage(next); window.localStorage.setItem(LANGUAGE_STORAGE_KEY, next);
  }

  return <div className="workbench localWorkbench">
    {settings ? <WorkspaceSettingsSidebar text={dictionaries[language]} language={language} tab={tab}
      pendingApprovalCount={0} pendingCandidateCount={0} onTabChange={setTab} onBack={() => setSettings(false)}
      availableTabs={["devices", "appearance"]} /> : <aside className="sidebar">
      <div className="brandRow"><strong>SparkClaw</strong></div>
      <button className="primaryButton" type="button" disabled={busy} onClick={() => void create()}><Plus size={16} />{zh ? "新建本地对话" : "New local conversation"}</button>
      <nav className="localConversationList" aria-label={zh ? "本机对话" : "Device conversations"}>
        {loading && <p role="status">{zh ? "正在读取本机对话…" : "Loading device conversations…"}</p>}
        {!loading && !conversations.length && <p>{zh ? "本机还没有对话。新建一个即可开始保存。" : "No conversations on this device. Create one to start saving."}</p>}
        {conversations.map((conversation) => <button key={conversation.id} type="button" aria-current={selected === conversation.id ? "page" : undefined}
          disabled={busy} onClick={() => void select(conversation.id).catch(surfaceError)}>{conversation.title}</button>)}
      </nav>
      <div className="sidebarFooter">
        <button className="sidebarAccountMenuItem" type="button" onClick={() => setSettings(true)}><Settings size={16} />{zh ? "设置" : "Settings"}</button>
        <button className="sidebarAccountMenuItem" type="button" onClick={() => void logout().catch(surfaceError)}><LogOut size={16} />{zh ? "退出登录" : "Sign out"}</button>
      </div>
    </aside>}
    <main className="workspace">
      <header className="topbar"><h1>{settings ? (zh ? "设置" : "Settings") : (zh ? "本机工作台" : "Device workspace")}</h1>
        <select aria-label={zh ? "语言" : "Language"} value={language} onChange={(event) => changeLanguage(event.target.value as Language)}>
          <option value="zh">简体中文</option><option value="en">English</option>
        </select>
      </header>
      {error && <div className="localFeedback" role="alert"><p>{error}</p><button type="button" onClick={() => void reload().then(() => setError("")).catch(surfaceError)}>{zh ? "重试读取" : "Retry loading"}</button></div>}
      {notice && <p className="localFeedback" role="status">{notice}</p>}
      {settings ? <div className="localSettingsContent"><InspectorColumn settingsPage showTabs={false} tab={tab} onTabChange={setTab}
        text={dictionaries[language]} language={language} pendingApprovalCount={0} pendingCandidateCount={0}
        toolCalls={[]} approvals={[]} candidates={[]} memories={[]} traceRun={null} traceList={[]} traceLoading={false}
        ready={null} modelCalls={[]} auditEvents={[]} artifacts={[]} episodes={[]} evalRuns={[]} runtimeConfig={null}
        ownerProfile={null} clients={[]} connectors={[]} notificationBindings={[]} onOpenTrace={() => {}}
        setError={setError} surfaceError={surfaceError} refreshGlobal={async () => { await api.clients(); }}
        refreshActiveSession={async () => {}} setEvalRuns={() => {}} setNotificationBindings={() => {}}
        setConnectors={() => {}} setRuntimeConfig={() => {}} setOwnerProfile={() => {}}
        onLanguageChange={changeLanguage} onOpenSchedules={() => {}} currentClientID={currentClientID}
        onCurrentClientRevoked={logout} onLogout={logout} /></div> : <>
        <div className="localHistory">
          <p className="localPhaseNotice">{zh ? "对话和文件保存在本机。当前版本支持本地保存与设备管理；任务执行、邮箱同步和自动化将在后续阶段开放。" : "Conversations and files are saved on this device. This release supports local saving and device management. Execution, mailbox sync and automation arrive in later phases."}</p>
          {!selected && <p>{zh ? "选择或新建本地对话。" : "Select or create a local conversation."}</p>}
          {content.messages.map((message) => <article className={`message ${message.role}`} key={message.id}><p>{message.content}</p></article>)}
          {!!content.tasks.length && <details><summary>{zh ? "本机保存记录" : "Local save records"} ({content.tasks.length})</summary>
            {content.tasks.map((task) => <p key={task.id}><code>{task.request_id}</code> · {zh ? "未提交执行" : "Not submitted"}</p>)}</details>}
          {!!content.files.length && <section aria-label={zh ? "本机文件" : "Device files"}><h2>{zh ? "本机文件" : "Device files"}</h2>
            {content.files.map((file) => <div className="localFileRow" key={file.id}><span>{file.name}</span><small>{new Intl.NumberFormat().format(file.size)} B</small>
              <button type="button" disabled={busy} onClick={() => void action(async () => {
                const result = await store.exportFile(file.id);
                if (result.saved) setNotice(zh ? "已另存文件。" : "File exported.");
              })}><FileDown size={16} />{zh ? "另存" : "Export"}</button></div>)}</section>}
        </div>
        <form className="localComposer" onSubmit={(event) => void save(event)}>
          <label htmlFor="localDraft">{zh ? "本地输入" : "Local input"}</label>
          <textarea id="localDraft" value={draft} onChange={(event) => setDraft(event.target.value)} disabled={!selected || busy} rows={4} />
          <div><label className="localFileInput">{zh ? "保存本机文件" : "Save a local file"}<input type="file" disabled={!selected || busy} onChange={(event) => {
            void saveFile(event.target.files?.[0]); event.target.value = "";
          }} /></label><button className="primaryButton" type="submit" disabled={!selected || !draft.trim() || busy}>{busy ? (zh ? "正在保存…" : "Saving…") : (zh ? "保存到本机" : "Save on this device")}</button></div>
        </form>
      </>}
    </main>
  </div>;
}
