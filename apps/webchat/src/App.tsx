import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { PanelLeft, PanelRight, Plus, X } from "lucide-react";
import { TaskSearch, WorkbenchWelcome, workbenchCopy, type WorkspacePage } from "./components/workbench";
import { api, APIError, saveAPIToken, streamWorkbenchInvalidations } from "./api/client";
import { dictionaries, initialLanguage, LANGUAGE_STORAGE_KEY } from "./i18n";
import type { Language } from "./i18n";
import { BrowserPanel } from "./desktop/BrowserPanel";
import { desktopCapability } from "./desktop/capability";
import {
  MessageBubble,
  streamStatusFromEvent,
  upsertStreamStatus
} from "./components/messages";
import type { StreamStatus } from "./components/messages";
import { InspectorColumn } from "./components/inspector";
import type { PanelTab } from "./components/inspector";
import { ComposerDock } from "./components/composer";
import { NotificationCenter } from "./components/notificationCenter";
import { ScheduleBar, ScheduleCreateDialog } from "./components/schedules";
import { SessionSidebar } from "./components/sidebar";
import { WorkspaceSettingsSidebar } from "./components/settingsSidebar";
import { WorkbenchAccess } from "./components/workbenchAccess";
import { useWorkbenchAccess } from "./hooks/useWorkbenchAccess";
import { useDeliveryTarget } from "./hooks/useDeliveryTarget";
import { usePassiveNotifications } from "./hooks/usePassiveNotifications";
import { useSchedules } from "./hooks/useSchedules";
import { useSessionCrud } from "./hooks/useSessionCrud";
import { useVoiceInput } from "./hooks/useVoiceInput";
import type { VoiceDraftAnchor } from "./hooks/useVoiceInput";
import { hasPersistedResultMessage, MESSAGE_STREAM_STARTED_EVENT, messageStreamFailureDisposition } from "./lib/messageStream";
import { insertVoiceTranscript } from "./lib/voiceDraft";
import { applyAppearance } from "./lib/appearance";
import type {
  Approval,
  ArtifactObject,
  AuditEvent,
  Client,
  ConnectorStatus,
  EpisodeSummary,
  EvalRun,
  Memory,
  MemoryCandidate,
  Message,
  MessageAttachment,
  ModelCall,
  NotificationBinding,
  OwnerProfile,
  PublicConfig,
  ReadyStatus,
  RunTrace,
  Session,
  ToolCall,
  TraceMetadata
} from "./api/types";

export function App() {
  useEffect(() => applyAppearance(), []);
  const desktop = desktopCapability();
  const [language, setLanguage] = useState<Language>(() => initialLanguage());
  const text = dictionaries[language];
  const copy = workbenchCopy[language];
  const [page, setPage] = useState<WorkspacePage>("chat");
  const [inspectorOpen, setInspectorOpen] = useState(false);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const [scheduleCreateOpen, setScheduleCreateOpen] = useState(false);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [activeSession, setActiveSession] = useState<string>("");
  const [messages, setMessages] = useState<Message[]>([]);
  const [streamStatusesByMessage, setStreamStatusesByMessage] = useState<Record<string, StreamStatus[]>>({});
  const [toolCalls, setToolCalls] = useState<ToolCall[]>([]);
  const [modelCalls, setModelCalls] = useState<ModelCall[]>([]);
  const [auditEvents, setAuditEvents] = useState<AuditEvent[]>([]);
  const [episodes, setEpisodes] = useState<EpisodeSummary[]>([]);
  const [approvals, setApprovals] = useState<Approval[]>([]);
  const [candidates, setCandidates] = useState<MemoryCandidate[]>([]);
  const [memories, setMemories] = useState<Memory[]>([]);
  const [ready, setReady] = useState<ReadyStatus | null>(null);
  const [runtimeConfig, setRuntimeConfig] = useState<PublicConfig | null>(null);
  const [ownerProfile, setOwnerProfile] = useState<OwnerProfile | null>(null);
  const [clients, setClients] = useState<Client[]>([]);
  const [notificationBindings, setNotificationBindings] = useState<NotificationBinding[]>([]);
  const [connectors, setConnectors] = useState<ConnectorStatus[]>([]);
  const [evalRuns, setEvalRuns] = useState<EvalRun[]>([]);
  const [artifacts, setArtifacts] = useState<ArtifactObject[]>([]);
  const [traceRun, setTraceRun] = useState<RunTrace | null>(null);
  const [traceList, setTraceList] = useState<TraceMetadata[]>([]);
  const [traceLoading, setTraceLoading] = useState(false);
  const [draftsBySession, setDraftsBySession] = useState<Record<string, string>>({});
  const draftsBySessionRef = useRef(draftsBySession);
  draftsBySessionRef.current = draftsBySession;
  const [attachmentsBySession, setAttachmentsBySession] = useState<Record<string, MessageAttachment[]>>({});
  const [busy, setBusy] = useState(false);
  const [error, setErrorMessage] = useState("");
  const [notice, setNotice] = useState("");
  const [desktopConnectionState, setDesktopConnectionState] = useState("checking");
  const access = useWorkbenchAccess(!desktop || desktopConnectionState === "connected");
  const passiveNotifications = usePassiveNotifications(Boolean(access.identity));

  const setError = useCallback((message: string) => {
    setErrorMessage(message);
  }, []);

  const surfaceError = useCallback((err: unknown, fallback: string) => {
    const unauthorized = err instanceof APIError && err.status === 401;
    if (desktop && unauthorized) setDesktopConnectionState("invalid_authentication");
    if (unauthorized) access.invalidate(err);
    setErrorMessage(err instanceof Error && err.message ? err.message : fallback);
  }, [desktop, access.invalidate]);
  const [tab, setTab] = useState<PanelTab>("timeline");
  const composerInputRef = useRef<HTMLTextAreaElement | null>(null);
  const settingsReturnPageRef = useRef<WorkspacePage>("chat");
  const activeMessageStreamRef = useRef<string>("");
  const activeSessionRef = useRef(activeSession);
  activeSessionRef.current = activeSession;
  const sessionRefreshGenerationRef = useRef<Record<string, number>>({});
  const sessionListRefreshGenerationRef = useRef(0);
  const globalRefreshGenerationRef = useRef(0);

  useEffect(() => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, language);
    document.documentElement.lang = language === "zh" ? "zh-CN" : "en";
  }, [language]);

  function changeLanguage(next: Language) {
    setLanguage(next);
    if (!access.identity) return;
    void api.updateLanguage(next).then(setOwnerProfile).catch((err) => surfaceError(err, text.errors.connect));
  }

  useEffect(() => {
    if (access.error) surfaceError(access.error, text.errors.connect);
  }, [access.error, surfaceError, text.errors.connect]);

  const activeInput = activeSession ? draftsBySession[activeSession] ?? "" : "";
  const activeAttachments = activeSession ? attachmentsBySession[activeSession] ?? [] : [];

  const refreshSession = useCallback(async (sessionId: string) => {
    if (!sessionId) return;
    const generation = (sessionRefreshGenerationRef.current[sessionId] ?? 0) + 1;
    sessionRefreshGenerationRef.current[sessionId] = generation;
    const [messageList, callList, modelCallList, auditList, episodeList] = await Promise.all([
      api.messages(sessionId),
      api.toolCalls(sessionId),
      api.modelCalls(sessionId),
      api.audit(sessionId),
      api.episodes(sessionId)
    ]);
    if (sessionRefreshGenerationRef.current[sessionId] !== generation || activeSessionRef.current !== sessionId) return;
    if (activeMessageStreamRef.current !== sessionId) {
      setMessages(messageList.messages ?? []);
    }
    setToolCalls(callList.tool_calls ?? []);
    setModelCalls(modelCallList.model_calls ?? []);
    setAuditEvents(auditList.audit_events ?? []);
    setEpisodes(episodeList.episodes ?? []);
  }, []);

  useEffect(() => {
    if (!desktop) {
      setDesktopConnectionState("web");
      return;
    }
    let active = true;
    const unsubscribe = desktop.onLocalConnection((status) => {
      if (active) setDesktopConnectionState(status.state);
    });
    void desktop.localConnection().then((status) => {
      if (active) setDesktopConnectionState(status.state);
    }).catch(() => {
      if (active) setDesktopConnectionState("service_unavailable");
    });
    return () => { active = false; unsubscribe(); };
  }, [desktop]);

  const refreshSessionList = useCallback(async () => {
    const generation = sessionListRefreshGenerationRef.current + 1;
    sessionListRefreshGenerationRef.current = generation;
    const sessionList = await api.sessions();
    const nextSessions = sessionList.sessions ?? [];
    if (sessionListRefreshGenerationRef.current !== generation) return null;
    setSessions(nextSessions);
    const currentID = activeSessionRef.current;
    if (!currentID || !nextSessions.some((session) => session.id === currentID)) {
      const nextID = nextSessions[0]?.id ?? "";
      activeSessionRef.current = nextID;
      setActiveSession(nextID);
      if (nextID) {
        await refreshSession(nextID);
      } else {
        setMessages([]);
        setToolCalls([]);
        setModelCalls([]);
        setAuditEvents([]);
        setEpisodes([]);
      }
    }
    return nextSessions;
  }, [refreshSession]);

  const {
    schedules,
    setSchedules,
    scheduleBarOpen,
    setScheduleBarOpen,
    schedulesRefreshing,
    scheduleBusyId,
    refreshSchedules,
    editSchedule,
    deleteSchedule
  } = useSchedules({ activeSession, language, text, setError, surfaceError, refreshSession });

  const {
    activeTargetEndpointID,
    refreshDeliverySurface,
    clearSessionTarget
  } = useDeliveryTarget(activeSession);

  const refreshGlobal = useCallback(async () => {
    const generation = globalRefreshGenerationRef.current + 1;
    globalRefreshGenerationRef.current = generation;
    const configStatus = await api.config();
    if (globalRefreshGenerationRef.current !== generation) return;
    setRuntimeConfig(configStatus);
    const [readyStatus, owner, clientList, connectorList, bindingList, approvalList, candidateList, memoryList, evalList, artifactList, traces, scheduleList] =
      await Promise.allSettled([
        api.ready(),
        api.owner(),
        api.clients(),
        api.connectors(),
        api.notificationBindings(),
        api.approvals(),
        api.memoryCandidates(),
        api.memories(),
        api.evalRuns(),
        api.artifacts(),
        api.traces(),
        api.schedules()
      ]);
    if (globalRefreshGenerationRef.current !== generation) return;
    if (readyStatus.status === "fulfilled") setReady(readyStatus.value);
    if (owner.status === "fulfilled") setOwnerProfile(owner.value);
    if (clientList.status === "fulfilled") setClients(clientList.value.clients ?? []);
    if (connectorList.status === "fulfilled") setConnectors(connectorList.value.connectors ?? []);
    if (bindingList.status === "fulfilled") setNotificationBindings(bindingList.value.bindings ?? []);
    if (approvalList.status === "fulfilled") setApprovals(approvalList.value.approvals);
    if (candidateList.status === "fulfilled") setCandidates(candidateList.value.memory_candidates);
    if (memoryList.status === "fulfilled") setMemories(memoryList.value.memories);
    if (evalList.status === "fulfilled") setEvalRuns(evalList.value.eval_runs ?? []);
    if (artifactList.status === "fulfilled") setArtifacts(artifactList.value.artifacts ?? []);
    if (traces.status === "fulfilled") setTraceList(traces.value.traces ?? []);
    if (scheduleList.status === "fulfilled") setSchedules(scheduleList.value.schedules ?? []);
  }, []);

  const {
    editingSession,
    sessionTitleDraft,
    setSessionTitleDraft,
    sessionActionId,
    createSession,
    startRenameSession,
    cancelRenameSession,
    renameSession,
    deleteSession
  } = useSessionCrud({
    activeSession,
    text,
    setError,
    surfaceError,
    setSessions,
    setActiveSession,
    setMessages,
    setDraftsBySession,
    setAttachmentsBySession,
    setToolCalls,
    setModelCalls,
    setAuditEvents,
    setEpisodes,
    setTab,
    setTraceRun,
    clearSessionTarget,
    refreshSession,
    refreshGlobal
  });

  useEffect(() => {
    if (!access.identity) return;
    let cancelled = false;
    async function boot() {
      try {
        setError("");
        await Promise.all([refreshSessionList(), refreshGlobal(), refreshDeliverySurface()]);
        if (cancelled) return;
      } catch (err) {
        if (!cancelled) surfaceError(err, dictionaries[initialLanguage()].errors.connect);
      }
    }
    void boot();
    return () => {
      cancelled = true;
    };
  }, [access.identity, refreshDeliverySurface, refreshGlobal, refreshSessionList, setError, surfaceError]);

  useEffect(() => {
    if (!access.identity) return;
    let stopped = false;
    let retryTimer = 0;
    let flushTimer = 0;
    let controller: AbortController | null = null;
    const dirty = new Set<string>();

    const reconcile = async (full = false) => {
      if (document.visibilityState === "hidden" && !full) return;
      const categories = new Set(dirty);
      dirty.clear();
      const refreshEverything = full || categories.has("all") || categories.has("shared");
      const listChanged = refreshEverything || categories.has("sessions");
      const currentID = activeSessionRef.current;
      const tasksChanged = refreshEverything || categories.has("conversation") || categories.has("tasks") || categories.has("approvals") || categories.has("memories");
      const globalChanged = refreshEverything || categories.size === 0 || [...categories].some((category) => !["sessions", "conversation"].includes(category));
      const results = await Promise.allSettled([
        listChanged ? refreshSessionList() : Promise.resolve(),
        currentID && tasksChanged && activeMessageStreamRef.current !== currentID ? refreshSession(currentID) : Promise.resolve(),
        globalChanged ? refreshGlobal() : Promise.resolve(),
        globalChanged ? refreshDeliverySurface() : Promise.resolve()
      ]);
      const unauthorized = results.find((result) => result.status === "rejected" && result.reason instanceof APIError && result.reason.status === 401);
      if (unauthorized?.status === "rejected") surfaceError(unauthorized.reason, text.auth.unauthorized);
    };
    const queue = (category: string) => {
      dirty.add(category || "all");
      if (flushTimer) return;
      flushTimer = window.setTimeout(() => {
        flushTimer = 0;
        void reconcile(false);
      }, 80);
    };
    const subscribe = async () => {
      if (stopped) return;
      controller = new AbortController();
      try {
        await streamWorkbenchInvalidations(controller.signal, (event) => queue(event.reason === "resync" ? "all" : event.category));
      } catch (err) {
        if (controller.signal.aborted || stopped) return;
        if (err instanceof APIError && err.status === 401) {
          surfaceError(err, text.auth.unauthorized);
          return;
        }
      }
      if (!stopped) retryTimer = window.setTimeout(() => void subscribe(), 1000);
    };
    const foregroundReconcile = () => {
      if (document.visibilityState !== "hidden") void reconcile(true);
    };
    void subscribe();
    const poll = window.setInterval(() => void reconcile(true), 5000);
    window.addEventListener("focus", foregroundReconcile);
    window.addEventListener("online", foregroundReconcile);
    document.addEventListener("visibilitychange", foregroundReconcile);
    return () => {
      stopped = true;
      controller?.abort();
      window.clearInterval(poll);
      window.clearTimeout(retryTimer);
      window.clearTimeout(flushTimer);
      window.removeEventListener("focus", foregroundReconcile);
      window.removeEventListener("online", foregroundReconcile);
      document.removeEventListener("visibilitychange", foregroundReconcile);
    };
  }, [access.identity, refreshDeliverySurface, refreshGlobal, refreshSession, refreshSessionList, surfaceError, text.auth.unauthorized]);

  async function retryDesktopConnection() {
    if (!desktop) return;
    setDesktopConnectionState("reconnecting");
    const status = await desktop.retryLocalConnection().catch(() => ({ state: "service_unavailable" } as const));
    setDesktopConnectionState(status.state);
  }

  const pendingApprovals = useMemo(() => approvals.filter((approval) => approval.status === "pending"), [approvals]);
  const pendingCandidates = useMemo(() => candidates.filter((candidate) => candidate.status === "pending"), [candidates]);
  const active = sessions.find((session) => session.id === activeSession);
  const applyVoiceTranscript = useCallback((result: { text: string }, anchor: VoiceDraftAnchor) => {
    const currentDraft = draftsBySessionRef.current[anchor.sessionId] ?? "";
    if (currentDraft !== anchor.draft) return false;
    const start = Math.min(anchor.selectionStart, currentDraft.length);
    const end = Math.min(Math.max(anchor.selectionEnd, start), currentDraft.length);
    const inserted = insertVoiceTranscript(currentDraft, result.text, start, end);
    const nextDrafts = { ...draftsBySessionRef.current, [anchor.sessionId]: inserted.value };
    draftsBySessionRef.current = nextDrafts;
    setDraftsBySession(nextDrafts);
    if (anchor.sessionId === activeSession) {
      window.requestAnimationFrame(() => {
        composerInputRef.current?.focus();
        composerInputRef.current?.setSelectionRange(inserted.caret, inserted.caret);
      });
    }
    return true;
  }, [activeSession]);

  const voice = useVoiceInput({
    speech: ready?.speech ?? null,
    sessionId: activeSession,
    language: runtimeConfig?.speech.default_language ?? "auto",
    externallyDisabled: !access.identity || busy || !activeSession || active?.source === "mcp",
    onTranscript: applyVoiceTranscript
  });
  async function send(content = activeInput, sessionId = activeSession) {
    const trimmed = content.trim();
    const attachments = attachmentsBySession[sessionId] ?? [];
    const session = sessions.find((item) => item.id === sessionId);
    if (!sessionId || session?.source === "mcp" || (!trimmed && attachments.length === 0) || busy || voice.active) return;
    const userMessageId = `local-user-${Date.now()}`;
    const assistantMessageId = `local-assistant-${Date.now()}`;
    let streamAccepted = false;
    try {
      setBusy(true);
      setError("");
      setNotice("");
      setDraftsBySession((current) => ({ ...current, [sessionId]: "" }));
      activeMessageStreamRef.current = sessionId;
      const now = new Date().toISOString();
      setMessages((current) => [
        ...current,
        { id: userMessageId, session_id: sessionId, role: "user", content: trimmed, attachments, created_at: now },
        { id: assistantMessageId, session_id: sessionId, role: "assistant", content: "", created_at: now }
      ]);
      setAttachmentsBySession((current) => ({ ...current, [sessionId]: [] }));
      setStreamStatusesByMessage((current) => ({
        ...current,
        [assistantMessageId]: [{ id: "waiting", type: "waiting", text: text.chat.waiting }]
      }));
      let receivedDelta = false;
      await api.sendMessageStream(sessionId, trimmed, attachments, {
        targetEndpointId: sessionId === activeSession ? activeTargetEndpointID : "",
        onEvent: (event, data) => {
          if (event === MESSAGE_STREAM_STARTED_EVENT) {
            streamAccepted = true;
          }
          const status = streamStatusFromEvent(event, data, text);
          if (!status) return;
          setStreamStatusesByMessage((current) => ({
            ...current,
            [assistantMessageId]: upsertStreamStatus(current[assistantMessageId] ?? [], status)
          }));
        },
        onTextDelta: (delta) => {
          receivedDelta = true;
          setStreamStatusesByMessage((current) => {
            const next = { ...current };
            next[assistantMessageId] = (next[assistantMessageId] ?? []).filter((status) => status.id !== "waiting");
            return next;
          });
          setMessages((current) =>
            current.map((message) => (message.id === assistantMessageId ? { ...message, content: `${message.content}${delta}` } : message))
          );
        },
        onFinal: (result) => {
          if (!hasPersistedResultMessage(result.message)) {
            setMessages((current) => current.filter((message) => message.id !== assistantMessageId));
            setStreamStatusesByMessage((current) => {
              const next = { ...current };
              delete next[assistantMessageId];
              return next;
            });
            return;
          }
          setMessages((current) =>
            current.map((message) => {
              if (message.id !== assistantMessageId) return message;
              if (!receivedDelta || (result.message.attachments?.length ?? 0) > 0) return result.message;
              return message;
            })
          );
        },
        onError: (streamError) => {
          throw streamError;
        }
      });
      if (activeMessageStreamRef.current === sessionId) {
        activeMessageStreamRef.current = "";
      }
      const [sessionList] = await Promise.all([api.sessions(), refreshSession(sessionId), refreshGlobal()]);
      setSessions(sessionList.sessions ?? []);
      setAttachmentsBySession((current) => ({ ...current, [sessionId]: [] }));
    } catch (err) {
      setMessages((current) => current.filter((message) => message.id !== userMessageId && message.id !== assistantMessageId));
      setStreamStatusesByMessage((current) => {
        const next = { ...current };
        delete next[assistantMessageId];
        return next;
      });
      if (activeMessageStreamRef.current === sessionId) {
        activeMessageStreamRef.current = "";
      }
      const disposition = messageStreamFailureDisposition(streamAccepted, err);
      if (disposition === "delivery_failed") {
        // The run finished and its result is persisted server-side, but the
        // outbound delivery failed: surface the real delivery error and give
        // the draft back so the owner can retry the send.
        setDraftsBySession((current) => ({ ...current, [sessionId]: trimmed }));
        setAttachmentsBySession((current) => ({ ...current, [sessionId]: attachments }));
        setError(err instanceof Error && err.message ? `${text.errors.delivery}: ${err.message}` : text.errors.delivery);
      } else if (disposition === "restore_draft") {
        setDraftsBySession((current) => ({ ...current, [sessionId]: trimmed }));
        setAttachmentsBySession((current) => ({ ...current, [sessionId]: attachments }));
        surfaceError(err, text.errors.message);
      } else {
        // The gateway accepted the run and keeps executing it server-side;
        // losing the stream is not a failure, so surface an informational
        // notice instead of an error banner.
        setAttachmentsBySession((current) => ({ ...current, [sessionId]: [] }));
        setNotice(text.chat.streamDetached);
      }
      try {
        const [sessionList] = await Promise.all([api.sessions(), refreshSession(sessionId), refreshGlobal()]);
        setSessions(sessionList.sessions ?? []);
      } catch {
        // Best-effort recovery refresh; surface only the original stream error.
      }
    } finally {
      if (activeMessageStreamRef.current === sessionId) {
        activeMessageStreamRef.current = "";
      }
      setBusy(false);
    }
  }

  async function saveFeedback(message: Message, rating: "up" | "down" | "corrected", correction = "") {
    if (!message.run_id) return;
    try {
      setError("");
      await api.saveRunFeedback(message.run_id, message.id, rating, "", correction);
      await Promise.all([refreshSession(activeSession), refreshGlobal()]);
      if (traceRun?.run.id === message.run_id) {
        await openTrace(message.run_id);
      }
    } catch (err) {
      surfaceError(err, text.errors.feedback);
      throw err;
    }
  }

  async function openTrace(runId: string) {
    try {
      setTraceLoading(true);
      setError("");
      setTab("trace");
      setPage("settings");
      setInspectorOpen(false);
      const [trace, traces] = await Promise.all([api.trace(runId), api.traces()]);
      setTraceRun(trace);
      setTraceList(traces.traces ?? []);
    } catch (err) {
      surfaceError(err, text.errors.trace);
    } finally {
      setTraceLoading(false);
    }
  }

  async function submitToken(token: string) {
    if (!token) return;
    saveAPIToken(token);
    await reconnect();
  }

  async function reconnect(local = false) {
    try {
      setError("");
      await access.connect(local);
    } catch (err) {
      surfaceError(err, text.auth.unauthorized);
    }
  }

  function navigate(next: WorkspacePage) {
    if (window.matchMedia("(max-width: 700px)").matches) setSidebarCollapsed(false);
    if (next === "settings") {
      if (page !== "settings") settingsReturnPageRef.current = page;
      setTab("settings");
      setPage("settings");
      return;
    }
    setPage(next);
    if (next === "chat") setTab("timeline");
    if (next === "memory" || next === "approvals") setTab(next);
    if (next === "channels") setTab("settings");
    if (next === "schedules") { setScheduleBarOpen(true); void refreshSchedules(); }
  }

  function selectTask(session: Session) {
    if (window.matchMedia("(max-width: 700px)").matches) setSidebarCollapsed(false);
    setPage("chat");
    setInspectorOpen(false);
    activeSessionRef.current = session.id;
    setActiveSession(session.id);
    setTab("timeline");
    setSearchOpen(false);
    void refreshSession(session.id);
  }

  async function newTask(prompt = "") {
    if (!access.identity || busy || voice.active) return;
    const session = await createSession();
    if (!session) return;
    if (window.matchMedia("(max-width: 700px)").matches) setSidebarCollapsed(false);
    setPage("chat");
    setInspectorOpen(false);
    if (prompt) setDraftsBySession(current => ({ ...current, [session.id]: prompt }));
    window.requestAnimationFrame(() => composerInputRef.current?.focus());
  }

  async function createSchedule(request: string) {
    if (busy || voice.active) return null;
    try {
      setBusy(true);
      setError("");
      setNotice("");
      const result = await api.createSchedule(request);
      await refreshGlobal();
      const success = result.state === "completed";
      const message = result.message.trim() || (success ? text.schedules.createSuccess : text.schedules.createNeedsAttention);
      if (success) setNotice(message);
      return {
        success,
        message
      };
    } catch (err) {
      surfaceError(err, text.errors.message);
      return null;
    } finally {
      setBusy(false);
    }
  }

  const showHome = page === "chat" && messages.length === 0;
  const fullPanel = page !== "chat" && page !== "schedules" && page !== "settings";

  useEffect(() => {
    function shortcut(event: KeyboardEvent) {
      if (event.isComposing) return;
      if (event.key === "Escape") { setSearchOpen(false); setInspectorOpen(false); }
      if (!(event.metaKey || event.ctrlKey)) return;
      if (event.key.toLowerCase() === "k") { event.preventDefault(); setSearchOpen(true); }
      if (event.key.toLowerCase() === "n") { event.preventDefault(); void newTask(); }
    }
    window.addEventListener("keydown", shortcut);
    return () => window.removeEventListener("keydown", shortcut);
  }, [access.identity, busy, voice.active, createSession]);

  useEffect(() => {
    if (page !== "schedules") setScheduleCreateOpen(false);
  }, [page]);

  useEffect(() => {
    if (page !== "settings") return;
    window.requestAnimationFrame(() => {
      document.querySelector<HTMLElement>(".settingsPageMain")?.scrollTo({ top: 0, behavior: "auto" });
    });
  }, [page, tab]);

  useEffect(() => {
    if (!access.identity || page !== "settings" || !["models-tools", "permissions", "connections"].includes(tab)) return;
    let active = true;
    void api.config().then((config) => {
      if (active) setRuntimeConfig(config);
    }).catch((err) => {
      if (active) surfaceError(err, text.settings.unavailable);
    });
    if (tab === "connections") {
      void api.connectors().then((result) => {
        if (active) setConnectors(result.connectors ?? []);
      }).catch((err) => {
        if (active) surfaceError(err, text.errors.connectorUpdate);
      });
    }
    return () => { active = false; };
  }, [access.identity, page, surfaceError, tab, text.errors.connectorUpdate, text.settings.unavailable]);

  function renderInspectorColumn(connectionsOnly = false, showTabs = true) {
    return (
      <InspectorColumn
        key={connectionsOnly ? "channels" : showTabs ? "inspector" : "settings-page"}
        connectionsOnly={connectionsOnly}
        showTabs={showTabs}
        settingsPage={page === "settings"}
        tab={tab}
        onTabChange={setTab}
        text={text}
        language={language}
        pendingApprovalCount={pendingApprovals.length}
        pendingCandidateCount={pendingCandidates.length}
        toolCalls={toolCalls}
        approvals={approvals}
        candidates={candidates}
        memories={memories}
        traceRun={traceRun}
        traceList={traceList}
        traceLoading={traceLoading}
        ready={ready}
        modelCalls={modelCalls}
        auditEvents={auditEvents}
        artifacts={artifacts}
        episodes={episodes}
        evalRuns={evalRuns}
        runtimeConfig={runtimeConfig}
        ownerProfile={ownerProfile}
        clients={clients}
        currentClientID={access.identity?.client_id}
        accessMode={access.identity?.access_mode}
        onCurrentClientRevoked={async () => access.invalidate(new APIError(401, text.auth.unauthorized))}
        connectors={connectors}
        notificationBindings={notificationBindings}
        onOpenTrace={(runId) => void openTrace(runId)}
        setError={setError}
        surfaceError={surfaceError}
        refreshGlobal={refreshGlobal}
        refreshActiveSession={() => refreshSession(activeSession)}
        setEvalRuns={setEvalRuns}
        setNotificationBindings={setNotificationBindings}
        setConnectors={setConnectors}
        setRuntimeConfig={setRuntimeConfig}
        setOwnerProfile={setOwnerProfile}
        onLanguageChange={changeLanguage}
        onOpenSchedules={() => navigate("schedules")}
      />
    );
  }

  const settingsPageTitle = copy.settingsTitles[tab];
  const settingsPageDescription = tab === "memory" || tab === "approvals" ? "" : copy.settingsDescriptions[tab];

  if (!access.identity && !desktop) {
    return <main className="shell workbench accessGate gateway-offline">
      <section className="workspace homeWorkspace">
        <header className="topbar"><span className="workspaceLabel">{copy.local}</span></header>
        <WorkbenchAccess text={text} language={language} connecting={access.connecting} error={error}
          onToken={submitToken} onLocal={() => reconnect(true)} onRetry={() => reconnect()} />
        <WorkbenchWelcome language={language} />
      </section>
    </main>;
  }

  return (
    <main className={`shell workbench ${page === "settings" ? "settingsPageMode" : ""} ${sidebarCollapsed ? "sidebarCollapsed" : ""} ${ready?.ok ? "gateway-ready" : "gateway-offline"}`}>
      <div className="connectionBar" aria-hidden="true" />
      {desktop && desktopConnectionState !== "connected" ? <div className="desktopConnectionBanner" role="status">
        <span>{text.auth.desktopConnection[desktopConnectionState as keyof typeof text.auth.desktopConnection] ?? text.auth.desktopConnection.service_unavailable}</span>
        <button type="button" disabled={desktopConnectionState === "reconnecting" || desktopConnectionState === "checking"} onClick={() => void retryDesktopConnection()}>{text.auth.retryConnection}</button>
      </div> : null}
      {page !== "settings" && <SessionSidebar
        text={text}
        language={language}
        page={page}
        ownerProfile={ownerProfile}
        onNavigate={(next) => { if (next === "chat") { if (messages.length) void newTask(); else navigate(next); } else navigate(next); }}
        onSearch={() => setSearchOpen(true)}
        onToggleSidebar={() => setSidebarCollapsed(current => !current)}
        sessions={sessions}
        activeSession={activeSession}
        editingSession={editingSession}
        sessionTitleDraft={sessionTitleDraft}
        sessionActionId={sessionActionId}
        onCreateSession={() => void newTask()}
        onSelectSession={selectTask}
        onStartRename={startRenameSession}
        onCancelRename={cancelRenameSession}
        onRenameSubmit={(id) => void renameSession(id)}
        onTitleDraftChange={setSessionTitleDraft}
        onDeleteSession={(id) => void deleteSession(id)}
      />}

      {page !== "settings" && <section className={`workspace ${error ? "hasError" : ""} ${showHome ? "homeWorkspace" : ""} ${fullPanel ? "panelWorkspace" : ""} ${inspectorOpen && page === "chat" ? "withInspector" : ""} ${desktopCapability() ? "desktopWorkbench" : ""}`}>
        <header className="topbar">
          <button className="iconButton sidebarToggle" onClick={() => setSidebarCollapsed(current => !current)} aria-label={copy.toggleNav}><PanelLeft size={18} /></button>
          <span className="workspaceLabel">{access.identity?.access_mode === "local" ? text.auth.localAccess : copy.local}</span>
          <div className="topbarActions">
            <NotificationCenter
              notifications={passiveNotifications.notifications}
              unreadCount={passiveNotifications.unreadCount}
              open={passiveNotifications.open}
              toast={passiveNotifications.toast}
              error={passiveNotifications.error}
              language={language}
              text={text}
              onToggle={() => passiveNotifications.setOpen((current) => !current)}
              onDismissToast={passiveNotifications.dismissToast}
              onRead={passiveNotifications.markRead}
              onReadAll={passiveNotifications.markAllRead}
            />
            {page === "chat" && <button className={`iconButton rightSidebarToggle ${inspectorOpen ? "active" : ""}`} onClick={() => setInspectorOpen(current => !current)} aria-label={copy.toggleInspector} aria-expanded={inspectorOpen}><PanelRight size={18} /></button>}
          </div>
        </header>

        {error && (
          <div className="errorBanner">
            <span>{error}</span>
          </div>
        )}

        {notice && (
          <div className="noticeBanner" role="status">
            <span>{notice}</span>
            <button className="iconButton" onClick={() => setNotice("")} title={text.chat.dismissNotice} aria-label={text.chat.dismissNotice}>
              <X size={15} />
            </button>
          </div>
        )}

        {page === "schedules" && <div className="workbenchPage schedulePage"><div className="workbenchPageHeader"><div><h1>{copy.pageTitles.schedules}</h1><p>{copy.pageDescriptions.schedules}</p></div><button className="primaryButton" onClick={() => setScheduleCreateOpen(true)} disabled={busy || voice.active}><Plus size={15} />{copy.newSchedule}</button></div><ScheduleBar
          schedules={schedules}
          open={scheduleBarOpen}
          loading={schedulesRefreshing}
          busyId={scheduleBusyId}
          language={language}
          text={text}
          onToggle={() => setScheduleBarOpen((current) => !current)}
          onRefresh={() => void refreshSchedules()}
          onEdit={editSchedule}
          onDelete={deleteSchedule}
        />{scheduleCreateOpen ? <ScheduleCreateDialog
          busy={busy || voice.active}
          text={text}
          onClose={() => setScheduleCreateOpen(false)}
          onCreate={createSchedule}
        /> : null}</div>}

        {page === "chat" && <section className={`chatColumn ${showHome ? "homeChat" : ""}`}>
          <div className="messageList" aria-label={showHome ? text.chat.emptyTitle : undefined}>
            {messages.length === 0 ? (
              <WorkbenchWelcome language={language} />
            ) : (
              messages.map((message) => (
                <MessageBubble
                  key={message.id}
                  message={message}
                  streamStatuses={streamStatusesByMessage[message.id] ?? []}
                  text={text}
                  language={language}
                  sessionSource={active?.source}
                  onFeedback={(rating, correction) => saveFeedback(message, rating, correction)}
                />
              ))
            )}
          </div>
          {active?.source !== "mcp" && (
            <ComposerDock
              text={text}
              language={language}
              activeSession={activeSession}
              activeInput={activeInput}
              activeAttachments={activeAttachments}
              busy={busy}
              voice={voice}
              composerInputRef={composerInputRef}
              setDraftsBySession={setDraftsBySession}
              setAttachmentsBySession={setAttachmentsBySession}
              setError={setError}
              refreshGlobal={refreshGlobal}
              onSend={() => void send()}
            />
          )}
        </section>}
      {page === "chat" && inspectorOpen && (desktopCapability()
        ? <BrowserPanel language={language} />
        : <aside className="taskInspector emptyTaskInspector" aria-label={copy.inspector} />)}
      {fullPanel && <div className="workbenchPage">
      <div className="workbenchPageHeader"><div><h1>{copy.pageTitles[page as Exclude<WorkspacePage, "chat" | "schedules">]}</h1><p>{copy.pageDescriptions[page as Exclude<WorkspacePage, "chat" | "schedules">]}</p></div>{page === "memory" && <button className="primaryButton" onClick={() => void newTask(copy.memoryPrompt)}><Plus size={15} />{copy.addMemory}</button>}</div>
      {renderInspectorColumn(page === "channels")}</div>}
      </section>}
      {page === "settings" && <>
        <WorkspaceSettingsSidebar
          text={text}
          language={language}
          tab={tab}
          pendingApprovalCount={0}
          pendingCandidateCount={0}
          onTabChange={setTab}
          onBack={() => navigate(settingsReturnPageRef.current === "settings" ? "chat" : settingsReturnPageRef.current)}
        />
        <section className="settingsPageMain">
          <div className="settingsPageContent">
            <header className="settingsPageHeader">
              <h1>{settingsPageTitle}</h1>
              {settingsPageDescription && <p>{settingsPageDescription}</p>}
            </header>
            {renderInspectorColumn(false, false)}
          </div>
        </section>
      </>}
      {searchOpen && <TaskSearch language={language} sessions={sessions} onSelect={selectTask} onClose={() => setSearchOpen(false)} />}
    </main>
  );
}
