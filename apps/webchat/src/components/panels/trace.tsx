import { ScrollText } from "lucide-react";
import type { RunTrace, TraceMetadata } from "../../api/types";
import type { Copy as CopyText, Language } from "../../i18n";
import { formatLatency, formatRisk, formatState, formatTime, shortId } from "../../lib/format";
import { SectionHeader } from "./primitives";
import { EpisodeCard } from "./status";

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
