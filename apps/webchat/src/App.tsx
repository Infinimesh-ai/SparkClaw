import { FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import {
  Activity,
  Check,
  Clock3,
  CalendarDays,
  Database,
  FileSearch,
  Gauge,
  Globe2,
  Mail,
  Inbox,
  Download,
  UserRound,
  Library,
  ListChecks,
  MemoryStick,
  MessageSquarePlus,
  Settings,
  RefreshCw,
  ScrollText,
  Send,
  ShieldAlert,
  ThumbsDown,
  ThumbsUp,
  Trash2,
  KeyRound,
  Sparkles,
  Pencil,
  Terminal,
  X
} from "lucide-react";
import { api, apiToken, clearAPIToken, saveAPIToken, sessionEventsURL } from "./api/client";
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
  ModelCall,
  OwnerProfile,
  PublicConfig,
  ReadyStatus,
  RunTrace,
  Session,
  Skill,
  TraceMetadata,
  ToolCall
} from "./api/types";

type PanelTab = "approvals" | "memory" | "status" | "settings" | "trace";

const starterPrompts = [
  "Search for SparkClaw in the workspace",
  "Read https://example.com with browser.read",
  "Search email for deployment",
  "Read calendar for today",
  "Remember that SparkClaw prefers approval-first workflows",
  "Run shell command `ls -la` in the sandbox"
];

export function App() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [activeSession, setActiveSession] = useState<string>("");
  const [messages, setMessages] = useState<Message[]>([]);
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
  const [evalRun, setEvalRun] = useState<EvalRun | null>(null);
  const [evalRuns, setEvalRuns] = useState<EvalRun[]>([]);
  const [artifacts, setArtifacts] = useState<ArtifactObject[]>([]);
  const [traceRun, setTraceRun] = useState<RunTrace | null>(null);
  const [traceList, setTraceList] = useState<TraceMetadata[]>([]);
  const [traceLoading, setTraceLoading] = useState(false);
  const [skills, setSkills] = useState<Skill[]>([]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [pairing, setPairing] = useState(false);
  const [tokenInput, setTokenInput] = useState("");
  const [error, setError] = useState("");
  const [tab, setTab] = useState<PanelTab>("approvals");

  const refreshGlobal = useCallback(async () => {
    const [readyStatus, configStatus, owner, clientList, approvalList, candidateList, memoryList, skillList, evalList, artifactList, traces] = await Promise.all([
      api.ready(),
      api.config(),
      api.owner(),
      api.clients(),
      api.approvals(),
      api.memoryCandidates(),
      api.memories(),
      api.skills(),
      api.evalRuns(),
      api.artifacts(),
      api.traces()
    ]);
    setReady(readyStatus);
    setRuntimeConfig(configStatus);
    setOwnerProfile(owner);
    setClients(clientList.clients ?? []);
    setApprovals(approvalList.approvals);
    setCandidates(candidateList.memory_candidates);
    setMemories(memoryList.memories);
    setSkills(skillList.skills);
    setEvalRuns(evalList.eval_runs ?? []);
    setArtifacts(artifactList.artifacts ?? []);
    setTraceList(traces.traces ?? []);
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
    setMessages(messageList.messages ?? []);
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
        const [sessionList] = await Promise.all([api.sessions(), refreshGlobal()]);
        if (cancelled) return;
        let next = sessionList.sessions[0];
        if (!next) {
          next = await api.createSession("Local Agent Workbench");
        }
        setSessions(next ? [next, ...sessionList.sessions.filter((session) => session.id !== next.id)] : sessionList.sessions);
        setActiveSession(next.id);
        await refreshSession(next.id);
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to connect to SparkClaw Gateway");
      }
    }
    void boot();
    return () => {
      cancelled = true;
    };
  }, [refreshGlobal, refreshSession]);

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
      void refreshSession(activeSession);
      void refreshGlobal();
    }, 5000);
    return () => {
      window.clearInterval(id);
      events?.close();
    };
  }, [activeSession, refreshGlobal, refreshSession]);

  const pendingApprovals = useMemo(() => approvals.filter((approval) => approval.status === "pending"), [approvals]);
  const pendingCandidates = useMemo(() => candidates.filter((candidate) => candidate.status === "pending"), [candidates]);
  const active = sessions.find((session) => session.id === activeSession);

  async function createSession() {
    try {
      setError("");
      const session = await api.createSession("Local Agent Workbench");
      setSessions((current) => [session, ...current]);
      setActiveSession(session.id);
      setMessages([]);
      setToolCalls([]);
      setModelCalls([]);
      setAuditEvents([]);
      setEpisodes([]);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not create session");
    }
  }

  async function send(content = input) {
    if (!activeSession || !content.trim() || busy) return;
    try {
      setBusy(true);
      setError("");
      setInput("");
      await api.sendMessage(activeSession, content.trim());
      await Promise.all([refreshSession(activeSession), refreshGlobal()]);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Message failed");
    } finally {
      setBusy(false);
    }
  }

  function onSubmit(event: FormEvent) {
    event.preventDefault();
    void send();
  }

  async function resolveApproval(id: string, accepted: boolean) {
    try {
      setError("");
      if (accepted) await api.approve(id);
      else await api.reject(id);
      await Promise.all([refreshGlobal(), refreshSession(activeSession)]);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Approval update failed");
    }
  }

  async function modifyApproval(id: string, args: Record<string, unknown>) {
    try {
      setError("");
      await api.modifyApproval(id, args);
      await Promise.all([refreshGlobal(), refreshSession(activeSession)]);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Approval edit failed");
    }
  }

  async function resolveMemory(id: string, accepted: boolean) {
    try {
      setError("");
      if (accepted) await api.acceptMemory(id);
      else await api.rejectMemory(id);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Memory update failed");
    }
  }

  async function updateMemory(id: string, kind: string, content: string) {
    try {
      setError("");
      await api.updateMemory(id, kind, content);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Memory edit failed");
      throw err;
    }
  }

  async function deleteMemory(id: string) {
    try {
      setError("");
      await api.deleteMemory(id);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Memory delete failed");
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
      setError(err instanceof Error ? err.message : "Feedback save failed");
      throw err;
    }
  }

  async function archiveMemoryExport() {
    try {
      setError("");
      await api.archiveMemoryExport();
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Memory export failed");
      throw err;
    }
  }

  async function revokeClient(id: string) {
    try {
      setError("");
      await api.revokeClient(id);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Client revoke failed");
      throw err;
    }
  }

  async function updateToolPolicy(deny: string[], approvalRequired: string[]) {
    try {
      setError("");
      await api.updateToolPolicy(deny, approvalRequired);
      await refreshGlobal();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Tool policy update failed");
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
      setError(err instanceof Error ? err.message : "Owner profile update failed");
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
      setError(err instanceof Error ? err.message : "Trace unavailable");
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
      setError(err instanceof Error ? err.message : "Pairing failed");
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
      setError(err instanceof Error ? err.message : "Token authentication failed");
    }
  }

  async function bootstrappedRefresh() {
    const [sessionList] = await Promise.all([api.sessions(), refreshGlobal()]);
    let next = sessionList.sessions[0];
    if (!next) next = await api.createSession("Local Agent Workbench");
    setSessions(next ? [next, ...sessionList.sessions.filter((session) => session.id !== next.id)] : sessionList.sessions);
    setActiveSession(next.id);
    await refreshSession(next.id);
  }

  return (
    <main className="shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brandMark">
            <Sparkles size={18} />
          </div>
          <div>
            <strong>SparkClaw</strong>
            <span>Local agent runtime</span>
          </div>
        </div>
        <button className="primaryButton" onClick={() => void createSession()} title="New session">
          <MessageSquarePlus size={17} />
          New Session
        </button>
        <div className="sessionList">
          {sessions.map((session) => (
            <button
              className={`sessionItem ${session.id === activeSession ? "active" : ""}`}
              key={session.id}
              onClick={() => {
                setActiveSession(session.id);
                void refreshSession(session.id);
              }}
            >
              <span>{session.title}</span>
              <small>{shortId(session.id)}</small>
            </button>
          ))}
        </div>
      </aside>

      <section className="workspace">
        <header className="topbar">
          <div>
            <h1>{active?.title ?? "Agent Workbench"}</h1>
            <p>{ready ? `${ready.model_mode} model mode · ${ready.workspace_root}` : "Connecting to Gateway"}</p>
          </div>
          <button className="iconButton" onClick={() => void Promise.all([refreshGlobal(), refreshSession(activeSession)])} title="Refresh">
            <RefreshCw size={18} />
          </button>
        </header>

        {error && (
          <div className="errorBanner">
            <span>{error}</span>
            {error.toLowerCase().includes("token") || error.toLowerCase().includes("unauthorized") ? (
              <div className="authActions">
                <form className="tokenForm" onSubmit={(event) => void submitToken(event)}>
                  <input
                    aria-label="Gateway token"
                    value={tokenInput}
                    onChange={(event) => setTokenInput(event.target.value)}
                    placeholder="Gateway token"
                    type="password"
                  />
                  <button type="submit" disabled={!tokenInput.trim()} title="Save token">
                    <KeyRound size={15} />
                  </button>
                </form>
                <button onClick={() => void pairClient()} disabled={pairing}>
                  {pairing ? "Pairing" : "Pair"}
                </button>
              </div>
            ) : null}
          </div>
        )}

        <div className="contentGrid">
          <section className="chatColumn">
            <div className="messageList">
              {messages.length === 0 ? (
                <div className="emptyState">
                  <Activity size={24} />
                  <span>Ready for bounded local work.</span>
                </div>
              ) : (
                messages.map((message) => (
                  <MessageBubble
                    key={message.id}
                    message={message}
                    onFeedback={(rating, correction) => saveFeedback(message, rating, correction)}
                  />
                ))
              )}
            </div>
            <div className="starterRow">
              {starterPrompts.map((prompt) => (
                <button key={prompt} onClick={() => void send(prompt)} disabled={busy}>
                  {prompt}
                </button>
              ))}
            </div>
            <form className="composer" onSubmit={onSubmit}>
              <textarea
                value={input}
                onChange={(event) => setInput(event.target.value)}
                placeholder="Ask SparkClaw to search files, propose memory, or stage risky work..."
                disabled={busy}
              />
              <button className="sendButton" disabled={busy || !input.trim()} title="Send">
                <Send size={18} />
              </button>
            </form>
          </section>

          <section className="timelineColumn">
            <div className="sectionHeader">
              <FileSearch size={17} />
              <h2>Tool Timeline</h2>
            </div>
            <div className="timeline">
              {toolCalls.length === 0 ? (
                <span className="muted">No tool calls yet.</span>
              ) : (
                toolCalls.map((call) => <ToolCallItem key={call.id} call={call} onTrace={(runId) => void openTrace(runId)} />)
              )}
            </div>
          </section>

          <aside className="controlColumn">
            <div className="tabs">
              <button className={tab === "approvals" ? "selected" : ""} onClick={() => setTab("approvals")}>
                <ShieldAlert size={16} />
                {pendingApprovals.length}
              </button>
              <button className={tab === "memory" ? "selected" : ""} onClick={() => setTab("memory")}>
                <MemoryStick size={16} />
                {pendingCandidates.length}
              </button>
              <button className={tab === "status" ? "selected" : ""} onClick={() => setTab("status")}>
                <Gauge size={16} />
                Status
              </button>
              <button className={tab === "settings" ? "selected" : ""} onClick={() => setTab("settings")}>
                <Settings size={16} />
                Policy
              </button>
              <button className={tab === "trace" ? "selected" : ""} onClick={() => setTab("trace")}>
                <ScrollText size={16} />
                Trace
              </button>
            </div>
            {tab === "approvals" && (
              <ApprovalPanel
                approvals={approvals}
                onResolve={(id, accepted) => void resolveApproval(id, accepted)}
                onModify={(id, args) => void modifyApproval(id, args)}
              />
            )}
            {tab === "memory" && (
              <MemoryPanel
                candidates={candidates}
                memories={memories}
                onResolve={(id, accepted) => void resolveMemory(id, accepted)}
                onUpdate={(id, kind, content) => updateMemory(id, kind, content)}
                onDelete={(id) => deleteMemory(id)}
                onExport={() => archiveMemoryExport()}
              />
            )}
            {tab === "status" && <StatusPanel ready={ready} modelCalls={modelCalls} auditEvents={auditEvents} />}
            {tab === "status" && <ArtifactPanel artifacts={artifacts} />}
            {tab === "status" && <EpisodePanel episodes={episodes} />}
            {tab === "status" && (
              <EvalPanel
                evalRun={evalRun}
                evalRuns={evalRuns}
                onRun={async () => {
                  setError("");
                  const result = await api.runEval("smoke");
                  setEvalRun(result);
                  setEvalRuns([result, ...evalRuns.filter((run) => run.id !== result.id)]);
                }}
                onSelect={async (id) => {
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
                onUpdateOwner={(displayName, email, preferences) => updateOwner(displayName, email, preferences)}
                onRevokeClient={(id) => revokeClient(id)}
                onUpdatePolicy={(deny, approvalRequired) => updateToolPolicy(deny, approvalRequired)}
              />
            )}
            {tab === "trace" && <TracePanel trace={traceRun} traces={traceList} loading={traceLoading} onOpen={(runId) => void openTrace(runId)} />}
            {tab === "status" && <SkillsPanel skills={skills} />}
          </aside>
        </div>
      </section>
    </main>
  );
}

function MessageBubble({
  message,
  onFeedback
}: {
  message: Message;
  onFeedback: (rating: "up" | "down" | "corrected", correction?: string) => Promise<void>;
}) {
  const [correction, setCorrection] = useState("");
  const [saving, setSaving] = useState(false);

  async function submit(rating: "up" | "down" | "corrected") {
    if (saving || !message.run_id) return;
    setSaving(true);
    try {
      await onFeedback(rating, rating === "corrected" ? correction.trim() : "");
      if (rating === "corrected") setCorrection("");
    } catch {
      return;
    } finally {
      setSaving(false);
    }
  }

  return (
    <article className={`message ${message.role}`}>
      <div className="messageMeta">
        <span>{message.role === "user" ? "You" : "SparkClaw"}</span>
        <time>{formatTime(message.created_at)}</time>
      </div>
      <p>{message.content}</p>
      {message.role === "assistant" && message.run_id && (
        <div className="feedbackBar">
          <button onClick={() => void submit("up")} disabled={saving} title="Mark helpful">
            <ThumbsUp size={14} />
          </button>
          <button onClick={() => void submit("down")} disabled={saving} title="Mark not helpful">
            <ThumbsDown size={14} />
          </button>
          <input
            aria-label="Correction"
            value={correction}
            onChange={(event) => setCorrection(event.target.value)}
            disabled={saving}
            placeholder="Correction"
          />
          <button onClick={() => void submit("corrected")} disabled={saving || !correction.trim()} title="Save correction">
            <Check size={14} />
          </button>
        </div>
      )}
    </article>
  );
}

function ToolCallItem({ call, onTrace }: { call: ToolCall; onTrace: (runId: string) => void }) {
  const Icon = call.tool.includes("shell")
    ? Terminal
    : call.tool.includes("memory")
      ? Database
      : call.tool.includes("knowledge")
        ? Library
        : call.tool.includes("browser")
          ? Globe2
          : call.tool.includes("email")
            ? Mail
            : call.tool.includes("calendar")
              ? CalendarDays
              : FileSearch;
  return (
    <article className={`toolCall ${call.risk}`}>
      <div className="toolIcon">
        <Icon size={16} />
      </div>
      <div>
        <div className="toolTitle">
          <strong title={call.tool}>{call.tool}</strong>
          <span className="toolBadges">
            <button className="miniIconButton" onClick={() => onTrace(call.run_id)} title="Open run trace">
              <ScrollText size={14} />
            </button>
            <RiskPill risk={call.risk} />
          </span>
        </div>
        <span className="statusLine">{call.status}</span>
        {call.observation_summary && <small>{call.observation_summary}</small>}
        {call.error && <p className="compactError">{call.error}</p>}
        {call.approval_id && <small>Approval {shortId(call.approval_id)}</small>}
      </div>
    </article>
  );
}

function TracePanel({
  trace,
  traces,
  loading,
  onOpen
}: {
  trace: RunTrace | null;
  traces: TraceMetadata[];
  loading: boolean;
  onOpen: (runId: string) => void;
}) {
  return (
    <div className="panelStack">
      <div className="sectionHeader">
        <ScrollText size={17} />
        <h2>Trace</h2>
      </div>
      {traces.length > 0 && (
        <div className="traceHistory">
          {traces.slice(0, 6).map((item) => (
            <button
              key={item.run_id}
              className={trace?.run.id === item.run_id ? "selected" : ""}
              onClick={() => onOpen(item.run_id)}
              title={item.artifact_uri || item.run_id}
            >
              <ScrollText size={14} />
              <span>{item.state}</span>
              <strong>{item.tool_call_count} tools</strong>
              <small>{shortId(item.run_id)}</small>
            </button>
          ))}
        </div>
      )}
      {loading ? (
        <span className="muted">Loading trace.</span>
      ) : !trace ? (
        <span className="muted">No trace selected.</span>
      ) : (
        <>
          <article className="traceSummary">
            <div className="approvalTop">
              <strong>{trace.run.state}</strong>
              <span className="pill">{shortId(trace.run.id)}</span>
            </div>
            <dl className="statusGrid compact">
              <dt>Lane</dt>
              <dd>{trace.model.lane}</dd>
              <dt>Model</dt>
              <dd>{trace.model.model}</dd>
              <dt>Calls</dt>
              <dd>{trace.model_calls?.length ?? 0}</dd>
              <dt>Tokens</dt>
              <dd>{(trace.model_calls ?? []).reduce((sum, call) => sum + call.total_tokens, 0)}</dd>
              <dt>Latency</dt>
              <dd>{formatLatency(trace.model_calls)}</dd>
              <dt>Risk</dt>
              <dd>{trace.run.risk}</dd>
              <dt>Tools</dt>
              <dd>{trace.tool_calls.length}</dd>
              <dt>Approvals</dt>
              <dd>{trace.approvals.length}</dd>
              <dt>Feedback</dt>
              <dd>{trace.feedback?.length ?? 0}</dd>
              <dt>Audit</dt>
              <dd>{trace.audit.length}</dd>
            </dl>
          </article>
          <article className="traceSummary">
            <strong>Model Note</strong>
            <p>{trace.model.content}</p>
          </article>
          {trace.model_calls && trace.model_calls.length > 0 && (
            <div className="traceList">
              {trace.model_calls.map((call) => (
                <article className="traceRow" key={call.id}>
                  <span>{call.operation} · {call.lane}</span>
                  <small>{call.status} · {call.total_tokens} tokens · {call.latency_ms} ms</small>
                </article>
              ))}
            </div>
          )}
          {trace.episode && (
            <EpisodeCard episode={trace.episode} compact />
          )}
          {trace.feedback && trace.feedback.length > 0 && (
            <div className="traceList">
              {trace.feedback.map((feedback) => (
                <article className="traceRow" key={feedback.id}>
                  <span>{feedback.rating}</span>
                  <small>{feedback.correction || feedback.note || shortId(feedback.message_id || feedback.run_id)}</small>
                </article>
              ))}
            </div>
          )}
          <div className="traceList">
            {trace.tool_calls.map((call) => (
              <article className="traceRow" key={call.id}>
                <span>{call.tool}</span>
                <small>{call.observation_summary || call.status}</small>
              </article>
            ))}
          </div>
        </>
      )}
    </div>
  );
}

function EpisodePanel({ episodes }: { episodes: EpisodeSummary[] }) {
  return (
    <div className="panelStack">
      <div className="sectionHeader">
        <ScrollText size={17} />
        <h2>Episodes</h2>
      </div>
      {episodes.length === 0 ? (
        <span className="muted">No episodes yet.</span>
      ) : (
        episodes.slice(0, 5).map((episode) => <EpisodeCard key={episode.id} episode={episode} />)
      )}
    </div>
  );
}

function ArtifactPanel({ artifacts }: { artifacts: ArtifactObject[] }) {
  return (
    <div className="panelStack">
      <div className="sectionHeader">
        <Database size={17} />
        <h2>Artifacts</h2>
      </div>
      {artifacts.length === 0 ? (
        <span className="muted">No artifacts archived.</span>
      ) : (
        <div className="artifactList">
          {artifacts.slice(0, 5).map((artifact) => (
            <article className="artifactItem" key={artifact.id} title={artifact.path || artifact.uri}>
              <div className="approvalTop">
                <strong>{artifact.kind}</strong>
                <span className="pill">{artifact.backend}</span>
              </div>
              <span>{artifact.uri}</span>
              <small>{artifact.bytes} bytes · {artifact.content_type}</small>
            </article>
          ))}
        </div>
      )}
    </div>
  );
}

function EpisodeCard({ episode, compact = false }: { episode: EpisodeSummary; compact?: boolean }) {
  return (
    <article className={`episodeItem ${compact ? "compactEpisode" : ""}`}>
      <div className="approvalTop">
        <strong>{episode.outcome}</strong>
        <span className="pill">{shortId(episode.run_id)}</span>
      </div>
      <p>{episode.summary}</p>
      <dl className="statusGrid compact">
        <dt>Lane</dt>
        <dd>{episode.model_lane}</dd>
        <dt>Risk</dt>
        <dd>{episode.risk}</dd>
        <dt>Repair</dt>
        <dd>{episode.repair_performed ? "yes" : "no"}</dd>
      </dl>
      {episode.tools.length > 0 && (
        <div className="evalCases">
          {episode.tools.slice(0, compact ? 4 : 8).map((tool) => (
            <span key={tool}>{tool}</span>
          ))}
        </div>
      )}
      {episode.failures && episode.failures.length > 0 && (
        <div className="evalCases">
          {episode.failures.slice(0, 3).map((failure) => (
            <span className="failed" key={failure}>
              {failure}
            </span>
          ))}
        </div>
      )}
    </article>
  );
}

function ApprovalPanel({
  approvals,
  onResolve,
  onModify
}: {
  approvals: Approval[];
  onResolve: (id: string, accepted: boolean) => void;
  onModify: (id: string, args: Record<string, unknown>) => void;
}) {
  const [editing, setEditing] = useState("");
  const [draft, setDraft] = useState("");
  const [parseError, setParseError] = useState("");

  function startEdit(approval: Approval) {
    setEditing(approval.id);
    setDraft(JSON.stringify(stripSystemArgs(approval.arguments), null, 2));
    setParseError("");
  }

  function saveEdit(id: string) {
    try {
      const parsed = JSON.parse(draft) as Record<string, unknown>;
      onModify(id, parsed);
      setEditing("");
      setParseError("");
    } catch {
      setParseError("Invalid JSON");
    }
  }

  return (
    <div className="panelStack">
      <div className="sectionHeader">
        <Inbox size={17} />
        <h2>Approval Inbox</h2>
      </div>
      {approvals.length === 0 ? (
        <span className="muted">No approvals.</span>
      ) : (
        approvals.map((approval) => (
          <article className="approvalItem" key={approval.id}>
            <div className="approvalTop">
              <strong>{approval.summary}</strong>
              <RiskPill risk={approval.risk} />
            </div>
            <p>{approval.reason}</p>
            <code>{JSON.stringify(approval.arguments)}</code>
            {approval.status === "pending" ? (
              <>
                {editing === approval.id && (
                  <div className="approvalEdit">
                    <textarea value={draft} onChange={(event) => setDraft(event.target.value)} />
                    {parseError && <span className="compactError">{parseError}</span>}
                  </div>
                )}
                <div className="buttonRow">
                  <button className="approve" onClick={() => onResolve(approval.id, true)} title="Approve">
                    <Check size={16} />
                  </button>
                  <button className="edit" onClick={() => (editing === approval.id ? saveEdit(approval.id) : startEdit(approval))} title="Edit arguments">
                    <Pencil size={15} />
                  </button>
                  <button className="reject" onClick={() => onResolve(approval.id, false)} title="Reject">
                    <X size={16} />
                  </button>
                </div>
              </>
            ) : (
              <span className="resolved">{approval.status}</span>
            )}
          </article>
        ))
      )}
    </div>
  );
}

function stripSystemArgs(args: Record<string, unknown>) {
  return Object.fromEntries(Object.entries(args).filter(([key]) => !key.startsWith("_")));
}

function MemoryPanel({
  candidates,
  memories,
  onResolve,
  onUpdate,
  onDelete,
  onExport
}: {
  candidates: MemoryCandidate[];
  memories: Memory[];
  onResolve: (id: string, accepted: boolean) => void;
  onUpdate: (id: string, kind: string, content: string) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
  onExport: () => Promise<void>;
}) {
  const [editingId, setEditingId] = useState("");
  const [editKind, setEditKind] = useState("");
  const [editContent, setEditContent] = useState("");
  const [savingId, setSavingId] = useState("");
  const [exporting, setExporting] = useState(false);

  function startEdit(memory: Memory) {
    setEditingId(memory.id);
    setEditKind(memory.kind);
    setEditContent(memory.content);
  }

  function cancelEdit() {
    setEditingId("");
    setEditKind("");
    setEditContent("");
  }

  async function saveEdit(memory: Memory) {
    if (!editKind.trim() || !editContent.trim() || savingId) return;
    setSavingId(memory.id);
    try {
      await onUpdate(memory.id, editKind.trim(), editContent.trim());
      cancelEdit();
    } catch {
      return;
    } finally {
      setSavingId("");
    }
  }

  async function removeMemory(memory: Memory) {
    if (savingId) return;
    setSavingId(memory.id);
    try {
      await onDelete(memory.id);
      if (editingId === memory.id) cancelEdit();
    } catch {
      return;
    } finally {
      setSavingId("");
    }
  }

  async function archiveExport() {
    if (exporting) return;
    setExporting(true);
    try {
      await onExport();
    } catch {
      return;
    } finally {
      setExporting(false);
    }
  }

  return (
    <div className="panelStack">
      <div className="sectionHeader">
        <MemoryStick size={17} />
        <h2>Memory Review</h2>
      </div>
      {candidates.length === 0 ? (
        <span className="muted">No memory candidates.</span>
      ) : (
        candidates.map((candidate) => (
          <article className="approvalItem" key={candidate.id}>
            <div className="approvalTop">
              <strong>{candidate.kind}</strong>
              <span className="pill">{candidate.sensitivity}</span>
            </div>
            <p>{candidate.content}</p>
            {candidate.status === "pending" ? (
              <div className="buttonRow">
                <button className="approve" onClick={() => onResolve(candidate.id, true)} title="Accept memory">
                  <Check size={16} />
                </button>
                <button className="reject" onClick={() => onResolve(candidate.id, false)} title="Reject memory">
                  <X size={16} />
                </button>
              </div>
            ) : (
              <span className="resolved">{candidate.status}</span>
            )}
          </article>
        ))
      )}
      <div className="sectionHeader smallHeader">
        <Database size={15} />
        <h2>Accepted</h2>
        <button className="miniIconButton headerAction" onClick={() => void archiveExport()} disabled={exporting} title="Archive memory export">
          <Download size={14} />
        </button>
      </div>
      <dl className="statusGrid compact memoryCounts">
        <dt>Accepted</dt>
        <dd>{memories.length}</dd>
        <dt>Pending</dt>
        <dd>{candidates.filter((candidate) => candidate.status === "pending").length}</dd>
      </dl>
      {memories.map((memory) => (
        <article className="memoryItem" key={memory.id}>
          {editingId === memory.id ? (
            <div className="memoryEdit">
              <input
                aria-label="Memory kind"
                value={editKind}
                onChange={(event) => setEditKind(event.target.value)}
                disabled={savingId === memory.id}
              />
              <textarea
                aria-label="Memory content"
                value={editContent}
                onChange={(event) => setEditContent(event.target.value)}
                disabled={savingId === memory.id}
              />
              <div className="buttonRow">
                <button
                  className="approve"
                  onClick={() => void saveEdit(memory)}
                  disabled={!editKind.trim() || !editContent.trim() || savingId === memory.id}
                  title="Save memory"
                >
                  <Check size={16} />
                </button>
                <button className="edit" onClick={cancelEdit} disabled={savingId === memory.id} title="Cancel edit">
                  <X size={16} />
                </button>
              </div>
            </div>
          ) : (
            <>
              <div className="approvalTop">
                <strong>{memory.kind}</strong>
                <div className="buttonRow compactButtons">
                  <button className="edit" onClick={() => startEdit(memory)} disabled={savingId === memory.id} title="Edit memory">
                    <Pencil size={15} />
                  </button>
                  <button className="reject" onClick={() => void removeMemory(memory)} disabled={savingId === memory.id} title="Delete memory">
                    <Trash2 size={15} />
                  </button>
                </div>
              </div>
              <p>{memory.content}</p>
            </>
          )}
        </article>
      ))}
    </div>
  );
}

function StatusPanel({
  ready,
  modelCalls,
  auditEvents
}: {
  ready: ReadyStatus | null;
  modelCalls: ModelCall[];
  auditEvents: AuditEvent[];
}) {
  const recentModelCalls = modelCalls.slice(-6).reverse();
  const recentAuditEvents = auditEvents.slice(-6).reverse();
  return (
    <div className="panelStack">
      <div className="sectionHeader">
        <Clock3 size={17} />
        <h2>Runtime</h2>
      </div>
      {!ready ? (
        <span className="muted">Gateway unavailable.</span>
      ) : (
        <dl className="statusGrid">
          <dt>Gateway</dt>
          <dd>{ready.gateway_binding}</dd>
          <dt>Model</dt>
          <dd>{ready.model_mode}</dd>
          <dt>Rate Limit</dt>
          <dd>{rateLimitLabel(ready.rate_limit)}</dd>
          <dt>Workspace</dt>
          <dd>{ready.workspace_root}</dd>
          <dt>Trace</dt>
          <dd>{ready.trace_dir}</dd>
          <dt>State</dt>
          <dd>{ready.state_backend} · {ready.state_path}</dd>
          {ready.state_dsn && (
            <>
              <dt>DSN</dt>
              <dd>{ready.state_dsn}</dd>
            </>
          )}
        </dl>
      )}
      <div className="diagnosticList">
        <strong>Model Calls</strong>
        {recentModelCalls.length === 0 ? (
          <span className="muted">No model calls in this session.</span>
        ) : (
          recentModelCalls.map((call) => (
            <article className="diagnosticRow" key={call.id}>
              <div>
                <span>{call.operation} · {call.lane}</span>
                <small>{call.profile || call.model}</small>
              </div>
              <small>{call.status} · {call.total_tokens} tokens · {call.latency_ms} ms</small>
            </article>
          ))
        )}
      </div>
      <div className="diagnosticList">
        <strong>Audit</strong>
        {recentAuditEvents.length === 0 ? (
          <span className="muted">No audit events in this session.</span>
        ) : (
          recentAuditEvents.map((event) => (
            <article className="diagnosticRow" key={event.id}>
              <div>
                <span>{event.type}</span>
                <small>{event.actor}</small>
              </div>
              <small>{event.summary}</small>
            </article>
          ))
        )}
      </div>
    </div>
  );
}

function SettingsPanel({
  runtimeConfig,
  ownerProfile,
  clients,
  onUpdateOwner,
  onRevokeClient,
  onUpdatePolicy
}: {
  runtimeConfig: PublicConfig | null;
  ownerProfile: OwnerProfile | null;
  clients: Client[];
  onUpdateOwner: (displayName: string, email: string, preferences: Record<string, string>) => Promise<void>;
  onRevokeClient: (id: string) => Promise<void>;
  onUpdatePolicy: (deny: string[], approvalRequired: string[]) => Promise<void>;
}) {
  const [editingOwner, setEditingOwner] = useState(false);
  const [ownerName, setOwnerName] = useState("");
  const [ownerEmail, setOwnerEmail] = useState("");
  const [ownerPrefsText, setOwnerPrefsText] = useState("");
  const [ownerError, setOwnerError] = useState("");
  const [savingOwner, setSavingOwner] = useState(false);
  const [editingPolicy, setEditingPolicy] = useState(false);
  const [denyText, setDenyText] = useState("");
  const [approvalText, setApprovalText] = useState("");
  const [savingPolicy, setSavingPolicy] = useState(false);
  const [revokingClient, setRevokingClient] = useState("");

  if (!runtimeConfig) {
    return (
      <div className="panelStack">
        <div className="sectionHeader">
          <Settings size={17} />
          <h2>Settings</h2>
        </div>
        <span className="muted">Configuration unavailable.</span>
      </div>
    );
  }
  const policy = runtimeConfig.tool_policy;
  const riskCounts = Object.entries(policy.risk_counts).sort(([left], [right]) => left.localeCompare(right));
  const preferences = ownerProfile?.preferences ?? {};

  function startOwnerEdit() {
    setOwnerName(ownerProfile?.display_name ?? "");
    setOwnerEmail(ownerProfile?.email ?? "");
    setOwnerPrefsText(formatPreferences(preferences));
    setOwnerError("");
    setEditingOwner(true);
  }

  function cancelOwnerEdit() {
    setEditingOwner(false);
    setOwnerName("");
    setOwnerEmail("");
    setOwnerPrefsText("");
    setOwnerError("");
  }

  async function saveOwnerEdit() {
    if (savingOwner) return;
    setSavingOwner(true);
    setOwnerError("");
    try {
      await onUpdateOwner(ownerName, ownerEmail, parsePreferences(ownerPrefsText));
      cancelOwnerEdit();
    } catch (err) {
      setOwnerError(err instanceof Error ? err.message : "Owner profile update failed");
    } finally {
      setSavingOwner(false);
    }
  }

  function startPolicyEdit() {
    setDenyText(policy.denied_tools.join("\n"));
    setApprovalText(policy.configured_approval_required_tools.join("\n"));
    setEditingPolicy(true);
  }

  function cancelPolicyEdit() {
    setEditingPolicy(false);
    setDenyText("");
    setApprovalText("");
  }

  async function savePolicyEdit() {
    if (savingPolicy) return;
    setSavingPolicy(true);
    try {
      await onUpdatePolicy(parseToolList(denyText), parseToolList(approvalText));
      cancelPolicyEdit();
    } catch {
      return;
    } finally {
      setSavingPolicy(false);
    }
  }

  async function revokeClient(id: string) {
    if (revokingClient) return;
    setRevokingClient(id);
    try {
      await onRevokeClient(id);
    } catch {
      return;
    } finally {
      setRevokingClient("");
    }
  }

  return (
    <div className="panelStack">
      <div className="sectionHeader">
        <Settings size={17} />
        <h2>Settings</h2>
      </div>
      <article className="settingsBlock">
        <div className="approvalTop">
          <span className="settingsTitle">
            <UserRound size={15} />
            <strong>Owner Profile</strong>
          </span>
          <div className="buttonRow compactButtons">
            {editingOwner ? (
              <>
                <button className="approve" onClick={() => void saveOwnerEdit()} disabled={savingOwner} title="Save owner profile">
                  <Check size={15} />
                </button>
                <button className="edit" onClick={cancelOwnerEdit} disabled={savingOwner} title="Cancel owner edit">
                  <X size={15} />
                </button>
              </>
            ) : (
              <button className="edit" onClick={startOwnerEdit} title="Edit owner profile">
                <Pencil size={15} />
              </button>
            )}
          </div>
        </div>
        {editingOwner ? (
          <div className="ownerEditor">
            <label>
              <span>Name</span>
              <input value={ownerName} onChange={(event) => setOwnerName(event.target.value)} disabled={savingOwner} />
            </label>
            <label>
              <span>Email</span>
              <input value={ownerEmail} onChange={(event) => setOwnerEmail(event.target.value)} disabled={savingOwner} />
            </label>
            <label>
              <span>Preferences</span>
              <textarea value={ownerPrefsText} onChange={(event) => setOwnerPrefsText(event.target.value)} disabled={savingOwner} />
            </label>
            {ownerError && <span className="compactError">{ownerError}</span>}
          </div>
        ) : ownerProfile ? (
          <>
            <dl className="statusGrid compact">
              <dt>Name</dt>
              <dd>{ownerProfile.display_name}</dd>
              <dt>Email</dt>
              <dd>{ownerProfile.email || "not set"}</dd>
            </dl>
            <div className="evalCases">
              {Object.entries(preferences).map(([key, value]) => (
                <span key={key}>{key}:{value}</span>
              ))}
              {Object.keys(preferences).length === 0 && <span>none</span>}
            </div>
          </>
        ) : (
          <span className="muted">Owner unavailable.</span>
        )}
      </article>
      <article className="settingsBlock">
        <div className="approvalTop">
          <strong>Tool Policy</strong>
          <span className="pill">{policy.definition_count} tools</span>
        </div>
        <dl className="statusGrid compact">
          <dt>File</dt>
          <dd>{policy.policy_path}</dd>
          <dt>External</dt>
          <dd>{policy.external_content_untrusted ? "untrusted" : "trusted"}</dd>
          <dt>Dangerous</dt>
          <dd>{policy.approval_required_for_dangerous_tools ? "approval required" : "not forced"}</dd>
          <dt>Verifier</dt>
          <dd>{policy.dangerous_tools_deep_verification ? "deep check" : "standard"}</dd>
          <dt>Sandbox</dt>
          <dd>{policy.sandbox_required_for_mutating_tools ? "mutations require sandbox" : "not forced"}</dd>
        </dl>
        <div className="evalCases">
          {riskCounts.map(([risk, count]) => (
            <span key={risk}>{risk}:{count}</span>
          ))}
        </div>
      </article>
      <article className="settingsBlock">
        <div className="approvalTop">
          <strong>Paired Clients</strong>
          <span className="pill">{clients.length}</span>
        </div>
        {clients.length === 0 ? (
          <span className="muted">No paired clients.</span>
        ) : (
          <div className="clientList">
            {clients.map((client) => (
              <div className="clientItem" key={client.id}>
                <div>
                  <strong>{client.name}</strong>
                  <small>{client.revoked_at ? "revoked" : client.last_seen_at ? `seen ${formatTime(client.last_seen_at)}` : "not seen"}</small>
                </div>
                {!client.revoked_at && (
                  <button className="reject" onClick={() => void revokeClient(client.id)} disabled={revokingClient === client.id} title="Revoke client">
                    <Trash2 size={14} />
                  </button>
                )}
              </div>
            ))}
          </div>
        )}
      </article>
      <article className="settingsBlock">
        <strong>Definition Approval Tools</strong>
        <div className="evalCases">
          {policy.definition_approval_required_tools.map((tool) => (
            <span key={tool}>{tool}</span>
          ))}
          {policy.definition_approval_required_tools.length === 0 && <span>none</span>}
        </div>
      </article>
      <article className="settingsBlock">
        <strong>Config Approval Additions</strong>
        {editingPolicy ? (
          <div className="policyEditor">
            <label>
              <span>Approval</span>
              <textarea value={approvalText} onChange={(event) => setApprovalText(event.target.value)} disabled={savingPolicy} />
            </label>
          </div>
        ) : (
          <div className="evalCases">
            {policy.configured_approval_required_tools.map((tool) => (
              <span key={`configured-${tool}`}>{tool}</span>
            ))}
            {policy.configured_approval_required_tools.length === 0 && <span>none</span>}
          </div>
        )}
      </article>
      <article className="settingsBlock">
        <div className="approvalTop">
          <strong>Denied Tools</strong>
          <div className="buttonRow compactButtons">
            {editingPolicy ? (
              <>
                <button className="approve" onClick={() => void savePolicyEdit()} disabled={savingPolicy} title="Save tool policy">
                  <Check size={15} />
                </button>
                <button className="edit" onClick={cancelPolicyEdit} disabled={savingPolicy} title="Cancel policy edit">
                  <X size={15} />
                </button>
              </>
            ) : (
              <button className="edit" onClick={startPolicyEdit} title="Edit tool policy">
                <Pencil size={15} />
              </button>
            )}
          </div>
        </div>
        {editingPolicy ? (
          <div className="policyEditor">
            <label>
              <span>Deny</span>
              <textarea value={denyText} onChange={(event) => setDenyText(event.target.value)} disabled={savingPolicy} />
            </label>
          </div>
        ) : (
          <div className="evalCases">
            {policy.denied_tools.map((tool) => (
              <span className="failed" key={tool}>{tool}</span>
            ))}
            {policy.denied_tools.length === 0 && <span>none</span>}
          </div>
        )}
      </article>
      <article className="settingsBlock">
        <strong>Model Profiles</strong>
        <dl className="statusGrid compact">
          <dt>Mode</dt>
          <dd>{runtimeConfig.model.mock ? "mock" : "external"}</dd>
          <dt>Fast</dt>
          <dd>{profileLabel(runtimeConfig.model.fast)}</dd>
          <dt>Deep</dt>
          <dd>{profileLabel(runtimeConfig.model.deep)}</dd>
          <dt>Embed</dt>
          <dd>{profileLabel(runtimeConfig.model.embedding)}</dd>
          <dt>Rerank</dt>
          <dd>{profileLabel(runtimeConfig.model.reranker)}</dd>
          <dt>Guard</dt>
          <dd>{profileLabel(runtimeConfig.model.guard)}</dd>
        </dl>
      </article>
      <article className="settingsBlock">
        <strong>Runtime Boundaries</strong>
        <dl className="statusGrid compact">
          <dt>Gateway</dt>
          <dd>{runtimeConfig.gateway.bind}:{runtimeConfig.gateway.port}</dd>
          <dt>Remote</dt>
          <dd>{runtimeConfig.gateway.remote_access}</dd>
          <dt>Rate Limit</dt>
          <dd>{rateLimitLabel(runtimeConfig.gateway.rate_limit)}</dd>
          <dt>Workspace</dt>
          <dd>{runtimeConfig.workspaces.default_root}</dd>
          <dt>Sandbox</dt>
          <dd>{runtimeConfig.sandbox.enabled ? `${runtimeConfig.sandbox.backend} · ${runtimeConfig.sandbox.network}` : "disabled"}</dd>
          <dt>State</dt>
          <dd>
            {runtimeConfig.state.backend} · {runtimeConfig.state.path || runtimeConfig.state.dsn}
            {runtimeConfig.state.encrypt_at_rest ? " · encrypted" : ""}
          </dd>
          <dt>Artifacts</dt>
          <dd>{runtimeConfig.storage.artifact_backend} · {runtimeConfig.storage.artifact_dir || runtimeConfig.storage.artifact_bucket}</dd>
        </dl>
      </article>
      <article className="settingsBlock">
        <strong>Adapters</strong>
        <dl className="statusGrid compact">
          <dt>Email</dt>
          <dd>{adapterLabel(runtimeConfig.adapters.email)}</dd>
          <dt>Calendar</dt>
          <dd>{adapterLabel(runtimeConfig.adapters.calendar)}</dd>
          <dt>Memory</dt>
          <dd>
            {runtimeConfig.memory.enabled
              ? `${runtimeConfig.memory.write_policy} · ${retentionLabel(runtimeConfig.memory.retention_days)}`
              : "disabled"}
          </dd>
          <dt>Skills</dt>
          <dd>{runtimeConfig.skills.dirs.join(", ")}</dd>
        </dl>
      </article>
    </div>
  );
}

function EvalPanel({
  evalRun,
  evalRuns,
  onRun,
  onSelect,
  onError
}: {
  evalRun: EvalRun | null;
  evalRuns: EvalRun[];
  onRun: () => Promise<void>;
  onSelect: (id: string) => Promise<void>;
  onError: (message: string) => void;
}) {
  const [running, setRunning] = useState(false);
  const [loadingId, setLoadingId] = useState("");
  async function run() {
    try {
      setRunning(true);
      await onRun();
    } catch (err) {
      onError(err instanceof Error ? err.message : "Eval failed");
    } finally {
      setRunning(false);
    }
  }
  async function select(id: string) {
    try {
      setLoadingId(id);
      await onSelect(id);
    } catch (err) {
      onError(err instanceof Error ? err.message : "Eval failed");
    } finally {
      setLoadingId("");
    }
  }
  return (
    <div className="panelStack evalPanel">
      <div className="sectionHeader">
        <ListChecks size={17} />
        <h2>Smoke Eval</h2>
      </div>
      <button className="secondaryButton" onClick={() => void run()} disabled={running} title="Run smoke eval">
        <ListChecks size={16} />
        {running ? "Running" : "Run"}
      </button>
      {!evalRun ? (
        <span className="muted">No eval run in this view.</span>
      ) : (
        <article className={`evalResult ${evalRun.status}`}>
          <div className="approvalTop">
            <strong>{evalRun.status}</strong>
            <span className="pill">{shortId(evalRun.id)}</span>
          </div>
          <p>{evalRun.summary}</p>
          <div className="evalCases">
            {evalRun.cases.map((item) => (
              <span key={item.name} className={item.status}>
                {item.name}
              </span>
            ))}
          </div>
          {evalRun.failure_archives && evalRun.failure_archives.length > 0 && (
            <div className="archiveList">
              {evalRun.failure_archives.map((archive) => (
                <span key={`${archive.case_name}-${archive.uri}`} title={archive.path || archive.uri}>
                  {archive.case_name}: {archive.uri}
                </span>
              ))}
            </div>
          )}
        </article>
      )}
      {evalRuns.length > 0 && (
        <div className="evalHistory">
          {evalRuns.slice(0, 5).map((run) => (
            <button
              key={run.id}
              className={evalRun?.id === run.id ? "selected" : ""}
              onClick={() => void select(run.id)}
              disabled={loadingId === run.id}
              title={`Open ${run.profile} eval ${shortId(run.id)}`}
            >
              <Clock3 size={14} />
              <span>{run.profile}</span>
              <strong className={run.status}>{run.status}</strong>
              <small>{shortId(run.id)}</small>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function SkillsPanel({ skills }: { skills: Skill[] }) {
  return (
    <div className="panelStack">
      <div className="sectionHeader">
        <Library size={17} />
        <h2>Skills</h2>
      </div>
      {skills.length === 0 ? (
        <span className="muted">No skills registered.</span>
      ) : (
        skills.map((skill) => (
          <article className="skillItem" key={skill.name}>
            <div className="approvalTop">
              <strong>{skill.name}</strong>
              <span className="pill">{skill.risk_level}</span>
            </div>
            <p>{skill.description}</p>
            <div className="evalCases">
              {skill.allowed_tools.slice(0, 4).map((tool) => (
                <span key={tool}>{tool}</span>
              ))}
            </div>
            {(skill.dependencies.length > 0 || skill.eval_cases.length > 0) && (
              <div className="skillMeta">
                {skill.dependencies.length > 0 && <small>{skill.dependencies.length} deps</small>}
                {skill.eval_cases.length > 0 && <small>{skill.eval_cases.length} evals</small>}
                {skill.input_schema && <small>schema</small>}
              </div>
            )}
          </article>
        ))
      )}
    </div>
  );
}

function RiskPill({ risk }: { risk: string }) {
  return <span className={`riskPill ${risk}`}>{risk}</span>;
}

function profileLabel(profile: PublicConfig["model"]["fast"]) {
  const model = profile.model || profile.name;
  return `${profile.name} · ${model} · ${profile.context_tokens.toLocaleString()} ctx${profile.mtp ? " · MTP" : ""}`;
}

function adapterLabel(adapter: { backend: string; base_url: string; token: string }) {
  const target = adapter.base_url || "local files";
  const token = adapter.token ? "token configured" : "no token";
  return `${adapter.backend} · ${target} · ${token}`;
}

function rateLimitLabel(limit?: { enabled: boolean; requests_per_minute: number; burst: number }) {
  if (!limit?.enabled) return "disabled";
  return `${limit.requests_per_minute}/min · burst ${limit.burst}`;
}

function retentionLabel(days: number) {
  if (!days || days <= 0) return "no auto prune";
  return `${days}d retention`;
}

function parseToolList(value: string) {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of value.split(/[\n,]/)) {
    const tool = raw.trim();
    if (!tool || seen.has(tool)) continue;
    seen.add(tool);
    out.push(tool);
  }
  return out;
}

function formatPreferences(preferences: Record<string, string>) {
  return Object.entries(preferences)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, value]) => `${key}=${value}`)
    .join("\n");
}

function parsePreferences(value: string) {
  const preferences: Record<string, string> = {};
  for (const line of value.split(/\n/)) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const separator = trimmed.indexOf("=");
    if (separator === -1) {
      throw new Error("Preferences use key=value lines");
    }
    const key = trimmed.slice(0, separator).trim();
    const itemValue = trimmed.slice(separator + 1).trim();
    if (!key) {
      throw new Error("Preference keys are required");
    }
    preferences[key] = itemValue;
  }
  return preferences;
}

function shortId(id: string) {
  return id.slice(0, 10);
}

function formatLatency(calls?: ModelCall[]) {
  if (!calls || calls.length === 0) return "0 ms";
  const total = calls.reduce((sum, call) => sum + call.latency_ms, 0);
  return `${Math.round(total / calls.length)} ms avg`;
}

function formatTime(value: string) {
  return new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" }).format(new Date(value));
}
