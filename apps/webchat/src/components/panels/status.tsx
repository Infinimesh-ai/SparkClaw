import { useState } from "react";
import { Clock3, Database, ListChecks, ScrollText } from "lucide-react";
import type {
  ArtifactObject,
  AuditEvent,
  EpisodeSummary,
  EvalRun,
  ModelCall,
  ReadyStatus
} from "../../api/types";
import type { Copy as CopyText, Language } from "../../i18n";
import { formatRisk, formatState, formatTime, rateLimitLabel, shortId } from "../../lib/format";
import { SectionHeader } from "./primitives";

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
        <strong>{text.status.residentServices}</strong>
        {(ready?.resident_services ?? []).map((service) => (
          <article className="diagnosticRow" key={service.lane}>
            <div>
              <span>{service.lane}</span>
              <small>{service.backend} · {service.model}</small>
            </div>
            <small>
              {formatState(service.readiness, text)} · {service.last_call_status
                ? `${text.status.lastCall}: ${formatState(service.last_call_status, text)}`
                : text.status.noServiceCalls}
            </small>
          </article>
        ))}
      </div>
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
