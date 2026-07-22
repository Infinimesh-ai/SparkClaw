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
  Gauge,
  Globe2,
  Inbox,
  KeyRound,
  Languages,
  Library,
  ListChecks,
  Mail,
  MessageSquare,
  MemoryStick,
  Pencil,
  Plus,
  RefreshCw,
  Save,
  ScrollText,
  Send,
  Settings,
  ShieldAlert,
  Terminal,
  ThumbsDown,
  ThumbsUp,
  Trash2,
  Upload,
  UserRound,
  X
} from "lucide-react";
import { APIError, api, apiToken, clearAPIToken, openDocumentFile, saveAPIToken, sessionEventsURL, workspaceScreenshotURL } from "./api/client";
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
import {
  ApprovalPanel,
  MemoryPanel,
  SettingsPanel,
  StatusStack,
  ToolTimelinePanel,
  TracePanel
} from "./components/panels";
import {
  DeliveryHistory,
  DeliveryReceiptSummary,
  DeliveryReviewDialog,
  ExternalPartTray
} from "./components/delivery";
import { VoiceInputButton, VoiceInputStatus } from "./components/VoiceInputButton";
import { ScheduleBar } from "./components/schedules";
import { useVoiceInput } from "./hooks/useVoiceInput";
import type { VoiceDraftAnchor, VoiceInputState } from "./hooks/useVoiceInput";
import {
  fileKindLabel,
  fileNameFromPath,
  formatBytes,
  formatDateTime,
  isBindingPending,
  loadDocumentUsage,
  saveDocumentUsage,
  shortId,
  sortDocumentsByUsage,
  sortNotificationBindings,
  isVisibleNotificationBinding
} from "./lib/format";
import type { DocumentUsage } from "./lib/format";
import { insertVoiceTranscript } from "./lib/voiceDraft";
import { notificationBindingErrorMessage } from "./lib/bindingError";
import {
  deliveryDraftParts,
  deliveryPartFromAttachment,
  emptyExternalDeliveryDraft,
  endpointsForSoftware,
  moveDeliveryPart,
  selectDeliverySoftware,
  validateDeliveryDraft
} from "./lib/deliveryDraft";
import type { ExternalDeliveryDraft } from "./lib/deliveryDraft";
import type {
  Approval,
  ArtifactObject,
  AuditEvent,
  Client,
  DeliveryEndpoint,
  DeliveryPart,
  EpisodeSummary,
  EvalRun,
  Memory,
  MemoryCandidate,
  Message,
  MessageAttachment,
  MessageDelivery,
  MessageHistoryItem,
  ModelCall,
  NotificationBinding,
  OwnerProfile,
  PublicConfig,
  ReadyStatus,
  RunTrace,
  Schedule,
  SessionEvent,
  Session,
  Skill,
  ToolCall,
  TraceMetadata
} from "./api/types";

type PanelTab = "timeline" | "approvals" | "memory" | "trace" | "status" | "settings";
type ComposerMode = "agent" | "external";


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
  const [evalRun, setEvalRun] = useState<EvalRun | null>(null);
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
  const [composerModesBySession, setComposerModesBySession] = useState<Record<string, ComposerMode>>({});
  const [externalDraftsBySession, setExternalDraftsBySession] = useState<Record<string, ExternalDeliveryDraft>>({});
  const [deliveryIdempotencyBySession, setDeliveryIdempotencyBySession] = useState<Record<string, string>>({});
  const [deliveryEndpoints, setDeliveryEndpoints] = useState<DeliveryEndpoint[]>([]);
  const [messageHistory, setMessageHistory] = useState<MessageHistoryItem[]>([]);
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [scheduleBarOpen, setScheduleBarOpen] = useState(true);
  const [schedulesRefreshing, setSchedulesRefreshing] = useState(false);
  const [lastDeliveriesBySession, setLastDeliveriesBySession] = useState<Record<string, MessageDelivery>>({});
  const [deliveryReviewOpen, setDeliveryReviewOpen] = useState(false);
  const [deliveryBusy, setDeliveryBusy] = useState(false);
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

  const refreshSchedules = useCallback(async () => {
    try {
      setSchedulesRefreshing(true);
      setError("");
      const result = await api.schedules();
      setSchedules(result.schedules ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.schedules);
    } finally {
      setSchedulesRefreshing(false);
    }
  }, [text.errors.schedules]);

  const refreshDeliverySurface = useCallback(async () => {
    const [endpointResult, historyResult] = await Promise.allSettled([
      api.deliveryEndpoints(),
      api.messageHistory()
    ]);
    if (endpointResult.status === "fulfilled") {
      setDeliveryEndpoints(endpointResult.value.endpoints ?? []);
    } else {
      setDeliveryEndpoints([]);
    }
    if (historyResult.status === "fulfilled") {
      setMessageHistory(historyResult.value.messages ?? []);
    } else {
      setMessageHistory([]);
    }
  }, []);

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
  const activeComposerMode = activeSession ? composerModesBySession[activeSession] ?? "agent" : "agent";
  const activeInput = activeSession ? draftsBySession[activeSession] ?? "" : "";
  const activeAttachments = activeSession ? attachmentsBySession[activeSession] ?? [] : [];
  const activeExternalDraft = activeSession
    ? externalDraftsBySession[activeSession] ?? emptyExternalDeliveryDraft()
    : emptyExternalDeliveryDraft();
  const deliverySoftware = useMemo(
    () => Array.from(new Map(deliveryEndpoints.map((endpoint) => [endpoint.channel, endpoint.software_display_name])).entries()),
    [deliveryEndpoints]
  );
  const activeDeliveryCandidates = useMemo(
    () => endpointsForSoftware(deliveryEndpoints, activeExternalDraft.software),
    [activeExternalDraft.software, deliveryEndpoints]
  );
  const activeDeliveryEndpoint = deliveryEndpoints.find((endpoint) => endpoint.id === activeExternalDraft.endpointId);
  const activeDeliveryValidation = validateDeliveryDraft(activeExternalDraft, activeDeliveryEndpoint);
  const activeLastDelivery = activeSession ? lastDeliveriesBySession[activeSession] ?? null : null;
  const sortedAvailableDocuments = useMemo(
    () => sortDocumentsByUsage(availableDocuments, documentUsage),
    [availableDocuments, documentUsage]
  );
  const languageLabel = language === "zh" ? "中" : "EN";
  const nextLanguage = language === "zh" ? "en" : "zh";

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
    externallyDisabled: busy || activeComposerMode !== "agent" || !activeSession,
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
      setExternalDraftsBySession((current) => ({ ...current, [session.id]: emptyExternalDeliveryDraft() }));
      setToolCalls([]);
      setModelCalls([]);
      setAuditEvents([]);
      setEpisodes([]);
      setTab("timeline");
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.createSession);
    }
  }

  function setComposerMode(mode: ComposerMode) {
    if (!activeSession) return;
    setComposerModesBySession((current) => ({ ...current, [activeSession]: mode }));
    setDeliveryReviewOpen(false);
    if (mode === "external") {
      void refreshDeliverySurface();
    }
  }

  function updateExternalDraft(update: (draft: ExternalDeliveryDraft) => ExternalDeliveryDraft) {
    if (!activeSession) return;
    setExternalDraftsBySession((current) => ({
      ...current,
      [activeSession]: update(current[activeSession] ?? emptyExternalDeliveryDraft())
    }));
    setDeliveryIdempotencyBySession((current) => {
      const next = { ...current };
      delete next[activeSession];
      return next;
    });
  }

  function updateExternalPart(id: string, update: Partial<DeliveryPart>) {
    updateExternalDraft((draft) => ({
      ...draft,
      parts: draft.parts.map((part) => (part.id === id ? { ...part, ...update } : part))
    }));
  }

  function openDeliveryReview() {
    if (!activeSession || !activeDeliveryValidation.valid || !activeDeliveryEndpoint) return;
    setDeliveryIdempotencyBySession((current) => ({
      ...current,
      [activeSession]: current[activeSession] || `web-${crypto.randomUUID()}`
    }));
    setDeliveryReviewOpen(true);
  }

  async function confirmExternalDelivery() {
    if (!activeSession || !activeDeliveryEndpoint || !activeDeliveryValidation.valid || deliveryBusy) return;
    const idempotencyKey = deliveryIdempotencyBySession[activeSession];
    if (!idempotencyKey) return;
    try {
      setDeliveryBusy(true);
      setError("");
      const delivery = await api.createDelivery(
        activeDeliveryEndpoint.id,
        idempotencyKey,
        deliveryDraftParts(activeExternalDraft)
      );
      setLastDeliveriesBySession((current) => ({ ...current, [activeSession]: delivery }));
      setExternalDraftsBySession((current) => ({
        ...current,
        [activeSession]: { ...activeExternalDraft, text: "", parts: [] }
      }));
      setDeliveryIdempotencyBySession((current) => {
        const next = { ...current };
        delete next[activeSession];
        return next;
      });
      setDeliveryReviewOpen(false);
      await refreshDeliverySurface();
    } catch (err) {
      const failed = err instanceof APIError && err.details && typeof err.details === "object"
        ? (err.details as { delivery?: MessageDelivery }).delivery
        : undefined;
      if (failed) {
        setLastDeliveriesBySession((current) => ({ ...current, [activeSession]: failed }));
        setDeliveryReviewOpen(false);
        await refreshDeliverySurface();
      }
      setError(err instanceof Error ? err.message : text.errors.message);
    } finally {
      setDeliveryBusy(false);
    }
  }

  async function retryExternalDelivery() {
    if (!activeSession || !activeLastDelivery || deliveryBusy) return;
    try {
      setDeliveryBusy(true);
      setError("");
      const delivery = await api.retryDelivery(activeLastDelivery.id);
      setLastDeliveriesBySession((current) => ({ ...current, [activeSession]: delivery }));
      if (delivery.status === "sent") {
        setExternalDraftsBySession((current) => ({
          ...current,
          [activeSession]: { ...(current[activeSession] ?? emptyExternalDeliveryDraft()), text: "", parts: [] }
        }));
        setDeliveryIdempotencyBySession((current) => omitSession(current, activeSession));
      }
      await refreshDeliverySurface();
    } catch (err) {
      const failed = err instanceof APIError && err.details && typeof err.details === "object"
        ? (err.details as { delivery?: MessageDelivery }).delivery
        : undefined;
      if (failed) setLastDeliveriesBySession((current) => ({ ...current, [activeSession]: failed }));
      setError(err instanceof Error ? err.message : text.errors.message);
      await refreshDeliverySurface();
    } finally {
      setDeliveryBusy(false);
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
    if (activeComposerMode === "external") {
      openDeliveryReview();
    } else {
      void send();
    }
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
      if (activeComposerMode === "external") {
        updateExternalDraft((draft) => ({
          ...draft,
          parts: [...draft.parts, deliveryPartFromAttachment(`part-${crypto.randomUUID()}`, attachment)]
        }));
      } else {
        setAttachmentsBySession((current) => ({ ...current, [activeSession]: [attachment] }));
      }
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
    if (activeComposerMode === "external") {
      updateExternalDraft((draft) => ({
        ...draft,
        parts: [...draft.parts, deliveryPartFromAttachment(`part-${crypto.randomUUID()}`, attachment)]
      }));
    } else {
      setAttachmentsBySession((current) => ({ ...current, [activeSession]: [attachment] }));
    }
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
    setAttachmentsBySession((current) => ({
      ...current,
      [sessionId]: (current[sessionId] ?? []).filter((item) => item !== attachment)
    }));
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
      setComposerModesBySession((current) => omitSession(current, id));
      setExternalDraftsBySession((current) => omitSession(current, id));
      setDeliveryIdempotencyBySession((current) => omitSession(current, id));
      setLastDeliveriesBySession((current) => omitSession(current, id));
      setDeliveryReviewOpen(false);
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
    if (activeComposerMode === "external") {
      openDeliveryReview();
    } else {
      void send();
    }
  }

  async function resolveApproval(id: string, accepted: boolean) {
    try {
      setError("");
      if (accepted) await api.approve(id);
      else await api.reject(id);
      await Promise.all([refreshGlobal(), refreshSession(activeSession)]);
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.approval);
    }
  }

  async function modifyApproval(id: string, args: Record<string, unknown>) {
    try {
      setError("");
      await api.modifyApproval(id, args);
      await Promise.all([refreshGlobal(), refreshSession(activeSession)]);
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.approvalEdit);
    }
  }

  async function resolveMemory(id: string, accepted: boolean) {
    try {
      setError("");
      if (accepted) await api.acceptMemory(id);
      else await api.rejectMemory(id);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.memory);
    }
  }

  async function updateMemory(id: string, kind: string, content: string) {
    try {
      setError("");
      await api.updateMemory(id, kind, content);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.memoryEdit);
      throw err;
    }
  }

  async function deleteMemory(id: string) {
    try {
      setError("");
      await api.deleteMemory(id);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.memoryDelete);
      throw err;
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

  async function archiveMemoryExport() {
    try {
      setError("");
      await api.archiveMemoryExport();
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.memoryExport);
      throw err;
    }
  }

  async function revokeClient(id: string) {
    try {
      setError("");
      await api.revokeClient(id);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.clientRevoke);
      throw err;
    }
  }

  async function startNotificationBinding(channel: string, botToken = "", scopes = ["reminder_send_self"]) {
    try {
      setError("");
      const binding = await api.startNotificationBinding(channel, botToken, scopes);
      setNotificationBindings((current) => [binding, ...current.filter((item) => item.id !== binding.id)]);
      if (channel === "telegram") {
        setRuntimeConfig(await api.config());
      } else {
        await refreshGlobal();
      }
      setTab("settings");
    } catch (err) {
      const message = notificationBindingErrorMessage(err, text);
      setError(message);
      throw new Error(message);
    }
  }

  async function refreshNotificationBinding(id: string) {
    const binding = await api.notificationBinding(id);
    setNotificationBindings((current) => [binding, ...current.filter((item) => item.id !== binding.id)]);
    const awaitingTelegramMessage = binding.channel === "telegram" && binding.status === "active" && !binding.external_user_id && !binding.context_token;
    if (!isBindingPending(binding.status) && !awaitingTelegramMessage) {
      await refreshGlobal();
    }
    return binding;
  }

  async function revokeNotificationBinding(id: string) {
    try {
      setError("");
      await api.revokeNotificationBinding(id);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.binding);
      throw err;
    }
  }

  async function updateToolPolicy(deny: string[], approvalRequired: string[]) {
    try {
      setError("");
      await api.updateToolPolicy(deny, approvalRequired);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.policyUpdate);
      throw err;
    }
  }

  async function updateOwner(displayName: string, email: string, preferences: Record<string, string>) {
    try {
      setError("");
      const updated = await api.updateOwner(displayName, email, preferences);
      setOwnerProfile(updated);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.ownerUpdate);
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
      <aside className="sidebar">
        <div className="brandRow">
          <div className="brand">
            <div className="brandMark">
              <Terminal size={18} />
            </div>
            <div>
              <strong>{text.app.name}</strong>
              <span>{text.app.tagline}</span>
            </div>
          </div>
          <button className="iconButton subtle" onClick={() => setLanguage(nextLanguage)} title={text.nav.language}>
            <Languages size={17} />
            <span>{languageLabel}</span>
          </button>
        </div>

        <div className="navStatus">
          <div className={`statusDot ${ready?.ok ? "ready" : "offline"}`} />
          <div>
            <strong>{ready?.ok ? text.nav.ready : text.nav.offline}</strong>
            <span>{ready ? ready.model_mode : text.topbar.connecting}</span>
          </div>
        </div>

        <button className="primaryButton" onClick={() => void createSession()} title={text.nav.newSession}>
          <Plus size={17} />
          <span>{text.nav.newSession}</span>
        </button>

        <dl className="navMetrics">
          <dt>{text.nav.sessions}</dt>
          <dd>{sessions.length}</dd>
          <dt>{text.nav.approvals}</dt>
          <dd>{pendingApprovals.length}</dd>
          <dt>{text.nav.memories}</dt>
          <dd>{pendingCandidates.length}</dd>
        </dl>

        <div className="sessionList" aria-label={text.nav.sessions}>
          {sessions.map((session) => (
            <div className={`sessionItem ${session.id === activeSession ? "active" : ""}`} key={session.id}>
              {editingSession === session.id ? (
                <form
                  className="sessionRenameForm"
                  onSubmit={(event) => {
                    event.preventDefault();
                    void renameSession(session.id);
                  }}
                >
                  <input
                    aria-label={text.nav.renameSession}
                    value={sessionTitleDraft}
                    onChange={(event) => setSessionTitleDraft(event.target.value)}
                    disabled={sessionActionId === session.id}
                  />
                  <button className="miniIconButton" disabled={!sessionTitleDraft.trim() || sessionActionId === session.id} title={text.nav.saveSessionName}>
                    <Save size={13} />
                  </button>
                  <button className="miniIconButton" type="button" onClick={cancelRenameSession} disabled={sessionActionId === session.id} title={text.common.cancel}>
                    <X size={13} />
                  </button>
                </form>
              ) : (
                <>
                  <button
                    className="sessionSelect"
                    onClick={() => {
                      setActiveSession(session.id);
                      setDeliveryReviewOpen(false);
                      setTab("timeline");
                      void refreshSession(session.id);
                    }}
                  >
                    <span>{session.title}</span>
                    <small>{shortId(session.id)}</small>
                  </button>
                  <div className="sessionActions">
                    <button className="miniIconButton" onClick={() => startRenameSession(session)} disabled={sessionActionId === session.id} title={text.nav.renameSession}>
                      <Pencil size={13} />
                    </button>
                    <button className="miniIconButton dangerIcon" onClick={() => void deleteSession(session.id)} disabled={sessionActionId === session.id} title={text.nav.deleteSession}>
                      <Trash2 size={13} />
                    </button>
                  </div>
                </>
              )}
            </div>
          ))}
        </div>
      </aside>

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
          language={language}
          text={text}
          onToggle={() => setScheduleBarOpen((current) => !current)}
          onRefresh={() => void refreshSchedules()}
        />

        <section className="chatColumn">
          <div className="messageList">
            {activeComposerMode === "external" ? (
              <DeliveryHistory items={messageHistory} text={text} language={language} />
            ) : messages.length === 0 ? (
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
            <div className="composerToolbar">
              <div className="composerMode" role="group" aria-label={text.nav.mode}>
                <button type="button" className={activeComposerMode === "agent" ? "selected" : ""} onClick={() => setComposerMode("agent")}>
                  <MessageSquare size={15} />
                  <span>{text.chat.agentMode}</span>
                </button>
                <button type="button" className={activeComposerMode === "external" ? "selected" : ""} onClick={() => setComposerMode("external")}>
                  <Send size={15} />
                  <span>{text.chat.externalMode}</span>
                </button>
              </div>
            </div>
            {activeComposerMode === "agent" && (
              <div className="starterRow">
                {text.starters.map((prompt) => (
                  <button key={prompt} onClick={() => void send(prompt, activeSession)} disabled={busy}>
                    {prompt}
                  </button>
                ))}
              </div>
            )}
            {activeComposerMode === "external" && (
              <>
                <DeliveryReceiptSummary
                  delivery={activeLastDelivery}
                  retrying={deliveryBusy}
                  text={text}
                  onRetry={() => void retryExternalDelivery()}
                />
                <div className="deliveryTargetSelectors">
                  <label>
                    <span>{text.chat.software}</span>
                    <select
                      value={activeExternalDraft.software}
                      onChange={(event) => updateExternalDraft((draft) => selectDeliverySoftware(draft, event.target.value, deliveryEndpoints))}
                      disabled={deliveryBusy || deliveryEndpoints.length === 0}
                    >
                      <option value="">{text.chat.chooseSoftware}</option>
                      {deliverySoftware.map(([channel, label]) => (
                        <option key={channel} value={channel}>{label}</option>
                      ))}
                    </select>
                  </label>
                  <label>
                    <span>{text.chat.recipient}</span>
                    <select
                      value={activeExternalDraft.endpointId}
                      onChange={(event) => updateExternalDraft((draft) => ({ ...draft, endpointId: event.target.value }))}
                      disabled={deliveryBusy || !activeExternalDraft.software || activeDeliveryCandidates.length === 0}
                    >
                      <option value="">{activeDeliveryCandidates.length === 0 ? text.chat.noDeliveryEndpoints : text.chat.chooseRecipient}</option>
                      {activeDeliveryCandidates.map((endpoint) => (
                        <option key={endpoint.id} value={endpoint.id}>
                          {endpoint.recipient.display_name} · {endpoint.account_display_name}
                          {endpoint.conversation_label ? ` · ${endpoint.conversation_label}` : ""}
                        </option>
                      ))}
                    </select>
                  </label>
                </div>
              </>
            )}
            {activeComposerMode === "agent" && activeAttachments.length > 0 && (
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
            {activeComposerMode === "external" && (
              <ExternalPartTray
                parts={activeExternalDraft.parts}
                fallbackPartIds={activeDeliveryValidation.fallbackPartIds}
                supportsCaption={activeDeliveryEndpoint?.capabilities.supports_caption === true}
                text={text}
                onChange={updateExternalPart}
                onMove={(index, offset) => updateExternalDraft((draft) => ({ ...draft, parts: moveDeliveryPart(draft.parts, index, offset) }))}
                onRemove={(id) => updateExternalDraft((draft) => ({ ...draft, parts: draft.parts.filter((part) => part.id !== id) }))}
              />
            )}
            {activeComposerMode === "external" && activeDeliveryValidation.error && (
              <span className="deliveryValidation">{deliveryValidationMessage(activeDeliveryValidation.error, text)}</span>
            )}
            <form className={`composer ${activeComposerMode}`} onSubmit={onSubmit}>
              <input
                ref={uploadInputRef}
                className="documentUploadInput"
                type="file"
                accept={activeComposerMode === "agent" ? ".txt,.md,.csv,.pdf,.docx,.xlsx,.pptx,.png,.jpg,.jpeg,.gif,.webp,image/png,image/jpeg,image/gif,image/webp" : undefined}
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
              {activeComposerMode === "agent" && (
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
                value={activeComposerMode === "external" ? activeExternalDraft.text : activeInput}
                onChange={(event) => {
                  if (!activeSession) return;
                  const value = event.target.value;
                  if (activeComposerMode === "external") {
                    updateExternalDraft((draft) => ({ ...draft, text: value }));
                  } else {
                    setDraftsBySession((current) => ({ ...current, [activeSession]: value }));
                  }
                }}
                onKeyDown={onComposerKeyDown}
                onCompositionStart={() => setIsComposingInput(true)}
                onCompositionEnd={() => {
                  setIsComposingInput(false);
                  setCompositionEndedAt(Date.now());
                }}
                placeholder={activeComposerMode === "external" ? text.chat.externalPlaceholder : text.chat.placeholder}
                disabled={busy || deliveryBusy}
              />
              <button
                className="sendButton"
                disabled={
                  activeComposerMode === "external"
                    ? deliveryBusy || !activeDeliveryValidation.valid
                    : busy || voice.active || (!activeInput.trim() && activeAttachments.length === 0)
                }
                title={activeComposerMode === "external" ? text.chat.reviewSend : text.chat.send}
              >
                <Send size={18} />
              </button>
              {activeComposerMode === "agent" && (
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

      <aside className="inspectorColumn">
        <div className="inspectorTitle">INSPECTOR</div>
        <div className="tabs">
          <button className={tab === "timeline" ? "selected" : ""} onClick={() => setTab("timeline")} title={text.tabs.timeline}>
            <FileSearch size={16} />
            <span>{text.tabs.timeline}</span>
          </button>
          <button className={tab === "approvals" ? "selected" : ""} onClick={() => setTab("approvals")} title={text.tabs.approvals}>
            <ShieldAlert size={16} />
            <span>{pendingApprovals.length}</span>
          </button>
          <button className={tab === "memory" ? "selected" : ""} onClick={() => setTab("memory")} title={text.tabs.memory}>
            <MemoryStick size={16} />
            <span>{pendingCandidates.length}</span>
          </button>
          <button className={tab === "trace" ? "selected" : ""} onClick={() => setTab("trace")} title={text.tabs.trace}>
            <ScrollText size={16} />
            <span>{text.tabs.trace}</span>
          </button>
          <button className={tab === "status" ? "selected" : ""} onClick={() => setTab("status")} title={text.tabs.status}>
            <Gauge size={16} />
            <span>{text.tabs.status}</span>
          </button>
          <button className={tab === "settings" ? "selected" : ""} onClick={() => setTab("settings")} title={text.tabs.settings}>
            <Settings size={16} />
            <span>{text.tabs.settings}</span>
          </button>
        </div>

        {tab === "timeline" && <ToolTimelinePanel calls={toolCalls} text={text} onTrace={(runId) => void openTrace(runId)} />}
        {tab === "approvals" && (
          <ApprovalPanel
            approvals={approvals}
            text={text}
            onResolve={(id, accepted) => void resolveApproval(id, accepted)}
            onModify={(id, args) => void modifyApproval(id, args)}
          />
        )}
        {tab === "memory" && (
          <MemoryPanel
            candidates={candidates}
            memories={memories}
            text={text}
            onResolve={(id, accepted) => void resolveMemory(id, accepted)}
            onUpdate={(id, kind, content) => updateMemory(id, kind, content)}
            onDelete={(id) => deleteMemory(id)}
            onExport={() => archiveMemoryExport()}
          />
        )}
        {tab === "trace" && (
          <TracePanel trace={traceRun} traces={traceList} loading={traceLoading} text={text} language={language} onOpen={(runId) => void openTrace(runId)} />
        )}
        {tab === "status" && (
          <StatusStack
            ready={ready}
            modelCalls={modelCalls}
            auditEvents={auditEvents}
            artifacts={artifacts}
            episodes={episodes}
            evalRun={evalRun}
            evalRuns={evalRuns}
            skills={skills}
            text={text}
            language={language}
            onRunEval={async () => {
              setError("");
              const result = await api.runEval("smoke");
              setEvalRun(result);
              setEvalRuns([result, ...evalRuns.filter((run) => run.id !== result.id)]);
            }}
            onSelectEval={async (id) => {
              setError("");
              setEvalRun(await api.evalRun(id));
            }}
            onError={(message) => setError(message)}
          />
        )}
        {tab === "settings" && (
          <SettingsPanel
            runtimeConfig={runtimeConfig}
            ownerProfile={ownerProfile}
            clients={clients}
            weixinBindings={weixinBindings}
            telegramBindings={telegramBindings}
            text={text}
            language={language}
            onUpdateOwner={(displayName, email, preferences) => updateOwner(displayName, email, preferences)}
            onRevokeClient={(id) => revokeClient(id)}
            onStartNotificationBinding={(channel, botToken, scopes) => startNotificationBinding(channel, botToken, scopes)}
            onRefreshNotificationBinding={(id) => refreshNotificationBinding(id)}
            onRevokeNotificationBinding={(id) => revokeNotificationBinding(id)}
            onUpdatePolicy={(deny, approvalRequired) => updateToolPolicy(deny, approvalRequired)}
          />
        )}
      </aside>
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
