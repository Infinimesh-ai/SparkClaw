import { FormEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Activity, KeyRound, RefreshCw, X } from "lucide-react";
import { api, apiToken, clearAPIToken, saveAPIToken, sessionEventsURL } from "./api/client";
import { dictionaries, initialLanguage, LANGUAGE_STORAGE_KEY } from "./i18n";
import type { Language } from "./i18n";
import {
  attachmentOnlyPrompt,
  MessageBubble,
  streamStatusFromEvent,
  upsertStreamStatus
} from "./components/messages";
import type { StreamStatus } from "./components/messages";
import { InspectorColumn } from "./components/inspector";
import type { PanelTab } from "./components/inspector";
import { ComposerDock } from "./components/composer";
import { DeliveryTargetPicker } from "./components/deliveryTargetPicker";
import { ScheduleBar } from "./components/schedules";
import { SessionSidebar } from "./components/sidebar";
import { useExternalDelivery } from "./hooks/useExternalDelivery";
import { useSchedules } from "./hooks/useSchedules";
import { useSessionCrud } from "./hooks/useSessionCrud";
import { useVoiceInput } from "./hooks/useVoiceInput";
import type { VoiceDraftAnchor } from "./hooks/useVoiceInput";
import { sortNotificationBindings, isVisibleNotificationBinding } from "./lib/format";
import { MESSAGE_STREAM_STARTED_EVENT, messageStreamFailureDisposition } from "./lib/messageStream";
import { insertVoiceTranscript } from "./lib/voiceDraft";
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
  Session,
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
  const [draftsBySession, setDraftsBySession] = useState<Record<string, string>>({});
  const [attachmentsBySession, setAttachmentsBySession] = useState<Record<string, MessageAttachment[]>>({});
  const [busy, setBusy] = useState(false);
  const [pairing, setPairing] = useState(false);
  const [tokenInput, setTokenInput] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [tab, setTab] = useState<PanelTab>("timeline");
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
    deliveryEndpoints,
    activeDeliveryEndpoint,
    activeExternalDraft,
    externalDeliveryIntent,
    activeDeliveryValidation,
    activeLastDelivery,
    refreshDeliverySurface,
    updateExternalDraft,
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
    const [readyStatus, configStatus, owner, clientList, bindingList, approvalList, candidateList, memoryList, evalList, artifactList, traces, scheduleList] =
      await Promise.all([
        api.ready(),
        api.config(),
        api.owner(),
        api.clients(),
        api.notificationBindings(),
        api.approvals(),
        api.memoryCandidates(),
        api.memories(),
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
    setEvalRuns(evalList.eval_runs ?? []);
    setArtifacts(artifactList.artifacts ?? []);
    setTraceList(traces.traces ?? []);
    setSchedules(scheduleList.schedules ?? []);
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
    resetSessionDraft,
    clearSessionState,
    refreshSession,
    refreshGlobal
  });

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
  async function send(content = activeInput, sessionId = activeSession) {
    const trimmed = content.trim();
    const attachments = attachmentsBySession[sessionId] ?? [];
    if (!sessionId || (!trimmed && attachments.length === 0) || busy || voice.active) return;
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
      await api.sendMessageStream(sessionId, trimmed || attachmentOnlyPrompt(language), attachments, {
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
      const disposition = messageStreamFailureDisposition(streamAccepted);
      if (disposition === "restore_draft") {
        setDraftsBySession((current) => ({ ...current, [sessionId]: trimmed }));
        setAttachmentsBySession((current) => ({ ...current, [sessionId]: attachments }));
        setError(err instanceof Error ? err.message : text.errors.message);
      } else {
        // The gateway accepted the run and keeps executing it server-side;
        // losing the stream is not a failure, so surface an informational
        // notice instead of an error banner.
        setAttachmentsBySession((current) => ({ ...current, [sessionId]: [] }));
        resetSessionDraft(sessionId);
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
            <DeliveryTargetPicker
              endpoints={deliveryEndpoints}
              activeEndpoint={activeDeliveryEndpoint}
              hasExternalIntent={externalDeliveryIntent}
              disabled={busy || deliveryBusy || voice.active}
              text={text}
              onSelect={selectDeliveryTarget}
            />
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

        {notice && (
          <div className="noticeBanner" role="status">
            <span>{notice}</span>
            <button className="iconButton" onClick={() => setNotice("")} title={text.chat.dismissNotice} aria-label={text.chat.dismissNotice}>
              <X size={15} />
            </button>
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
            deliveryBusy={deliveryBusy}
            deliveryReviewOpen={deliveryReviewOpen}
            setDeliveryReviewOpen={setDeliveryReviewOpen}
            activeDeliveryEndpoint={activeDeliveryEndpoint}
            activeExternalDraft={activeExternalDraft}
            externalDeliveryIntent={externalDeliveryIntent}
            activeDeliveryValidation={activeDeliveryValidation}
            activeLastDelivery={activeLastDelivery}
            updateExternalDraft={updateExternalDraft}
            updateExternalPart={updateExternalPart}
            removeExternalPart={removeExternalPart}
            openDeliveryReview={openDeliveryReview}
            confirmExternalDelivery={confirmExternalDelivery}
            retryExternalDelivery={retryExternalDelivery}
          />
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
