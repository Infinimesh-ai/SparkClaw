import { Fragment, FormEvent, KeyboardEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import {
  Activity,
  Bot,
  CalendarDays,
  Check,
  CheckCircle2,
  Clock3,
  Copy,
  Database,
  Download,
  FileSearch,
  Globe2,
  Inbox,
  KeyRound,
  Library,
  ListChecks,
  Mail,
  RefreshCw,
  Send,
  ThumbsDown,
  ThumbsUp,
  Upload,
  UserRound,
  X
} from "lucide-react";
import { api, apiToken, clearAPIToken, openDocumentFile, saveAPIToken, sessionEventsURL } from "./api/client";
import { dictionaries, initialLanguage, LANGUAGE_STORAGE_KEY } from "./i18n";
import type { Copy as CopyText, Language } from "./i18n";
import {
  attachmentOnlyPrompt,
  isImageAttachment,
  isImageContentType,
  MessageBubble,
  WorkspaceFileImage,
  streamStatusFromEvent,
  upsertStreamStatus
} from "./components/messages";
import type { StreamStatus } from "./components/messages";
import { InspectorColumn } from "./components/inspector";
import type { PanelTab } from "./components/inspector";
import {
  DeliveryReceiptSummary,
  DeliveryReviewDialog,
  ExternalPartTray
} from "./components/delivery";
import { VoiceInputButton, VoiceInputStatus } from "./components/VoiceInputButton";
import { ScheduleBar } from "./components/schedules";
import { SessionSidebar } from "./components/sidebar";
import { useExternalDelivery } from "./hooks/useExternalDelivery";
import { useSchedules } from "./hooks/useSchedules";
import { useVoiceInput } from "./hooks/useVoiceInput";
import type { VoiceDraftAnchor, VoiceInputState } from "./hooks/useVoiceInput";
import {
  fileKindLabel,
  fileNameFromPath,
  formatBytes,
  formatDateTime,
  loadDocumentUsage,
  saveDocumentUsage,
  sortDocumentsByUsage,
  sortNotificationBindings,
  isVisibleNotificationBinding
} from "./lib/format";
import type { DocumentUsage } from "./lib/format";
import { insertVoiceTranscript } from "./lib/voiceDraft";
import {
  deliveryPartIDFromAttachment,
  deliveryPartFromAttachment,
  moveDeliveryPart
} from "./lib/deliveryDraft";
import type {
  Approval,
  ArtifactObject,
  AuditEvent,
  Client,
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
  SessionEvent,
  Session,
  Skill,
  ToolCall,
  TraceMetadata
} from "./api/types";

export function App() {
  const [language, setLanguage] = useState<Language>(() => initialLanguage());
  const text = dictionaries[language];
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
  const [evalRuns, setEvalRuns] = useState<EvalRun[]>([]);
  const [artifacts, setArtifacts] = useState<ArtifactObject[]>([]);
  const [traceRun, setTraceRun] = useState<RunTrace | null>(null);
  const [traceList, setTraceList] = useState<TraceMetadata[]>([]);
  const [traceLoading, setTraceLoading] = useState(false);
  const [skills, setSkills] = useState<Skill[]>([]);
  const [availableDocuments, setAvailableDocuments] = useState<ArtifactObject[]>([]);
  const [choosingDocument, setChoosingDocument] = useState(false);
  const [documentPickerOpen, setDocumentPickerOpen] = useState(false);
  const [documentUsage, setDocumentUsage] = useState<Record<string, DocumentUsage>>(() => loadDocumentUsage());
  const [draftsBySession, setDraftsBySession] = useState<Record<string, string>>({});
  const [attachmentsBySession, setAttachmentsBySession] = useState<Record<string, MessageAttachment[]>>({});
  const [isComposingInput, setIsComposingInput] = useState(false);
  const [compositionEndedAt, setCompositionEndedAt] = useState(0);
  const [busy, setBusy] = useState(false);
  const [uploadingDocument, setUploadingDocument] = useState(false);
  const [pairing, setPairing] = useState(false);
  const [tokenInput, setTokenInput] = useState("");
  const [error, setError] = useState("");
  const [tab, setTab] = useState<PanelTab>("timeline");
  const [editingSession, setEditingSession] = useState("");
  const [sessionTitleDraft, setSessionTitleDraft] = useState("");
  const [sessionActionId, setSessionActionId] = useState("");
  const uploadInputRef = useRef<HTMLInputElement | null>(null);
  const composerInputRef = useRef<HTMLTextAreaElement | null>(null);
  const activeMessageStreamRef = useRef<string>("");

  useEffect(() => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, language);
    document.documentElement.lang = language === "zh" ? "zh-CN" : "en";
  }, [language]);

  const activeInput = activeSession ? draftsBySession[activeSession] ?? "" : "";
  const activeAttachments = activeSession ? attachmentsBySession[activeSession] ?? [] : [];

  const refreshSession = useCallback(async (sessionId: string) => {
    if (!sessionId) return;
    const [messageList, callList, modelCallList, auditList, episodeList] = await Promise.all([
      api.messages(sessionId),
      api.toolCalls(sessionId),
      api.modelCalls(sessionId),
      api.audit(sessionId),
      api.episodes(sessionId)
    ]);
    if (activeMessageStreamRef.current !== sessionId) {
      setMessages(messageList.messages ?? []);
    }
    setToolCalls(callList.tool_calls ?? []);
    setModelCalls(modelCallList.model_calls ?? []);
    setAuditEvents(auditList.audit_events ?? []);
    setEpisodes(episodeList.episodes ?? []);
  }, []);

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
  } = useSchedules({ activeSession, language, text, setError, refreshSession });

  const {
    deliveryBusy,
    deliveryReviewOpen,
    setDeliveryReviewOpen,
    deliverySoftwareOptions,
    activeDeliveryCandidates,
    activeDeliveryEndpoint,
    activeExternalDraft,
    externalDeliveryIntent,
    activeDeliveryValidation,
    activeLastDelivery,
    refreshDeliverySurface,
    updateExternalDraft,
    chooseDeliverySoftware,
    selectDeliveryTarget,
    updateExternalPart,
    removeExternalPart,
    openDeliveryReview,
    confirmExternalDelivery,
    retryExternalDelivery,
    resetSessionDraft,
    clearSessionState
  } = useExternalDelivery({
    activeSession,
    activeInput,
    activeAttachments,
    setDraftsBySession,
    setAttachmentsBySession,
    setError,
    deliveryErrorFallback: text.errors.message
  });

  const refreshGlobal = useCallback(async () => {
    const [readyStatus, configStatus, owner, clientList, bindingList, approvalList, candidateList, memoryList, skillList, evalList, artifactList, traces, scheduleList] =
      await Promise.all([
        api.ready(),
        api.config(),
        api.owner(),
        api.clients(),
        api.notificationBindings(),
        api.approvals(),
        api.memoryCandidates(),
        api.memories(),
        api.skills(),
        api.evalRuns(),
        api.artifacts(),
        api.traces(),
        api.schedules()
      ]);
    setReady(readyStatus);
    setRuntimeConfig(configStatus);
    setOwnerProfile(owner);
    setClients(clientList.clients ?? []);
    setNotificationBindings(bindingList.bindings ?? []);
    setApprovals(approvalList.approvals);
    setCandidates(candidateList.memory_candidates);
    setMemories(memoryList.memories);
    setSkills(skillList.skills);
    setEvalRuns(evalList.eval_runs ?? []);
    setArtifacts(artifactList.artifacts ?? []);
    setTraceList(traces.traces ?? []);
    setSchedules(scheduleList.schedules ?? []);
  }, []);

  useEffect(() => {
    let cancelled = false;
    async function boot() {
      try {
        setError("");
        const [sessionList] = await Promise.all([api.sessions(), refreshGlobal(), refreshDeliverySurface()]);
        if (cancelled) return;
        let next = sessionList.sessions[0];
        if (!next) {
          next = await api.createSession();
        }
        setSessions(next ? [next, ...sessionList.sessions.filter((session) => session.id !== next.id)] : sessionList.sessions);
        setActiveSession(next.id);
        await refreshSession(next.id);
      } catch (err) {
        setError(err instanceof Error ? err.message : dictionaries[initialLanguage()].errors.connect);
      }
    }
    void boot();
    return () => {
      cancelled = true;
    };
  }, [refreshDeliverySurface, refreshGlobal, refreshSession]);

  useEffect(() => {
    if (!activeSession) return;
    let refreshQueued = false;
    const refreshFromEvent = () => {
      if (refreshQueued) return;
      refreshQueued = true;
      window.setTimeout(() => {
        refreshQueued = false;
        void refreshSession(activeSession);
        void refreshGlobal();
        void refreshDeliverySurface();
      }, 80);
    };
    let events: EventSource | null = null;
    if (!apiToken() && "EventSource" in window) {
      events = new EventSource(sessionEventsURL(activeSession));
      events.onmessage = refreshFromEvent;
      events.addEventListener("message.created", refreshFromEvent);
      events.addEventListener("tool_call.completed", refreshFromEvent);
      events.addEventListener("tool_call.approval_pending", refreshFromEvent);
      events.addEventListener("tool_call.completed_after_approval", refreshFromEvent);
      events.addEventListener("approval.pending", refreshFromEvent);
      events.addEventListener("approval.approved", refreshFromEvent);
      events.addEventListener("approval.rejected", refreshFromEvent);
      events.addEventListener("memory_candidate.created", refreshFromEvent);
      events.addEventListener("memory.updated", refreshFromEvent);
      events.addEventListener("memory.deleted", refreshFromEvent);
      events.addEventListener("episode_summary.saved", refreshFromEvent);
    }
    const id = window.setInterval(() => {
      if (activeMessageStreamRef.current !== activeSession) {
        void refreshSession(activeSession);
        void refreshGlobal();
        void refreshDeliverySurface();
      }
    }, 5000);
    return () => {
      window.clearInterval(id);
      events?.close();
    };
  }, [activeSession, refreshDeliverySurface, refreshGlobal, refreshSession]);

  const pendingApprovals = useMemo(() => approvals.filter((approval) => approval.status === "pending"), [approvals]);
  const pendingCandidates = useMemo(() => candidates.filter((candidate) => candidate.status === "pending"), [candidates]);
  const weixinBindings = useMemo(
    () => sortNotificationBindings(notificationBindings.filter((binding) => binding.channel === "weixin" && isVisibleNotificationBinding(binding.status))),
    [notificationBindings]
  );
  const telegramBindings = useMemo(
    () => sortNotificationBindings(notificationBindings.filter((binding) => binding.channel === "telegram" && isVisibleNotificationBinding(binding.status))),
    [notificationBindings]
  );
  const active = sessions.find((session) => session.id === activeSession);
  const sortedAvailableDocuments = useMemo(
    () => sortDocumentsByUsage(availableDocuments, documentUsage),
    [availableDocuments, documentUsage]
  );
  const applyVoiceTranscript = useCallback((result: { text: string }, anchor: VoiceDraftAnchor) => {
    let nextCaret = 0;
    setDraftsBySession((current) => {
      const currentDraft = current[anchor.sessionId] ?? "";
      const selectionStillValid = currentDraft === anchor.draft;
      const start = selectionStillValid ? Math.min(anchor.selectionStart, currentDraft.length) : currentDraft.length;
      const end = selectionStillValid ? Math.min(Math.max(anchor.selectionEnd, start), currentDraft.length) : currentDraft.length;
      const inserted = insertVoiceTranscript(currentDraft, result.text, start, end);
      nextCaret = inserted.caret;
      return { ...current, [anchor.sessionId]: inserted.value };
    });
    if (anchor.sessionId === activeSession) {
      window.requestAnimationFrame(() => {
        composerInputRef.current?.focus();
        composerInputRef.current?.setSelectionRange(nextCaret, nextCaret);
      });
    }
  }, [activeSession]);

  const voice = useVoiceInput({
    speech: ready?.speech ?? null,
    sessionId: activeSession,
    language: runtimeConfig?.speech.default_language ?? "auto",
    externallyDisabled: busy || deliveryBusy || externalDeliveryIntent || !activeSession,
    onTranscript: applyVoiceTranscript
  });
  const voiceLabel = voiceInputLabel(voice.state, voice.errorCode, voice.errorDetail, text);
  const voiceTitle = voiceInputTitle(voice.state, voiceLabel, text);

  async function createSession() {
    try {
      setError("");
      const session = await api.createSession();
      setSessions((current) => [session, ...current]);
      setActiveSession(session.id);
      setMessages([]);
      setAttachmentsBySession((current) => ({ ...current, [session.id]: [] }));
      resetSessionDraft(session.id);
      setToolCalls([]);
      setModelCalls([]);
      setAuditEvents([]);
      setEpisodes([]);
      setTab("timeline");
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.createSession);
    }
  }

  async function send(content = activeInput, sessionId = activeSession) {
    const trimmed = content.trim();
    const attachments = attachmentsBySession[sessionId] ?? [];
    if (!sessionId || (!trimmed && attachments.length === 0) || busy || voice.active) return;
    const userMessageId = `local-user-${Date.now()}`;
    const assistantMessageId = `local-assistant-${Date.now()}`;
    try {
      setBusy(true);
      setError("");
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
      await api.sendMessageStream(sessionId, trimmed || attachmentOnlyPrompt(language), attachments, {
        onEvent: (event, data) => {
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
      resetSessionDraft(sessionId);
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
      try {
        await api.sendMessage(sessionId, trimmed || attachmentOnlyPrompt(language), attachments);
        const [sessionList] = await Promise.all([api.sessions(), refreshSession(sessionId), refreshGlobal()]);
        setSessions(sessionList.sessions ?? []);
        setAttachmentsBySession((current) => ({ ...current, [sessionId]: [] }));
        resetSessionDraft(sessionId);
      } catch (fallbackErr) {
        setAttachmentsBySession((current) => ({ ...current, [sessionId]: attachments }));
        setError(fallbackErr instanceof Error ? fallbackErr.message : err instanceof Error ? err.message : text.errors.message);
      }
    } finally {
      if (activeMessageStreamRef.current === sessionId) {
        activeMessageStreamRef.current = "";
      }
      setBusy(false);
    }
  }

  function onSubmit(event: FormEvent) {
    event.preventDefault();
    if (isComposingInput || Date.now() - compositionEndedAt < 80) return;
    if (externalDeliveryIntent) {
      openDeliveryReview();
    } else {
      void send();
    }
  }

  function stageAttachment(attachment: MessageAttachment) {
    if (!activeSession) return;
    const partID = deliveryPartIDFromAttachment(attachment);
    setAttachmentsBySession((current) => {
      const existing = externalDeliveryIntent ? current[activeSession] ?? [] : [];
      return {
        ...current,
        [activeSession]: [...existing.filter((item) => deliveryPartIDFromAttachment(item) !== partID), attachment]
      };
    });
    updateExternalDraft((draft) => {
      const existing = externalDeliveryIntent ? draft.parts : [];
      return {
        ...draft,
        parts: [...existing.filter((part) => part.id !== partID), deliveryPartFromAttachment(partID, attachment)]
      };
    });
  }

  async function uploadDocument(file: File | null) {
    if (!file || !activeSession || uploadingDocument) return;
    try {
      setUploadingDocument(true);
      setError("");
      const result = await api.uploadDocument(activeSession, file);
      const attachment: MessageAttachment = {
        artifact_id: result.artifact?.id,
        name: file.name,
        rel_path: result.rel_path || result.artifact?.key || file.name,
        uri: result.artifact?.uri,
        content_type: result.artifact?.content_type || file.type,
        bytes: result.bytes || result.artifact?.bytes,
        width: result.media?.width,
        height: result.media?.height,
        sha256: result.media?.sha256,
        source: isImageContentType(result.artifact?.content_type || file.type) ? "web_upload" : undefined
      };
      stageAttachment(attachment);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.upload);
    } finally {
      setUploadingDocument(false);
      if (uploadInputRef.current) {
        uploadInputRef.current.value = "";
      }
    }
  }

  async function openDocumentPicker() {
    if (!activeSession || choosingDocument) return;
    try {
      setChoosingDocument(true);
      setError("");
      const result = await api.availableDocuments(activeSession);
      const documents = result.documents ?? [];
      setAvailableDocuments(documents);
      setDocumentPickerOpen(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.upload);
    } finally {
      setChoosingDocument(false);
    }
  }

  function chooseAvailableDocument(document: ArtifactObject) {
    if (!activeSession) return;
    const attachment: MessageAttachment = {
      artifact_id: document.id,
      name: fileNameFromPath(document.key),
      rel_path: document.key,
      uri: document.uri,
      content_type: document.content_type,
      bytes: document.bytes
    };
    stageAttachment(attachment);
    setDocumentUsage((current) => {
      const previous = current[document.key] ?? { count: 0, last_used_at: "" };
      const next = {
        ...current,
        [document.key]: { count: previous.count + 1, last_used_at: new Date().toISOString() }
      };
      saveDocumentUsage(next);
      return next;
    });
    setDocumentPickerOpen(false);
  }

  function removeAttachment(sessionId: string, attachment: MessageAttachment) {
    if (!sessionId) return;
    const partID = deliveryPartIDFromAttachment(attachment);
    setAttachmentsBySession((current) => ({
      ...current,
      [sessionId]: (current[sessionId] ?? []).filter((item) => item !== attachment)
    }));
    if (sessionId === activeSession) {
      updateExternalDraft((draft) => ({ ...draft, parts: draft.parts.filter((part) => part.id !== partID) }));
    }
  }

  function startRenameSession(session: Session) {
    setEditingSession(session.id);
    setSessionTitleDraft(session.title);
  }

  function cancelRenameSession() {
    setEditingSession("");
    setSessionTitleDraft("");
  }

  async function renameSession(id: string) {
    const title = sessionTitleDraft.trim();
    if (!title || sessionActionId) return;
    try {
      setSessionActionId(id);
      setError("");
      const updated = await api.updateSession(id, title);
      setSessions((current) => current.map((session) => (session.id === id ? updated : session)));
      cancelRenameSession();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.renameSession);
    } finally {
      setSessionActionId("");
    }
  }

  async function deleteSession(id: string) {
    if (sessionActionId || !window.confirm(text.nav.confirmDeleteSession)) return;
    try {
      setSessionActionId(id);
      setError("");
      await api.deleteSession(id);
      const sessionList = await api.sessions();
      let next = id === activeSession ? sessionList.sessions[0] : sessionList.sessions.find((session) => session.id === activeSession);
      if (!next) next = await api.createSession();
      setDraftsBySession((current) => {
        const nextDrafts = { ...current };
        delete nextDrafts[id];
        return nextDrafts;
      });
      setAttachmentsBySession((current) => omitSession(current, id));
      clearSessionState(id);
      setSessions(next ? [next, ...sessionList.sessions.filter((session) => session.id !== next.id)] : sessionList.sessions);
      setActiveSession(next.id);
      cancelRenameSession();
      setTraceRun(null);
      setTab("timeline");
      await Promise.all([refreshSession(next.id), refreshGlobal()]);
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.deleteSession);
    } finally {
      setSessionActionId("");
    }
  }

  function onComposerKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key !== "Enter" || event.shiftKey) return;
    if (isComposingInput || event.nativeEvent.isComposing || Date.now() - compositionEndedAt < 80) {
      return;
    }
    event.preventDefault();
    if (externalDeliveryIntent) {
      openDeliveryReview();
    } else {
      void send();
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
      setError(err instanceof Error ? err.message : text.errors.feedback);
      throw err;
    }
  }

  async function openTrace(runId: string) {
    try {
      setTraceLoading(true);
      setError("");
      setTab("trace");
      const [trace, traces] = await Promise.all([api.trace(runId), api.traces()]);
      setTraceRun(trace);
      setTraceList(traces.traces ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.trace);
    } finally {
      setTraceLoading(false);
    }
  }

  async function pairClient() {
    try {
      setPairing(true);
      setError("");
      const started = await api.startPairing();
      const claimed = await api.claimPairing(started.pairing_id, started.code, "WebChat");
      saveAPIToken(claimed.token);
      await bootstrappedRefresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.pairing);
    } finally {
      setPairing(false);
    }
  }

  async function submitToken(event: FormEvent) {
    event.preventDefault();
    const token = tokenInput.trim();
    if (!token) return;
    try {
      setError("");
      saveAPIToken(token);
      await bootstrappedRefresh();
      setTokenInput("");
    } catch (err) {
      clearAPIToken();
      setError(err instanceof Error ? err.message : text.auth.unauthorized);
    }
  }

  async function bootstrappedRefresh() {
    const [sessionList] = await Promise.all([api.sessions(), refreshGlobal(), refreshDeliverySurface()]);
    let next = sessionList.sessions[0];
    if (!next) next = await api.createSession();
    setSessions(next ? [next, ...sessionList.sessions.filter((session) => session.id !== next.id)] : sessionList.sessions);
    setActiveSession(next.id);
    await refreshSession(next.id);
  }

  return (
    <main className={`shell ${ready?.ok ? "gateway-ready" : "gateway-offline"}`}>
      <div className="connectionBar" aria-hidden="true" />
      <SessionSidebar
        text={text}
        language={language}
        ready={ready}
        sessions={sessions}
        activeSession={activeSession}
        pendingApprovalCount={pendingApprovals.length}
        pendingCandidateCount={pendingCandidates.length}
        editingSession={editingSession}
        sessionTitleDraft={sessionTitleDraft}
        sessionActionId={sessionActionId}
        onLanguageChange={setLanguage}
        onCreateSession={() => void createSession()}
        onSelectSession={(session) => {
          setActiveSession(session.id);
          setDeliveryReviewOpen(false);
          setTab("timeline");
          void refreshSession(session.id);
        }}
        onStartRename={startRenameSession}
        onCancelRename={cancelRenameSession}
        onRenameSubmit={(id) => void renameSession(id)}
        onTitleDraftChange={setSessionTitleDraft}
        onDeleteSession={(id) => void deleteSession(id)}
      />

      <section className={`workspace ${error ? "hasError" : ""}`}>
        <header className="topbar">
          <div>
            <h1>{active?.title ?? text.app.titleFallback}</h1>
            <p>{ready ? `${ready.model_mode} ${text.topbar.modelMode} · ${ready.workspace_root}` : text.topbar.connecting}</p>
          </div>
          <div className="topbarActions">
            <span className="statusChip">
              <Bot size={14} />
              {ready?.ok ? text.nav.ready : text.nav.offline}
            </span>
            <button className="iconButton" onClick={() => void Promise.all([refreshGlobal(), refreshDeliverySurface(), refreshSession(activeSession)])} title={text.common.refresh}>
              <RefreshCw size={18} />
            </button>
          </div>
        </header>

        {error && (
          <div className="errorBanner">
            <span>{error}</span>
            {error.toLowerCase().includes("token") || error.toLowerCase().includes("unauthorized") ? (
              <div className="authActions">
                <form className="tokenForm" onSubmit={(event) => void submitToken(event)}>
                  <input
                    aria-label={text.auth.gatewayToken}
                    value={tokenInput}
                    onChange={(event) => setTokenInput(event.target.value)}
                    placeholder={text.auth.gatewayToken}
                    type="password"
                  />
                  <button type="submit" disabled={!tokenInput.trim()} title={text.common.saveToken}>
                    <KeyRound size={15} />
                  </button>
                </form>
                <button className="dangerButton" onClick={() => void pairClient()} disabled={pairing}>
                  <KeyRound size={15} />
                  <span>{pairing ? text.common.pairing : text.common.pair}</span>
                </button>
              </div>
            ) : null}
          </div>
        )}

        <ScheduleBar
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
        />

        <section className="chatColumn">
          <div className="messageList">
            {messages.length === 0 ? (
              <div className="emptyState">
                <Activity size={25} />
                <span>{text.chat.emptyTitle}</span>
              </div>
            ) : (
              messages.map((message) => (
                <MessageBubble
                  key={message.id}
                  message={message}
                  streamStatuses={streamStatusesByMessage[message.id] ?? []}
                  text={text}
                  language={language}
                  onFeedback={(rating, correction) => saveFeedback(message, rating, correction)}
                />
              ))
            )}
          </div>
          <div className="composerDock">
            <DeliveryReceiptSummary
              delivery={activeLastDelivery}
              retrying={deliveryBusy}
              text={text}
              onRetry={() => void retryExternalDelivery()}
            />
            <div className="composerToolbar">
              <div className="deliveryTargetControl">
                <Send size={15} aria-hidden="true" />
                <div className="deliveryTargetSelectors">
                  <label>
                    <span>{text.chat.software}</span>
                    <select
                      aria-label={text.chat.software}
                      value={activeExternalDraft.software}
                      onChange={(event) => chooseDeliverySoftware(event.target.value)}
                      disabled={busy || deliveryBusy || voice.active || deliverySoftwareOptions.length === 0}
                    >
                      <option value="">{deliverySoftwareOptions.length === 0 ? text.chat.noDeliveryEndpoints : text.chat.chooseSoftware}</option>
                      {deliverySoftwareOptions.map((software) => (
                        <option key={software.value} value={software.value}>{software.label}</option>
                      ))}
                    </select>
                  </label>
                  <label>
                    <span>{text.chat.recipient}</span>
                    <select
                      aria-label={text.chat.recipient}
                      value={activeExternalDraft.endpointId}
                      onChange={(event) => selectDeliveryTarget(event.target.value)}
                      disabled={busy || deliveryBusy || voice.active || !externalDeliveryIntent}
                    >
                      <option value="">{text.chat.chooseRecipient}</option>
                      {activeDeliveryCandidates.map((endpoint) => (
                        <option key={endpoint.id} value={endpoint.id}>
                          {endpoint.recipient.display_name} · {endpoint.account_display_name}
                          {endpoint.conversation_label ? ` · ${endpoint.conversation_label}` : ""}
                        </option>
                      ))}
                    </select>
                  </label>
                </div>
              </div>
            </div>
            {!externalDeliveryIntent && activeAttachments.length > 0 && (
              <div className="attachmentTray">
                {activeAttachments.map((attachment) => (
                  <div className="attachmentChip" key={`${attachment.artifact_id ?? attachment.rel_path}-${attachment.rel_path}`}>
                    <button
                      type="button"
                      className={`attachmentOpen ${isImageAttachment(attachment) ? "image" : ""}`}
                      title={text.chat.openAttachment}
                      onClick={() => void openDocumentFile(attachment.rel_path, activeSession).catch(() => undefined)}
                    >
                      {isImageAttachment(attachment) ? (
                        <WorkspaceFileImage path={attachment.rel_path} sessionId={activeSession} alt={attachment.name || attachment.rel_path} />
                      ) : (
                        <FileSearch size={15} />
                      )}
                      <span>{attachment.name || attachment.rel_path}</span>
                    </button>
                    <button
                      type="button"
                      className="attachmentRemove"
                      title={text.chat.removeAttachment}
                      onClick={() => removeAttachment(activeSession, attachment)}
                    >
                      <X size={14} />
                    </button>
                  </div>
                ))}
              </div>
            )}
            {externalDeliveryIntent && (
              <ExternalPartTray
                parts={activeExternalDraft.parts}
                fallbackPartIds={activeDeliveryValidation.fallbackPartIds}
                supportsCaption={activeDeliveryEndpoint?.capabilities.supports_caption === true}
                text={text}
                onChange={updateExternalPart}
                onMove={(index, offset) => updateExternalDraft((draft) => ({ ...draft, parts: moveDeliveryPart(draft.parts, index, offset) }))}
                onRemove={removeExternalPart}
              />
            )}
            {externalDeliveryIntent && activeDeliveryValidation.error && (
              <span className="deliveryValidation">{deliveryValidationMessage(activeDeliveryValidation.error, text)}</span>
            )}
            <form className={`composer ${externalDeliveryIntent ? "external" : ""}`} onSubmit={onSubmit}>
              <input
                ref={uploadInputRef}
                className="documentUploadInput"
                type="file"
                accept={externalDeliveryIntent ? undefined : ".txt,.md,.csv,.pdf,.docx,.xlsx,.pptx,.png,.jpg,.jpeg,.gif,.webp,image/png,image/jpeg,image/gif,image/webp"}
                onChange={(event) => void uploadDocument(event.target.files?.[0] ?? null)}
              />
              <button
                className="uploadButton"
                type="button"
                disabled={busy || deliveryBusy || uploadingDocument || !activeSession}
                title={uploadingDocument ? text.chat.uploading : text.chat.upload}
                onClick={() => uploadInputRef.current?.click()}
              >
                <Upload size={18} />
              </button>
              <button
                className="uploadButton"
                type="button"
                disabled={busy || deliveryBusy || choosingDocument || !activeSession}
                title={choosingDocument ? text.chat.choosingFile : text.chat.chooseFile}
                onClick={() => void openDocumentPicker()}
              >
                <FileSearch size={18} />
              </button>
              {!externalDeliveryIntent && (
                <VoiceInputButton
                  state={voice.state}
                  disabled={voice.disabled}
                  title={voiceTitle}
                  onClick={() => {
                    const input = composerInputRef.current;
                    voice.toggle({
                      sessionId: activeSession,
                      draft: activeInput,
                      selectionStart: input?.selectionStart ?? activeInput.length,
                      selectionEnd: input?.selectionEnd ?? activeInput.length
                    });
                  }}
                />
              )}
              <textarea
                ref={composerInputRef}
                value={activeInput}
                onChange={(event) => {
                  if (!activeSession) return;
                  setDraftsBySession((current) => ({ ...current, [activeSession]: event.target.value }));
                }}
                onKeyDown={onComposerKeyDown}
                onCompositionStart={() => setIsComposingInput(true)}
                onCompositionEnd={() => {
                  setIsComposingInput(false);
                  setCompositionEndedAt(Date.now());
                }}
                placeholder={text.chat.placeholder}
                disabled={busy || deliveryBusy}
              />
              <button
                className="sendButton"
                disabled={
                  externalDeliveryIntent
                    ? deliveryBusy || !activeDeliveryValidation.valid
                    : busy || voice.active || (!activeInput.trim() && activeAttachments.length === 0)
                }
                title={externalDeliveryIntent ? text.chat.reviewSend : text.chat.send}
              >
                <Send size={18} />
              </button>
              {!externalDeliveryIntent && (
                <VoiceInputStatus state={voice.state} level={voice.level} elapsedMs={voice.elapsedMs} label={voiceLabel} />
              )}
            </form>
          </div>
          {documentPickerOpen && (
            <div className="documentPickerOverlay" role="dialog" aria-modal="true" aria-label={text.chat.chooseFile}>
              <div className="documentPicker">
                <div className="documentPickerHeader">
                  <strong>{text.chat.chooseFile}</strong>
                  <button type="button" className="attachmentRemove" onClick={() => setDocumentPickerOpen(false)} title={text.common.cancel}>
                    <X size={14} />
                  </button>
                </div>
                {sortedAvailableDocuments.length === 0 ? (
                  <span className="muted">{text.chat.noUploadedFiles}</span>
                ) : (
                  <div className="documentPickerList">
                    <div className="finderHeader">
                      <span>{text.chat.fileName}</span>
                      <span>{text.chat.fileUsage}</span>
                      <span>{text.chat.fileRecentUse}</span>
                      <span>{text.chat.fileSize}</span>
                      <span>{text.chat.fileKind}</span>
                    </div>
                    {sortedAvailableDocuments.map((document) => {
                      const usage = documentUsage[document.key];
                      return (
                        <button className="finderRow file" key={document.id} type="button" onClick={() => chooseAvailableDocument(document)}>
                          <span className="finderName fileName">
                            <FileSearch size={16} />
                            <strong>{fileNameFromPath(document.key)}</strong>
                          </span>
                          <span>{usage ? `${usage.count} ${text.chat.usedTimes}` : text.chat.neverUsed}</span>
                          <span>{usage ? formatDateTime(usage.last_used_at, language) : "--"}</span>
                          <span>{formatBytes(document.bytes)}</span>
                          <span>{fileKindLabel(document)}</span>
                        </button>
                      );
                    })}
                  </div>
                )}
              </div>
            </div>
          )}
          {deliveryReviewOpen && activeDeliveryEndpoint && activeDeliveryValidation.valid && (
            <DeliveryReviewDialog
              endpoint={activeDeliveryEndpoint}
              draft={activeExternalDraft}
              validation={activeDeliveryValidation}
              busy={deliveryBusy}
              text={text}
              onCancel={() => setDeliveryReviewOpen(false)}
              onConfirm={() => void confirmExternalDelivery()}
            />
          )}
        </section>
      </section>

      <InspectorColumn
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
        skills={skills}
        runtimeConfig={runtimeConfig}
        ownerProfile={ownerProfile}
        clients={clients}
        weixinBindings={weixinBindings}
        telegramBindings={telegramBindings}
        onOpenTrace={(runId) => void openTrace(runId)}
        setError={setError}
        refreshGlobal={refreshGlobal}
        refreshActiveSession={() => refreshSession(activeSession)}
        setEvalRuns={setEvalRuns}
        setNotificationBindings={setNotificationBindings}
        setRuntimeConfig={setRuntimeConfig}
        setOwnerProfile={setOwnerProfile}
      />
    </main>
  );
}

function voiceInputTitle(state: VoiceInputState, label: string, text: CopyText) {
  if (state === "recording") return text.chat.voiceStop;
  if (state === "encoding" || state === "transcribing") return text.chat.voiceCancel;
  if (state === "requesting_permission") return text.chat.voiceRequesting;
  if (state === "disabled") return label;
  return text.chat.voiceStart;
}

function voiceInputLabel(state: VoiceInputState, errorCode: string, errorDetail: string, text: CopyText) {
  if (state === "requesting_permission") return text.chat.voiceRequesting;
  if (state === "recording") return text.chat.voiceRecording;
  if (state === "encoding") return text.chat.voicePreparing;
  if (state === "transcribing") return text.chat.voiceTranscribing;
  switch (errorCode) {
    case "voice_capture_unsupported":
      return text.chat.voiceUnsupported;
    case "voice_permission_denied":
      return text.chat.voicePermissionDenied;
    case "voice_no_device":
      return text.chat.voiceNoDevice;
    case "voice_capture_failed":
      return text.chat.voiceCaptureFailed;
    case "speech_too_short":
      return text.chat.voiceTooShort;
    case "speech_no_speech":
      return text.chat.voiceNoSpeech;
    case "speech_too_large":
      return text.chat.voiceTooLarge;
    case "speech_busy":
      return text.chat.voiceBusy;
    case "speech_disabled":
    case "speech_model_unavailable":
      return state === "disabled" ? text.chat.voiceUnavailable : errorDetail || text.chat.voiceUnavailable;
    case "speech_timeout":
      return text.chat.voiceTimeout;
    case "speech_inference_failed":
      return errorDetail || text.chat.voiceFailed;
    default:
      return state === "error" ? errorDetail || text.chat.voiceFailed : text.chat.voiceUnavailable;
  }
}

function deliveryValidationMessage(error: string, text: CopyText) {
  switch (error) {
    case "recipient_required":
      return text.chat.recipientRequired;
    case "content_required":
      return text.chat.contentRequired;
    case "part_unsupported":
      return text.chat.partUnsupported;
    case "too_many_parts":
      return text.chat.tooManyParts;
    case "payload_too_large":
      return text.chat.payloadTooLarge;
    default:
      return "";
  }
}

function omitSession<T>(current: Record<string, T>, sessionId: string) {
  const next = { ...current };
  delete next[sessionId];
  return next;
}
