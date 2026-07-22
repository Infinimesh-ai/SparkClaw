import { ChevronDown, Clock3, RefreshCw } from "lucide-react";
import type { Schedule } from "../api/types";
import type { Copy, Language } from "../i18n";
import { formatScheduleTime, schedulePattern } from "../lib/schedules";

type ScheduleBarProps = {
  schedules: Schedule[];
  open: boolean;
  loading: boolean;
  language: Language;
  text: Copy;
  onToggle: () => void;
  onRefresh: () => void;
};

export function ScheduleBar({ schedules, open, loading, language, text, onToggle, onRefresh }: ScheduleBarProps) {
  const next = schedules[0];
  const patternLabels = {
    oneTime: text.schedules.oneTime,
    daily: text.schedules.daily,
    weekly: text.schedules.weekly,
    monthly: text.schedules.monthly
  };

  return (
    <section className={`scheduleBar ${open ? "open" : ""}`} aria-label={text.schedules.title}>
      <header className="scheduleBarHeader">
        <div className="scheduleBarIdentity">
          <Clock3 size={16} />
          <strong>{text.schedules.title}</strong>
          <span className="scheduleCount">{schedules.length} {text.schedules.activeSuffix}</span>
        </div>
        <p className="scheduleNext">
          {next ? `${text.schedules.nextRun} ${formatScheduleTime(next, language)}` : text.schedules.noCurrent}
        </p>
        <div className="scheduleBarActions">
          <button className="miniIconButton" type="button" onClick={onRefresh} disabled={loading} title={loading ? text.schedules.refreshing : text.common.refresh}>
            <RefreshCw className={loading ? "spin" : ""} size={14} />
          </button>
          <button className="miniIconButton scheduleToggle" type="button" onClick={onToggle} aria-expanded={open} title={open ? text.schedules.collapse : text.schedules.expand}>
            <ChevronDown size={15} />
          </button>
        </div>
      </header>
      {open ? (
        <div className="scheduleList">
          {schedules.length === 0 ? (
            <div className="scheduleEmpty">{text.schedules.noCurrent}</div>
          ) : schedules.map((schedule) => (
            <div className="scheduleRow" key={schedule.id}>
              <span className={`scheduleStatusDot ${schedule.status}`} aria-hidden="true" />
              <strong title={schedule.title}>{schedule.title}</strong>
              <span className="schedulePattern" title={schedule.recurrence}>{schedulePattern(schedule, patternLabels)}</span>
              <time dateTime={schedule.due_time}>{formatScheduleTime(schedule, language)}</time>
              <span className={`scheduleState ${schedule.status}`}>{schedule.status === "sending" ? text.schedules.statusSending : text.schedules.statusPending}</span>
            </div>
          ))}
        </div>
      ) : null}
    </section>
  );
}
