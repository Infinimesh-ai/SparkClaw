import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { FileDown, LogOut, Plus, Settings, Send, RefreshCw, X, Globe2 } from "lucide-react";
import { api } from "../api/client";
import { InspectorColumn, type PanelTab } from "../components/inspector";
import { WorkspaceSettingsSidebar } from "../components/settingsSidebar";
import { dictionaries, initialLanguage, LANGUAGE_STORAGE_KEY, type Language } from "../i18n";
import { applyAppearance } from "../lib/appearance";
import { clientStore, type LocalConversation, type LocalConversationContent, type LocalTask } from "./clientStore";
import { desktopCapability } from "./capability";
import type { DesktopConnectionStatus, DesktopState } from "./types";
import { MailCachePanel } from "./MailCachePanel";
import { BrowserPanel } from "./BrowserPanel";
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
  const [inputFiles, setInputFiles] = useState<string[]>([]);
  const [scheduleDraft, setScheduleDraft] = useState("");
  const [scheduleDate, setScheduleDate] = useState("");
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [settings, setSettings] = useState(false);
  const [browserOpen, setBrowserOpen] = useState(false);
  const [tab, setTab] = useState<PanelTab>("devices");
  const [currentClientID, setCurrentClientID] = useState("");
  const [connection, setConnection] = useState<DesktopConnectionStatus>();
  const [browserState, setBrowserState] = useState<DesktopState>();

  const surfaceError = useCallback((err: unknown) => {
    setError(err instanceof Error ? err.message : zh ? "本地保存失败，请重试。" : "Local save failed. Try again.");
  }, [zh]);
  const reload = useCallback(async () => { setConversations(await store.list()); }, [store]);
  const select = useCallback(async (id: string) => {
    selectedRef.current = id; setSelected(id); setContent(emptyContent); setDraft(""); setInputFiles([]); setScheduleDraft(""); setScheduleDate("");
    const generation = ++readGeneration.current;
    await desktop.selectConversation?.(id);
    const next = await store.read(id);
    if (selectedRef.current === id && readGeneration.current === generation) setContent(next);
  }, [desktop, store]);
  useEffect(() => {
    applyAppearance();
    let active = true;
    void store.list().then((rows) => { if (active) setConversations(rows); }).catch(surfaceError)
      .finally(() => { if (active) setLoading(false); });
    const connectionChanged = (status: DesktopConnectionStatus) => {
      if (active) { setCurrentClientID(status.client_id ?? ""); setConnection(status); }
    };
    void desktop.localConnection().then(connectionChanged).catch(surfaceError);
    const unsubscribe = desktop.onLocalConnection?.(connectionChanged);
    const browserChanged = (state: DesktopState) => { if (active) setBrowserState(state); };
    if (typeof desktop.state === "function") void desktop.state().then(browserChanged).catch(surfaceError);
    const unsubscribeBrowser = desktop.onState?.(browserChanged);
    return () => { active = false; unsubscribe?.(); unsubscribeBrowser?.(); ++readGeneration.current; };
  }, [desktop, store, surfaceError]);
  useEffect(() => {
    if (!selected) return;
    let active = true;
    const refresh = () => {
      if (busyRef.current || !active) return;
      const generation = readGeneration.current;
      void store.read(selected).then((next) => {
        if (active && selectedRef.current === selected && generation === readGeneration.current) setContent(next);
      }).catch(surfaceError);
    };
    const timer = window.setInterval(refresh, 3000);
    const unsubscribe = store.onChange?.(refresh);
    return () => { active = false; unsubscribe?.(); window.clearInterval(timer); };
  }, [selected, store, surfaceError]);

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
  async function save(event: FormEvent, submit = false) {
    event.preventDefault();
    const text = draft.trim(); const id = selected;
    if (!text || !id) return;
    await action(async () => {
      const task = inputFiles.length ? await store.enqueue(id, text, inputFiles) : await store.enqueue(id, text);
      if (selectedRef.current === id) {
        setDraft(""); setInputFiles([]); setContent(await store.read(id));
      }
      await reload();
      setNotice(zh ? "已保存到本机，尚未提交执行。" : "Saved on this device. Execution has not been submitted.");
      if (submit) {
        await execute(task, "submit", id);
        setNotice(zh ? "输入已保存并提交，可在任务记录中查看状态。" : "Input saved and submitted. Follow its status in the task records.");
      }
    });
  }
  async function execute(task: LocalTask, operation: "submit" | "reconcile" | "cancel", id = selected) {
    try { await store[operation](task.request_id); }
    finally {
      if (selectedRef.current === id) setContent(await store.read(id));
      await reload();
    }
  }
  async function schedule(event: FormEvent) {
    event.preventDefault();
    const id = selected;
    if (!id || !scheduleDraft.trim() || !scheduleDate) return;
    await action(async () => {
      await store.scheduleCreate(id, scheduleDraft.trim(), new Date(scheduleDate).toISOString());
      if (selectedRef.current === id) { setScheduleDraft(""); setScheduleDate(""); setContent(await store.read(id)); }
      await reload();
      setNotice(zh ? "单次任务定义已保存。到期执行需要设备保持连接并续租。" : "Single-run definition saved. Execution at the due time requires this device to remain connected and renew its lease.");
    });
  }
  async function scheduleAction(requestID: string, operation: "scheduleCheck" | "scheduleCancel" | "scheduleRunNow") {
    const id = selected;
    try { await store[operation](requestID); }
    finally { if (selectedRef.current === id) setContent(await store.read(id)); await reload(); }
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

  return <div className="shell workbench localWorkbench">
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
    <main className={`workspace ${browserOpen && !settings ? "localWithBrowser" : ""}`} aria-busy={busy}>
      <header className="topbar"><h1>{settings ? (zh ? "设置" : "Settings") : (zh ? "本机工作台" : "Device workspace")}</h1>
        {!settings && typeof desktop.state === "function" && <button className="localBrowserToggle" type="button" aria-expanded={browserOpen} onClick={() => setBrowserOpen((open) => !open)}><Globe2 size={16} />{browserOpen ? (zh ? "收起浏览器" : "Hide browser") : (zh ? "浏览器" : "Browser")}</button>}
        <select aria-label={zh ? "语言" : "Language"} value={language} onChange={(event) => changeLanguage(event.target.value as Language)}>
          <option value="zh">简体中文</option><option value="en">English</option>
        </select>
      </header>
      {error && <div className="localFeedback" role="alert"><p>{error}</p><button type="button" onClick={() => void reload().then(() => setError("")).catch(surfaceError)}>{zh ? "重试读取" : "Retry loading"}</button></div>}
      {notice && <p className="localFeedback" role="status">{notice}</p>}
      {connection?.state === "service_unavailable" && <div className="localFeedback" role="status"><p>{zh ? "当前离线，本机对话和文件仍可读取。重新连接后可查询已提交的原请求。" : "Offline. Your device conversations and files remain available. Reconnect to check your submitted requests."}</p><button type="button" disabled={busy} onClick={() => void action(async () => { setConnection(await desktop.retryLocalConnection()); })}>{zh ? "重新连接" : "Reconnect"}</button></div>}
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
          <p className="localPhaseNotice">{zh ? "对话和文件保存在本机。只有点击提交才会执行；断线后按原请求查询，执行结果保存成功后才确认接收。" : "Conversations and files stay on this device. Submit explicitly to execute. Reconnection checks the same request; results are acknowledged after local saving succeeds."}</p>
          <MailCachePanel language={language} conversationID={selected} onFileSaved={() => {
            const id = selectedRef.current;
            const generation = readGeneration.current;
            if (id) void store.read(id).then((next) => { if (selectedRef.current === id && readGeneration.current === generation) setContent(next); }).catch(surfaceError);
          }} />
          {!selected && <p>{zh ? "选择或新建本地对话。" : "Select or create a local conversation."}</p>}
          {selected && desktop.grantBrowserHost && <button className="localBrowserGrant" type="button" disabled={busy || connection?.state !== "connected"} onClick={() => void action(async () => {
            await desktop.grantBrowserHost!();
            setNotice(zh ? "已授权本机浏览器执行当前设备的任务。" : "This device's browser is authorized for its tasks.");
          })}>{zh ? "授权本机浏览器" : "Authorize this browser"}</button>}
          {browserState?.browser_host?.unknown_writes.filter((command) => command.local_conversation_id === selected).map((command) => <section className="localUnknownWrite" role="alert" key={command.command_id}>
            <h2>{zh ? "浏览器操作结果不确定" : "Browser action outcome uncertain"}</h2>
            <p>{zh ? "此操作不能重发。先独立检查网站的实际状态，再记录你已确认的结果。" : "This action cannot be resent. Inspect the actual website state independently, then record the outcome you verified."}</p>
            {desktop.reconcileBrowserHost && <div className="localTaskActions">{(["observed_completed", "observed_not_applied"] as const).map((outcome) => <button type="button" disabled={busy || connection?.state !== "connected"} key={outcome} onClick={() => void action(async () => {
              await desktop.reconcileBrowserHost!(command.command_id, command.digest, outcome);
              setBrowserState(await desktop.state());
              setNotice(zh ? "已记录核实结果，没有重发浏览器操作。" : "Verified outcome recorded. The browser action was not resent.");
            })}>{outcome === "observed_completed" ? (zh ? "我已核实操作完成" : "I verified the action completed") : (zh ? "我已核实操作未生效" : "I verified no change occurred")}</button>)}</div>}
          </section>)}
          {content.messages.map((message) => <article className={`message ${message.role}`} key={message.id}><p>{message.content}</p></article>)}
          {!!content.tasks.length && <section className="localTaskList" aria-label={zh ? "任务记录" : "Task records"} aria-live="polite"><h2>{zh ? "任务记录" : "Task records"} ({content.tasks.length})</h2>
            {content.tasks.map((task) => <div className="localTaskRow" key={task.id}>
              <p><code>{task.request_id}</code><span>{taskLabel(task.status, zh)}</span></p>
              {task.status === "unknown" && <small>{zh ? "执行结果不确定，不能自动重发。可继续查询原请求。" : "Execution outcome is uncertain. Check the original request; it cannot be resent automatically."}</small>}
              {task.status === "delivery_expired" && <small>{zh ? "后端结果已过期，无法恢复本机尚未保存的内容。" : "The backend result expired. Content not saved locally can no longer be recovered."}</small>}
              <div className="localTaskActions">
                {["awaiting_runtime", "submission_pending"].includes(task.status) && <button type="button" disabled={busy || connection?.state !== "connected"} onClick={() => void action(() => execute(task, "submit"))}><Send size={14} />{task.status === "awaiting_runtime" ? (zh ? "提交执行" : "Submit") : (zh ? "重试原请求" : "Retry original request")}</button>}
                {task.explicitly_submitted === 1 && task.status !== "delivered" && <button type="button" disabled={busy} onClick={() => void action(() => execute(task, "reconcile"))}><RefreshCw size={14} />{zh ? "查询状态" : "Check status"}</button>}
                {["submission_pending", "accepted", "running", "cancel_pending"].includes(task.status) && <button type="button" disabled={busy} onClick={() => void action(() => execute(task, "cancel"))}><X size={14} />{zh ? "取消任务" : "Cancel"}</button>}
              </div>
            </div>)}</section>}
          {!!content.files.length && <section aria-label={zh ? "本机文件" : "Device files"}><h2>{zh ? "本机文件" : "Device files"}</h2>
            {content.files.map((file) => <div className="localFileRow" key={file.id}><label><input type="checkbox" disabled={busy || file.size > 8 * 1024 * 1024} checked={inputFiles.includes(file.id)} onChange={(event) => setInputFiles((current) => event.target.checked ? [...current, file.id] : current.filter((id) => id !== file.id))} />{file.name}<small>{file.size > 8 * 1024 * 1024 ? (zh ? "仅本地保存，超过执行附件 8 MiB 限制" : "Local copy; exceeds the 8 MiB execution attachment limit") : (zh ? "加入下一条输入" : "Attach to next input")}</small></label><small>{new Intl.NumberFormat().format(file.size)} B</small>
              <button type="button" disabled={busy} onClick={() => void action(async () => {
                const result = await store.exportFile(file.id);
                if (result.saved) setNotice(zh ? "已另存文件。" : "File exported.");
              })}><FileDown size={16} />{zh ? "另存" : "Export"}</button></div>)}</section>}
          {selected && <details className="localSchedules"><summary>{zh ? "单次定时任务" : "Single-run scheduled tasks"}</summary>
            <p>{zh ? "定义保存在本机，可安排未来 24 小时内的一次执行。设备必须保持在线；离线错过到期时间不会补跑，可明确点击立即执行新请求。" : "Definitions stay on this device. Schedule one execution within the next 24 hours and keep this device online. Missed offline work does not catch up; Run now explicitly creates a new request."}</p>
            {(content.schedules ?? []).map((item) => <div className="localTaskRow" key={item.request_id}><p><span>{new Date(item.due_at).toLocaleString(zh ? "zh-CN" : "en-US")}</span><span>{scheduleLabel(item.state, zh)}</span></p>
              <div className="localTaskActions">
                {["saved", "registering", "leased", "cancel_pending"].includes(item.state) && <><button type="button" disabled={busy || connection?.state !== "connected"} onClick={() => void action(() => scheduleAction(item.request_id, "scheduleCheck"))}>{zh ? "检查租约" : "Check lease"}</button><button type="button" disabled={busy} onClick={() => void action(() => scheduleAction(item.request_id, "scheduleCancel"))}>{zh ? "取消定时任务" : "Cancel schedule"}</button></>}
                {["missed", "admission_rejected"].includes(item.state) && <button type="button" disabled={busy || connection?.state !== "connected"} onClick={() => void action(() => scheduleAction(item.request_id, "scheduleRunNow"))}>{zh ? "立即执行新请求" : "Run now as a new request"}</button>}
              </div>
            </div>)}
            <form onSubmit={(event) => void schedule(event)}><label htmlFor="scheduleDraft">{zh ? "定时任务输入" : "Scheduled task input"}</label><textarea id="scheduleDraft" rows={2} value={scheduleDraft} disabled={busy} onChange={(event) => setScheduleDraft(event.target.value)} />
              <label htmlFor="scheduleDate">{zh ? "本地到期时间" : "Due time in your local timezone"}</label><input id="scheduleDate" type="datetime-local" value={scheduleDate} disabled={busy} onChange={(event) => setScheduleDate(event.target.value)} />
              <button type="submit" disabled={busy || !scheduleDraft.trim() || !scheduleDate}>{zh ? "保存单次定时任务" : "Save single-run schedule"}</button>
            </form>
          </details>}
        </div>
        <form className="localComposer" onSubmit={(event) => void save(event)}>
          <label htmlFor="localDraft">{zh ? "本地输入" : "Local input"}</label>
          <textarea id="localDraft" value={draft} onChange={(event) => setDraft(event.target.value)} disabled={!selected || busy} rows={4} />
          <div><label className="localFileInput">{zh ? "保存本机文件" : "Save a local file"}<input type="file" disabled={!selected || busy} onChange={(event) => {
            void saveFile(event.target.files?.[0]); event.target.value = "";
          }} /></label><div className="localComposerActions"><button type="submit" disabled={!selected || !draft.trim() || busy}>{zh ? "保存到本机" : "Save on this device"}</button><button className="primaryButton" type="button" disabled={!selected || !draft.trim() || busy || connection?.state !== "connected"} onClick={(event) => void save(event, true)}><Send size={16} />{busy ? (zh ? "正在处理…" : "Working…") : (zh ? "保存并提交" : "Save and submit")}</button></div></div>
        </form>
        {browserOpen && typeof desktop.state === "function" && <BrowserPanel language={language} localConversationID={selected} />}
      </>}
    </main>
  </div>;
}

function taskLabel(status: string, zh: boolean) {
  const labels: Record<string, [string, string]> = {
    awaiting_runtime: ["未提交执行", "Not submitted"], submission_pending: ["提交待确认", "Submission awaiting confirmation"],
    scheduled_local: ["定时任务定义已保存", "Scheduled definition saved"], schedule_pending: ["定时任务待执行", "Scheduled execution pending"],
    schedule_missed: ["定时任务已错过", "Schedule missed"], schedule_canceled: ["定时任务已取消", "Schedule canceled"], schedule_admission_rejected: ["定时准入被拒绝", "Scheduled admission rejected"],
    accepted: ["后端已接收", "Accepted"], running: ["正在执行", "Running"], cancel_pending: ["取消待确认", "Cancellation awaiting confirmation"],
    saved: ["已保存，待确认接收", "Saved locally; acknowledgement pending"], delivered: ["已保存并确认接收", "Saved and acknowledged"],
    failed: ["执行失败", "Failed"], canceled: ["已取消", "Canceled"], unknown: ["执行结果不确定", "Outcome uncertain"],
    delivery_expired: ["结果已过期", "Result expired"],
  };
  return labels[status]?.[zh ? 0 : 1] ?? (zh ? "状态待确认" : "Status awaiting confirmation");
}

function scheduleLabel(state: string, zh: boolean) {
  const labels: Record<string, [string, string]> = {
    saved: ["定义已保存，尚未注册", "Definition saved; not registered"], registering: ["注册待确认", "Registration awaiting confirmation"],
    leased: ["已注册，保持连接才能到期执行", "Leased; stay connected until due"], cancel_pending: ["取消待确认", "Cancellation awaiting confirmation"],
    missed: ["离线或租约过期，已错过", "Missed while offline or lease expired"], admission_rejected: ["后端未准入", "Backend admission rejected"],
    run_now: ["已作为新请求执行", "Started as a new request"], canceled: ["已取消", "Canceled"],
  };
  return labels[state]?.[zh ? 0 : 1] ?? taskLabel(state, zh);
}
