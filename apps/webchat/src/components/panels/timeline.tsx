import { Database, FileSearch, Globe2, ScrollText, Terminal } from "lucide-react";
import type { ToolCall } from "../../api/types";
import type { Copy as CopyText } from "../../i18n";
import { cssToken, formatState, shortId, stripSystemArgs } from "../../lib/format";
import { JsonBlock, RiskPill, SectionHeader } from "./primitives";

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
