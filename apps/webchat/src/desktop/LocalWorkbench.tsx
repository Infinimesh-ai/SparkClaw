import { VoiceDraftPersistence } from "../lib/voiceDraftPersistence";
import { useConfirm } from "../components/confirmation";
import { BROWSER_CONVERSATION_RESERVE, BROWSER_DEFAULT_WIDTH, useBrowserLayout } from "../hooks/useBrowserLayout";
import { useMessageScroll } from "../hooks/useMessageScroll";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ArrowLeft, Plus } from "lucide-react";
import { ScheduleBar, ScheduleCreateDialog, ScheduleSuggestions } from "../components/schedules";
import { useLocalSchedules } from "./useLocalSchedules";
import { SessionSidebar } from "../components/sidebar";
import { ClientConversationTab, ClientSidebarResizeHandle, useClientTitlebar, WorkspaceTitlebar } from "../components/clientWindow";
import { usePageHistory } from "../hooks/usePageHistory";
import { useArchivedConversations } from "../hooks/useArchivedConversations";
import { TaskSearch, WorkbenchWelcome, workbenchCopy } from "../components/workbench";
import { MailWorkspaceContext } from "./MailWorkspaceContext";
import type {} from "./mailCapability";
import { ISCPDeviceAuthorization, desktopSettingsAccess, iscpSettingsUnavailable } from "./iscpSettings";
import { surfaceEnabled } from "./capability";
import { api } from "../api/client";
import { InspectorColumn, type PanelTab } from "../components/inspector";
import { InspectorResizeHandle, useInspectorWidth } from "../components/inspectorResize";
import { WorkspaceSettingsSidebar } from "../components/settingsSidebar";
import { dictionaries, initialLanguage, LANGUAGE_STORAGE_KEY, type Language } from "../i18n";
import { applyAppearance } from "../lib/appearance";
import { clientStore, type LocalConversation, type LocalConversationContent, type LocalTask, type LocalApproval, type LocalDraft } from "./clientStore";
import { desktopCapability } from "./capability";
import type { DesktopConnectionStatus, DesktopState } from "./types";
import { BrowserPanel } from "./BrowserPanel";
import { LocalComposerApprovals, pendingLocalApprovals } from "./localComposerApprovals";
import { localMessageRows } from "./localMessageRows";
import { ConversationReadVersion } from "./conversationReadVersion";
import { MessageBubble, TaskProgressMessage } from "../components/messages";
import { useConversationActivity } from "../hooks/useConversationActivity";
import { conversationActivity, isTaskInProgress, localExecutionStates, unseenActivity } from "../lib/conversationActivity";
import { ComposerDocumentPicker, ComposerSurface, ComposerUploadError } from "../components/composer";
import { useWorkbenchDraft } from "../hooks/useWorkbenchDraft";
import { useVoiceInput } from "../hooks/useVoiceInput";
import type { VoiceDraftAnchor } from "../hooks/useVoiceInput";
import { insertVoiceTranscript } from "../lib/voiceDraft";
import { usePassiveNotifications } from "../hooks/usePassiveNotifications";
import { NotificationCenter } from "../components/notificationCenter";
import { ActionIcon } from "../components/ActionIcon";
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
  useEffect(() => { document.documentElement.lang = language === "zh" ? "zh-CN" : "en"; }, [language]);
  const confirm = useConfirm(dictionaries[language]);
  const zh = language === "zh";
  const copy = workbenchCopy[language];
  const inspectorResize = useInspectorWidth({ persist: false });
  const [conversations, setConversations] = useState<LocalConversation[]>([]);
  const [selected, setSelected] = useState("");
  const selectedRef = useRef(selected); selectedRef.current = selected;
  const readGeneration = useRef(0);
  const [content, setContent] = useState(emptyContent);
  const approvalPending = pendingLocalApprovals(content.tasks).length > 0;
  const approvalPendingRef = useRef(approvalPending); approvalPendingRef.current = approvalPending;
  const contentReads = useRef(new ConversationReadVersion());
  const readContent = useCallback((id: string) => {
    const generation = readGeneration.current;
    return contentReads.current.read(() => store.read(id), () => selectedRef.current === id && readGeneration.current === generation, setContent);
  }, [store]);
  const draftRef = useRef("");
  const inputFilesRef = useRef<string[]>([]);
  const [scheduleCreateOpen, setScheduleCreateOpen] = useState(false);
  const [scheduleRequest, setScheduleRequest] = useState("");
  const [scheduleBarOpen, setScheduleBarOpen] = useState(true);
  const [editingSession, setEditingSession] = useState("");
  const [sessionTitleDraft, setSessionTitleDraft] = useState("");
  const [sessionActionID, setSessionActionID] = useState("");
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);
  const [submittingConversation, setSubmittingConversation] = useState("");
  const [submittingTaskID, setSubmittingTaskID] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [settings, setSettings] = useState(false);
  const [page, setPage] = useState<"chat" | "schedules">("chat");
  const [searchOpen, setSearchOpen] = useState(false);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const toggleSidebar = () => setSidebarCollapsed((current) => !current);
  const [mailIdentity, setMailIdentity] = useState(0);
  const [browserOpen, setBrowserOpen] = useState(false);
  const [documentPickerOpen, setDocumentPickerOpen] = useState(false);
  useEffect(() => { if (approvalPending) setDocumentPickerOpen(false); }, [approvalPending]);
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
    settingsBindings: surfaceEnabled(connection, "settings_bindings"),
  };
  const browserLayout = useBrowserLayout(browserOpen, inspectorResize.width, capabilities.browser && !settings && page === "chat", true);
  const messageScroll = useMessageScroll(selected, !settings && page === "chat");
  const browserCovering = capabilities.browser && browserLayout.covering;
  const closeBrowser = () => { setBrowserOpen(false); browserLayout.restoreTrigger(); };
  const toggleBrowser = () => {
    if (browserOpen && browserLayout.visible) closeBrowser();
    else { browserLayout.reveal(); setBrowserOpen(true); }
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
  const settingsAccess = {
    ...desktopSettingsAccess(connection, language),
    ...(iscp && connection ? { deviceAuthorization: <ISCPDeviceAuthorization key={settingsScope} connection={connection} language={language} /> } : {}),
  };

  const surfaceError = useCallback((err: unknown, fallback = zh ? "操作失败，请重试。" : "Something went wrong. Try again.") => {
    setError(err instanceof Error ? err.message : fallback);
  }, [zh]);
  useEffect(() => {
    if (!iscp) return;
    setConnectors([]);
    setNotificationBindings([]);
    if (!settings || tab !== "connections" || !capabilities.settingsConnectors) return;
    let active = true;
    void api.connectors().then(value => { if (active) setConnectors(value.connectors); }).catch(surfaceError);
    if (capabilities.settingsBindings) {
      void api.notificationBindings().then(value => { if (active) setNotificationBindings(value.bindings); }).catch(surfaceError);
    }
    return () => { active = false; };
  }, [iscp, settings, tab, capabilities.settingsConnectors, capabilities.settingsBindings, settingsScope, surfaceError]);
  const scopeError = useRef(surfaceError); scopeError.current = surfaceError;
  const localScopeKey = JSON.stringify([
    Boolean(connection && ["connected", "reconnecting", "service_unavailable"].includes(connection.state)),
    connection?.backend?.deployment_id, connection?.owner_id, currentClientID,
  ]);
  const readActivityStates = useCallback(async (id: string) => localExecutionStates((await store.read(id)).tasks), [store]);
  const activityByConversation = useConversationActivity(conversations.map(row => row.id), readActivityStates, localScopeKey, store.onChange, page === "chat" ? selected : "");
  const draftScopes = useMemo(() => new Map<string, string>(), [store, localScopeKey]);
  const draftAdapter = useMemo(() => ({
    load: async (id: string) => {
      const value = await store.draft(id);
      const expected = draftScopes.get(id);
      if (expected && expected !== value.scope_key) throw new Error("Draft authentication changed; reload this workbench");
      draftScopes.set(id, value.scope_key); return workbenchDraft(value);
    },
    save: async (id: string, value: import("../lib/workbenchDraft").WorkbenchDraft) =>
      workbenchDraft(await store.saveDraft(id, value.content, value.attachment_ids, value.revision, draftScopes.get(id) ?? "", value.recording_request_id)),
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
      const [status, owner, connectorList, bindingList] = await Promise.all([
        api.ready(), api.owner(),
        capabilities.settingsConnectors ? api.connectors() : null,
        capabilities.settingsBindings ? api.notificationBindings() : null,
      ]);
      setReady(status); setOwnerProfile(owner);
      if (connectorList) setConnectors(connectorList.connectors);
      if (bindingList) setNotificationBindings(bindingList.bindings);
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
  }, [iscp, capabilities.settingsConnectors, capabilities.settingsBindings]);
  const select = useCallback(async (id: string) => {
    setPage("chat");
    if (selectedRef.current === id) return;
    const generation = ++readGeneration.current;
    await selectDraft(id);
    if (generation !== readGeneration.current) return;
    selectedRef.current = id; setSelected(id); setContent(emptyContent);
    if (capabilities.browser) await desktop.selectConversation?.(id);
    await readContent(id);
  }, [desktop, store, selectDraft, capabilities.browser, readContent]);
  useEffect(() => {
    const disposeAppearance = applyAppearance();
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
    return () => { active = false; disposeAppearance(); unsubscribe?.(); unsubscribeBrowser?.(); ++readGeneration.current; };
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
    let paint: number | undefined;
    let reading = false;
    let dirty = false;
    const refresh = () => {
      if (!active) return;
      dirty = true;
      if (paint !== undefined || reading) return;
      paint = window.requestAnimationFrame(() => {
        paint = undefined;
        if (!active) return;
        reading = true; dirty = false;
        void readContent(selected).catch((error) => { if (active && selectedRef.current === selected) surfaceError(error); }).finally(() => {
          reading = false;
          if (active && dirty) refresh();
        });
      });
    };
    const timer = window.setInterval(refresh, 3000);
    const unsubscribe = store.onChange?.(refresh);
    return () => { active = false; unsubscribe?.(); window.clearInterval(timer); if (paint !== undefined) window.cancelAnimationFrame(paint); };
  }, [selected, store, surfaceError, readContent]);

  const voiceDraftPersistence = useMemo(() => new VoiceDraftPersistence(), [store, localScopeKey]);
  const applyVoiceTranscript = useCallback(async (result: { text: string; request_id: string }, anchor: VoiceDraftAnchor) => {
    return voiceDraftPersistence.apply(JSON.stringify([anchor.sessionId, result.request_id]), () => {
      if (approvalPendingRef.current || anchor.sessionId !== selectedRef.current || draftRef.current !== anchor.draft) return false;
      const start = Math.min(anchor.selectionStart, draftRef.current.length);
      const end = Math.min(Math.max(anchor.selectionEnd, start), draftRef.current.length);
      const inserted = insertVoiceTranscript(draftRef.current, result.text, start, end);
      draftRef.current = inserted.value;
      setDraft(inserted.value);
      if (iscp) drafts.markRecording(anchor.sessionId, result.request_id);
      window.requestAnimationFrame(() => {
        composerInputRef.current?.focus();
        composerInputRef.current?.setSelectionRange(inserted.caret, inserted.caret);
      });
      return true;
    }, () => drafts.flush(anchor.sessionId), iscp ? async () => {
      if (!store.recordingApplied) throw new Error("Recording draft persistence is unavailable");
      return store.recordingApplied(anchor.sessionId, result.request_id, draftScopes.get(anchor.sessionId) ?? "");
    } : undefined);
  }, [drafts, setDraft, voiceDraftPersistence, iscp, store, draftScopes]);
  const voice = useVoiceInput({
    speech: ready?.speech ?? null,
    sessionId: selected,
    language: runtimeConfig?.speech.default_language ?? "auto",
    externallyDisabled: approvalPending || busy || !selected || !capabilities.speech,
    iscp, iscpRealtime: surfaceEnabled(connection, "speech_realtime"),
    onTranscript: applyVoiceTranscript
  });
  const passiveNotifications = usePassiveNotifications(connection?.state === "connected" && capabilities.notifications, iscp ? "poll" : "stream");

  async function action(operation: () => Promise<void>, rethrow = false) {
    if (busyRef.current) return;
    busyRef.current = true;
    setBusy(true); setError(""); setNotice("");
    try { await operation(); } catch (err) { surfaceError(err); if (rethrow) throw err; } finally { busyRef.current = false; setBusy(false); }
  }
  // A new conversation starts from the welcome input; its row is created by
  // the first send, so the list never shows an empty placeholder.
  async function startConversation() {
    await action(async () => {
      setPage("chat");
      if (!selectedRef.current && drafts.id() === "") return;
      ++readGeneration.current;
      selectedRef.current = ""; setSelected(""); setContent(emptyContent);
      setScheduleCreateOpen(false); setDocumentPickerOpen(false);
      await selectDraft("");
      if (capabilities.browser) await desktop.selectConversation?.("");
    });
  }
  function deleteConversation(id: string) {
    return deleteConversations([id], dictionaries[language].nav.confirmDeleteLocalSession);
  }
  // One confirmation covers every id. Conversations with unresolved tasks are
  // kept; a failure stops the batch after reconciling what was already deleted.
  async function deleteConversations(ids: string[], confirmation: string) {
    if (!ids.length || busyRef.current || !(await confirm(confirmation, { danger: true }))) return;
    await action(async () => {
      await drafts.flush();
      ++readGeneration.current;
      const deleted: string[] = [];
      let blocked = 0, cleanupPending = false, failure: unknown;
      for (const id of ids) {
        try {
          const result = await store.remove(id);
          if (!result.deleted) { blocked++; continue; }
          deleted.push(id);
          draftScopes.delete(id);
          if (result.cleanup_pending) cleanupPending = true;
        } catch (err) {
          failure = err;
          break;
        }
      }
      if (blocked) setError(ids.length === 1
        ? zh ? "此会话还有执行中或待核实的任务，请完成或取消并核实后再删除。" : "This conversation has an active or unresolved task. Finish or cancel it and verify its outcome before deleting."
        : zh ? `${blocked} 个会话还有执行中或待核实的任务，已保留。请完成或取消并核实后再删除。` : `${blocked} conversations have active or unresolved tasks and were kept. Finish or cancel them and verify their outcomes before deleting.`);
      if (!deleted.length) { if (failure) throw failure; return; }
      setConversations((current) => current.filter((conversation) => !deleted.includes(conversation.id)));
      if (cleanupPending) setNotice(zh ? "会话已删除，但本地文件清理未完成。请重启应用重试清理。" : "Conversation deleted. Local file cleanup is pending; restart the app to retry cleanup.");
      const selectedID = selectedRef.current;
      const wasSelected = deleted.includes(selectedID);
      if (wasSelected) {
        selectedRef.current = ""; setSelected(""); setContent(emptyContent);
        // A selection whose draft never loaded has no draft entry to discard.
        if (drafts.id() === selectedID) drafts.accept(selectedID, { content: "", attachment_ids: [], revision: 0 });
        setScheduleCreateOpen(false);
        setDocumentPickerOpen(false); setBrowserOpen(false); setPage("chat");
        await selectDraft("");
      }
      const remaining = await store.list();
      setConversations(remaining);
      if (wasSelected && remaining[0]) await select(remaining[0].id);
      if (failure) throw failure;
    });
  }
  async function ensureConversation(onTargetResolved?: (id: string) => void) {
    if (selectedRef.current) { onTargetResolved?.(selectedRef.current); return selectedRef.current; }
    if (drafts.id() !== "") await selectDraft("");
    const source = await drafts.flush();
    const conversation = await store.create(zh ? "新对话" : "New conversation", true);
    try {
      const moved = await store.moveWelcomeDraft(conversation.id, source.revision, draftScopes.get("") ?? "");
      drafts.accept("", workbenchDraft(moved.source));
    } catch (error) {
      // Keep the welcome input in place and leave no empty conversation row.
      await store.remove(conversation.id).catch(() => undefined);
      throw error;
    }
    ++readGeneration.current;
    selectedRef.current = conversation.id; setSelected(conversation.id); setContent(emptyContent);
    onTargetResolved?.(conversation.id);
    await selectDraft(conversation.id);
    await desktop.selectConversation?.(conversation.id);
    await reload();
    return conversation.id;
  }
  async function save(submit = true) {
    if (approvalPendingRef.current) return;
    if (!draft.trim() && !inputFiles.length) return;
    await action(async () => {
      if (submit) setSubmittingConversation(selectedRef.current);
      try {
        const id = await ensureConversation();
        if (submit) setSubmittingConversation(id);
        const saved = await drafts.flush();
        if (approvalPendingRef.current) return;
        const sourceID = drafts.id();
        const queued = await store.enqueueDraft(id, sourceID, saved.revision, draftScopes.get(sourceID) ?? "");
        if (submit) setSubmittingTaskID(queued.task.id);
        drafts.accept(sourceID, workbenchDraft(queued.draft));
        if (selectedRef.current === id) await readContent(id);
        await reload();
        if (submit) await submitTask(queued.task, id);
      } finally { setSubmittingConversation(""); setSubmittingTaskID(""); }
    });
  }
  async function submitTask(task: LocalTask, id: string) {
    try { await store.submit(task.request_id); }
    finally {
      if (selectedRef.current === id) await readContent(id);
      await reload();
    }
  }
  async function decideApproval(task: LocalTask, approval: LocalApproval, decision: "approve" | "reject") {
    const id = selected;
    try {
      await store.decideApproval(task.request_id, approval.approval_id, approval.digest, decision);
    } finally { if (selectedRef.current === id) await readContent(id); await reload(); }
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
    if (approvalPendingRef.current || !file) return undefined;
    let uploadSession = selectedRef.current;
    let saved: Awaited<ReturnType<typeof store.saveFile>> | undefined;
    try {
      await action(async () => {
        if (file.size > 64 * 1024 * 1024) throw new Error(zh ? "文件超过 64 MiB。请选择较小的文件。" : "File exceeds 64 MiB. Choose a smaller file.");
        const id = await ensureConversation(target => { uploadSession = target; });
        saved = await store.saveFile(id, file.name, new Uint8Array(await file.arrayBuffer()));
        setInputFiles((current) => saved && !current.includes(saved.id) ? [...current, saved.id] : current);
        // The file is already durable. A refresh failure must not offer to save
        // it again under a new file ID; retain the attachment and report the read.
        if (selectedRef.current === id) await readContent(id).catch(surfaceError);
        setNotice(zh ? "文件已添加。" : "File added.");
      }, true);
    } catch (reason) {
      throw new ComposerUploadError(uploadSession, reason, text.errors.upload);
    }
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
  const submittingTask = content.tasks.find(task => task.id === submittingTaskID);
  const submitting = Boolean(selected && submittingConversation === selected &&
    (!submittingTask || submittingTask.status === "awaiting_runtime" || isTaskInProgress(submittingTask.status)));
  const home = page === "chat" && !submitting && !content.messages.length && !content.tasks.length && !content.files.length;
  const pendingTasks = content.tasks.filter(task => isTaskInProgress(task.status) || (submitting && task.id === submittingTaskID));
  const selectedActivity = submitting ? "running" : content.tasks.length ? conversationActivity(localExecutionStates(content.tasks)) : activityByConversation[selected];
  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing) return;
      const layered = document.querySelector('dialog[open], [role="dialog"], [role="menu"], .notificationInbox');
      if (event.key === "Escape") {
        if (!layered && browserLayout.visible) { event.preventDefault(); closeBrowser(); }
        return;
      }
      if (!(event.metaKey || event.ctrlKey) || event.altKey || layered) return;
      const key = event.key.toLowerCase();
      if (key === "k") { event.preventDefault(); setSearchOpen(true); }
      if (key === "n" && !busy && !voice.active) { event.preventDefault(); setSettings(false); void startConversation(); }
      if (key === ",") { event.preventDefault(); setTab("settings"); setSettings(true); }
      if (key === "b" && !(event.target instanceof Element && event.target.closest('input, textarea, [contenteditable="true"]'))) {
        event.preventDefault(); toggleSidebar();
      }
    };
    window.addEventListener("keydown", shortcut);
    return () => window.removeEventListener("keydown", shortcut);
  });
  // Archived conversations never stay in the main view: archiving the open one
  // starts a new conversation, and history and search skip archived ones.
  const { archived } = useArchivedConversations(ownerProfile?.id);
  const unarchivedConversations = conversations.filter((conversation) => !archived.includes(conversation.id));
  const selectedArchived = Boolean(selected) && archived.includes(selected);
  useEffect(() => { if (selectedArchived && !busy) void startConversation(); }, [selectedArchived, busy]);
  const pageHistory = usePageHistory({ page, settings, sessionID: selected, settingsTab: settings ? tab : "settings" }, {
    scope: localScopeKey,
    disabled: busy || voice.active,
    valid: (entry) => !entry.sessionID || unarchivedConversations.some((conversation) => conversation.id === entry.sessionID),
    restore: async (entry) => {
      try {
        if (entry.sessionID) await select(entry.sessionID); else await startConversation();
        setPage(entry.page); setSettings(entry.settings);
        if (entry.settings) setTab(entry.settingsTab);
        setSearchOpen(false); setScheduleCreateOpen(false);
      } catch (error) { surfaceError(error); throw error; }
    },
  }, useClientTitlebar());

  const homePresentation = home && !settings;
  return <div className={`shell workbench glassWorkbench localWorkbench ${homePresentation ? "homePresentation" : ""} ${settings ? "settingsPageMode" : sidebarCollapsed ? "sidebarCollapsed" : ""}`}>
    {settings ? <WorkspaceSettingsSidebar text={text} language={language} tab={tab}
      pendingApprovalCount={pendingApprovals.length} pendingCandidateCount={pendingCandidates.length} onTabChange={setTab} onBack={() => setSettings(false)} /> : <SessionSidebar
      text={text} language={language} page={page} ownerProfile={ownerProfile} schedulePlacement="top"
      sessions={conversations} activeSession={selected} busy={busy}
      activityBySession={{ ...activityByConversation, ...(selected ? { [selected]: unseenActivity(selectedActivity, page === "chat") } : {}) }}
      onCreateSession={() => void startConversation()} onSelectSession={(conversation) => void select(conversation.id).catch(surfaceError)}
      editingSession={editingSession} sessionTitleDraft={sessionTitleDraft} sessionActionId={sessionActionID}
      onStartRename={(conversation) => { setEditingSession(conversation.id); setSessionTitleDraft(conversation.title); }}
      onCancelRename={() => { setEditingSession(""); setSessionTitleDraft(""); }}
      onTitleDraftChange={setSessionTitleDraft} onRenameSubmit={(id) => void renameConversation(id)}
      onDeleteSession={(id) => void deleteConversation(id)}
      onNavigate={(next) => { if (next === "settings") { setTab("settings"); setSettings(true); } else if (next === "schedules") setPage("schedules"); else void startConversation(); }}
      onSearch={() => setSearchOpen(true)} onToggleSidebar={toggleSidebar}
      onLogout={() => void logout().catch(surfaceError)}
      listNotice={loading ? <p className="localListNotice" role="status">{zh ? "正在读取对话…" : "Loading conversations…"}</p> : !conversations.length ? <p className="localListNotice">{copy.empty}</p> : undefined}
    />}
    {!settings && <ClientSidebarResizeHandle label={zh ? "调整侧边栏宽度" : "Resize sidebar"} onHide={toggleSidebar} />}
    <main ref={browserLayout.workspaceRef} className={`${settings ? "settingsPageMain" : "workspace desktopWorkbench"} ${homePresentation ? "homeWorkspace" : ""} ${capabilities.browser && browserOpen && !settings && page === "chat" ? "withInspector" : ""} ${inspectorResize.resizing ? "resizingInspector" : ""} ${browserLayout.className}`} style={settings ? undefined : inspectorResize.style} aria-busy={busy}>
      <WorkspaceTitlebar language={language} hidden={settings} navigation={pageHistory}
        title={!settings && page === "chat" && !browserCovering ? conversations.find((conversation) => conversation.id === selected)?.title : undefined}
        tab={browserCovering && <ClientConversationTab title={conversations.find((conversation) => conversation.id === selected)?.title || (zh ? "新对话" : "New conversation")}
          selected={browserLayout.region === "conversation"} onSelect={browserLayout.showConversation} />}
        sidebar={!settings && <button className="iconButton sidebarToggle" type="button" aria-label={copy.toggleNav} onClick={toggleSidebar}><ActionIcon name="panel-left" /></button>}
        back={(settings || page !== "chat") && <button className="iconButton workspaceBackButton" type="button" disabled={busy}
          onClick={settings ? () => setSettings(false) : () => { setPage("chat"); setTab("timeline"); }}
          title={settings ? copy.backToApp : copy.backToHome} aria-label={settings ? copy.backToApp : copy.backToHome}><ArrowLeft size={18} strokeWidth={1.75} /></button>}
        actions={<>
          <NotificationCenter notifications={passiveNotifications.notifications} unreadCount={passiveNotifications.unreadCount}
            open={passiveNotifications.open} toast={passiveNotifications.toast} error={passiveNotifications.error}
            language={language} text={text} onToggle={() => passiveNotifications.setOpen((current) => !current)}
            onDismissToast={passiveNotifications.dismissToast} onRead={passiveNotifications.markRead} onReadAll={passiveNotifications.markAllRead} />
          {!settings && typeof desktop.state === "function" && <button disabled={!capabilities.browser || connection?.state !== "connected"} className={`iconButton rightSidebarToggle ${browserOpen ? "active" : ""}`} type="button" ref={browserLayout.triggerRef} aria-label={browserLayout.visible ? (zh ? "收起浏览器" : "Collapse browser") : (zh ? "打开浏览器" : "Open browser")} title={!capabilities.browser || connection?.state !== "connected" ? (zh ? "浏览器暂时不可用" : "Browser unavailable") : browserLayout.visible ? (zh ? "收起浏览器" : "Collapse browser") : (zh ? "打开浏览器" : "Open browser")} aria-expanded={browserLayout.visible} onClick={toggleBrowser}><ActionIcon name="globe" active={browserLayout.visible} /></button>}
        </>} />
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
        onCurrentClientRevoked={logout} onLogout={logout}
        conversations={conversations} conversationBusy={busy} onDeleteConversation={deleteConversation}
        onDeleteConversations={(ids) => deleteConversations(ids, dictionaries[language].nav.confirmDeleteArchivedLocalSessions.replace("{count}", String(ids.length)))} /></div> : <>
        <section inert={browserLayout.className === "browserFocus"} className={`chatColumn localChat ${home ? "homeChat" : ""}`} hidden={page !== "chat"}>
        <div ref={messageScroll.listRef} className="messageList localHistory">
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
          {localMessageRows(content, selected, pendingTasks).map(row => <MessageBubble key={row.key}
            message={row.message} scrollAnchorId={row.scrollAnchorId} inProgress={row.inProgress}
            streamStatuses={[]} text={text} language={language} />)}
          {submitting && !pendingTasks.length && <TaskProgressMessage id="submitting" sessionId={selected}
            createdAt={new Date().toISOString()} text={text} language={language} />}
          {content.tasks.filter((task) => task.status === "delivery_too_large").map((task) => <section className="localFeedback" role="alert" key={task.id}>
            <p>{zh ? "此任务的结果超过当前连接的传输上限，无法取回。已停止重试，任务不会自动重新执行。" : "This task's result exceeds the connection's transfer limit and cannot be retrieved. Retries have stopped; the task will not run again automatically."}</p>
          </section>)}
          {content.tasks.filter((task) => task.termination_reason).map((task) => <section className="localFeedback" role="status" key={`termination:${task.id}`}>
            <p>{zh ? "服务重启已终结此任务。已记录的审批决定仍保留，任务不会自动重发。" : "The gateway restart ended this task. Recorded approval decisions are retained; the task will not be resubmitted."}</p>
          </section>)}
        </div>
        {messageScroll.away && <div className="messageScrollAction"><button type="button" className="messageLatest" onClick={messageScroll.resume}>{zh ? "回到最新" : "Return to latest"}</button></div>}
        <MailWorkspaceContext.Provider value={{
          identity: mailIdentity,
          enabled: connection?.state === "connected" && capabilities.mail && Boolean(mailScope),
          loginEnabled: false,
          resumeEnabled: connection?.state === "connected" && surfaceEnabled(connection, "mail_settings"),
          loginNotice: settingsAccess.emailLoginNotice,
          accountsEnabled: connection?.state === "connected" && surfaceEnabled(connection, "mail_accounts"),
          sendEnabled: connection?.state === "connected" && surfaceEnabled(connection, "mail_send"),
          attachmentsEnabled: connection?.state === "connected" && surfaceEnabled(connection, "mail_send_attachments"),
          listFiles: store.listFiles, conversationID: selected,
          onFileSaved: async () => { if (selectedRef.current) await readContent(selectedRef.current); },
          download: async (mailboxID, mailID, partID) => {
            if (connection?.state !== "connected" || !capabilities.mail || !window.sparkclawMailSync?.saveAttachment) throw new Error(zh ? "邮件附件暂时不可用。" : "Mail attachments are unavailable.");
            const scope = lastMailScope.current;
            const id = await ensureConversation();
            if (!scope || scope !== lastMailScope.current) throw new Error("Mail authentication changed");
            const file = await window.sparkclawMailSync.saveAttachment(mailboxID, mailID, partID, id);
            if (scope !== lastMailScope.current) throw new Error("Mail authentication changed");
            if (selectedRef.current === id) await readContent(id);
            await store.exportFile(file.id);
          },
        }}>
        <ComposerSurface text={text} language={language} activeSession={selected} activeInput={draft}
          approvalPending={approvalPending}
          approvals={<LocalComposerApprovals key={`${localScopeKey}:${selected}`} tasks={content.tasks} text={text}
            enabled={!busy && connection?.state === "connected" && capabilities.approvals} onDecide={decideApproval} />}
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
        {documentPickerOpen && !approvalPending && <ComposerDocumentPicker documents={localDocuments} text={text} language={language}
          onChoose={(document) => {
            if (document.bytes > 8 * 1024 * 1024) { surfaceError(new Error(zh ? '文件超过执行附件 8 MiB 限制。' : 'File exceeds the 8 MiB execution attachment limit.')); return; }
            setInputFiles((current) => current.includes(document.id) ? current : [...current, document.id]);
            setDocumentPickerOpen(false);
          }} onClose={() => setDocumentPickerOpen(false)} />}
        </section>
        {page === "schedules" && <div className="workbenchPage schedulePage">
          <div className="workbenchPageHeader"><div><h1>{copy.pageTitles.schedules}</h1><p>{copy.pageDescriptions.schedules}</p></div><button className="primaryButton" onClick={() => { setScheduleRequest(""); setScheduleCreateOpen(true); }} disabled={busy || Boolean(localSchedules.busyID) || voice.active}><Plus size={15} />{copy.newSchedule}</button></div>
          <ScheduleSuggestions text={text} disabled={busy || Boolean(localSchedules.busyID) || voice.active}
            onChoose={(request) => { setScheduleRequest(request); setScheduleCreateOpen(true); }} />
          <ScheduleBar key={localScopeKey} schedules={localSchedules.schedules} open={scheduleBarOpen} loading={localSchedules.loading} busyId={localSchedules.busyID}
            language={language} text={text} onToggle={() => setScheduleBarOpen(value => !value)}
            onRefresh={() => void localSchedules.check().catch(() => {})} onEdit={localSchedules.edit} onDelete={localSchedules.cancel} onRunNow={localSchedules.runNow} />
          {scheduleCreateOpen && <ScheduleCreateDialog key={scheduleRequest} initialRequest={scheduleRequest} busy={busy || Boolean(localSchedules.busyID)} text={text} onClose={() => setScheduleCreateOpen(false)} onCreate={localSchedules.create} />}
        </div>}
        {capabilities.browser && browserLayout.visible && typeof desktop.state === "function" && <InspectorResizeHandle resize={inspectorResize} label={copy.resizeInspector} onHide={closeBrowser}
          reserve={BROWSER_CONVERSATION_RESERVE} defaultWidth={BROWSER_DEFAULT_WIDTH} onResizeStart={() => browserLayout.setWide(false)}
          onExpand={() => browserLayout.setWide(true)} />}

      </>}
        {browserLayout.mounted && typeof desktop.state === "function" && <BrowserPanel open={browserOpen} visible={capabilities.browser && browserLayout.visible} onCollapse={closeBrowser} wide={browserLayout.wide} onWideChange={browserLayout.setWide} language={language} localConversationID={selected}
          conversationInFront={browserCovering ? browserLayout.region === "conversation" : undefined} onShowBrowser={browserLayout.reveal} />}
    </main>
    {searchOpen && <TaskSearch language={language} sessions={unarchivedConversations} onSelect={(conversation) => { setSearchOpen(false); void select(conversation.id).catch(surfaceError); }} onClose={() => setSearchOpen(false)} />}
  </div>;
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
