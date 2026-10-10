import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { PanelLeft, PanelRight, Plus } from "lucide-react";
import { ScheduleBar, ScheduleCreateDialog } from "../components/schedules";
import { useLocalSchedules } from "./useLocalSchedules";
import { SessionSidebar } from "../components/sidebar";
import { TaskSearch, WorkbenchWelcome, workbenchCopy } from "../components/workbench";
import { MailWorkspaceContext } from "./MailWorkspaceContext";
import type {} from "./mailCapability";
import { ISCPDeviceAuthorization, iscpSettingsAccess, iscpSettingsUnavailable } from "./iscpSettings";
import { surfaceEnabled } from "./capability";
import { api } from "../api/client";
import { InspectorColumn, type PanelTab } from "../components/inspector";
import { WorkspaceSettingsSidebar } from "../components/settingsSidebar";
import { dictionaries, initialLanguage, LANGUAGE_STORAGE_KEY, type Language } from "../i18n";
import { applyAppearance } from "../lib/appearance";
import { clientStore, type LocalConversation, type LocalConversationContent, type LocalTask, type LocalApproval, type LocalDraft } from "./clientStore";
import { desktopCapability } from "./capability";
import type { DesktopConnectionStatus, DesktopState } from "./types";
import { BrowserPanel } from "./BrowserPanel";
import { MessageBubble } from "../components/messages";
import { ComposerDocumentPicker, ComposerSurface } from "../components/composer";
import { useWorkbenchDraft } from "../hooks/useWorkbenchDraft";
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

// The desktop workbench owns local conversations. The host App uses its own Store
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
  const draftRef = useRef("");
  const inputFilesRef = useRef<string[]>([]);
  const [scheduleCreateOpen, setScheduleCreateOpen] = useState(false);
  const [scheduleBarOpen, setScheduleBarOpen] = useState(true);
  const [editingSession, setEditingSession] = useState("");
  const [sessionTitleDraft, setSessionTitleDraft] = useState("");
  const [sessionActionID, setSessionActionID] = useState("");
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [settings, setSettings] = useState(false);
  const [page, setPage] = useState<"chat" | "schedules">("chat");
  const [searchOpen, setSearchOpen] = useState(false);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [mailIdentity, setMailIdentity] = useState(0);
  const [browserOpen, setBrowserOpen] = useState(false);
  const [documentPickerOpen, setDocumentPickerOpen] = useState(false);
  const [tab, setTab] = useState<PanelTab>("timeline");
  const [currentClientID, setCurrentClientID] = useState("");
  const [connection, setConnection] = useState<DesktopConnectionStatus>();
  const mailScope = mailEditorScope(connection);
  const lastMailScope = useRef("");
  const iscp = connection?.backend?.transport === "iscp";
  const capabilities = {
    files: surfaceEnabled(connection, "files"), mail: surfaceEnabled(connection, "mail_popup"),
    browser: surfaceEnabled(connection, "browser"), speech: surfaceEnabled(connection, "speech_recording"),
    approvals: surfaceEnabled(connection, "approvals"), notifications: surfaceEnabled(connection, "notifications"),
    settingsOwner: surfaceEnabled(connection, "settings_owner"), settingsConnectors: surfaceEnabled(connection, "settings_connectors"),
  };
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
  const settingsScope = connection && JSON.stringify([connection.backend?.deployment_id, connection.owner_id, connection.client_id, connection.authorization_revision]);
  const settingsAccess = iscp && connection ? {
    ...iscpSettingsAccess(connection, language),
    deviceAuthorization: <ISCPDeviceAuthorization key={settingsScope} connection={connection} language={language} />,
  } : undefined;

  const surfaceError = useCallback((err: unknown, fallback = zh ? "操作失败，请重试。" : "Something went wrong. Try again.") => {
    setError(err instanceof Error ? err.message : fallback);
  }, [zh]);
  useEffect(() => {
    if (!iscp) return;
    setConnectors([]);
    if (!settings || tab !== "connections" || !capabilities.settingsConnectors) return;
    let active = true;
    void api.connectors().then(value => { if (active) setConnectors(value.connectors); }).catch(surfaceError);
    return () => { active = false; };
  }, [iscp, settings, tab, capabilities.settingsConnectors, settingsScope, surfaceError]);
  const scopeError = useRef(surfaceError); scopeError.current = surfaceError;
  const localScopeKey = JSON.stringify([
    Boolean(connection && ["connected", "reconnecting", "service_unavailable"].includes(connection.state)),
    connection?.backend?.deployment_id, connection?.owner_id, currentClientID,
  ]);
  const draftScopes = useMemo(() => new Map<string, string>(), [store, localScopeKey]);
  const draftAdapter = useMemo(() => ({
    load: async (id: string) => {
      const value = await store.draft(id);
      const expected = draftScopes.get(id);
      if (expected && expected !== value.scope_key) throw new Error("Draft authentication changed; reload this workbench");
      draftScopes.set(id, value.scope_key); return workbenchDraft(value);
    },
    save: async (id: string, value: import("../lib/workbenchDraft").WorkbenchDraft) =>
      workbenchDraft(await store.saveDraft(id, value.content, value.attachment_ids, value.revision, draftScopes.get(id) ?? "")),
  }), [store, draftScopes]);
  const drafts = useWorkbenchDraft(draftAdapter, surfaceError);
  const draft = drafts.draft.content; draftRef.current = draft;
  const inputFiles = drafts.draft.attachment_ids; inputFilesRef.current = inputFiles;
  const setDraft = drafts.content;
  const selectDraft = drafts.select;
  const setInputFiles = (update: string[] | ((current: string[]) => string[])) => {
    const value = typeof update === "function" ? update(inputFilesRef.current) : update;
    inputFilesRef.current = value; drafts.attachments(value);
  };
  const reload = useCallback(async () => { setConversations(await store.list()); }, [store]);
  const refreshGlobal = useCallback(async () => {
    const config = await api.config();
    setRuntimeConfig(config);
    if (iscp) {
      const [status, owner] = await Promise.all([api.ready(), api.owner()]);
      setReady(status); setOwnerProfile(owner);
      return;
    }
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
  }, [iscp]);
  const select = useCallback(async (id: string) => {
    setPage("chat");
    if (selectedRef.current === id) return;
    const generation = ++readGeneration.current;
    await selectDraft(id);
    if (generation !== readGeneration.current) return;
    selectedRef.current = id; setSelected(id); setContent(emptyContent);
    if (capabilities.browser) await desktop.selectConversation?.(id);
    const next = await store.read(id);
    if (selectedRef.current === id && readGeneration.current === generation) setContent(next);
  }, [desktop, store, selectDraft, capabilities.browser]);
  useEffect(() => {
    applyAppearance();
    let active = true;
    const connectionChanged = (status: DesktopConnectionStatus) => {
      if (active) {
        const scope = mailEditorScope(status);
        // Clear even when a lock/revocation and a new connection are delivered
        // in one React batch. Temporary capability loss is not an identity change.
        if (!scope || scope !== lastMailScope.current) {
          setMailIdentity(value => value + 1);
        }
        lastMailScope.current = scope;
        setCurrentClientID(status.client_id ?? ""); setConnection(status);
      }
    };
    void desktop.localConnection().then(connectionChanged).catch(surfaceError);
    const unsubscribe = desktop.onLocalConnection?.(connectionChanged);
    const browserChanged = (state: DesktopState) => { if (active) setBrowserState(state); };
    if (connection?.backend && capabilities.browser && typeof desktop.state === "function") void desktop.state().then(browserChanged).catch(surfaceError);
    const unsubscribeBrowser = desktop.onState?.(browserChanged);
    return () => { active = false; unsubscribe?.(); unsubscribeBrowser?.(); ++readGeneration.current; };
  }, [desktop, store, surfaceError, capabilities.browser, connection?.backend?.origin]);
  useEffect(() => {
    let active = true;
    ++readGeneration.current; selectedRef.current = "";
    setSelected(""); setContent(emptyContent); setConversations([]); setLoading(true);
    void store.list().then((rows) => { if (active) setConversations(rows); }).catch((error) => { if (active) scopeError.current(error); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [store, localScopeKey]);
  useEffect(() => {
    if (connection?.state !== "connected") return;
    void refreshGlobal().catch(surfaceError);
  }, [connection?.state, refreshGlobal, surfaceError]);
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
  }, [setDraft]);
  const voice = useVoiceInput({
    speech: ready?.speech ?? null,
    sessionId: selected,
    language: runtimeConfig?.speech.default_language ?? "auto",
    externallyDisabled: busy || !selected || !capabilities.speech,
    iscp, iscpRealtime: surfaceEnabled(connection, "speech_realtime"),
    onTranscript: applyVoiceTranscript
  });
  const passiveNotifications = usePassiveNotifications(connection?.state === "connected" && capabilities.notifications, iscp ? "poll" : "stream");

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
  async function deleteConversation(id: string) {
    if (busyRef.current || !window.confirm(dictionaries[language].nav.confirmDeleteLocalSession)) return;
    await action(async () => {
      await drafts.flush();
      ++readGeneration.current;
      const result = await store.remove(id);
      if (!result.deleted) {
        setError(zh ? "此会话还有执行中或待核实的任务，请完成或取消并核实后再删除。" : "This conversation has an active or unresolved task. Finish or cancel it and verify its outcome before deleting.");
        return;
      }
      draftScopes.delete(id);
      setConversations((current) => current.filter((conversation) => conversation.id !== id));
      if (result.cleanup_pending) setNotice(zh ? "会话已删除，但本地文件清理未完成。请重启应用重试清理。" : "Conversation deleted. Local file cleanup is pending; restart the app to retry cleanup.");
      const wasSelected = selectedRef.current === id;
      if (wasSelected) {
        drafts.accept(id, { content: "", attachment_ids: [], revision: 0 });
        selectedRef.current = ""; setSelected(""); setContent(emptyContent);
        setScheduleCreateOpen(false);
        setDocumentPickerOpen(false); setBrowserOpen(false); setPage("chat");
        await selectDraft("");
      }
      const remaining = await store.list();
      setConversations(remaining);
      if (wasSelected && remaining[0]) await select(remaining[0].id);
    });
  }
  async function ensureConversation() {
    if (!selectedRef.current) {
      const conversation = await store.create(zh ? "新对话" : "New conversation");
      selectedRef.current = conversation.id; setSelected(conversation.id); setContent(emptyContent);
      await desktop.selectConversation?.(conversation.id);
      await reload();
    }
    const id = selectedRef.current;
    if (drafts.id() === "") {
      const source = await drafts.flush();
      const moved = await store.moveWelcomeDraft(id, source.revision, draftScopes.get("") ?? "");
      drafts.accept("", workbenchDraft(moved.source));
      await selectDraft(id);
    }
    return id;
  }
  async function save(submit = true) {
    if (!draft.trim() && !inputFiles.length) return;
    await action(async () => {
      const id = await ensureConversation();
      const saved = await drafts.flush();
      const sourceID = drafts.id();
      const queued = await store.enqueueDraft(id, sourceID, saved.revision, draftScopes.get(sourceID) ?? "");
      drafts.accept(sourceID, workbenchDraft(queued.draft));
      if (selectedRef.current === id) setContent(await store.read(id));
      await reload();
      if (submit) await submitTask(queued.task, id);
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
  async function renameConversation(id: string) {
    if (busyRef.current || sessionActionID || !sessionTitleDraft.trim()) return;
    setSessionActionID(id);
    try {
      await store.rename(id, sessionTitleDraft.trim());
      await reload(); setEditingSession(""); setSessionTitleDraft("");
    } catch (err) { surfaceError(err); }
    finally { setSessionActionID(""); }
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
    if (!iscp || capabilities.settingsOwner) void api.updateLanguage(next).then(setOwnerProfile).catch(() => undefined);
  }
  async function openTrace(runID: string) {
    setTraceLoading(true);
    try { setTraceRun(await api.trace(runID)); }
    catch (err) { surfaceError(err, dictionaries[language].errors.trace); }
    finally { setTraceLoading(false); }
  }

  const localSchedules = useLocalSchedules(store, localScopeKey, page === "schedules" && !settings, surfaceError);
  useEffect(() => { setEditingSession(""); setSessionTitleDraft(""); setScheduleCreateOpen(false); }, [localScopeKey]);
  useEffect(() => { if (page !== "schedules" || settings) setScheduleCreateOpen(false); }, [page, settings]);
  useEffect(() => { if (!capabilities.browser) setBrowserOpen(false); }, [capabilities.browser]);
  const text = dictionaries[language];
  const pendingApprovals = approvals.filter((approval) => approval.status === "pending");
  const pendingCandidates = candidates.filter((candidate) => candidate.status === "pending");
  const activeAttachments = useMemo<MessageAttachment[]>(() => inputFiles.map((id) => {
    const file = content.files.find((item) => item.id === id);
    return { artifact_id: id, rel_path: `local:${id}/${file?.name ?? id}`, name: file?.name ?? id, bytes: file?.size, content_type: file?.content_type };
  }), [content.files, inputFiles]);
  const localDocuments = useMemo<ArtifactObject[]>(() => content.files.map((file) => ({
    id: file.id,
    kind: "document",
    backend: "client_store",
    key: file.name,
    uri: `local:${file.id}`,
    content_type: file.content_type ?? "",
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
      editingSession={editingSession} sessionTitleDraft={sessionTitleDraft} sessionActionId={sessionActionID}
      onStartRename={(conversation) => { setEditingSession(conversation.id); setSessionTitleDraft(conversation.title); }}
      onCancelRename={() => { setEditingSession(""); setSessionTitleDraft(""); }}
      onTitleDraftChange={setSessionTitleDraft} onRenameSubmit={(id) => void renameConversation(id)}
      onDeleteSession={(id) => void deleteConversation(id)}
      onNavigate={(next) => { if (next === "settings") { setTab("settings"); setSettings(true); } else if (next === "schedules") setPage("schedules"); else void create(); }}
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
          {typeof desktop.state === "function" && <button disabled={!capabilities.browser || connection?.state !== "connected"} className={`iconButton rightSidebarToggle ${browserOpen ? "active" : ""}`} type="button" aria-label={copy.toggleInspector} title={zh ? "浏览器" : "Browser"} aria-expanded={browserOpen} onClick={() => setBrowserOpen((open) => !open)}><PanelRight size={18} /></button>}
        </div>
      </header>}
      {error && <div className="localFeedback" role="alert"><p>{error}</p><button type="button" onClick={() => void reload().then(async () => { if (iscp && connection?.state === "connected") await refreshGlobal(); setError(""); }).catch(surfaceError)}>{zh ? "重试读取" : "Retry loading"}</button></div>}
      {notice && <p className="localFeedback" role="status">{notice}</p>}
      {connection?.state === "service_unavailable" && <div className="localFeedback" role="status"><p>{zh ? "连接暂时不可用，请重新连接后继续。" : "Connection unavailable. Reconnect to continue."}</p><button type="button" disabled={busy} onClick={() => void action(async () => { setConnection(await desktop.retryLocalConnection()); })}>{zh ? "重新连接" : "Reconnect"}</button></div>}
      {settings ? <div className="settingsPageContent"><header className="settingsPageHeader"><h1>{copy.settingsTitles[tab]}</h1>{tab !== "memory" && tab !== "approvals" && <p>{copy.settingsDescriptions[tab]}</p>}</header><InspectorColumn key={settingsScope} settingsPage showTabs={false} tab={tab} onTabChange={setTab}
        settingsAccess={settingsAccess} activityUnavailable={iscp ? iscpSettingsUnavailable(language) : undefined}
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
          {capabilities.browser && browserState?.browser_host?.unknown_writes?.filter((command) => command.local_conversation_id === selected).map((command) => <section className="localUnknownWrite" role="alert" key={command.command_id}>
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
          {content.tasks.filter((task) => task.status === "delivery_too_large").map((task) => <section className="localFeedback" role="alert" key={task.id}>
            <p>{zh ? "此任务的结果超过当前连接的传输上限，无法取回。已停止重试，任务不会自动重新执行。" : "This task's result exceeds the connection's transfer limit and cannot be retrieved. Retries have stopped; the task will not run again automatically."}</p>
          </section>)}
          {content.tasks.filter((task) => task.termination_reason).map((task) => <section className="localFeedback" role="status" key={`termination:${task.id}`}>
            <p>{zh ? "服务重启已终结此任务。已记录的审批决定仍保留，任务不会自动重发。" : "The gateway restart ended this task. Recorded approval decisions are retained; the task will not be resubmitted."}</p>
          </section>)}
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
                  {pending && !expired && <div className="localTaskActions">{(["approve", "reject"] as const).filter((decision) => !approval.decision || approval.decision === decision).map((decision) => <button type="button" key={decision} disabled={busy || connection?.state !== "connected" || (!approval.actionable || !capabilities.approvals)} onClick={() => void action(() => decideApproval(task, approval, decision))}>
                    {approval.decision ? (decision === "approve" ? (zh ? "重试原批准决定" : "Retry original approval") : (zh ? "重试原拒绝决定" : "Retry original rejection")) : (decision === "approve" ? (zh ? "批准此操作" : "Approve this action") : (zh ? "拒绝此操作" : "Reject this action"))}
                  </button>)}</div>}
                </section>;
          })}


        </div>
        <MailWorkspaceContext.Provider value={{
          identity: mailIdentity,
          enabled: connection?.state === "connected" && capabilities.mail && Boolean(mailScope),
          loginEnabled: connection?.state === "connected" && surfaceEnabled(connection, "mail_settings"),
          sendEnabled: connection?.state === "connected" && surfaceEnabled(connection, "mail_send"),
          attachmentsEnabled: connection?.state === "connected" && surfaceEnabled(connection, "mail_send_attachments"),
          listFiles: store.listFiles, conversationID: selected,
          onFileSaved: async () => { if (selectedRef.current) setContent(await store.read(selectedRef.current)); },
          download: async (mailboxID, mailID, partID) => {
            if (connection?.state !== "connected" || !capabilities.mail || !window.sparkclawMailSync?.saveAttachment) throw new Error(zh ? "邮件附件暂时不可用。" : "Mail attachments are unavailable.");
            const scope = lastMailScope.current;
            const id = await ensureConversation();
            if (!scope || scope !== lastMailScope.current) throw new Error("Mail authentication changed");
            const file = await window.sparkclawMailSync.saveAttachment(mailboxID, mailID, partID, id);
            if (scope !== lastMailScope.current) throw new Error("Mail authentication changed");
            if (selectedRef.current === id) setContent(await store.read(id));
            await store.exportFile(file.id);
          },
        }}>
        <ComposerSurface text={text} language={language} activeSession={selected} activeInput={draft}
          activeAttachments={activeAttachments} busy={busy} voice={voice} composerInputRef={composerInputRef}
          filesEnabled={capabilities.files} mailEnabled={capabilities.mail && connection?.state === "connected"}
          canCompose={drafts.ready} canSend={connection?.state === "connected" && drafts.ready && (!iscp || Boolean(runtimeConfig && ready && ownerProfile))}
          onInputChange={(value) => { draftRef.current = value; setDraft(value); }}
          onUploadDocument={saveFile}
          onChooseDocument={() => setDocumentPickerOpen(true)}
          onOpenAttachment={async (attachment) => {
            const result = await store.exportFile(attachment.artifact_id ?? "");
            if (result.saved) setNotice(zh ? "已另存文件。" : "File exported.");
          }}
          onRemoveAttachment={(attachment) => setInputFiles((current) => current.filter((id) => id !== attachment.artifact_id))}
          onSend={() => void save(true)} />
        </MailWorkspaceContext.Provider>
        {drafts.status === "error" && <div className="localDraftRecovery" role="alert">
          <p>{zh ? "草稿尚未保存，请保留此窗口并重试" : "Draft not saved. Keep this window open and retry"}</p>
          <button type="button" onClick={() => void drafts.flush().catch(surfaceError)}>{zh ? "重试保存草稿" : "Retry saving draft"}</button>
          <button type="button" onClick={() => void drafts.saveCurrentOverLatest().catch(surfaceError)}>{zh ? "用我的输入替换已保存草稿" : "Replace saved draft with my text"}</button>
        </div>}
        {documentPickerOpen && <ComposerDocumentPicker documents={localDocuments} text={text} language={language}
          onChoose={(document) => {
            if (document.bytes > 8 * 1024 * 1024) { surfaceError(new Error(zh ? '文件超过执行附件 8 MiB 限制。' : 'File exceeds the 8 MiB execution attachment limit.')); return; }
            setInputFiles((current) => current.includes(document.id) ? current : [...current, document.id]);
            setDocumentPickerOpen(false);
          }} onClose={() => setDocumentPickerOpen(false)} />}
        </section>
        {page === "schedules" && <div className="workbenchPage schedulePage">
          <div className="workbenchPageHeader"><div><h1>{copy.pageTitles.schedules}</h1><p>{copy.pageDescriptions.schedules}</p></div><button className="primaryButton" onClick={() => setScheduleCreateOpen(true)} disabled={busy || Boolean(localSchedules.busyID) || voice.active}><Plus size={15} />{copy.newSchedule}</button></div>
          <ScheduleBar key={localScopeKey} schedules={localSchedules.schedules} open={scheduleBarOpen} loading={localSchedules.loading} busyId={localSchedules.busyID}
            language={language} text={text} onToggle={() => setScheduleBarOpen(value => !value)}
            onRefresh={() => void localSchedules.check().catch(() => {})} onEdit={localSchedules.edit} onDelete={localSchedules.cancel} onRunNow={localSchedules.runNow} />
          {scheduleCreateOpen && <ScheduleCreateDialog busy={busy || Boolean(localSchedules.busyID)} text={text} onClose={() => setScheduleCreateOpen(false)} onCreate={localSchedules.create} />}
        </div>}
        {capabilities.browser && browserOpen && typeof desktop.state === "function" && <BrowserPanel language={language} localConversationID={selected} toolbar={<div className="localBrowserAuthorization">{selected && desktop.grantBrowserHost && <button className="localBrowserGrant" type="button" disabled={busy || connection?.state !== "connected"} onClick={() => void action(async () => {
            await desktop.grantBrowserHost!();
            setNotice(zh ? "已授权本机浏览器执行当前设备的任务。" : "This device's browser is authorized for its tasks.");
          })}>{zh ? "授权本机浏览器" : "Authorize this browser"}</button>}</div>} />}
      </>}
    </main>
    {searchOpen && <TaskSearch language={language} sessions={conversations} onSelect={(conversation) => { setSearchOpen(false); void select(conversation.id).catch(surfaceError); }} onClose={() => setSearchOpen(false)} />}
  </div>;
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

function workbenchDraft(value: LocalDraft) { return { content: value.content, attachment_ids: value.local_file_ids, revision: value.revision }; }

// Retain only this verified identity's in-memory edits while its connection is
// recovering. Neither short Grants nor capability revisions own the editor.
function mailEditorScope(status: DesktopConnectionStatus | undefined) {
  if (!status || !["connected", "reconnecting", "service_unavailable"].includes(status.state) || status.authorization_deletion ||
      !status.backend?.deployment_id || !status.owner_id || !status.client_id) return "";
  const backend = status.backend;
  return JSON.stringify([backend.transport || "lan", backend.origin, backend.deployment_id, status.owner_id, status.client_id,
    backend.domain_id, backend.initiator_device_id, backend.responder_device_id, backend.responder_key_thumbprint, backend.tls_certificate_sha256]);
}
