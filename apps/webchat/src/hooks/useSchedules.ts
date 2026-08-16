// Schedule bar state plus edit/delete actions, which route through the agent
// message endpoint as schedule_action payloads so runs stay auditable.
// Extracted from App.tsx so the root component stays below the size baseline.
import { useCallback, useState } from "react";
import { api } from "../api/client";
import type { Schedule } from "../api/types";
import type { ScheduleEditDraft } from "../components/schedules";
import type { Copy, Language } from "../i18n";

type Options = {
  activeSession: string;
  language: Language;
  text: Copy;
  setError: (message: string) => void;
  surfaceError: (err: unknown, fallback: string) => void;
  refreshSession: (sessionId: string) => Promise<void>;
};

export function useSchedules({ activeSession, language, text, setError, surfaceError, refreshSession }: Options) {
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [scheduleBarOpen, setScheduleBarOpen] = useState(true);
  const [schedulesRefreshing, setSchedulesRefreshing] = useState(false);
  const [scheduleBusyId, setScheduleBusyId] = useState("");

  const refreshSchedules = useCallback(async () => {
    try {
      setSchedulesRefreshing(true);
      setError("");
      const result = await api.schedules();
      setSchedules(result.schedules ?? []);
    } catch (err) {
      surfaceError(err, text.errors.schedules);
    } finally {
      setSchedulesRefreshing(false);
    }
  }, [setError, surfaceError, text.errors.schedules]);

  async function editSchedule(schedule: Schedule, draft: ScheduleEditDraft) {
    const sessionId = activeSession || schedule.session_id || "";
    if (!sessionId || scheduleBusyId) return;
    try {
      setScheduleBusyId(schedule.id);
      setError("");
      const content = language === "zh"
        ? `编辑定时任务 ${schedule.id}：内容改为“${draft.text.trim()}”，执行时间改为 ${draft.dueTime}（${draft.timezone}），重复规则改为 ${draft.recurrence || "none"}。`
        : `Edit scheduled task ${schedule.id}: set the request to "${draft.text.trim()}", due time to ${draft.dueTime} (${draft.timezone}), and recurrence to ${draft.recurrence || "none"}.`;
      const result = await api.scheduleAction(sessionId, content, {
        operation: "edit", schedule_id: schedule.id, expected_updated_at: schedule.updated_at,
        text: draft.text.trim(), due_time: draft.dueTime, timezone: draft.timezone.trim(), recurrence: draft.recurrence.trim() || "none"
      });
      if (result.run.state === "blocked") throw new Error(result.message.content || text.errors.schedules);
      await refreshSchedules();
      if (activeSession) await refreshSession(activeSession);
    } catch (err) {
      surfaceError(err, text.errors.schedules);
      throw err;
    } finally {
      setScheduleBusyId("");
    }
  }

  async function deleteSchedule(schedule: Schedule) {
    const sessionId = activeSession || schedule.session_id || "";
    if (!sessionId || scheduleBusyId) return;
    try {
      setScheduleBusyId(schedule.id);
      setError("");
      const content = language === "zh" ? `删除定时任务 ${schedule.id}。` : `Delete scheduled task ${schedule.id}.`;
      const result = await api.scheduleAction(sessionId, content, {
        operation: "delete", schedule_id: schedule.id, expected_updated_at: schedule.updated_at
      });
      if (result.run.state === "blocked") throw new Error(result.message.content || text.errors.schedules);
      await refreshSchedules();
      if (activeSession) await refreshSession(activeSession);
    } catch (err) {
      surfaceError(err, text.errors.schedules);
      throw err;
    } finally {
      setScheduleBusyId("");
    }
  }

  return {
    schedules,
    setSchedules,
    scheduleBarOpen,
    setScheduleBarOpen,
    schedulesRefreshing,
    scheduleBusyId,
    refreshSchedules,
    editSchedule,
    deleteSchedule
  };
}
