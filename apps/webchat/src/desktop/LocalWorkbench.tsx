import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { FileDown, PanelLeft, PanelRight } from "lucide-react";
import { SessionSidebar } from "../components/sidebar";
import { TaskSearch, WorkbenchWelcome, workbenchCopy } from "../components/workbench";
import { api } from "../api/client";
import { InspectorColumn, type PanelTab } from "../components/inspector";
import { WorkspaceSettingsSidebar } from "../components/settingsSidebar";
import { dictionaries, initialLanguage, LANGUAGE_STORAGE_KEY, type Language } from "../i18n";
import { applyAppearance } from "../lib/appearance";
import { clientStore, type LocalConversation, type LocalConversationContent, type LocalTask, type LocalApproval } from "./clientStore";
import { desktopCapability } from "./capability";
import type { DesktopConnectionStatus, DesktopState } from "./types";
import { BrowserPanel } from "./BrowserPanel";
import { MessageBubble } from "../components/messages";
import { ComposerDocumentPicker, ComposerSurface } from "../components/composer";
import { useVoiceInput } from "../hooks/useVoiceInput";
import type { VoiceDraftAnchor } from "../hooks/useVoiceInput";
import { insertVoiceTranscript } from "../lib/voiceDraft";
import { usePassiveNotifications } from "../hooks/usePassiveNotifications";
import { NotificationCenter } from "../components/notificationCenter";
import type {
  Approval, ArtifactObject, AuditEvent, Client, ConnectorStatus, EpisodeSummary, EvalRun, Memory,
  MemoryCandidate, MessageAttachment, ModelCall, NotificationBinding, OwnerProfile, PublicConfig,
  ReadyStatus, RunTrace, ToolCall, TraceMetadata
} from "../api/types";
import "../styles/local-workbench.css";

const emptyContent: LocalConversationContent = { messages: [], tasks: [], files: [] };

// Desktop R3 has its own data path. It never mounts App's legacy shared-session
// hooks. Server-dependent features become available only as their phases pass.
export function LocalWorkbench() {
  const store = clientStore()!;
  const desktop = desktopCapability()!;
  const [language, setLanguage] = useState<Language>(initialLanguage);
  const zh = language === "zh";
  const copy = workbenchCopy[language];
  const [conversations, setConversations] = useState<LocalConversation[]>([]);
  const [selected, setSelected] = useState("");
  const selectedRef = useRef(selected); selectedRef.current = selected;
  const readGeneration = useRef(0);
  const [content, setContent] = useState(emptyContent);
  const [draft, setDraft] = useState("");
  const draftRef = useRef(draft); draftRef.current = draft;
  const [inputFiles, setInputFiles] = useState<string[]>([]);
  const [scheduleDraft, setScheduleDraft] = useState("");
  const [scheduleDate, setScheduleDate] = useState("");
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [settings, setSettings] = useState(false);
  const [page, setPage] = useState<"chat" | "schedules">("chat");
  const [searchOpen, setSearchOpen] = useState(false);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [browserOpen, setBrowserOpen] = useState(false);
  const [documentPickerOpen, setDocumentPickerOpen] = useState(false);
  const [tab, setTab] = useState<PanelTab>("timeline");
  const [currentClientID, setCurrentClientID] = useState("");
  const [connection, setConnection] = useState<DesktopConnectionStatus>();
  const [browserState, setBrowserState] = useState<DesktopState>();
  const composerInputRef = useRef<HTMLTextAreaElement | null>(null);
  const [ready, setReady] = useState<ReadyStatus | null>(null);
  const [runtimeConfig, setRuntimeConfig] = useState<PublicConfig | null>(null);
  const [ownerProfile, setOwnerProfile] = useState<OwnerProfile | null>(null);
  const [clients, setClients] = useState<Client[]>([]);
  const [connectors, setConnectors] = useState<ConnectorStatus[]>([]);
  const [notificationBindings, setNotificationBindings] = useState<NotificationBinding[]>([]);
  const [approvals, setApprovals] = useState<Approval[]>([]);
  const [candidates, setCandidates] = useState<MemoryCandidate[]>([]);
  const [memories, setMemories] = useState<Memory[]>([]);
  const [evalRuns, setEvalRuns] = useState<EvalRun[]>([]);
  const [artifacts, setArtifacts] = useState<ArtifactObject[]>([]);
  const [traceList, setTraceList] = useState<TraceMetadata[]>([]);
  const [traceRun, setTraceRun] = useState<RunTrace | null>(null);
  const [traceLoading, setTraceLoading] = useState(false);
  const [toolCalls] = useState<ToolCall[]>([]);
  const [modelCalls] = useState<ModelCall[]>([]);
  const [auditEvents] = useState<AuditEvent[]>([]);
  const [episodes] = useState<EpisodeSummary[]>([]);

  const surfaceError = useCallback((err: unknown, fallback = zh ? "操作失败，请重试。" : "Something went wrong. Try again.") => {
    setError(err instanceof Error ? err.message : fallback);
  }, [zh]);
  const reload = useCallback(async () => { setConversations(await store.list()); }, [store]);
  const refreshGlobal = useCallback(async () => {
    const config = await api.config();
    setRuntimeConfig(config);
    const results = await Promise.allSettled([
      api.ready(), api.owner(), api.clients(), api.connectors(), api.notificationBindings(), api.approvals(),
      api.memoryCandidates(), api.memories(), api.evalRuns(), api.artifacts(), api.traces()
    ]);
    const [readyResult, owner, clientList, connectorList, bindingList, approvalList, candidateList, memoryList, evalList, artifactList, traces] = results;
    if (readyResult.status === "fulfilled") setReady(readyResult.value);
    if (owner.status === "fulfilled") setOwnerProfile(owner.value);
    if (clientList.status === "fulfilled") setClients(clientList.value.clients ?? []);
    if (connectorList.status === "fulfilled") setConnectors(connectorList.value.connectors ?? []);
    if (bindingList.status === "fulfilled") setNotificationBindings(bindingList.value.bindings ?? []);
    if (approvalList.status === "fulfilled") setApprovals(approvalList.value.approvals ?? []);
    if (candidateList.status === "fulfilled") setCandidates(candidateList.value.memory_candidates ?? []);
    if (memoryList.status === "fulfilled") setMemories(memoryList.value.memories ?? []);
    if (evalList.status === "fulfilled") setEvalRuns(evalList.value.eval_runs ?? []);
    if (artifactList.status === "fulfilled") setArtifacts(artifactList.value.artifacts ?? []);
    if (traces.status === "fulfilled") setTraceList(traces.value.traces ?? []);
  }, []);
  const select = useCallback(async (id: string) => {
    setPage("chat");
    if (selectedRef.current === id) return;
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
    if (connection?.state !== "connected") return;
    void refreshGlobal().catch(() => undefined);
  }, [connection?.state, refreshGlobal]);
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

  const applyVoiceTranscript = useCallback((result: { text: string }, anchor: VoiceDraftAnchor) => {
    if (anchor.sessionId !== selectedRef.current || draftRef.current !== anchor.draft) return false;
    const start = Math.min(anchor.selectionStart, draftRef.current.length);
    const end = Math.min(Math.max(anchor.selectionEnd, start), draftRef.current.length);
    const inserted = insertVoiceTranscript(draftRef.current, result.text, start, end);
    draftRef.current = inserted.value;
    setDraft(inserted.value);
    window.requestAnimationFrame(() => {
      composerInputRef.current?.focus();
      composerInputRef.current?.setSelectionRange(inserted.caret, inserted.caret);
    });
    return true;
  }, []);
  const voice = useVoiceInput({
    speech: ready?.speech ?? null,
    sessionId: selected,
    language: runtimeConfig?.speech.default_language ?? "auto",
    externallyDisabled: busy || !selected,
    onTranscript: applyVoiceTranscript
  });
  const passiveNotifications = usePassiveNotifications(connection?.state === "connected");

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
  async function ensureConversation() {
    if (selectedRef.current) return selectedRef.current;
    const conversation = await store.create(zh ? "新对话" : "New conversation");
    await desktop.selectConversation?.(conversation.id);
    selectedRef.current = conversation.id; setSelected(conversation.id); setContent(emptyContent);
    await reload();
    return conversation.id;
  }
  async function save(submit = true) {
    const text = draft.trim();
    if (!text && !inputFiles.length) return;
    await action(async () => {
      const id = await ensureConversation();
      const task = inputFiles.length ? await store.enqueue(id, text, inputFiles) : await store.enqueue(id, text);
      if (selectedRef.current === id) {
        setDraft(""); setInputFiles([]); setContent(await store.read(id));
      }
      await reload();
      if (submit) {
        await submitTask(task, id);
      }
    });
  }
  async function submitTask(task: LocalTask, id: string) {
    try { await store.submit(task.request_id); }
    finally {
      if (selectedRef.current === id) setContent(await store.read(id));
      await reload();
    }
  }
  async function decideApproval(task: LocalTask, approval: LocalApproval, decision: "approve" | "reject") {
    const id = selected;
    try {
      await store.decideApproval(task.request_id, approval.approval_id, approval.digest, decision);
      setNotice(decision === "approve" ? (zh ? "批准决定已接收，任务将继续执行。" : "Approval accepted. The task can continue.") : (zh ? "拒绝决定已接收。" : "Rejection accepted."));
    } finally { if (selectedRef.current === id) setContent(await store.read(id)); await reload(); }
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
    if (!file) return undefined;
    let saved: Awaited<ReturnType<typeof store.saveFile>> | undefined;
    await action(async () => {
      if (file.size > 64 * 1024 * 1024) throw new Error(zh ? "文件超过 64 MiB。请选择较小的文件。" : "File exceeds 64 MiB. Choose a smaller file.");
      const id = await ensureConversation();
      saved = await store.saveFile(id, file.name, new Uint8Array(await file.arrayBuffer()));
      setInputFiles((current) => saved && !current.includes(saved.id) ? [...current, saved.id] : current);
      if (selectedRef.current === id) setContent(await store.read(id));
      setNotice(zh ? "文件已添加。" : "File added.");
    });
    return saved;
  }
  async function logout() { await desktop.logout!(); }
  function changeLanguage(next: Language) {
    setLanguage(next);
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, next);
    document.documentElement.lang = next === "zh" ? "zh-CN" : "en";
    void api.updateLanguage(next).then(setOwnerProfile).catch(() => undefined);
  }
  async function openTrace(runID: string) {
    setTraceLoading(true);
    try { setTraceRun(await api.trace(runID)); }
    catch (err) { surfaceError(err, dictionaries[language].errors.trace); }
    finally { setTraceLoading(false); }
  }

  const text = dictionaries[language];
  const pendingApprovals = approvals.filter((approval) => approval.status === "pending");
  const pendingCandidates = candidates.filter((candidate) => candidate.status === "pending");
  const activeAttachments = useMemo<MessageAttachment[]>(() => inputFiles.map((id) => {
    const file = content.files.find((item) => item.id === id);
    return { artifact_id: id, rel_path: `local:${id}`, name: file?.name ?? id, bytes: file?.size };
  }), [content.files, inputFiles]);
  const localDocuments = useMemo<ArtifactObject[]>(() => content.files.map((file) => ({
    id: file.id,
    kind: "document",
    backend: "client_store",
    key: file.name,
    uri: `local:${file.id}`,
    content_type: "",
    bytes: file.size,
    created_at: file.created_at
  })), [content.files]);
  const home = page === "chat" && !content.messages.length && !content.tasks.length && !content.files.length;
  return <div className={`shell workbench localWorkbench ${settings ? "settingsPageMode" : sidebarCollapsed ? "sidebarCollapsed" : ""}`}>
    {settings ? <WorkspaceSettingsSidebar text={text} language={language} tab={tab}
      pendingApprovalCount={pendingApprovals.length} pendingCandidateCount={pendingCandidates.length} onTabChange={setTab} onBack={() => setSettings(false)} /> : <SessionSidebar
      text={text} language={language} page={page} ownerProfile={ownerProfile}
      sessions={conversations} activeSession={selected} busy={busy}
      onCreateSession={() => void create()} onSelectSession={(conversation) => void select(conversation.id).catch(surfaceError)}
      onNavigate={(next) => { if (next === "settings") setSettings(true); else if (next === "schedules") setPage("schedules"); else void create(); }}
      onSearch={() => setSearchOpen(true)} onToggleSidebar={() => setSidebarCollapsed((current) => !current)}
      onLogout={() => void logout().catch(surfaceError)}
      listNotice={loading ? <p className="localListNotice" role="status">{zh ? "正在读取对话…" : "Loading conversations…"}</p> : !conversations.length ? <p className="localListNotice">{copy.empty}</p> : undefined}
    />}
    <main className={`${settings ? "settingsPageMain" : "workspace desktopWorkbench"} ${browserOpen && !settings ? "withInspector" : ""}`} aria-busy={busy}>
      {!settings && <header className="topbar">
        <button className="iconButton sidebarToggle" type="button" aria-label={copy.toggleNav} onClick={() => setSidebarCollapsed((current) => !current)}><PanelLeft size={18} /></button>
        <span className="workspaceLabel">{copy.local}</span>
        <div className="topbarActions">
          <NotificationCenter notifications={passiveNotifications.notifications} unreadCount={passiveNotifications.unreadCount}
            open={passiveNotifications.open} toast={passiveNotifications.toast} error={passiveNotifications.error}
            language={language} text={text} onToggle={() => passiveNotifications.setOpen((current) => !current)}
            onDismissToast={passiveNotifications.dismissToast} onRead={passiveNotifications.markRead} onReadAll={passiveNotifications.markAllRead} />
          {typeof desktop.state === "function" && <button className={`iconButton rightSidebarToggle ${browserOpen ? "active" : ""}`} type="button" aria-label={copy.toggleInspector} title={zh ? "浏览器" : "Browser"} aria-expanded={browserOpen} onClick={() => setBrowserOpen((open) => !open)}><PanelRight size={18} /></button>}
        </div>
      </header>}
      {error && <div className="localFeedback" role="alert"><p>{error}</p><button type="button" onClick={() => void reload().then(() => setError("")).catch(surfaceError)}>{zh ? "重试读取" : "Retry loading"}</button></div>}
      {notice && <p className="localFeedback" role="status">{notice}</p>}
      {connection?.state === "service_unavailable" && <div className="localFeedback" role="status"><p>{zh ? "连接暂时不可用，请重新连接后继续。" : "Connection unavailable. Reconnect to continue."}</p><button type="button" disabled={busy} onClick={() => void action(async () => { setConnection(await desktop.retryLocalConnection()); })}>{zh ? "重新连接" : "Reconnect"}</button></div>}
      {settings ? <div className="settingsPageContent"><header className="settingsPageHeader"><h1>{copy.settingsTitles[tab]}</h1>{tab !== "memory" && tab !== "approvals" && <p>{copy.settingsDescriptions[tab]}</p>}</header><InspectorColumn settingsPage showTabs={false} tab={tab} onTabChange={setTab}
        text={text} language={language} pendingApprovalCount={pendingApprovals.length} pendingCandidateCount={pendingCandidates.length}
        toolCalls={toolCalls} approvals={approvals} candidates={candidates} memories={memories} traceRun={traceRun} traceList={traceList} traceLoading={traceLoading}
        ready={ready} modelCalls={modelCalls} auditEvents={auditEvents} artifacts={artifacts} episodes={episodes} evalRuns={evalRuns} runtimeConfig={runtimeConfig}
        ownerProfile={ownerProfile} clients={clients} connectors={connectors} notificationBindings={notificationBindings} onOpenTrace={(runID) => void openTrace(runID)}
        setError={setError} surfaceError={surfaceError} refreshGlobal={refreshGlobal}
        refreshActiveSession={async () => {}} setEvalRuns={setEvalRuns} setNotificationBindings={setNotificationBindings}
        setConnectors={setConnectors} setRuntimeConfig={setRuntimeConfig} setOwnerProfile={setOwnerProfile}
        onLanguageChange={changeLanguage} onOpenSchedules={() => {}} currentClientID={currentClientID}
        onCurrentClientRevoked={logout} onLogout={logout} /></div> : <>
        <section className={`chatColumn localChat ${home ? "homeChat" : ""}`} hidden={page !== "chat"}>
        <div className="messageList localHistory">
          {home && <WorkbenchWelcome language={language} />}
          {browserState?.browser_host?.unknown_writes.filter((command) => command.local_conversation_id === selected).map((command) => <section className="localUnknownWrite" role="alert" key={command.command_id}>
            <h2>{zh ? "浏览器操作结果不确定" : "Browser action outcome uncertain"}</h2>
            <p>{zh ? "此操作不能重发。先独立检查网站的实际状态，再记录你已确认的结果。" : "This action cannot be resent. Inspect the actual website state independently, then record the outcome you verified."}</p>
            {desktop.reconcileBrowserHost && <div className="localTaskActions">{(["observed_completed", "observed_not_applied"] as const).map((outcome) => <button type="button" disabled={busy || connection?.state !== "connected"} key={outcome} onClick={() => void action(async () => {
              await desktop.reconcileBrowserHost!(command.command_id, command.digest, outcome);
              setBrowserState(await desktop.state());
              setNotice(zh ? "已记录核实结果，没有重发浏览器操作。" : "Verified outcome recorded. The browser action was not resent.");
            })}>{outcome === "observed_completed" ? (zh ? "我已核实操作完成" : "I verified the action completed") : (zh ? "我已核实操作未生效" : "I verified no change occurred")}</button>)}</div>}
          </section>)}
          {content.messages.map((message) => <MessageBubble key={message.id}
            message={{ ...message, session_id: selected }} streamStatuses={[]} text={text} language={language}
            onFeedback={async () => {}} />)}
          {content.tasks.flatMap((task) => (task.approvals ?? [])
            .filter((approval) => ["pending", "decision_pending", "decision_unknown"].includes(approval.state))
            .map((approval) => ({ task, approval }))).map(({ task, approval }) => {
                const pending = ["pending", "decision_pending"].includes(approval.state);
                const expired = Date.parse(approval.expires_at) <= Date.now();
                return <section className="localApproval" key={`${task.id}:${approval.approval_id}`} aria-label={zh ? "操作审批" : "Action approval"}>
                  <h3>{approval.tool}</h3><p>{approval.summary}</p>
                  <pre aria-label={zh ? "操作参数" : "Action parameters"}>{JSON.stringify(approval.arguments, null, 2)}</pre>
                  <p role="status">{pending && expired ? (zh ? "审批已过期，无法继续操作。" : "Approval expired. It can no longer be acted on.") : approvalLabel(approval.state, zh)}</p>
                  {pending && !expired && !approval.actionable && <small>{zh ? "请刷新当前执行后再决定；未核实的审批不能操作。" : "Refresh the current operation before deciding. Unverified approvals cannot be acted on."}</small>}
                  {pending && !expired && <div className="localTaskActions">{(["approve", "reject"] as const).filter((decision) => !approval.decision || approval.decision === decision).map((decision) => <button type="button" key={decision} disabled={busy || connection?.state !== "connected" || !approval.actionable} onClick={() => void action(() => decideApproval(task, approval, decision))}>
                    {approval.decision ? (decision === "approve" ? (zh ? "重试原批准决定" : "Retry original approval") : (zh ? "重试原拒绝决定" : "Retry original rejection")) : (decision === "approve" ? (zh ? "批准此操作" : "Approve this action") : (zh ? "拒绝此操作" : "Reject this action"))}
                  </button>)}</div>}
                </section>;
          })}
          {!!content.files.length && <section aria-label={zh ? "文件" : "Files"}><h2>{zh ? "文件" : "Files"}</h2>
            {content.files.map((file) => <div className="localFileRow" key={file.id}><label><input type="checkbox" disabled={busy || file.size > 8 * 1024 * 1024} checked={inputFiles.includes(file.id)} onChange={(event) => setInputFiles((current) => event.target.checked ? [...current, file.id] : current.filter((id) => id !== file.id))} />{file.name}<small>{file.size > 8 * 1024 * 1024 ? (zh ? "超过执行附件 8 MiB 限制" : "Exceeds the 8 MiB execution attachment limit") : (zh ? "加入下一条输入" : "Attach to next input")}</small></label><small>{new Intl.NumberFormat().format(file.size)} B</small>
              <button type="button" disabled={busy} onClick={() => void action(async () => {
                const result = await store.exportFile(file.id);
                if (result.saved) setNotice(zh ? "已另存文件。" : "File exported.");
              })}><FileDown size={16} />{zh ? "另存" : "Export"}</button></div>)}</section>}

        </div>
        <ComposerSurface text={text} language={language} activeSession={selected} activeInput={draft}
          activeAttachments={activeAttachments} busy={busy} voice={voice} composerInputRef={composerInputRef}
          canCompose canSend={connection?.state === "connected"}
          onInputChange={(value) => { draftRef.current = value; setDraft(value); }}
          onUploadDocument={saveFile}
          onChooseDocument={() => setDocumentPickerOpen(true)}
          onOpenAttachment={async (attachment) => {
            const result = await store.exportFile(attachment.artifact_id ?? "");
            if (result.saved) setNotice(zh ? "已另存文件。" : "File exported.");
          }}
          onRemoveAttachment={(attachment) => setInputFiles((current) => current.filter((id) => id !== attachment.artifact_id))}
          onSend={() => void save(true)} />
        {documentPickerOpen && <ComposerDocumentPicker documents={localDocuments} text={text} language={language}
          onChoose={(document) => {
            setInputFiles((current) => current.includes(document.id) ? current : [...current, document.id]);
            setDocumentPickerOpen(false);
          }} onClose={() => setDocumentPickerOpen(false)} />}
        </section>
        {page === "schedules" && <div className="workbenchPage schedulePage">
          <header className="workbenchPageHeader"><div><h1>{copy.pageTitles.schedules}</h1><p>{copy.pageDescriptions.schedules}</p></div></header>
          {!selected && <div className="localScheduleEmpty"><p>{zh ? "先选择或新建一个任务，再安排执行时间。" : "Choose or create a task to schedule it."}</p><button className="primaryButton" type="button" onClick={() => void create()}>{copy.newTask}</button></div>}
          {selected && <section className="localSchedules">
            <p>{zh ? "可安排未来 24 小时内的一次执行。设备必须保持在线；离线错过到期时间不会补跑，可明确点击立即执行新请求。" : "Schedule one execution within the next 24 hours and keep this device online. Missed offline work does not catch up; Run now explicitly creates a new request."}</p>
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
          </section>}
        </div>}
        {browserOpen && typeof desktop.state === "function" && <BrowserPanel language={language} localConversationID={selected} toolbar={<div className="localBrowserAuthorization">{selected && desktop.grantBrowserHost && <button className="localBrowserGrant" type="button" disabled={busy || connection?.state !== "connected"} onClick={() => void action(async () => {
            await desktop.grantBrowserHost!();
            setNotice(zh ? "已授权本机浏览器执行当前设备的任务。" : "This device's browser is authorized for its tasks.");
          })}>{zh ? "授权本机浏览器" : "Authorize this browser"}</button>}</div>} />}
      </>}
    </main>
    {searchOpen && <TaskSearch language={language} sessions={conversations} onSelect={(conversation) => { setSearchOpen(false); void select(conversation.id).catch(surfaceError); }} onClose={() => setSearchOpen(false)} />}
  </div>;
}

function scheduleLabel(state: string, zh: boolean) {
  const labels: Record<string, [string, string]> = {
    saved: ["定义已保存，尚未注册", "Definition saved; not registered"], registering: ["注册待确认", "Registration awaiting confirmation"],
    leased: ["已注册，保持连接才能到期执行", "Leased; stay connected until due"], cancel_pending: ["取消待确认", "Cancellation awaiting confirmation"],
    missed: ["离线或租约过期，已错过", "Missed while offline or lease expired"], admission_rejected: ["后端未准入", "Backend admission rejected"],
    run_now: ["已作为新请求执行", "Started as a new request"], canceled: ["已取消", "Canceled"],
  };
  return labels[state]?.[zh ? 0 : 1] ?? (zh ? "状态待确认" : "Status awaiting confirmation");
}

function approvalLabel(state: LocalApproval["state"], zh: boolean) {
  const labels: Record<LocalApproval["state"], [string, string]> = {
    pending: ["等待你明确批准或拒绝", "Waiting for your explicit approval or rejection"],
    decision_pending: ["决定已保存，后端接收结果待核对", "Decision saved; backend receipt awaiting reconciliation"],
    approved: ["已批准", "Approved"], rejected: ["已拒绝", "Rejected"], resolved: ["后端已不再等待此审批", "The backend no longer awaits this approval"],
    expired: ["审批已结束或过期", "Approval ended or expired"],
    decision_unknown: ["决定接收结果不确定，当前不能继续操作", "Decision receipt is uncertain. No further action is currently available"],
  };
  return labels[state][zh ? 0 : 1];
}
