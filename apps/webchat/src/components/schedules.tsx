import { useState, type FormEvent } from "react";
import { AlertTriangle, ChevronDown, Clock3, Pencil, RefreshCw, Trash2, X } from "lucide-react";
import type { Schedule } from "../api/types";
import type { Copy, Language } from "../i18n";
import { formatScheduleTime, schedulePattern } from "../lib/schedules";
import { clientTimezone } from "../lib/timezone";

export type ScheduleEditDraft = {
  text: string;
  dueTime: string;
  timezone: string;
  recurrence: string;
};

type ScheduleBarProps = {
  schedules: Schedule[];
  open: boolean;
  loading: boolean;
  busyId: string;
  language: Language;
  text: Copy;
  onToggle: () => void;
  onRefresh: () => void;
  onEdit: (schedule: Schedule, draft: ScheduleEditDraft) => Promise<void>;
  onDelete: (schedule: Schedule) => Promise<void>;
};

export function ScheduleBar({ schedules, open, loading, busyId, language, text, onToggle, onRefresh, onEdit, onDelete }: ScheduleBarProps) {
  const [editing, setEditing] = useState<Schedule | null>(null);
  const [deleting, setDeleting] = useState<Schedule | null>(null);
  const [draft, setDraft] = useState<ScheduleEditDraft>({ text: "", dueTime: "", timezone: "", recurrence: "" });
  const next = schedules[0];
  const patternLabels = {
    oneTime: text.schedules.oneTime,
    daily: text.schedules.daily,
    weekly: text.schedules.weekly,
    monthly: text.schedules.monthly
  };

  const beginEdit = (schedule: Schedule) => {
    const timezone = clientTimezone();
    setDraft({
      text: schedule.text,
      dueTime: scheduleLocalDateTime(schedule, timezone),
      timezone: timezone || schedule.timezone,
      recurrence: schedule.recurrence || "none"
    });
    setEditing(schedule);
  };

  const submitEdit = async (event: FormEvent) => {
    event.preventDefault();
    if (!editing) return;
    try {
      await onEdit(editing, draft);
      setEditing(null);
    } catch {
      // The parent keeps the dialog open and renders the gateway error banner.
    }
  };

  const confirmDelete = async () => {
    if (!deleting) return;
    try {
      await onDelete(deleting);
      setDeleting(null);
    } catch {
      // The parent keeps the confirmation open and renders the gateway error banner.
    }
  };

  return (
    <>
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
            ) : schedules.map((schedule) => {
              const endpoint = scheduleEndpointLabel(schedule, text);
              const busy = busyId === schedule.id;
              return (
                <div className="scheduleRow" key={schedule.id}>
                  <span className={`scheduleStatusDot ${schedule.status}`} aria-hidden="true" />
                  <strong title={schedule.text}>{schedule.title}</strong>
                  <span className="scheduleEndpoint" title={endpoint}>{endpoint}</span>
                  <span className="schedulePattern" title={schedule.recurrence}>{schedulePattern(schedule, patternLabels)}</span>
                  <time dateTime={schedule.due_time}>{formatScheduleTime(schedule, language)}</time>
                  <span className={`scheduleState ${schedule.status}`}>{schedule.status === "sending" ? text.schedules.statusSending : text.schedules.statusPending}</span>
                  <div className="scheduleRowActions">
                    <button className="miniIconButton" type="button" onClick={() => beginEdit(schedule)} disabled={!schedule.editable || busy} title={schedule.editable ? text.schedules.edit : text.schedules.editUnavailable}>
                      <Pencil size={13} />
                    </button>
                    <button className="miniIconButton dangerIconButton" type="button" onClick={() => setDeleting(schedule)} disabled={!schedule.cancelable || busy} title={text.schedules.delete}>
                      <Trash2 size={13} />
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
        ) : null}
      </section>

      {editing ? (
        <div className="documentPickerOverlay" role="dialog" aria-modal="true" aria-label={text.schedules.editTitle}>
          <form className="scheduleDialog" onSubmit={(event) => void submitEdit(event)}>
            <div className="documentPickerHeader">
              <strong>{text.schedules.editTitle}</strong>
              <button className="attachmentRemove" type="button" onClick={() => setEditing(null)} disabled={busyId === editing.id} title={text.common.cancel}>
                <X size={14} />
              </button>
            </div>
            <label>
              <span>{text.schedules.content}</span>
              <textarea value={draft.text} onChange={(event) => setDraft((current) => ({ ...current, text: event.target.value }))} required rows={4} />
            </label>
            <div className="scheduleDialogGrid">
              <label>
                <span>{text.schedules.dueTime}</span>
                <input type="datetime-local" value={draft.dueTime} onChange={(event) => setDraft((current) => ({ ...current, dueTime: event.target.value }))} required />
              </label>
              <label>
                <span>{text.schedules.timezone}</span>
                <input value={draft.timezone} onChange={(event) => setDraft((current) => ({ ...current, timezone: event.target.value }))} required />
              </label>
            </div>
            <label>
              <span>{text.schedules.recurrence}</span>
              <input list="schedule-recurrence-options" value={draft.recurrence} onChange={(event) => setDraft((current) => ({ ...current, recurrence: event.target.value }))} />
              <datalist id="schedule-recurrence-options">
                <option value="none">{text.schedules.oneTime}</option>
                <option value="daily">{text.schedules.daily}</option>
                <option value="weekly">{text.schedules.weekly}</option>
                <option value="monthly">{text.schedules.monthly}</option>
              </datalist>
            </label>
            <div className="scheduleDialogEndpoint">
              <span>{text.schedules.endpoint}</span>
              <strong>{scheduleEndpointLabel(editing, text)}</strong>
            </div>
            <div className="scheduleDialogActions">
              <button className="secondaryButton" type="button" onClick={() => setEditing(null)} disabled={busyId === editing.id}>{text.common.cancel}</button>
              <button className="primaryButton" type="submit" disabled={busyId === editing.id || !draft.text.trim() || !draft.dueTime || !draft.timezone.trim()}>
                <Pencil size={15} />
                <span>{text.common.save}</span>
              </button>
            </div>
          </form>
        </div>
      ) : null}

      {deleting ? (
        <div className="documentPickerOverlay" role="dialog" aria-modal="true" aria-label={text.schedules.deleteTitle}>
          <div className="scheduleDialog scheduleDeleteDialog">
            <AlertTriangle size={22} />
            <strong>{text.schedules.deleteTitle}</strong>
            <p>{text.schedules.deleteConfirm}</p>
            <blockquote>{deleting.title}</blockquote>
            <div className="scheduleDialogActions">
              <button className="secondaryButton" type="button" onClick={() => setDeleting(null)} disabled={busyId === deleting.id}>{text.common.cancel}</button>
              <button className="dangerButton" type="button" onClick={() => void confirmDelete()} disabled={busyId === deleting.id}>
                <Trash2 size={15} />
                <span>{text.schedules.confirmDelete}</span>
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </>
  );
}

function scheduleEndpointLabel(schedule: Schedule, text: Copy) {
  const endpoint = schedule.endpoint;
  const parts = [
    endpoint.software_display_name || endpoint.channel,
    endpoint.account_display_name,
    endpoint.recipient_display_name,
    endpoint.conversation_label
  ].filter((value, index, values): value is string => Boolean(value) && values.indexOf(value) === index);
  const label = parts.join(" · ") || text.schedules.endpointUnknown;
  return endpoint.status === "active" ? label : `${label} · ${text.schedules.endpointUnavailable}`;
}

function scheduleLocalDateTime(schedule: Schedule, timezone: string) {
  const date = new Date(schedule.due_time);
  if (Number.isNaN(date.getTime())) return "";
  try {
    const parts = new Intl.DateTimeFormat("en-CA", {
      ...(timezone ? { timeZone: timezone } : {}),
      year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23"
    }).formatToParts(date);
    const value = Object.fromEntries(parts.map((part) => [part.type, part.value]));
    return `${value.year}-${value.month}-${value.day}T${value.hour}:${value.minute}`;
  } catch {
    const pad = (value: number) => String(value).padStart(2, "0");
    return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
  }
}
