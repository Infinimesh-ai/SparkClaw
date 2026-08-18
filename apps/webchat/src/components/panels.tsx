// Side panel components: timeline, trace, approvals, memory, status,
// artifacts, episodes, evals, and settings.
import { useEffect, useRef, useState } from "react";
import type * as React from "react";
import {
  Activity,
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
  ListChecks,
  MonitorUp,
  MemoryStick,
  Pencil,
  Plus,
  RefreshCw,
  Save,
  ScrollText,
  Send,
  Settings,
  ShieldAlert,
  ShieldCheck,
  Terminal,
  ThumbsDown,
  ThumbsUp,
  Trash2,
  UserRound,
  X
} from "lucide-react";
import type {
  Approval,
  ApprovalPresentation,
  ArtifactObject,
  AuditEvent,
  Client,
  ConnectorStatus,
  EpisodeSummary,
  EvalRun,
  Memory,
  MemoryCandidate,
  ModelCall,
  NotificationBinding,
  OwnerProfile,
  PublicConfig,
  ReadyStatus,
  RunTrace,
  ToolCall,
  TraceMetadata
} from "../api/types";
import type { Copy as CopyText, Language } from "../i18n";
import {
  bindingStatusLabel,
  cssToken,
  formatLatency,
  formatPreferences,
  formatRisk,
  formatState,
  formatTime,
  isImageLikeQR,
  parsePreferences,
  parseToolList,
  profileLabel,
  qrImageSource,
  rateLimitLabel,
  retentionLabel,
  shortId,
  stripSystemArgs
} from "../lib/format";
import {
  bindingsForConnector,
  connectorBindingStartDisabled,
  isBindingSetupPending,
  pendingBindingPollKey
} from "../lib/connectors";
import { ExternalMCPSettings } from "./externalMCPSettings";

export function ToolTimelinePanel({ calls, text, onTrace }: { calls: ToolCall[]; text: CopyText; onTrace: (runId: string) => void }) {
  return (
    <div className="panelStack">
      <SectionHeader icon={<FileSearch size={17} />} title={text.timeline.title} />
      {calls.length === 0 ? (
        <span className="muted">{text.timeline.empty}</span>
      ) : (
        calls.map((call) => <ToolCallItem key={call.id} call={call} text={text} onTrace={onTrace} />)
      )}
    </div>
  );
}

export function ToolCallItem({ call, text, onTrace }: { call: ToolCall; text: CopyText; onTrace: (runId: string) => void }) {
  const Icon = call.tool.includes("shell")
    ? Terminal
    : call.tool.includes("memory")
      ? Database
      : call.tool.includes("browser")
        ? Globe2
        : FileSearch;
  return (
    <article className={`toolCall ${call.risk} ${cssToken(call.status)}`}>
      <div className="toolIcon">
        <Icon size={16} />
      </div>
      <div className="toolBody">
        <div className="toolTitle">
          <strong title={call.tool}>{call.tool}</strong>
          <span className="toolBadges">
            <button className="miniIconButton" onClick={() => onTrace(call.run_id)} title={text.timeline.openTrace}>
              <ScrollText size={14} />
            </button>
            <RiskPill risk={call.risk} text={text} />
          </span>
        </div>
        <span className="statusLine">{formatState(call.status, text)}</span>
        {call.observation_summary && <small>{call.observation_summary}</small>}
        {call.error && <p className="compactError">{call.error}</p>}
        {call.approval_id && <small>{text.timeline.approval} {shortId(call.approval_id)}</small>}
        {Object.keys(stripSystemArgs(call.arguments)).length > 0 && <JsonBlock value={stripSystemArgs(call.arguments)} />}
      </div>
    </article>
  );
}

export function TracePanel({
  trace,
  traces,
  loading,
  text,
  language,
  onOpen
}: {
  trace: RunTrace | null;
  traces: TraceMetadata[];
  loading: boolean;
  text: CopyText;
  language: Language;
  onOpen: (runId: string) => void;
}) {
  return (
    <div className="panelStack">
      <SectionHeader icon={<ScrollText size={17} />} title={text.trace.title} />
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
              <span>{formatState(item.state, text)}</span>
              <strong>{item.tool_call_count} {text.units.tools}</strong>
              <small>{shortId(item.run_id)}</small>
            </button>
          ))}
        </div>
      )}
      {loading ? (
        <span className="muted">{text.trace.loading}</span>
      ) : !trace ? (
        <span className="muted">{text.trace.empty}</span>
      ) : (
        <>
          <article className="traceSummary">
            <div className="approvalTop">
              <strong>{formatState(trace.run.state, text)}</strong>
              <span className="pill">{shortId(trace.run.id)}</span>
            </div>
            <dl className="statusGrid compact">
              <dt>{text.trace.lane}</dt>
              <dd>{trace.model.lane}</dd>
              <dt>{text.trace.model}</dt>
              <dd>{trace.model.model}</dd>
              <dt>{text.trace.calls}</dt>
              <dd>{trace.model_calls?.length ?? 0}</dd>
              <dt>{text.trace.tokens}</dt>
              <dd>{(trace.model_calls ?? []).reduce((sum, call) => sum + call.total_tokens, 0)}</dd>
              <dt>{text.trace.latency}</dt>
              <dd>{formatLatency(trace.model_calls, text)}</dd>
              <dt>{text.trace.risk}</dt>
              <dd>{formatRisk(trace.run.risk, text)}</dd>
              <dt>{text.trace.tools}</dt>
              <dd>{trace.tool_calls.length}</dd>
              <dt>{text.trace.approvals}</dt>
              <dd>{trace.approvals.length}</dd>
              <dt>{text.trace.feedback}</dt>
              <dd>{trace.feedback?.length ?? 0}</dd>
              <dt>{text.trace.audit}</dt>
              <dd>{trace.audit.length}</dd>
            </dl>
          </article>
          <article className="traceSummary">
            <strong>{text.trace.modelNote}</strong>
            <p>{trace.model.content}</p>
          </article>
          {trace.model_calls && trace.model_calls.length > 0 && (
            <div className="traceList">
              {trace.model_calls.map((call) => (
                <article className="traceRow" key={call.id}>
                  <span>{call.operation} · {call.lane}</span>
                  <small>
                    {formatState(call.status, text)} · {call.total_tokens} {text.units.tokens} · {call.latency_ms} ms
                  </small>
                </article>
              ))}
            </div>
          )}
          {trace.episode && <EpisodeCard episode={trace.episode} compact text={text} />}
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
                <small>{call.observation_summary || formatState(call.status, text)}</small>
              </article>
            ))}
          </div>
          <span className="muted">{formatTime(trace.run.started_at, language)}</span>
        </>
      )}
    </div>
  );
}

export function ApprovalPanel({
  approvals,
  text,
  resolvingId,
  onResolve,
  onModify,
  onModifyPlan
}: {
  approvals: Approval[];
  text: CopyText;
  resolvingId?: string;
  onResolve: (id: string, accepted: boolean) => void;
  onModify: (id: string, args: Record<string, unknown>) => void;
  onModifyPlan: (id: string, plan: string) => void;
}) {
  const [editing, setEditing] = useState("");
  const [draft, setDraft] = useState("");
  const [parseError, setParseError] = useState("");

  function startEdit(approval: Approval) {
    setEditing(approval.id);
    setDraft(approval.source === "happy_team_plan" ? approval.external_context?.plan ?? "" : JSON.stringify(stripSystemArgs(approval.arguments), null, 2));
    setParseError("");
  }

  function saveEdit(approval: Approval) {
    if (approval.source === "happy_team_plan") {
      onModifyPlan(approval.id, draft);
      setEditing("");
      setParseError("");
      return;
    }
    try {
      const parsed = JSON.parse(draft) as Record<string, unknown>;
      onModify(approval.id, parsed);
      setEditing("");
      setParseError("");
    } catch {
      setParseError(text.approval.invalidJson);
    }
  }

  return (
    <div className="panelStack">
      <SectionHeader icon={<Inbox size={17} />} title={text.approval.title} />
      {approvals.length === 0 ? (
        <span className="muted">{text.approval.empty}</span>
      ) : (
        approvals.map((approval) => {
          const happyPlan = approval.source === "happy_team_plan" ? approval.external_context : undefined;
          const planAvailable = happyPlan?.plan_availability === "available";
          const contextBound = Boolean(approval.policy_context);
          const workspaceAccess = approval.presentation?.kind === "external_mcp_workspace_data_access" ? approval.presentation : undefined;
          const resolving = resolvingId === approval.id;
          return (
          <article className={`approvalItem ${approval.risk}`} key={approval.id}>
            <div className="approvalTop">
              <strong>{workspaceAccess ? text.approval.workspaceDataTitle : approval.summary}</strong>
              <RiskPill risk={approval.risk} text={text} />
            </div>
            <p>{workspaceAccess ? text.approval.workspaceDataReason : approval.reason}</p>
            {!workspaceAccess && approval.resources.length > 0 && (
              <div className="evalCases">
                {approval.resources.map((resource) => (
                  <span key={resource}>{resource}</span>
                ))}
              </div>
            )}
            {workspaceAccess ? (
              <WorkspaceApprovalDetails presentation={workspaceAccess} argumentsValue={approval.arguments} text={text} />
            ) : happyPlan ? (
              <div className="happyPlanDetails">
                <span className="approvalSource">{text.approval.happyTeam}</span>
                <div>
                  <small>{text.approval.taskTitle}</small>
                  <p>{happyPlan.title}</p>
                </div>
                <div>
                  <small>{text.approval.taskGoal}</small>
                  <p>{happyPlan.goal_prompt}</p>
                </div>
                <div>
                  <small>{text.approval.taskPlan}</small>
                  {planAvailable ? <pre>{happyPlan.plan ?? ""}</pre> : <p className="compactError">{text.approval.planUnavailable}</p>}
                </div>
              </div>
            ) : (
              <JsonBlock value={stripSystemArgs(approval.arguments)} />
            )}
            {approval.status === "pending" ? (
              <>
                {!contextBound && editing === approval.id && (
                  <div className="approvalEdit">
                    <textarea value={draft} onChange={(event) => setDraft(event.target.value)} />
                    {parseError && <span className="compactError">{parseError}</span>}
                  </div>
                )}
                <div className="buttonRow">
                  <button className="approve" onClick={() => onResolve(approval.id, true)} title={planAvailable || !happyPlan ? text.common.approve : text.approval.planUnavailable} disabled={resolving || Boolean(happyPlan && !planAvailable)}>
                    {resolving ? <RefreshCw className="spin" size={16} /> : <Check size={16} />}
                  </button>
                  {!contextBound && (
                    <button className="edit" onClick={() => (editing === approval.id ? saveEdit(approval) : startEdit(approval))} title={happyPlan ? text.approval.editPlan : text.approval.editArguments} disabled={resolving || Boolean(happyPlan && !planAvailable)}>
                      <Pencil size={15} />
                    </button>
                  )}
                  <button className="reject" onClick={() => onResolve(approval.id, false)} title={text.common.reject} disabled={resolving}>
                    <X size={16} />
                  </button>
                </div>
              </>
            ) : (
              <span className="resolved">{formatState(approval.status, text)}</span>
            )}
          </article>
          );
        })
      )}
    </div>
  );
}

function WorkspaceApprovalDetails({
  presentation,
  argumentsValue,
  text
}: {
  presentation: ApprovalPresentation;
  argumentsValue: Record<string, unknown>;
  text: CopyText;
}) {
  return (
    <div className="workspaceApprovalDetails">
      <div className="workspaceApprovalIdentity">
        <ShieldCheck size={16} />
        <span>
          <small>{text.approval.requester}</small>
          <strong>{presentation.requester}</strong>
        </span>
      </div>
      <div>
        <small>{text.approval.requestedData}</small>
        <div className="workspaceApprovalLocators">
          {(presentation.locators ?? []).map((locator, index) => (
            <div key={`${locator.path || locator.name || locator.query || "locator"}-${index}`}>
              <span>{locator.caption || locator.path || locator.name || locator.query}</span>
              {locator.caption && <code>{locator.path || locator.name || locator.query}</code>}
              <em>{text.approval.unverified}</em>
            </div>
          ))}
        </div>
      </div>
      <dl>
        <dt>{text.approval.access}</dt>
        <dd>{workspaceAccessLabel(presentation.access_class, text)}</dd>
        <dt>{text.approval.output}</dt>
        <dd>{workspaceOutputLabel(presentation.output_class, text)}</dd>
        <dt>{text.approval.returnTo}</dt>
        <dd>{workspaceReturnTarget(presentation, text)}</dd>
        <dt>{text.approval.scope}</dt>
        <dd>{text.approval.singleOperation}</dd>
      </dl>
      <details>
        <summary>{text.approval.technicalDetails}</summary>
        <JsonBlock value={stripSystemArgs(argumentsValue)} />
      </details>
    </div>
  );
}

function workspaceAccessLabel(accessClass: string | undefined, text: CopyText) {
  if (accessClass === "workspace_derivative_disclosure") return text.approval.derivativeDisclosure;
  return text.approval.workspaceRead;
}

function workspaceOutputLabel(outputClass: string | undefined, text: CopyText) {
  if (outputClass === "response_media") return text.approval.responseMedia;
  if (outputClass === "document_derivative") return text.approval.documentDerivative;
  if (outputClass === "document_content") return text.approval.documentContent;
  return outputClass || text.common.notSet;
}

function workspaceReturnTarget(presentation: ApprovalPresentation, text: CopyText) {
  if (presentation.return_route.mode === "source") return text.approval.originalMCPConversation;
  if (presentation.return_route.mode === "endpoint") return presentation.return_route.endpoint_id || text.approval.approvedDestination;
  return text.approval.noReturn;
}

export function MemoryPanel({
  candidates,
  memories,
  text,
  onResolve,
  onUpdate,
  onDelete,
  onExport
}: {
  candidates: MemoryCandidate[];
  memories: Memory[];
  text: CopyText;
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
      <SectionHeader icon={<MemoryStick size={17} />} title={text.memory.title} />
      {candidates.length === 0 ? (
        <span className="muted">{text.memory.emptyCandidates}</span>
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
                <button className="approve" onClick={() => onResolve(candidate.id, true)} title={text.memory.acceptMemory}>
                  <Check size={16} />
                </button>
                <button className="reject" onClick={() => onResolve(candidate.id, false)} title={text.memory.rejectMemory}>
                  <X size={16} />
                </button>
              </div>
            ) : (
              <span className="resolved">{formatState(candidate.status, text)}</span>
            )}
          </article>
        ))
      )}
      <div className="sectionHeader smallHeader">
        <Database size={15} />
        <h2>{text.memory.accepted}</h2>
        <button className="miniIconButton headerAction" onClick={() => void archiveExport()} disabled={exporting} title={text.memory.archiveExport}>
          <Download size={14} />
        </button>
      </div>
      <dl className="statusGrid compact memoryCounts">
        <dt>{text.memory.accepted}</dt>
        <dd>{memories.length}</dd>
        <dt>{text.memory.pending}</dt>
        <dd>{candidates.filter((candidate) => candidate.status === "pending").length}</dd>
      </dl>
      {memories.map((memory) => (
        <article className="memoryItem" key={memory.id}>
          {editingId === memory.id ? (
            <div className="memoryEdit">
              <input
                aria-label={text.memory.kind}
                value={editKind}
                onChange={(event) => setEditKind(event.target.value)}
                disabled={savingId === memory.id}
              />
              <textarea
                aria-label={text.memory.content}
                value={editContent}
                onChange={(event) => setEditContent(event.target.value)}
                disabled={savingId === memory.id}
              />
              <div className="buttonRow">
                <button
                  className="approve"
                  onClick={() => void saveEdit(memory)}
                  disabled={!editKind.trim() || !editContent.trim() || savingId === memory.id}
                  title={text.memory.saveMemory}
                >
                  <Check size={16} />
                </button>
                <button className="edit" onClick={cancelEdit} disabled={savingId === memory.id} title={text.memory.cancelEdit}>
                  <X size={16} />
                </button>
              </div>
            </div>
          ) : (
            <>
              <div className="approvalTop">
                <strong>{memory.kind}</strong>
                <div className="buttonRow compactButtons">
                  <button className="edit" onClick={() => startEdit(memory)} disabled={savingId === memory.id} title={text.memory.editMemory}>
                    <Pencil size={15} />
                  </button>
                  <button className="reject" onClick={() => void removeMemory(memory)} disabled={savingId === memory.id} title={text.memory.deleteMemory}>
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

export function StatusStack({
  ready,
  modelCalls,
  auditEvents,
  artifacts,
  episodes,
  evalRun,
  evalRuns,
  text,
  language,
  onRunEval,
  onSelectEval,
  onError
}: {
  ready: ReadyStatus | null;
  modelCalls: ModelCall[];
  auditEvents: AuditEvent[];
  artifacts: ArtifactObject[];
  episodes: EpisodeSummary[];
  evalRun: EvalRun | null;
  evalRuns: EvalRun[];
  text: CopyText;
  language: Language;
  onRunEval: () => Promise<void>;
  onSelectEval: (id: string) => Promise<void>;
  onError: (message: string) => void;
}) {
  return (
    <div className="panelStack">
      <StatusPanel ready={ready} modelCalls={modelCalls} auditEvents={auditEvents} text={text} />
      <ArtifactPanel artifacts={artifacts} text={text} />
      <EpisodePanel episodes={episodes} text={text} />
      <EvalPanel evalRun={evalRun} evalRuns={evalRuns} text={text} language={language} onRun={onRunEval} onSelect={onSelectEval} onError={onError} />
    </div>
  );
}

export function StatusPanel({ ready, modelCalls, auditEvents, text }: { ready: ReadyStatus | null; modelCalls: ModelCall[]; auditEvents: AuditEvent[]; text: CopyText }) {
  const recentModelCalls = modelCalls.slice(-6).reverse();
  const recentAuditEvents = auditEvents.slice(-6).reverse();
  return (
    <div className="panelStack nestedPanel">
      <SectionHeader icon={<Clock3 size={17} />} title={text.status.runtime} />
      {!ready ? (
        <span className="muted">{text.common.gatewayUnavailable}</span>
      ) : (
        <dl className="statusGrid">
          <dt>{text.status.gateway}</dt>
          <dd>{ready.gateway_binding}</dd>
          <dt>{text.status.model}</dt>
          <dd>{ready.model_mode}</dd>
          <dt>{text.status.rateLimit}</dt>
          <dd>{rateLimitLabel(ready.rate_limit, text)}</dd>
          <dt>{text.status.workspace}</dt>
          <dd>{ready.workspace_root}</dd>
          <dt>{text.status.trace}</dt>
          <dd>{ready.trace_dir}</dd>
          <dt>{text.status.state}</dt>
          <dd>{ready.state_backend} · {ready.state_path}</dd>
          {ready.state_dsn && (
            <>
              <dt>{text.status.dsn}</dt>
              <dd>{ready.state_dsn}</dd>
            </>
          )}
        </dl>
      )}
      <div className="diagnosticList">
        <strong>{text.status.modelCalls}</strong>
        {recentModelCalls.length === 0 ? (
          <span className="muted">{text.status.noModelCalls}</span>
        ) : (
          recentModelCalls.map((call) => (
            <article className="diagnosticRow" key={call.id}>
              <div>
                <span>{call.operation} · {call.lane}</span>
                <small>{call.profile || call.model}</small>
              </div>
              <small>
                {formatState(call.status, text)} · {call.total_tokens} {text.units.tokens} · {call.latency_ms} ms
              </small>
            </article>
          ))
        )}
      </div>
      <div className="diagnosticList">
        <strong>{text.status.audit}</strong>
        {recentAuditEvents.length === 0 ? (
          <span className="muted">{text.status.noAudit}</span>
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

export function ArtifactPanel({ artifacts, text }: { artifacts: ArtifactObject[]; text: CopyText }) {
  return (
    <div className="panelStack nestedPanel">
      <SectionHeader icon={<Database size={17} />} title={text.status.artifacts} />
      {artifacts.length === 0 ? (
        <span className="muted">{text.status.noArtifacts}</span>
      ) : (
        <div className="artifactList">
          {artifacts.slice(0, 5).map((artifact) => (
            <article className="artifactItem" key={artifact.id} title={artifact.path || artifact.uri}>
              <div className="approvalTop">
                <strong>{artifact.kind}</strong>
                <span className="pill">{artifact.backend}</span>
              </div>
              <span>{artifact.uri}</span>
              <small>{artifact.bytes} {text.units.bytes} · {artifact.content_type}</small>
            </article>
          ))}
        </div>
      )}
    </div>
  );
}

export function EpisodePanel({ episodes, text }: { episodes: EpisodeSummary[]; text: CopyText }) {
  return (
    <div className="panelStack nestedPanel">
      <SectionHeader icon={<ScrollText size={17} />} title={text.status.episodes} />
      {episodes.length === 0 ? (
        <span className="muted">{text.status.noEpisodes}</span>
      ) : (
        episodes.slice(0, 5).map((episode) => <EpisodeCard key={episode.id} episode={episode} text={text} />)
      )}
    </div>
  );
}

export function EpisodeCard({ episode, text, compact = false }: { episode: EpisodeSummary; text: CopyText; compact?: boolean }) {
  return (
    <article className={`episodeItem ${compact ? "compactEpisode" : ""}`}>
      <div className="approvalTop">
        <strong>{episode.outcome}</strong>
        <span className="pill">{shortId(episode.run_id)}</span>
      </div>
      <p>{episode.summary}</p>
      <dl className="statusGrid compact">
        <dt>{text.trace.lane}</dt>
        <dd>{episode.model_lane}</dd>
        <dt>{text.trace.risk}</dt>
        <dd>{formatRisk(episode.risk, text)}</dd>
        <dt>Repair</dt>
        <dd>{episode.repair_performed ? text.common.yes : text.common.no}</dd>
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

export function EvalPanel({
  evalRun,
  evalRuns,
  text,
  language,
  onRun,
  onSelect,
  onError
}: {
  evalRun: EvalRun | null;
  evalRuns: EvalRun[];
  text: CopyText;
  language: Language;
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
      onError(err instanceof Error ? err.message : text.errors.eval);
    } finally {
      setRunning(false);
    }
  }
  async function select(id: string) {
    try {
      setLoadingId(id);
      await onSelect(id);
    } catch (err) {
      onError(err instanceof Error ? err.message : text.errors.eval);
    } finally {
      setLoadingId("");
    }
  }
  return (
    <div className="panelStack nestedPanel evalPanel">
      <SectionHeader icon={<ListChecks size={17} />} title={text.status.smokeEval} />
      <button className="secondaryButton" onClick={() => void run()} disabled={running} title={text.status.smokeEval}>
        <ListChecks size={16} />
        <span>{running ? text.common.running : text.common.run}</span>
      </button>
      {!evalRun ? (
        <span className="muted">{text.status.noEval}</span>
      ) : (
        <article className={`evalResult ${evalRun.status}`}>
          <div className="approvalTop">
            <strong>{formatState(evalRun.status, text)}</strong>
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
          {evalRuns.slice(0, 5).map((runItem) => (
            <button
              key={runItem.id}
              className={evalRun?.id === runItem.id ? "selected" : ""}
              onClick={() => void select(runItem.id)}
              disabled={loadingId === runItem.id}
              title={`${runItem.profile} ${shortId(runItem.id)}`}
            >
              <Clock3 size={14} />
              <span>{runItem.profile}</span>
              <strong className={runItem.status}>{formatState(runItem.status, text)}</strong>
              <small>{formatTime(runItem.started_at, language)}</small>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

export function SettingsPanel({
  runtimeConfig,
  ownerProfile,
  clients,
  connectors,
  notificationBindings,
  text,
  language,
  onUpdateOwner,
  onRevokeClient,
  onStartNotificationBinding,
  onRefreshNotificationBinding,
  onOpenNotificationBindingBrowser,
  onRevokeNotificationBinding,
  onUpdateConnector,
  onUpdatePolicy
}: {
  runtimeConfig: PublicConfig | null;
  ownerProfile: OwnerProfile | null;
  clients: Client[];
  connectors: ConnectorStatus[];
  notificationBindings: NotificationBinding[];
  text: CopyText;
  language: Language;
  onUpdateOwner: (displayName: string, email: string, preferences: Record<string, string>) => Promise<void>;
  onRevokeClient: (id: string) => Promise<void>;
  onStartNotificationBinding: (channel: string, botToken?: string) => Promise<void>;
  onRefreshNotificationBinding: (id: string, signal?: AbortSignal) => Promise<NotificationBinding>;
  onOpenNotificationBindingBrowser: (id: string) => Promise<void>;
  onRevokeNotificationBinding: (id: string) => Promise<void>;
  onUpdateConnector: (channel: string, enabled: boolean, expectedVersion: number) => Promise<ConnectorStatus>;
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
  const [bindingBusy, setBindingBusy] = useState(false);
  const [connectorBusy, setConnectorBusy] = useState("");
  const [bindingError, setBindingError] = useState("");
  const [telegramToken, setTelegramToken] = useState("");
  const refreshBindingRef = useRef(onRefreshNotificationBinding);
  const pendingBindingKey = pendingBindingPollKey(notificationBindings);

  useEffect(() => {
    refreshBindingRef.current = onRefreshNotificationBinding;
  }, [onRefreshNotificationBinding]);

  useEffect(() => {
    const pendingIDs = JSON.parse(pendingBindingKey) as string[];
    if (pendingIDs.length === 0) return;
    let cancelled = false;
    let timer = 0;
    let controller: AbortController | null = null;
    const poll = () => {
      controller = new AbortController();
      void Promise.allSettled(pendingIDs.map((id) => refreshBindingRef.current(id, controller?.signal)))
        .then((results) => {
          controller = null;
          if (cancelled) return;
          const rejected = results.find((result) => result.status === "rejected");
          if (rejected?.status === "rejected") {
            setBindingError(rejected.reason instanceof Error ? rejected.reason.message : text.errors.binding);
            timer = window.setTimeout(poll, 4000);
            return;
          }
          const hasStillPending = results.some((result) => result.status === "fulfilled" && isBindingSetupPending(result.value));
          if (!hasStillPending) return;
          timer = window.setTimeout(poll, 2000);
        })
        .catch((err: unknown) => {
          if (!cancelled) {
            setBindingError(err instanceof Error ? err.message : text.errors.binding);
            timer = window.setTimeout(poll, 4000);
          }
        });
    };
    timer = window.setTimeout(poll, 1000);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
      controller?.abort();
    };
  }, [pendingBindingKey, text.errors.binding]);

  if (!runtimeConfig) {
    return (
      <div className="panelStack">
        <SectionHeader icon={<Settings size={17} />} title={text.settings.title} />
        <span className="muted">{text.settings.unavailable}</span>
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
      await onUpdateOwner(ownerName, ownerEmail, parsePreferences(ownerPrefsText, text));
      cancelOwnerEdit();
    } catch (err) {
      setOwnerError(err instanceof Error ? err.message : text.errors.ownerUpdate);
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

  async function startBinding(channel: string) {
    if (bindingBusy) return;
    const connector = connectors.find((item) => item.channel === channel);
    const needsSecret = connector?.setup_kind === "secret";
    const botToken = needsSecret ? telegramToken.trim() : "";
    if (needsSecret && !botToken) {
      setBindingError(text.settings.telegramTokenRequired);
      return;
    }
    setBindingBusy(true);
    setBindingError("");
    try {
      await onStartNotificationBinding(channel, botToken);
      if (needsSecret) {
        setTelegramToken("");
      }
    } catch (err) {
      setBindingError(err instanceof Error ? err.message : text.errors.binding);
    } finally {
      setBindingBusy(false);
    }
  }

  async function toggleConnector(connector: ConnectorStatus) {
    if (connectorBusy) return;
    setConnectorBusy(connector.channel);
    setBindingError("");
    try {
      await onUpdateConnector(connector.channel, !connector.enabled, connector.version);
    } catch (err) {
      setBindingError(err instanceof Error ? err.message : text.errors.connectorUpdate);
    } finally {
      setConnectorBusy("");
    }
  }

  async function refreshBinding(id: string) {
    if (bindingBusy) return;
    setBindingBusy(true);
    setBindingError("");
    try {
      await onRefreshNotificationBinding(id);
    } catch (err) {
      setBindingError(err instanceof Error ? err.message : text.errors.binding);
    } finally {
      setBindingBusy(false);
    }
  }

  async function openBindingBrowser(id: string) {
    if (bindingBusy) return;
    setBindingBusy(true);
    setBindingError("");
    try {
      await onOpenNotificationBindingBrowser(id);
    } catch (err) {
      setBindingError(err instanceof Error ? err.message : text.errors.binding);
    } finally {
      setBindingBusy(false);
    }
  }

  async function revokeBinding(id: string) {
    if (bindingBusy) return;
    setBindingBusy(true);
    setBindingError("");
    try {
      await onRevokeNotificationBinding(id);
    } catch (err) {
      setBindingError(err instanceof Error ? err.message : text.errors.binding);
    } finally {
      setBindingBusy(false);
    }
  }

  function renderNotificationBindingSection(connector: ConnectorStatus) {
    const channel = connector.channel;
    const bindings = bindingsForConnector(notificationBindings, channel);
    const isTelegram = channel === "telegram";
    const isSecret = connector.setup_kind === "secret";
    const Icon = isTelegram ? Send : KeyRound;
    const title = isTelegram ? text.settings.telegramBinding : channel === "weixin" ? text.settings.weixinBinding : channel;
    const addTitle = isTelegram ? text.settings.addTelegramBinding : text.settings.addWeixinBinding;
    const bindLabel = isTelegram ? text.settings.bindTelegram : text.settings.bindWeixin;
    const missing = isTelegram ? text.settings.telegramBindingMissing : text.settings.bindingMissing;
    const waitingInstruction = text.settings.scanWeixin;
    const scannedInstruction = text.settings.scannedWeixin;
    const tokenEditable = isSecret && connector.enabled && connector.binding_startable;
    const startDisabled = connectorBindingStartDisabled(connector, bindingBusy || connectorBusy !== "", Boolean(telegramToken.trim()));
    const capabilityNote = connectorStatusLabel(connector, text);
    const toggleTitle = connector.enabled ? text.settings.disableConnector : text.settings.enableConnector;
    return (
      <article className="settingsBlock" key={channel}>
        <div className="approvalTop">
          <span className="settingsTitle">
            <Icon size={15} />
            <strong>{title}</strong>
          </span>
          <div className="buttonRow compactButtons">
            <label className="connectorToggle" title={toggleTitle}>
              <input
                type="checkbox"
                checked={connector.enabled}
                onChange={() => void toggleConnector(connector)}
                disabled={connectorBusy !== ""}
                aria-label={toggleTitle}
              />
              <span aria-hidden="true" />
            </label>
            <button className="approve" onClick={() => void startBinding(channel)} disabled={startDisabled} title={addTitle}>
              <Plus size={15} />
            </button>
          </div>
        </div>
        {capabilityNote && <span className="muted bindingCapability">{capabilityNote}</span>}
        {isSecret && (
          <label className="inputGroup compact telegramTokenInput">
            <span>{text.settings.telegramToken}</span>
            <input
              type="password"
              value={telegramToken}
              onChange={(event) => setTelegramToken(event.target.value)}
              placeholder={text.settings.telegramTokenPlaceholder}
              autoComplete="new-password"
              spellCheck={false}
              disabled={bindingBusy || connectorBusy !== "" || !tokenEditable}
            />
          </label>
        )}
        {bindings.length > 0 ? (
          <div className="bindingList">
            {bindings.map((binding) => (
              <div className="bindingItem" key={binding.id}>
                <div className="bindingItemTop">
                  <div>
                    <strong>{binding.display_name || binding.external_user_id || binding.account_id || binding.id}</strong>
                    <span className="muted">{bindingStatusLabel(binding.status, text)}{binding.default_for_channel ? ` · ${text.settings.defaultBinding}` : ""}</span>
                  </div>
                  <div className="buttonRow compactButtons">
                    <button className="edit" onClick={() => void refreshBinding(binding.id)} disabled={bindingBusy || !isBindingSetupPending(binding)} title={text.common.refresh}>
                      <RefreshCw size={15} />
                    </button>
                    <button className="reject" onClick={() => void revokeBinding(binding.id)} disabled={bindingBusy || binding.status === "revoked"} title={text.settings.revokeBinding}>
                      <Trash2 size={15} />
                    </button>
                  </div>
                </div>
                <dl className="statusGrid compact">
                  <dt>{text.settings.bindingProvider}</dt>
                  <dd>{binding.provider}</dd>
                  <dt>{text.settings.bindingAccount}</dt>
                  <dd>{binding.external_user_id || binding.account_id || text.common.notSet}</dd>
                  <dt>{text.settings.bindingContext}</dt>
                  <dd>{binding.context_token || text.common.notSet}</dd>
                  <dt>{text.settings.bindingBaseUrl}</dt>
                  <dd>{binding.base_url || text.common.notSet}</dd>
                  <dt>{text.settings.bindingExpires}</dt>
                  <dd>{binding.expires_at ? formatTime(binding.expires_at, language) : text.common.none}</dd>
                </dl>
                {binding.status === "waiting_scan" && (
                  <div className="bindingQr">
                    {binding.qr_code_image || isImageLikeQR(binding.qr_code_url) ? (
                      <img src={qrImageSource(binding.qr_code_image || binding.qr_code_url)} alt={waitingInstruction} />
                    ) : binding.qr_code_url ? (
                      <button className="secondaryButton" onClick={() => void openBindingBrowser(binding.id)} disabled={bindingBusy}>
                        <MonitorUp size={15} />
                        <span>{text.settings.openWeixinLogin}</span>
                      </button>
                    ) : (
                      <span className="muted">{text.settings.bindingQrUnavailable}</span>
                    )}
                    <small>{waitingInstruction}</small>
                  </div>
                )}
                {binding.status === "waiting_confirm" && !isTelegram && (
                  <div className="bindingScanned">
                    <CheckCircle2 size={18} />
                    <span>{scannedInstruction}</span>
                  </div>
                )}
                {isSecret && binding.status === "active" && !binding.external_user_id && !binding.context_token && (
                  <div className="bindingScanned">
                    <Send size={18} />
                    <span>{text.settings.telegramAwaitingMessage}</span>
                  </div>
                )}
                {binding.last_error && <span className="compactError">{binding.last_error}</span>}
              </div>
            ))}
          </div>
        ) : (
          <div className="bindingEmpty">
            <span className="muted">{missing}</span>
            <button className="secondaryButton" onClick={() => void startBinding(channel)} disabled={startDisabled}>
              <Icon size={15} />
              <span>{bindLabel}</span>
            </button>
          </div>
        )}
      </article>
    );
  }

  return (
    <div className="panelStack">
      <SectionHeader icon={<Settings size={17} />} title={text.settings.title} />
      <ExternalMCPSettings
		connector={connectors.find((item) => item.channel === "mcp")}
		text={text}
		language={language}
		onUpdateConnector={onUpdateConnector}
	  />
      {connectors.filter((item) => item.channel !== "mcp").map(renderNotificationBindingSection)}
      {bindingError && <span className="compactError">{bindingError}</span>}
      <article className="settingsBlock">
        <div className="approvalTop">
          <span className="settingsTitle">
            <UserRound size={15} />
            <strong>{text.settings.ownerProfile}</strong>
          </span>
          <div className="buttonRow compactButtons">
            {editingOwner ? (
              <>
                <button className="approve" onClick={() => void saveOwnerEdit()} disabled={savingOwner} title={text.settings.saveOwner}>
                  <Check size={15} />
                </button>
                <button className="edit" onClick={cancelOwnerEdit} disabled={savingOwner} title={text.settings.cancelOwner}>
                  <X size={15} />
                </button>
              </>
            ) : (
              <button className="edit" onClick={startOwnerEdit} title={text.settings.editOwner}>
                <Pencil size={15} />
              </button>
            )}
          </div>
        </div>
        {editingOwner ? (
          <div className="ownerEditor">
            <label>
              <span>{text.settings.name}</span>
              <input value={ownerName} onChange={(event) => setOwnerName(event.target.value)} disabled={savingOwner} />
            </label>
            <label>
              <span>{text.settings.email}</span>
              <input value={ownerEmail} onChange={(event) => setOwnerEmail(event.target.value)} disabled={savingOwner} />
            </label>
            <label>
              <span>{text.settings.preferences}</span>
              <textarea value={ownerPrefsText} onChange={(event) => setOwnerPrefsText(event.target.value)} disabled={savingOwner} />
            </label>
            {ownerError && <span className="compactError">{ownerError}</span>}
          </div>
        ) : ownerProfile ? (
          <>
            <dl className="statusGrid compact">
              <dt>{text.settings.name}</dt>
              <dd>{ownerProfile.display_name}</dd>
              <dt>{text.settings.email}</dt>
              <dd>{ownerProfile.email || text.common.notSet}</dd>
            </dl>
            <div className="evalCases">
              {Object.entries(preferences).map(([key, value]) => (
                <span key={key}>{key}:{value}</span>
              ))}
              {Object.keys(preferences).length === 0 && <span>{text.common.none}</span>}
            </div>
          </>
        ) : (
          <span className="muted">{text.settings.ownerUnavailable}</span>
        )}
      </article>
      <article className="settingsBlock">
        <div className="approvalTop">
          <strong>{text.settings.toolPolicy}</strong>
          <span className="pill">{policy.definition_count} {text.trace.tools}</span>
        </div>
        <dl className="statusGrid compact">
          <dt>{text.settings.file}</dt>
          <dd>{policy.policy_path}</dd>
          <dt>{text.settings.external}</dt>
          <dd>{policy.external_content_untrusted ? text.settings.untrusted : text.settings.trusted}</dd>
          <dt>{text.settings.dangerous}</dt>
          <dd>{policy.approval_required_for_dangerous_tools ? text.settings.approvalRequired : text.settings.notForced}</dd>
          <dt>{text.settings.verifier}</dt>
          <dd>{policy.dangerous_tools_deep_verification ? text.settings.deepCheck : text.settings.standard}</dd>
          <dt>{text.settings.sandbox}</dt>
          <dd>{policy.sandbox_required_for_mutating_tools ? text.settings.mutationsRequireSandbox : text.settings.notForced}</dd>
        </dl>
        <div className="evalCases">
          {riskCounts.map(([risk, count]) => (
            <span key={risk}>{formatRisk(risk, text)}:{count}</span>
          ))}
        </div>
      </article>
      <article className="settingsBlock">
        <div className="approvalTop">
          <strong>{text.settings.pairedClients}</strong>
          <span className="pill">{clients.length}</span>
        </div>
        {clients.length === 0 ? (
          <span className="muted">{text.settings.noClients}</span>
        ) : (
          <div className="clientList">
            {clients.map((client) => (
              <div className="clientItem" key={client.id}>
                <div>
                  <strong>{client.name}</strong>
                  <small>
                    {client.revoked_at
                      ? text.common.revoked
                      : client.last_seen_at
                        ? `${text.settings.seen} ${formatTime(client.last_seen_at, language)}`
                        : text.settings.notSeen}
                  </small>
                </div>
                {!client.revoked_at && (
                  <button className="reject" onClick={() => void revokeClient(client.id)} disabled={revokingClient === client.id} title={text.settings.revokeClient}>
                    <Trash2 size={14} />
                  </button>
                )}
              </div>
            ))}
          </div>
        )}
      </article>
      <article className="settingsBlock">
        <strong>{text.settings.definitionApprovalTools}</strong>
        <div className="evalCases">
          {policy.definition_approval_required_tools.map((tool) => (
            <span key={tool}>{tool}</span>
          ))}
          {policy.definition_approval_required_tools.length === 0 && <span>{text.common.none}</span>}
        </div>
      </article>
      <article className="settingsBlock">
        <strong>{text.settings.configApprovalAdditions}</strong>
        {editingPolicy ? (
          <div className="policyEditor">
            <label>
              <span>{text.settings.approval}</span>
              <textarea value={approvalText} onChange={(event) => setApprovalText(event.target.value)} disabled={savingPolicy} />
            </label>
          </div>
        ) : (
          <div className="evalCases">
            {policy.configured_approval_required_tools.map((tool) => (
              <span key={`configured-${tool}`}>{tool}</span>
            ))}
            {policy.configured_approval_required_tools.length === 0 && <span>{text.common.none}</span>}
          </div>
        )}
      </article>
      <article className="settingsBlock">
        <div className="approvalTop">
          <strong>{text.settings.deniedTools}</strong>
          <div className="buttonRow compactButtons">
            {editingPolicy ? (
              <>
                <button className="approve" onClick={() => void savePolicyEdit()} disabled={savingPolicy} title={text.settings.saveToolPolicy}>
                  <Check size={15} />
                </button>
                <button className="edit" onClick={cancelPolicyEdit} disabled={savingPolicy} title={text.settings.cancelPolicy}>
                  <X size={15} />
                </button>
              </>
            ) : (
              <button className="edit" onClick={startPolicyEdit} title={text.settings.editPolicy}>
                <Pencil size={15} />
              </button>
            )}
          </div>
        </div>
        {editingPolicy ? (
          <div className="policyEditor">
            <label>
              <span>{text.settings.deny}</span>
              <textarea value={denyText} onChange={(event) => setDenyText(event.target.value)} disabled={savingPolicy} />
            </label>
          </div>
        ) : (
          <div className="evalCases">
            {policy.denied_tools.map((tool) => (
              <span className="failed" key={tool}>{tool}</span>
            ))}
            {policy.denied_tools.length === 0 && <span>{text.common.none}</span>}
          </div>
        )}
      </article>
      <article className="settingsBlock">
        <strong>{text.settings.modelProfiles}</strong>
        <dl className="statusGrid compact">
          <dt>{text.settings.mode}</dt>
          <dd>{runtimeConfig.model.mock ? text.settings.mock : text.settings.externalModel}</dd>
          <dt>{text.settings.fast}</dt>
          <dd>{profileLabel(runtimeConfig.model.fast, text)}</dd>
          <dt>{text.settings.deep}</dt>
          <dd>{profileLabel(runtimeConfig.model.deep, text)}</dd>
          <dt>{text.settings.embed}</dt>
          <dd>{profileLabel(runtimeConfig.model.embedding, text)}</dd>
          <dt>{text.settings.guard}</dt>
          <dd>{profileLabel(runtimeConfig.model.guard, text)}</dd>
        </dl>
      </article>
      <article className="settingsBlock">
        <strong>{text.settings.runtimeBoundaries}</strong>
        <dl className="statusGrid compact">
          <dt>{text.status.gateway}</dt>
          <dd>{runtimeConfig.gateway.bind}:{runtimeConfig.gateway.port}</dd>
          <dt>{text.settings.remote}</dt>
          <dd>{runtimeConfig.gateway.remote_access}</dd>
          <dt>{text.status.rateLimit}</dt>
          <dd>{rateLimitLabel(runtimeConfig.gateway.rate_limit, text)}</dd>
          <dt>{text.status.workspace}</dt>
          <dd>{runtimeConfig.workspaces.default_root}</dd>
          <dt>{text.settings.sandbox}</dt>
          <dd>{runtimeConfig.sandbox.enabled ? `${runtimeConfig.sandbox.backend} · ${runtimeConfig.sandbox.network}` : text.common.disabled}</dd>
          <dt>{text.status.state}</dt>
          <dd>
            {runtimeConfig.state.backend} · {runtimeConfig.state.path || runtimeConfig.state.dsn}
            {runtimeConfig.state.encrypt_at_rest ? ` · ${text.settings.encrypted}` : ""}
          </dd>
          <dt>{text.settings.artifacts}</dt>
          <dd>{runtimeConfig.storage.artifact_backend} · {runtimeConfig.storage.artifact_dir || runtimeConfig.storage.artifact_bucket}</dd>
          <dt>{text.settings.memory}</dt>
          <dd>
            {runtimeConfig.memory.enabled
              ? `${runtimeConfig.memory.write_policy} · ${retentionLabel(runtimeConfig.memory.retention_days, text)}`
              : text.common.disabled}
          </dd>
        </dl>
      </article>
    </div>
  );
}

function connectorStatusLabel(connector: ConnectorStatus, text: CopyText) {
  switch (connector.state) {
    case "disabled":
      return text.settings.connectorDisabled;
    case "unavailable":
      return connector.disabled_reason === "credential_key_unavailable"
        ? text.settings.bindingCredentialUnavailable
        : text.settings.bindingUnavailable;
    case "starting":
      return text.settings.connectorStarting;
    case "setup_required":
      return text.settings.connectorNeedsSetup;
    case "setup_pending":
      return bindingStatusLabel(connector.binding_status, text);
    case "active":
      return text.settings.bound;
    case "error":
      return text.settings.connectorError;
    default:
      return connector.provider;
  }
}

export function SectionHeader({ icon, title }: { icon: React.ReactNode; title: string }) {
  return (
    <div className="sectionHeader">
      {icon}
      <h2>{title}</h2>
    </div>
  );
}

export function JsonBlock({ value }: { value: unknown }) {
  const raw = JSON.stringify(value, null, 2);
  async function copy() {
    await navigator.clipboard?.writeText(raw).catch(() => undefined);
  }
  return (
    <div className="jsonBlock">
      <button className="miniIconButton jsonCopy" onClick={() => void copy()} title="Copy JSON">
        <Copy size={13} />
      </button>
      <pre>{raw}</pre>
    </div>
  );
}

export function RiskPill({ risk, text }: { risk: string; text: CopyText }) {
  return <span className={`riskPill ${risk}`}>{formatRisk(risk, text)}</span>;
}
